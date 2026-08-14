package deployment

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
)

type fakeResolver struct{}

func (fakeResolver) Resolve(context.Context, string, string, string) (Commit, error) {
	return Commit{SHA: "0123456789012345678901234567890123456789", Message: "test", Author: "tester"}, nil
}
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}
func insertProject(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,build_steps_json,deploy_steps_json,created_at,updated_at) VALUES(?,?,?,?,?,'[]','[]',?,?)`, id, id, id, "https://example.com/repo.git", "main", now, now)
	if err != nil {
		t.Fatal(err)
	}
}
func TestCreateSnapshotsVariables(t *testing.T) {
	db := testDB(t)
	insertProject(t, db, "one")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = db.Exec(`INSERT INTO project_variables(project_id,name,version,is_secret,plain_value,created_at) VALUES('one','VALUE',1,0,'old',?)`, now)
	s := New(db, fakeResolver{})
	d, err := s.Create(context.Background(), "one", "manual")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(`UPDATE project_variables SET replaced_at=? WHERE project_id='one' AND name='VALUE'`, now)
	_, _ = db.Exec(`INSERT INTO project_variables(project_id,name,version,is_secret,plain_value,created_at) VALUES('one','VALUE',2,0,'new',?)`, now)
	var value string
	var version int
	if err = db.QueryRow(`SELECT plain_value,source_version FROM deployment_variables WHERE deployment_id=?`, d.ID).Scan(&value, &version); err != nil {
		t.Fatal(err)
	}
	if value != "old" || version != 1 {
		t.Fatalf("snapshot changed: %q v%d", value, version)
	}
}
func TestAtomicClaimKeepsProjectExclusive(t *testing.T) {
	db := testDB(t)
	insertProject(t, db, "one")
	s := New(db, fakeResolver{})
	d1, _ := s.Create(context.Background(), "one", "manual")
	_, _ = s.Create(context.Background(), "one", "manual")
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, ok, _ := s.Claim(context.Background(), "runner"); results <- ok }()
	}
	wg.Wait()
	close(results)
	count := 0
	for ok := range results {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one claim, got %d", count)
	}
	if err := s.Finish(context.Background(), d1.ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Claim(context.Background(), "runner"); err != nil || !ok {
		t.Fatalf("second task not claimable: %v %v", ok, err)
	}
}
func TestCancelIsIdempotent(t *testing.T) {
	db := testDB(t)
	insertProject(t, db, "one")
	s := New(db, fakeResolver{})
	d, _ := s.Create(context.Background(), "one", "manual")
	if err := s.Cancel(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(context.Background(), d.ID)
	if got.Status != "cancelled" {
		t.Fatalf("got %s", got.Status)
	}
}

func TestCancellationWinsFinishRace(t *testing.T) {
	db := testDB(t)
	insertProject(t, db, "one")
	s := New(db, fakeResolver{})
	d, _ := s.Create(context.Background(), "one", "manual")
	claimed, ok, err := s.Claim(context.Background(), "runner")
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err = s.Cancel(context.Background(), claimed.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(context.Background(), claimed.ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(context.Background(), d.ID)
	if got.Status != "cancelled" {
		t.Fatalf("cancel was overwritten by %s", got.Status)
	}
}

func TestCreateAtCommitAndHealthSnapshot(t *testing.T) {
	db := testDB(t)
	insertProject(t, db, "one")
	_, _ = db.Exec(`UPDATE projects SET health_enabled=1,health_url='http://127.0.0.1/health',health_retries=2 WHERE id='one'`)
	s := New(db, fakeResolver{})
	d, err := s.CreateAtCommit(context.Background(), "one", "redeploy", "0123456789012345678901234567890123456789")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(`UPDATE projects SET health_enabled=0,health_url='' WHERE id='one'`)
	var enabled bool
	var url string
	if err = db.QueryRow(`SELECT health_enabled,health_url FROM deployments WHERE id=?`, d.ID).Scan(&enabled, &url); err != nil {
		t.Fatal(err)
	}
	if !enabled || url != "http://127.0.0.1/health" {
		t.Fatalf("health snapshot changed: %v %q", enabled, url)
	}
}
