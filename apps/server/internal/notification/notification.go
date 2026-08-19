package notification

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/pipelineconfig"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
)

type Manager struct {
	db     *sql.DB
	box    *secret.Box
	client *http.Client
	logger *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type delivery struct {
	id, deploymentID                                                int64
	projectID, projectName, status, commitSHA, errorSummary, config string
	attempts                                                        int
}

func New(db *sql.DB, box *secret.Box, logger *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{db: db, box: box, client: &http.Client{Timeout: 10 * time.Second}, logger: logger, ctx: ctx, cancel: cancel}
}

func (m *Manager) Start() {
	_, _ = m.db.Exec(`UPDATE notification_deliveries SET status='pending' WHERE status='delivering'`)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			m.drain()
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (m *Manager) Stop() { m.cancel(); m.wg.Wait() }

func (m *Manager) drain() {
	for {
		d, ok, err := m.claim(m.ctx)
		if err != nil {
			if m.ctx.Err() == nil {
				m.logger.Error("claim notification", "error", err)
			}
			return
		}
		if !ok {
			return
		}
		err = m.deliver(m.ctx, d)
		if err == nil {
			_, err = m.db.ExecContext(m.ctx, `UPDATE notification_deliveries SET status='delivered',delivered_at=?,last_error='' WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), d.id)
		} else {
			message := err.Error()
			if len(message) > 500 {
				message = message[:500]
			}
			if d.attempts >= 5 {
				_, _ = m.db.ExecContext(m.ctx, `UPDATE notification_deliveries SET status='failed',last_error=? WHERE id=?`, message, d.id)
			} else {
				delay := time.Duration(1<<min(d.attempts, 6)) * time.Minute
				_, _ = m.db.ExecContext(m.ctx, `UPDATE notification_deliveries SET status='pending',next_attempt_at=?,last_error=? WHERE id=?`, time.Now().UTC().Add(delay).Format(time.RFC3339Nano), message, d.id)
			}
			m.logger.Warn("deployment notification failed", "deployment", d.deploymentID, "attempt", d.attempts, "error", message)
		}
	}
}

func (m *Manager) claim(ctx context.Context) (delivery, bool, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return delivery{}, false, err
	}
	defer tx.Rollback()
	var d delivery
	err = tx.QueryRowContext(ctx, `SELECT n.id,n.deployment_id,n.attempts,d.project_id,p.name,d.status,COALESCE(d.commit_sha,''),d.error_summary,d.notification_config_json FROM notification_deliveries n JOIN deployments d ON d.id=n.deployment_id JOIN projects p ON p.id=d.project_id WHERE n.status='pending' AND datetime(n.next_attempt_at)<=datetime(?) ORDER BY n.id LIMIT 1`, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&d.id, &d.deploymentID, &d.attempts, &d.projectID, &d.projectName, &d.status, &d.commitSHA, &d.errorSummary, &d.config)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery{}, false, nil
	}
	if err != nil {
		return delivery{}, false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='delivering',attempts=attempts+1 WHERE id=? AND status='pending'`, d.id)
	if err != nil {
		return delivery{}, false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return delivery{}, false, nil
	}
	d.attempts++
	if err = tx.Commit(); err != nil {
		return delivery{}, false, err
	}
	return d, true, nil
}

func (m *Manager) deliver(ctx context.Context, d delivery) error {
	var configs []pipelineconfig.Notification
	if err := json.Unmarshal([]byte(d.config), &configs); err != nil {
		return err
	}
	for _, cfg := range configs {
		if !contains(cfg.Events, d.status) {
			continue
		}
		endpoint, err := m.secretURL(ctx, d.projectID, d.deploymentID, cfg.URLVariable)
		if err != nil {
			return fmt.Errorf("%s: %w", cfg.Name, err)
		}
		payload := map[string]any{"event": "deployment." + d.status, "notification": cfg.Name, "deploymentId": d.deploymentID, "projectId": d.projectID, "projectName": d.projectName, "status": d.status, "commitSha": d.commitSHA, "errorSummary": d.errorSummary}
		raw, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "mini-ci-cd/notification")
		resp, err := m.client.Do(req)
		if err != nil {
			return fmt.Errorf("%s: %w", cfg.Name, err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("%s: webhook returned %d: %s", cfg.Name, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
	return nil
}

func (m *Manager) secretURL(ctx context.Context, projectID string, deploymentID int64, name string) (string, error) {
	var cipher []byte
	var secretValue bool
	err := m.db.QueryRowContext(ctx, `SELECT cipher_value,is_secret FROM deployment_variables WHERE deployment_id=? AND name=?`, deploymentID, name).Scan(&cipher, &secretValue)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("URL Secret snapshot was not found")
	}
	if err != nil {
		return "", err
	}
	if !secretValue || len(cipher) == 0 {
		return "", errors.New("URL variable must be a Secret")
	}
	plain, err := m.box.Decrypt(cipher, "project:"+projectID+":variable:"+name)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(string(plain))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("URL Secret must contain an HTTP or HTTPS URL")
	}
	return parsed.String(), nil
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
