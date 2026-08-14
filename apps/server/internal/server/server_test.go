package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	cfg := config.Config{SessionTTL: time.Hour}
	return New(db, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
