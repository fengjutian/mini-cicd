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
	const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

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

INSERT OR IGNORE INTO schema_migrations(version) VALUES (1);
`
	_, err := db.Exec(schema)
	if err != nil {
		return err
	}
	const v2 = `
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
    status TEXT NOT NULL CHECK(status IN ('resolving','queued','preparing','running','cancelling','cancelled','succeeded','failed','timed_out')),
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

INSERT OR IGNORE INTO schema_migrations(version) VALUES (2);
`
	_, err = db.Exec(v2)
	return err
}
