package runner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/deployment"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/gitops"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/logstore"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/project"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/workspace"
)

type fixture struct {
	db      *sql.DB
	deps    *deployment.Service
	manager *Manager
	logs    *logstore.Store
	data    string
}

func newFixture(t *testing.T, steps []project.Step) *fixture {
	t.Helper()
	data, err := os.MkdirTemp(".", ".runner-test-")
	if err != nil {
		t.Fatal(err)
	}
	data, err = filepath.Abs(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(data) })
	repo := filepath.Join(data, "repo")
	if err = os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-b", "main")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test")
	if err = os.WriteFile(filepath.Join(repo, "README.md"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "initial")
	db, err := database.Open(filepath.Join(data, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	build, _ := json.Marshal(steps)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,build_steps_json,deploy_steps_json,step_timeout_seconds,deployment_timeout_seconds,created_at,updated_at) VALUES('p','p','p',?,'main',?,'[]',5,20,?,?)`, repo, string(build), now, now)
	if err != nil {
		t.Fatal(err)
	}
	box, _ := secret.New(bytes.Repeat([]byte{8}, 32))
	cipher, _ := box.Encrypt([]byte("do-not-leak"), "project:p:variable:MY_SECRET")
	_, err = db.Exec(`INSERT INTO project_variables(project_id,name,version,is_secret,cipher_value,created_at) VALUES('p','MY_SECRET',1,1,?,?)`, cipher, now)
	if err != nil {
		t.Fatal(err)
	}
	g := gitops.New(data, db, box)
	deps := deployment.New(db, g)
	spaces, _ := workspace.New(data)
	logs, _ := logstore.New(data, 1024*1024)
	shell := "/bin/bash"
	if runtime.GOOS == "windows" {
		shell = "powershell.exe"
	}
	m := New(db, deps, g, spaces, logs, box, shell, 1, 200*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.Start()
	t.Cleanup(m.Stop)
	return &fixture{db: db, deps: deps, manager: m, logs: logs, data: data}
}
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}
func waitTerminal(t *testing.T, s *deployment.Service, id int64, timeout time.Duration) deployment.Deployment {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d, err := s.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if d.Status == "succeeded" || d.Status == "failed" || d.Status == "cancelled" || d.Status == "timed_out" {
			return d
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("deployment did not finish")
	return deployment.Deployment{}
}
func TestEndToEndRunnerAndRedaction(t *testing.T) {
	command := `printf '%s\n' "$MY_SECRET"; printf built > artifact.txt`
	if runtime.GOOS == "windows" {
		command = `Write-Output $env:MY_SECRET; Set-Content -Path artifact.txt -Value built -NoNewline`
	}
	f := newFixture(t, []project.Step{{Name: "build", Command: command}})
	d, err := f.deps.Create(context.Background(), "p", "manual")
	if err != nil {
		t.Fatal(err)
	}
	f.manager.Wake()
	done := waitTerminal(t, f.deps, d.ID, 15*time.Second)
	if done.Status != "succeeded" {
		t.Fatalf("status=%s error=%s", done.Status, done.ErrorSummary)
	}
	raw, err := os.ReadFile(f.logs.Path("p", d.ID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "do-not-leak") || !strings.Contains(string(raw), "********") {
		t.Fatalf("bad redaction: %s", raw)
	}
	artifact := filepath.Join(f.data, "workspaces", "p", fmt.Sprint(d.ID), "source", "artifact.txt")
	if _, err = os.Stat(artifact); err != nil {
		t.Fatal(err)
	}
}
func TestRunningDeploymentCanBeCancelled(t *testing.T) {
	command := "sleep 30"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 30"
	}
	f := newFixture(t, []project.Step{{Name: "wait", Command: command}})
	d, err := f.deps.Create(context.Background(), "p", "manual")
	if err != nil {
		t.Fatal(err)
	}
	f.manager.Wake()
	deadline := time.Now().Add(10 * time.Second)
	for {
		current, _ := f.deps.Get(context.Background(), d.ID)
		if current.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deployment did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err = f.deps.Cancel(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	done := waitTerminal(t, f.deps, d.ID, 10*time.Second)
	if done.Status != "cancelled" {
		t.Fatalf("status=%s error=%s", done.Status, done.ErrorSummary)
	}
}

func TestStepTimeout(t *testing.T) {
	command := "sleep 30"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 30"
	}
	f := newFixture(t, []project.Step{{Name: "timeout", Command: command}})
	_, _ = f.db.Exec(`UPDATE projects SET step_timeout_seconds=1 WHERE id='p'`)
	d, err := f.deps.Create(context.Background(), "p", "manual")
	if err != nil {
		t.Fatal(err)
	}
	f.manager.Wake()
	done := waitTerminal(t, f.deps, d.ID, 10*time.Second)
	if done.Status != "timed_out" {
		t.Fatalf("status=%s error=%s", done.Status, done.ErrorSummary)
	}
}

func TestRecoveryFailsInterruptedTaskAndReleasesLock(t *testing.T) {
	f := newFixture(t, nil)
	f.manager.Stop()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := f.db.Exec(`INSERT INTO deployments(project_id,status,trigger_type,branch,commit_sha,created_at,started_at) VALUES('p','running','manual','main','0123456789012345678901234567890123456789',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err = f.db.Exec(`INSERT INTO project_locks(project_id,deployment_id,acquired_at) VALUES('p',?,?)`, id, now); err != nil {
		t.Fatal(err)
	}
	if err = f.manager.recover(); err != nil {
		t.Fatal(err)
	}
	d, err := f.deps.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "failed" || !strings.Contains(d.ErrorSummary, "restarted") {
		t.Fatalf("unexpected recovery: %#v", d)
	}
	var locks int
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM project_locks`).Scan(&locks); err != nil || locks != 0 {
		t.Fatalf("locks=%d err=%v", locks, err)
	}
}
