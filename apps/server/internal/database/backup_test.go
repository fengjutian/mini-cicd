package database

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupAndRestore(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "active.db")
	db, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO users(id,email,username,password_hash,role,created_at) VALUES('u','owner@example.com','owner','hash','owner','now')`); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(dir, "backup.db")
	if err = Backup(db, backupPath); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(databasePath, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous, err := Restore(backupPath, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if previous == "" {
		t.Fatal("expected previous database to be preserved")
	}
	restored, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var count int
	if err = restored.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("restored users=%d err=%v", count, err)
	}
}
