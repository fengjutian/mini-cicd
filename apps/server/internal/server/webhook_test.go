package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/config"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
)

func webhookFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	data := t.TempDir()
	repo := filepath.Join(data, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "init", "-b", "main")
	gitCommand(t, repo, "config", "user.name", "Test")
	gitCommand(t, repo, "config", "user.email", "test@example.com")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("test"), 0600)
	gitCommand(t, repo, "add", ".")
	gitCommand(t, repo, "commit", "-m", "initial")
	sha := gitOutput(t, repo, "rev-parse", "HEAD")
	db, err := database.Open(filepath.Join(data, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{9}, 32)
	box, _ := secret.New(key)
	cipher, _ := box.Encrypt([]byte("hook-secret"), "project:p:webhook-secret")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,auto_deploy,webhook_provider,webhook_secret_cipher,build_steps_json,deploy_steps_json,created_at,updated_at) VALUES('p','p','p',?,'main',1,'github',?,'[]','[]',?,?)`, repo, cipher, now, now)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataDir: data, SessionTTL: time.Hour, MasterKey: key, GlobalParallel: 1, Shell: "powershell.exe", LogMaxBytes: 1024 * 1024, CancelGrace: 100 * time.Millisecond}
	s, err := New(db, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, repo, sha
}
func gitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
}
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSpace(out))
}
func githubRequest(t *testing.T, s http.Handler, repo, sha, delivery, secretValue string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"ref": "refs/heads/main", "after": sha, "repository": map[string]string{"clone_url": repo}})
	mac := hmac.New(sha256.New, []byte(secretValue))
	_, _ = mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/p/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}
func TestGitHubWebhookCreatesOneDeployment(t *testing.T) {
	s, repo, sha := webhookFixture(t)
	first := githubRequest(t, s, repo, sha, "delivery-1", "hook-secret")
	if first.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", first.Code, first.Body.String())
	}
	duplicate := githubRequest(t, s, repo, sha, "delivery-1", "hook-secret")
	if duplicate.Code != http.StatusAccepted {
		t.Fatal(duplicate.Code)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var count int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE project_id='p'`).Scan(&count)
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("webhook deployment was not created")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var deliveries int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&deliveries)
	if deliveries != 1 {
		t.Fatalf("deliveries=%d", deliveries)
	}
}
func TestGitHubWebhookRejectsBadSignature(t *testing.T) {
	s, repo, sha := webhookFixture(t)
	rec := githubRequest(t, s, repo, sha, "bad", "wrong-secret")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&count)
	if count != 0 {
		t.Fatal("invalid webhook occupied a delivery ID")
	}
}
func TestProviderSignatureVerification(t *testing.T) {
	body := []byte(`{"x":1}`)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Gitlab-Token", "secret")
	if !verifyWebhook("gitlab", req, body, []byte("secret")) {
		t.Fatal("GitLab token rejected")
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	req.Header.Set("X-Gitea-Signature", hex.EncodeToString(mac.Sum(nil)))
	if !verifyWebhook("gitea", req, body, []byte("secret")) {
		t.Fatal("Gitea signature rejected")
	}
	req.Header.Set("X-Gitea-Signature", fmt.Sprint("00"))
	if verifyWebhook("gitea", req, body, []byte("secret")) {
		t.Fatal("bad Gitea signature accepted")
	}
}
