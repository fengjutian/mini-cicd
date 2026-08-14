package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/config"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{SessionTTL: time.Hour, DataDir: t.TempDir(), GlobalParallel: 1, Shell: "powershell.exe", LogMaxBytes: 1024 * 1024, CancelGrace: time.Second}
	s, err := New(db, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestOwnerSetupLoginAndLogout(t *testing.T) {
	handler := newTestServer(t)

	status := requestJSON(t, handler, http.MethodGet, "/api/v1/status", nil)
	if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte(`"initialized":false`)) {
		t.Fatalf("unexpected initial status: %d %s", status.Code, status.Body.String())
	}

	input := map[string]string{
		"email": "owner@example.com", "username": "owner",
		"password": "correct horse battery staple", "confirmPassword": "correct horse battery staple",
	}
	setup := requestJSON(t, handler, http.MethodPost, "/api/v1/setup", input)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	if len(setup.Result().Cookies()) != 1 || !setup.Result().Cookies()[0].HttpOnly {
		t.Fatal("setup did not issue an HttpOnly session cookie")
	}

	secondSetup := requestJSON(t, handler, http.MethodPost, "/api/v1/setup", input)
	if secondSetup.Code != http.StatusConflict {
		t.Fatalf("second setup should conflict: %d %s", secondSetup.Code, secondSetup.Body.String())
	}

	badLogin := requestJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "owner@example.com", "password": "not-the-password"})
	if badLogin.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password should fail: %d", badLogin.Code)
	}

	login := requestJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "owner@example.com", "password": "correct horse battery staple"})
	if login.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	me := requestJSON(t, handler, http.MethodGet, "/api/v1/auth/me", nil, cookie)
	if me.Code != http.StatusOK || !bytes.Contains(me.Body.Bytes(), []byte(`"role":"owner"`)) {
		t.Fatalf("me failed: %d %s", me.Code, me.Body.String())
	}

	logout := requestJSON(t, handler, http.MethodPost, "/api/v1/auth/logout", map[string]string{}, cookie)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout failed: %d %s", logout.Code, logout.Body.String())
	}
	meAfterLogout := requestJSON(t, handler, http.MethodGet, "/api/v1/auth/me", nil, cookie)
	if meAfterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out session remained valid: %d", meAfterLogout.Code)
	}
}

func TestSetupValidation(t *testing.T) {
	handler := newTestServer(t)
	input := map[string]string{"email": "bad", "username": "x", "password": "short", "confirmPassword": "different"}
	rec := requestJSON(t, handler, http.MethodPost, "/api/v1/setup", input)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid setup returned %d: %s", rec.Code, rec.Body.String())
	}
	var count int
	if err := handler.(*Server).db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("anonymous failure was audited: count=%d err=%v", count, err)
	}
}

func TestRejectsCrossOriginMutation(t *testing.T) {
	handler := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewReader([]byte(`{}`)))
	req.Host = "minicicd.example"
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin mutation returned %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSSEResumesAfterLastEventID(t *testing.T) {
	handler := newTestServer(t)
	s := handler.(*Server)
	setup := requestJSON(t, handler, http.MethodPost, "/api/v1/setup", map[string]string{"email": "owner@example.com", "username": "owner", "password": "correct horse battery staple", "confirmPassword": "correct horse battery staple"})
	cookie := setup.Result().Cookies()[0]
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,created_at,updated_at) VALUES('p','p','p','https://example/repo','main',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO deployments(project_id,status,trigger_type,branch,commit_sha,created_at,finished_at) VALUES('p','succeeded','manual','main','0123456789012345678901234567890123456789',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	w, err := s.logs.Open("p", id, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WriteStep(1, "output", "first")
	_ = w.WriteStep(1, "output", "second")
	_ = w.Close()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/deployments/%d/logs", id), nil)
	req.Header.Set("Last-Event-ID", "1")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "first") || !strings.Contains(body, "second") || !strings.Contains(body, "id: 2") {
		t.Fatalf("unexpected SSE body: %s", body)
	}
}
