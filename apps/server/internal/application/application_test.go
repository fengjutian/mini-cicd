package application

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
)

type fakeExecutor struct {
	dir, name string
	args      []string
}

func (f *fakeExecutor) Run(_ context.Context, dir, name string, args ...string) (string, error) {
	f.dir, f.name, f.args = dir, name, args
	return "running", nil
}

func appDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,created_at,updated_at) VALUES('p','p','p','https://example.com/p.git','main',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDockerStatusAndLogsUseSnapshot(t *testing.T) {
	db := appDB(t)
	_, err := db.Exec(`INSERT INTO deployments(project_id,status,trigger_type,branch,created_at,workspace_path,application_config_json) VALUES('p','succeeded','manual','main',?,'/work/one','{"adapter":"docker-compose","composeFile":"deploy/compose.yaml","projectName":"app","services":["web"]}')`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{}
	service := NewWithExecutor(db, executor)
	if _, err = service.Status(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	want := []string{"compose", "-f", filepath.Join("/work/one", "deploy", "compose.yaml"), "-p", "app", "ps"}
	if executor.name != "docker" || !reflect.DeepEqual(executor.args, want) {
		t.Fatalf("command: %s %#v", executor.name, executor.args)
	}
	if _, err = service.Logs(context.Background(), "p", 5000); err != nil {
		t.Fatal(err)
	}
	if executor.args[len(executor.args)-2] != "2000" || executor.args[len(executor.args)-1] != "web" {
		t.Fatalf("log args: %#v", executor.args)
	}
}

func TestNotConfigured(t *testing.T) {
	service := NewWithExecutor(appDB(t), &fakeExecutor{})
	if _, err := service.Status(context.Background(), "p"); err != ErrNotConfigured {
		t.Fatalf("got %v", err)
	}
}

func TestLifecycleActionUsesAdapterSnapshot(t *testing.T) {
	db := appDB(t)
	_, err := db.Exec(`INSERT INTO deployments(project_id,status,trigger_type,branch,created_at,workspace_path,application_config_json) VALUES('p','succeeded','manual','main',?,'/work/one','{"adapter":"systemd","unit":"app.service"}')`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{}
	service := NewWithExecutor(db, executor)
	if _, err = service.Action(context.Background(), "p", "restart"); err != nil {
		t.Fatal(err)
	}
	want := []string{"--user", "restart", "app.service"}
	if executor.name != "systemctl" || !reflect.DeepEqual(executor.args, want) {
		t.Fatalf("command: %s %#v", executor.name, executor.args)
	}
}
