package providerstatus

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/pipelineconfig"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
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
type job struct {
	id, deployment               int64
	attempts                     int
	project, status, sha, config string
}

func New(db *sql.DB, box *secret.Box, logger *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{db: db, box: box, client: &http.Client{Timeout: 10 * time.Second}, logger: logger, ctx: ctx, cancel: cancel}
}
func (m *Manager) Start() {
	_, _ = m.db.Exec(`UPDATE commit_status_deliveries SET status='pending' WHERE status='delivering'`)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			m.drain()
			select {
			case <-m.ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
func (m *Manager) Stop() { m.cancel(); m.wg.Wait() }
func (m *Manager) drain() {
	for {
		j, ok, err := m.claim()
		if err != nil || !ok {
			return
		}
		err = m.send(j)
		if err == nil {
			_, _ = m.db.Exec(`UPDATE commit_status_deliveries SET status='delivered',last_error='' WHERE id=?`, j.id)
		} else {
			msg := err.Error()
			if len(msg) > 500 {
				msg = msg[:500]
			}
			state := "pending"
			if j.attempts >= 5 {
				state = "failed"
			}
			_, _ = m.db.Exec(`UPDATE commit_status_deliveries SET status=?,next_attempt_at=?,last_error=? WHERE id=?`, state, time.Now().UTC().Add(time.Duration(1<<min(j.attempts, 6))*time.Minute).Format(time.RFC3339Nano), msg, j.id)
			m.logger.Warn("commit status delivery failed", "deployment", j.deployment, "error", msg)
		}
	}
}
func (m *Manager) claim() (job, bool, error) {
	tx, err := m.db.BeginTx(m.ctx, nil)
	if err != nil {
		return job{}, false, err
	}
	defer tx.Rollback()
	var j job
	err = tx.QueryRow(`SELECT q.id,q.deployment_id,q.attempts,d.project_id,q.deployment_status,d.commit_sha,d.commit_status_config_json FROM commit_status_deliveries q JOIN deployments d ON d.id=q.deployment_id WHERE q.status='pending' AND datetime(q.next_attempt_at)<=datetime(?) ORDER BY q.id LIMIT 1`, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&j.id, &j.deployment, &j.attempts, &j.project, &j.status, &j.sha, &j.config)
	if errors.Is(err, sql.ErrNoRows) {
		return job{}, false, nil
	}
	if err != nil {
		return job{}, false, err
	}
	res, err := tx.Exec(`UPDATE commit_status_deliveries SET status='delivering',attempts=attempts+1 WHERE id=? AND status='pending'`, j.id)
	if err != nil {
		return job{}, false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return job{}, false, nil
	}
	j.attempts++
	if err = tx.Commit(); err != nil {
		return job{}, false, err
	}
	return j, true, nil
}
func (m *Manager) send(j job) error {
	var cfg pipelineconfig.CommitStatus
	if err := json.Unmarshal([]byte(j.config), &cfg); err != nil {
		return err
	}
	var cipher []byte
	var isSecret bool
	if err := m.db.QueryRow(`SELECT cipher_value,is_secret FROM deployment_variables WHERE deployment_id=? AND name=?`, j.deployment, cfg.TokenVariable).Scan(&cipher, &isSecret); err != nil {
		return err
	}
	if !isSecret {
		return errors.New("provider token variable must be a Secret")
	}
	token, err := m.box.Decrypt(cipher, "project:"+j.project+":variable:"+cfg.TokenVariable)
	if err != nil {
		return err
	}
	state := map[string]string{"queued": "pending", "succeeded": "success", "failed": "failure", "cancelled": "error", "timed_out": "error"}[j.status]
	var endpoint string
	var payload any
	if cfg.Provider == "github" {
		endpoint = strings.TrimRight(cfg.APIBase, "/") + "/repos/" + strings.Trim(cfg.Repository, "/") + "/statuses/" + j.sha
		payload = map[string]string{"state": state, "context": cfg.Context, "description": "mini-ci-cd deployment " + j.status}
	} else {
		endpoint = strings.TrimRight(cfg.APIBase, "/") + "/projects/" + url.PathEscape(cfg.Repository) + "/statuses/" + j.sha
		gitlab := map[string]string{"pending": "pending", "success": "success", "failure": "failed", "error": "canceled"}[state]
		payload = map[string]string{"state": gitlab, "name": cfg.Context}
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(m.ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Provider == "github" {
		req.Header.Set("Authorization", "Bearer "+string(token))
		req.Header.Set("Accept", "application/vnd.github+json")
	} else {
		req.Header.Set("PRIVATE-TOKEN", string(token))
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
