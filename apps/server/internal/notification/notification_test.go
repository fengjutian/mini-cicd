package notification

import (
	"context"
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
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
)

func notificationDB(t *testing.T) (*sql.DB, *secret.Box) {
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
	_, err = db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,created_at,updated_at) VALUES('p','Project','p','https://example/repo','main',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	return db, box
}

func TestWebhookDeliveryUsesSecretSnapshot(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("content type")
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(204)
	}))
	defer server.Close()
	db, box := notificationDB(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	config := `[{"name":"ops","type":"webhook","urlVariable":"HOOK_URL","events":["succeeded"]}]`
	res, err := db.Exec(`INSERT INTO deployments(project_id,status,trigger_type,branch,commit_sha,created_at,notification_config_json) VALUES('p','succeeded','manual','main','0123456789012345678901234567890123456789',?,?)`, now, config)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	cipher, err := box.Encrypt([]byte(server.URL), "project:p:variable:HOOK_URL")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO deployment_variables(deployment_id,name,is_secret,cipher_value,source_version) VALUES(?,'HOOK_URL',1,?,1)`, id, cipher)
	if err != nil {
		t.Fatal(err)
	}
	m := New(db, box, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := delivery{deploymentID: id, projectID: "p", projectName: "Project", status: "succeeded", commitSHA: "0123456789012345678901234567890123456789", config: config}
	if err = m.deliver(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if payload["event"] != "deployment.succeeded" || int64(payload["deploymentId"].(float64)) != id {
		t.Fatalf("payload: %#v", payload)
	}
}

func TestTerminalTransitionCreatesOutbox(t *testing.T) {
	db, _ := notificationDB(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.Exec(`INSERT INTO deployments(project_id,status,trigger_type,branch,created_at,notification_config_json) VALUES('p','running','manual','main',?,'[{"name":"ops"}]')`, now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	_, err = db.Exec(`UPDATE deployments SET status='failed',finished_at=? WHERE id=?`, now, id)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM notification_deliveries WHERE deployment_id=?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("outbox count %d", count)
	}
}
