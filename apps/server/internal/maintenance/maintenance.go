package maintenance

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"sync"
	"time"

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
	return m.pruneDeployments(ctx)
}
func (m *Manager) cleanWorkspaces(ctx context.Context, before string) error {
	rows, err := m.db.QueryContext(ctx, `SELECT id,project_id FROM deployments WHERE finished_at<? AND workspace_path IS NOT NULL AND status IN ('succeeded','failed','cancelled','timed_out')`, before)
	if err != nil {
		return err
	}
	items, err := pairs(rows)
	if err != nil {
		return err
	}
	for _, x := range items {
		if err = m.spaces.Remove(x.project, x.id); err != nil && !os.IsNotExist(err) {
			m.logger.Warn("remove workspace", "deployment", x.id, "error", err)
			continue
		}
		_, _ = m.db.ExecContext(ctx, `UPDATE deployments SET workspace_path=NULL WHERE id=?`, x.id)
	}
	return nil
}
func (m *Manager) cleanLogs(ctx context.Context, before string) error {
	rows, err := m.db.QueryContext(ctx, `SELECT id,project_id FROM deployments WHERE finished_at<? AND log_path IS NOT NULL AND status IN ('succeeded','failed','cancelled','timed_out')`, before)
	if err != nil {
		return err
	}
	items, err := pairs(rows)
	if err != nil {
		return err
	}
	for _, x := range items {
		if err = m.logs.Remove(x.project, x.id); err != nil && !os.IsNotExist(err) {
			m.logger.Warn("remove log", "deployment", x.id, "error", err)
			continue
		}
		_, _ = m.db.ExecContext(ctx, `UPDATE deployments SET log_path=NULL WHERE id=?`, x.id)
	}
	return nil
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
