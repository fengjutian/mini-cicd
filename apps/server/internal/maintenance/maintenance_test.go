package maintenance

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/logstore"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/workspace"
)

func TestRunOnceCleansAndPrunes(t *testing.T) {
	data := t.TempDir()
	db, err := database.Open(filepath.Join(data, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	_, _ = db.Exec(`INSERT INTO users(id,email,username,password_hash,role,created_at) VALUES('u','u@example.com','u','x','owner',?)`, old)
	_, _ = db.Exec(`INSERT INTO sessions(id_hash,user_id,created_at,expires_at,last_seen_at) VALUES(X'01','u',?,?,?)`, old, old, old)
	_, err = db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,created_at,updated_at) VALUES('p','p','p','https://example/repo','main',?,?)`, old, old)
	if err != nil {
		t.Fatal(err)
	}
	spaces, _ := workspace.New(data)
	logs, _ := logstore.New(data, 1024*1024)
	for range 3 {
		res, e := db.Exec(`INSERT INTO deployments(project_id,status,trigger_type,branch,commit_sha,created_at,finished_at) VALUES('p','succeeded','manual','main','0123456789012345678901234567890123456789',?,?)`, old, old)
		if e != nil {
			t.Fatal(e)
		}
		id, _ := res.LastInsertId()
		space, e := spaces.Create("p", id)
		if e != nil {
			t.Fatal(e)
		}
		w, e := logs.Open("p", id, nil)
		if e != nil {
			t.Fatal(e)
		}
		_ = w.WriteStep(0, "system", "done")
		_ = w.Close()
		_, _ = db.Exec(`UPDATE deployments SET workspace_path=?,log_path=? WHERE id=?`, space, logs.Path("p", id), id)
	}
	m := New(db, logs, spaces, time.Hour, time.Nanosecond, time.Nanosecond, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err = m.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var sessions, deployments, paths int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions)
	_ = db.QueryRow(`SELECT COUNT(*) FROM deployments`).Scan(&deployments)
	_ = db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE workspace_path IS NOT NULL OR log_path IS NOT NULL`).Scan(&paths)
	if sessions != 0 || deployments != 1 || paths != 0 {
		t.Fatalf("sessions=%d deployments=%d paths=%d", sessions, deployments, paths)
	}
}
