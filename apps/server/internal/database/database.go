package database

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	dsn := "file:" + url.PathEscape(filepath.ToSlash(absPath)) + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(db *sql.DB) error {
	if err := bootstrapMigrationsTable(db); err != nil {
		return err
	}
	version, err := currentSchemaVersion(db)
	if err != nil {
		return err
	}
	if version < 1 {
		if err := applyV1(db); err != nil {
			return err
		}
	}
	if version < 2 {
		if err := applyV2(db); err != nil {
			return err
		}
	}
	if version < 3 {
		if err := applyV3(db); err != nil {
			return err
		}
	}
	if version < 4 {
		if err := applyV4(db); err != nil {
			return err
		}
	}
	if version < 5 {
		if err := applyV5(db); err != nil {
			return err
		}
	}
	if version < 6 {
		if err := applyV6(db); err != nil {
			return err
		}
	}
	if version < 7 {
		if err := applyV7(db); err != nil {
			return err
		}
	}
	if version < 8 {
		if err := applyV8(db); err != nil {
			return err
		}
	}
	if version < 9 {
		if err := applyV9(db); err != nil {
			return err
		}
	}
	return nil
}

func applyV9(db *sql.DB) error {
	columns := []struct{ table, name, definition string }{
		{"deployments", "artifact_config_json", "TEXT NOT NULL DEFAULT '{}'"},
		{"deployments", "artifact_path", "TEXT"},
		{"deployments", "artifact_source_deployment_id", "INTEGER REFERENCES deployments(id)"},
	}
	for _, c := range columns {
		if err := ensureColumn(db, c.table, c.name, c.definition); err != nil {
			return err
		}
	}
	return recordMigration(db, 9)
}

func applyV8(db *sql.DB) error {
	if err := ensureColumn(db, "deployments", "notification_config_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	const schema = `
CREATE TABLE IF NOT EXISTS notification_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    deployment_id INTEGER NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','delivering','delivered','failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    delivered_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notification_pending ON notification_deliveries(status,next_attempt_at,id);
CREATE TRIGGER IF NOT EXISTS enqueue_deployment_notification
AFTER UPDATE OF status ON deployments
WHEN OLD.status NOT IN ('succeeded','failed','cancelled','timed_out')
 AND NEW.status IN ('succeeded','failed','cancelled','timed_out')
 AND NEW.notification_config_json <> '[]'
BEGIN
  INSERT OR IGNORE INTO notification_deliveries(deployment_id,next_attempt_at,created_at)
  VALUES(NEW.id,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
END;`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	return recordMigration(db, 8)
}

func applyV7(db *sql.DB) error {
	if err := ensureColumn(db, "deployments", "application_config_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return err
	}
	return recordMigration(db, 7)
}

func applyV6(db *sql.DB) error {
	columns := []struct{ table, name, definition string }{
		{"deployments", "step_timeout_seconds", "INTEGER NOT NULL DEFAULT 900"},
		{"deployments", "deployment_timeout_seconds", "INTEGER NOT NULL DEFAULT 3600"},
		{"deployments", "config_source", "TEXT NOT NULL DEFAULT 'project'"},
		{"deployments", "config_snapshot", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range columns {
		if err := ensureColumn(db, c.table, c.name, c.definition); err != nil {
			return err
		}
	}
	return recordMigration(db, 6)
}

func bootstrapMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);`)
	return err
}

func currentSchemaVersion(db *sql.DB) (int, error) {
	var version int
	row := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`)
	if err := row.Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema_migrations: %w", err)
	}
	return version, nil
}

func recordMigration(db *sql.DB, version int) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, version)
	return err
}

func applyV1(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL COLLATE NOCASE UNIQUE,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role = 'owner'),
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id_hash BLOB PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	return recordMigration(db, 1)
}

func applyV2(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL COLLATE NOCASE UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    repository_url TEXT NOT NULL,
    branch TEXT NOT NULL DEFAULT 'main',
    auth_type TEXT NOT NULL DEFAULT 'none' CHECK(auth_type IN ('none','https','ssh')),
    git_username TEXT NOT NULL DEFAULT '',
    git_secret_cipher BLOB,
    ssh_private_key_cipher BLOB,
    ssh_known_hosts TEXT NOT NULL DEFAULT '',
    build_steps_json TEXT NOT NULL DEFAULT '[]',
    deploy_steps_json TEXT NOT NULL DEFAULT '[]',
    step_timeout_seconds INTEGER NOT NULL DEFAULT 900 CHECK(step_timeout_seconds > 0),
    deployment_timeout_seconds INTEGER NOT NULL DEFAULT 3600 CHECK(deployment_timeout_seconds > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    archived_at TEXT
);

CREATE TABLE IF NOT EXISTS project_variables (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    version INTEGER NOT NULL,
    is_secret INTEGER NOT NULL CHECK(is_secret IN (0,1)),
    plain_value TEXT,
    cipher_value BLOB,
    created_at TEXT NOT NULL,
    replaced_at TEXT,
    UNIQUE(project_id,name,version)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_project_variables_active ON project_variables(project_id,name) WHERE replaced_at IS NULL;

CREATE TABLE IF NOT EXISTS deployments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT NOT NULL REFERENCES projects(id),
    status TEXT NOT NULL CHECK(status IN ('queued','preparing','running','cancelling','cancelled','succeeded','failed','timed_out')),
    trigger_type TEXT NOT NULL CHECK(trigger_type IN ('manual','webhook','redeploy')),
    branch TEXT NOT NULL,
    commit_sha TEXT,
    commit_message TEXT NOT NULL DEFAULT '',
    commit_author TEXT NOT NULL DEFAULT '',
    error_summary TEXT NOT NULL DEFAULT '',
    cancel_requested_at TEXT,
    queued_at TEXT,
    started_at TEXT,
    finished_at TEXT,
    runner_id TEXT,
    workspace_path TEXT,
    log_path TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deployments_queue ON deployments(status,queued_at,id);
CREATE INDEX IF NOT EXISTS idx_deployments_project ON deployments(project_id,id DESC);

CREATE TABLE IF NOT EXISTS deployment_variables (
    deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    is_secret INTEGER NOT NULL CHECK(is_secret IN (0,1)),
    plain_value TEXT,
    cipher_value BLOB,
    source_version INTEGER NOT NULL,
    PRIMARY KEY(deployment_id,name)
);

CREATE TABLE IF NOT EXISTS deployment_steps (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    deployment_id INTEGER NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    phase TEXT NOT NULL,
    name TEXT NOT NULL,
    command_text TEXT NOT NULL,
    working_directory TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK(status IN ('pending','running','succeeded','failed','cancelled','timed_out','skipped')),
    exit_code INTEGER,
    started_at TEXT,
    finished_at TEXT,
    UNIQUE(deployment_id,sequence)
);

CREATE TABLE IF NOT EXISTS project_locks (
    project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    deployment_id INTEGER NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE,
    acquired_at TEXT NOT NULL
);
`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	columns := []struct{ table, name, definition string }{
		{"projects", "health_enabled", "INTEGER NOT NULL DEFAULT 0 CHECK(health_enabled IN (0,1))"},
		{"projects", "health_url", "TEXT NOT NULL DEFAULT ''"},
		{"projects", "health_initial_delay_seconds", "INTEGER NOT NULL DEFAULT 0"},
		{"projects", "health_timeout_seconds", "INTEGER NOT NULL DEFAULT 5"},
		{"projects", "health_retries", "INTEGER NOT NULL DEFAULT 3"},
		{"projects", "health_retry_interval_seconds", "INTEGER NOT NULL DEFAULT 2"},
		{"projects", "health_expected_status", "TEXT NOT NULL DEFAULT '200-299'"},
		{"projects", "auto_deploy", "INTEGER NOT NULL DEFAULT 0 CHECK(auto_deploy IN (0,1))"},
		{"projects", "webhook_provider", "TEXT NOT NULL DEFAULT 'github'"},
		{"projects", "webhook_secret_cipher", "BLOB"},
		{"deployments", "health_enabled", "INTEGER NOT NULL DEFAULT 0"},
		{"deployments", "health_url", "TEXT NOT NULL DEFAULT ''"},
		{"deployments", "health_initial_delay_seconds", "INTEGER NOT NULL DEFAULT 0"},
		{"deployments", "health_timeout_seconds", "INTEGER NOT NULL DEFAULT 5"},
		{"deployments", "health_retries", "INTEGER NOT NULL DEFAULT 3"},
		{"deployments", "health_retry_interval_seconds", "INTEGER NOT NULL DEFAULT 2"},
		{"deployments", "health_expected_status", "TEXT NOT NULL DEFAULT '200-299'"},
	}
	for _, column := range columns {
		if err := ensureColumn(db, column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	return recordMigration(db, 2)
}

func applyV3(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    delivery_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    accepted INTEGER NOT NULL CHECK(accepted IN (0,1)),
    rejection_reason TEXT NOT NULL DEFAULT '',
    deployment_id INTEGER REFERENCES deployments(id),
    commit_sha TEXT NOT NULL DEFAULT '',
    received_at TEXT NOT NULL,
    UNIQUE(provider,delivery_id)
);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_project ON webhook_deliveries(project_id,received_at DESC);
`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if err := ensureColumn(db, "webhook_deliveries", "commit_sha", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return recordMigration(db, 3)
}

func applyV4(db *sql.DB) error {
	columns := []struct{ name, definition string }{
		{"ip_address", "TEXT NOT NULL DEFAULT ''"},
		{"user_agent", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		if err := ensureColumn(db, "sessions", column.name, column.definition); err != nil {
			return err
		}
	}
	return recordMigration(db, 4)
}

func applyV5(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT,
    username TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    status_code INTEGER NOT NULL,
    ip_address TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_events_created ON audit_events(created_at DESC,id DESC);
`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	return recordMigration(db, 5)
}

// ensureColumn runs an idempotent ADD COLUMN only for the table/column
// pairs the migration system is allowed to manage. Both identifiers must
// match `safeIdent`; this keeps the PRAGMA and ALTER statements from being
// driven by unchecked input.
func ensureColumn(db *sql.DB, table, name, definition string) error {
	if !safeIdent(table) || !safeIdent(name) {
		return fmt.Errorf("refusing to ensure column with unsafe identifier: %s.%s", table, name)
	}
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var column, kind string
		var notNull, pk int
		var defaultValue any
		if err = rows.Scan(&cid, &column, &kind, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if column == name {
			found = true
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + definition)
	return err
}

// safeIdent returns true when the value can be used as a bare SQL identifier
// in the migration helpers. The rules are intentionally strict because the
// only callers ship a fixed set of names from this package.
func safeIdent(v string) bool {
	if v == "" {
		return false
	}
	for i, r := range v {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
