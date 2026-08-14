package database

import (
	"path/filepath"
	"testing"
)

func TestMigrateIsIdempotentAndIncludesV3(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	var versions int
	if err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version IN (1,2,3)`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 3 {
		t.Fatalf("versions=%d", versions)
	}
	var column int
	if err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name='webhook_secret_cipher'`).Scan(&column); err != nil || column != 1 {
		t.Fatalf("column=%d err=%v", column, err)
	}
	var table string
	if err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='webhook_deliveries'`).Scan(&table); err != nil {
		t.Fatal(err)
	}
}
