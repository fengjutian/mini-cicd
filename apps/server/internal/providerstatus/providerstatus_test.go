package providerstatus

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/pipelineconfig"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
)

func statusDB(t *testing.T) (*sql.DB, *secret.Box) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	box, err := secret.New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,created_at,updated_at) VALUES('p','Project','p','https://example/repo','main',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	return db, box
}

func TestSendGitHubCommitStatus(t *testing.T) {
	var payload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/api/statuses/0123456789012345678901234567890123456789" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer github-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	db, box := statusDB(t)
	id := insertStatusDeployment(t, db, box, pipelineconfig.CommitStatus{Enabled: true, Provider: "github", Repository: "acme/api", TokenVariable: "GIT_TOKEN", APIBase: server.URL, Context: "deploy/prod"}, "succeeded", "github-token")
	m := New(db, box, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := m.send(job{deployment: id, project: "p", status: "succeeded", sha: "0123456789012345678901234567890123456789", config: statusConfig(t, db, id)}); err != nil {
		t.Fatal(err)
	}
	if payload["state"] != "success" || payload["context"] != "deploy/prod" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSendGitLabCommitStatus(t *testing.T) {
	var payload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/projects/acme%2Fapi/statuses/0123456789012345678901234567890123456789" {
			t.Errorf("path = %q", r.URL.EscapedPath())
		}
		if r.Header.Get("PRIVATE-TOKEN") != "gitlab-token" {
			t.Errorf("token = %q", r.Header.Get("PRIVATE-TOKEN"))
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	db, box := statusDB(t)
	id := insertStatusDeployment(t, db, box, pipelineconfig.CommitStatus{Enabled: true, Provider: "gitlab", Repository: "acme/api", TokenVariable: "GIT_TOKEN", APIBase: server.URL, Context: "mini-ci-cd"}, "failed", "gitlab-token")
	m := New(db, box, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := m.send(job{deployment: id, project: "p", status: "failed", sha: "0123456789012345678901234567890123456789", config: statusConfig(t, db, id)}); err != nil {
		t.Fatal(err)
	}
	if payload["state"] != "failed" || payload["name"] != "mini-ci-cd" {
		t.Fatalf("payload = %#v", payload)
	}
}

func insertStatusDeployment(t *testing.T, db *sql.DB, box *secret.Box, cfg pipelineconfig.CommitStatus, state, token string) int64 {
	t.Helper()
	raw, _ := json.Marshal(cfg)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.Exec(`INSERT INTO deployments(project_id,status,trigger_type,branch,commit_sha,commit_status_config_json,created_at) VALUES('p',?,'manual','main','0123456789012345678901234567890123456789',?,?)`, state, raw, now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	cipher, err := box.Encrypt([]byte(token), "project:p:variable:GIT_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO deployment_variables(deployment_id,name,is_secret,cipher_value,source_version) VALUES(?,'GIT_TOKEN',1,?,1)`, id, cipher); err != nil {
		t.Fatal(err)
	}
	return id
}

func statusConfig(t *testing.T, db *sql.DB, deploymentID int64) string {
	t.Helper()
	var raw string
	if err := db.QueryRow(`SELECT commit_status_config_json FROM deployments WHERE id=?`, deploymentID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}
