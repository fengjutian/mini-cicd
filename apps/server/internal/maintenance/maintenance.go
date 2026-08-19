package maintenance

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/logstore"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/workspace"
)

type Manager struct {
	db                                         *sql.DB
	logs                                       *logstore.Store
	spaces                                     *workspace.Manager
	interval, workspaceRetention, logRetention time.Duration
	deploymentRetention                        int
	logger                                     *slog.Logger
	ctx                                        context.Context
	cancel                                     context.CancelFunc
	wg                                         sync.WaitGroup
	databasePath, backupDir                    string
	backupInterval                             time.Duration
	backupRetention                            int
	auditRetention                             time.Duration
	auditMaxEvents                             int
}

func (m *Manager) ConfigureAudit(retention time.Duration, maxEvents int) *Manager {
	m.auditRetention = retention
	m.auditMaxEvents = maxEvents
	return m
}

func (m *Manager) ConfigureBackups(databasePath, backupDir string, interval time.Duration, retention int) *Manager {
	m.databasePath = databasePath
	m.backupDir = backupDir
	m.backupInterval = interval
	m.backupRetention = retention
	return m
}

func New(db *sql.DB, logs *logstore.Store, spaces *workspace.Manager, interval, workspaceRetention, logRetention time.Duration, deploymentRetention int, logger *slog.Logger) *Manager {
	if interval <= 0 {
		interval = time.Hour
	}
	if workspaceRetention <= 0 {
		workspaceRetention = 24 * time.Hour
	}
	if logRetention <= 0 {
		logRetention = 30 * 24 * time.Hour
	}
	if deploymentRetention < 1 {
		deploymentRetention = 100
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{db: db, logs: logs, spaces: spaces, interval: interval, workspaceRetention: workspaceRetention, logRetention: logRetention, deploymentRetention: deploymentRetention, logger: logger, ctx: ctx, cancel: cancel}
}
func (m *Manager) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		if err := m.RunOnce(m.ctx); err != nil && m.ctx.Err() == nil {
			m.logger.Error("initial maintenance failed", "error", err)
		}
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				if err := m.RunOnce(m.ctx); err != nil {
					m.logger.Error("maintenance failed", "error", err)
				}
			}
		}
	}()
}
func (m *Manager) Stop() { m.cancel(); m.wg.Wait() }
func (m *Manager) RunOnce(ctx context.Context) error {
	now := time.Now().UTC()
	if _, err := m.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<=?`, now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := m.cleanWorkspaces(ctx, now.Add(-m.workspaceRetention).Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := m.cleanLogs(ctx, now.Add(-m.logRetention).Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := m.pruneDeployments(ctx); err != nil {
		return err
	}
	if err := m.pruneAudit(ctx, now); err != nil {
		return err
	}
	return m.automaticBackup(ctx, now)
}

func (m *Manager) pruneAudit(ctx context.Context, now time.Time) error {
	if m.auditRetention > 0 {
		if _, err := m.db.ExecContext(ctx, `DELETE FROM audit_events WHERE created_at<?`, now.Add(-m.auditRetention).Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if m.auditMaxEvents > 0 {
		_, err := m.db.ExecContext(ctx, `DELETE FROM audit_events WHERE id NOT IN (SELECT id FROM audit_events ORDER BY id DESC LIMIT ?)`, m.auditMaxEvents)
		return err
	}
	return nil
}
func (m *Manager) automaticBackup(ctx context.Context, now time.Time) error {
	if m.databasePath == "" || m.backupDir == "" || m.backupInterval <= 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(m.backupDir, 0o700); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(m.backupDir, "mini-cicd-*.db"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	if len(files) > 0 {
		if info, e := os.Stat(files[len(files)-1]); e == nil && now.Sub(info.ModTime()) < m.backupInterval {
			return nil
		}
	}
	name := filepath.Join(m.backupDir, "mini-cicd-"+now.Format("20060102T150405Z")+".db")
	if err = database.Backup(m.db, name); err != nil {
		return err
	}
	files = append(files, name)
	sort.Strings(files)
	for len(files) > m.backupRetention {
		old := files[0]
		files = files[1:]
		if filepath.Dir(old) == m.backupDir {
			if err = os.Remove(old); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	m.logger.Info("automatic database backup created", "path", name)
	return nil
}
func (m *Manager) cleanWorkspaces(ctx context.Context, before string) error {
	items, err := m.claimDeploymentPaths(ctx, "workspace_path", before)
	if err != nil {
		return err
	}
	for _, x := range items {
		if err := m.spaces.Remove(x.project, x.id); err != nil && !os.IsNotExist(err) {
			m.logger.Warn("remove workspace", "deployment", x.id, "error", err)
		}
	}
	return nil
}
func (m *Manager) cleanLogs(ctx context.Context, before string) error {
	items, err := m.claimDeploymentPaths(ctx, "log_path", before)
	if err != nil {
		return err
	}
	for _, x := range items {
		if err := m.logs.Remove(x.project, x.id); err != nil && !os.IsNotExist(err) {
			m.logger.Warn("remove log", "deployment", x.id, "error", err)
		}
	}
	return nil
}

// claimDeploymentPaths atomically nulls out the given path column on every
// terminal deployment finished before the cutoff, then returns the rows so
// the caller can safely delete the on-disk files. Clearing the column
// inside a transaction ensures the database never points to a file that
// might be re-claimed by the next cleanup pass while a previous run is
// still in the middle of removing it.
func (m *Manager) claimDeploymentPaths(ctx context.Context, column, before string) ([]pair, error) {
	if !safeColumnName(column) {
		return nil, fmt.Errorf("refusing to clean unknown deployment path column: %s", column)
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	extra := ""
	if column == "workspace_path" {
		// The latest successful adapter deployment is the source of truth for
		// Docker Compose status and logs, which require its snapshotted file.
		extra = ` AND id NOT IN (SELECT MAX(id) FROM deployments WHERE status='succeeded' AND application_config_json<>'{}' GROUP BY project_id)`
	}
	query := fmt.Sprintf(`SELECT id,project_id FROM deployments WHERE finished_at<? AND %s IS NOT NULL AND status IN ('succeeded','failed','cancelled','timed_out')%s`, column, extra)
	rows, err := tx.QueryContext(ctx, query, before)
	if err != nil {
		return nil, err
	}
	items, err := pairs(rows)
	if err != nil {
		return nil, err
	}
	for _, x := range items {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE deployments SET %s=NULL WHERE id=?`, column), x.id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func safeColumnName(name string) bool {
	switch name {
	case "workspace_path", "log_path":
		return true
	default:
		return false
	}
}
func (m *Manager) pruneDeployments(ctx context.Context) error {
	rows, err := m.db.QueryContext(ctx, `SELECT id,project_id FROM (SELECT id,project_id,status,ROW_NUMBER() OVER(PARTITION BY project_id ORDER BY id DESC) AS rn FROM deployments) WHERE rn>? AND status IN ('succeeded','failed','cancelled','timed_out')`, m.deploymentRetention)
	if err != nil {
		return err
	}
	items, err := pairs(rows)
	if err != nil {
		return err
	}
	for _, x := range items {
		_ = m.spaces.Remove(x.project, x.id)
		_ = m.logs.Remove(x.project, x.id)
		tx, e := m.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE webhook_deliveries SET accepted=0,rejection_reason='deployment pruned',deployment_id=NULL WHERE deployment_id=?`, x.id); e == nil {
			_, e = tx.ExecContext(ctx, `DELETE FROM deployments WHERE id=?`, x.id)
		}
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}

type pair struct {
	id      int64
	project string
}

func pairs(rows *sql.Rows) ([]pair, error) {
	defer rows.Close()
	out := []pair{}
	for rows.Next() {
		var x pair
		if err := rows.Scan(&x.id, &x.project); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
