package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/pipelineconfig"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/procenv"
)

const maxOutput = 1 << 20

var ErrNotConfigured = errors.New("application adapter is not configured on a successful deployment")

type Result struct {
	Adapter      string `json:"adapter"`
	DeploymentID int64  `json:"deploymentId"`
	Output       string `json:"output"`
	CheckedAt    string `json:"checkedAt"`
}

type Executor interface {
	Run(context.Context, string, string, ...string) (string, error)
}

type Service struct {
	db           *sql.DB
	exec         Executor
	healthClient *http.Client
}

func New(db *sql.DB) *Service {
	return &Service{db: db, exec: commandExecutor{}, healthClient: &http.Client{Timeout: 10 * time.Second}}
}
func NewWithExecutor(db *sql.DB, executor Executor) *Service {
	return &Service{db: db, exec: executor, healthClient: &http.Client{Timeout: 10 * time.Second}}
}

func (s *Service) Action(ctx context.Context, projectID, action string) (Result, error) {
	if action != "start" && action != "stop" && action != "restart" {
		return Result{}, errors.New("invalid application action")
	}
	id, workspace, app, err := s.snapshot(ctx, projectID)
	if err != nil {
		return Result{}, err
	}
	dir, name, args, err := command(app, workspace, action, 0)
	if err != nil {
		return Result{}, err
	}
	out, err := s.exec.Run(ctx, dir, name, args...)
	result := Result{Adapter: app.Adapter, DeploymentID: id, Output: out, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err != nil {
		return result, err
	}
	if action != "stop" {
		if err = s.health(ctx, id); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Service) Status(ctx context.Context, projectID string) (Result, error) {
	id, workspace, app, err := s.snapshot(ctx, projectID)
	if err != nil {
		return Result{}, err
	}
	dir, name, args, err := command(app, workspace, "status", 0)
	if err != nil {
		return Result{}, err
	}
	out, err := s.exec.Run(ctx, dir, name, args...)
	return Result{Adapter: app.Adapter, DeploymentID: id, Output: out, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}, err
}

func (s *Service) Logs(ctx context.Context, projectID string, lines int) (Result, error) {
	if lines < 1 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}
	id, workspace, app, err := s.snapshot(ctx, projectID)
	if err != nil {
		return Result{}, err
	}
	dir, name, args, err := command(app, workspace, "logs", lines)
	if err != nil {
		return Result{}, err
	}
	out, err := s.exec.Run(ctx, dir, name, args...)
	return Result{Adapter: app.Adapter, DeploymentID: id, Output: out, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}, err
}

func (s *Service) snapshot(ctx context.Context, projectID string) (int64, string, pipelineconfig.Application, error) {
	var id int64
	var workspace, raw string
	err := s.db.QueryRowContext(ctx, `SELECT id,COALESCE(workspace_path,''),application_config_json FROM deployments WHERE project_id=? AND status='succeeded' AND application_config_json<>'{}' ORDER BY id DESC LIMIT 1`, projectID).Scan(&id, &workspace, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", pipelineconfig.Application{}, ErrNotConfigured
	}
	if err != nil {
		return 0, "", pipelineconfig.Application{}, err
	}
	var app pipelineconfig.Application
	if err = json.Unmarshal([]byte(raw), &app); err != nil || app.Adapter == "" {
		return 0, "", app, ErrNotConfigured
	}
	return id, workspace, app, nil
}

func command(app pipelineconfig.Application, workspace, action string, lines int) (string, string, []string, error) {
	switch app.Adapter {
	case "docker-compose":
		if workspace == "" {
			return "", "", nil, errors.New("deployment workspace is unavailable")
		}
		file := filepath.Join(workspace, filepath.FromSlash(app.ComposeFile))
		args := []string{"compose", "-f", file}
		if app.ProjectName != "" {
			args = append(args, "-p", app.ProjectName)
		}
		if action == "status" {
			args = append(args, "ps")
		} else if action == "logs" {
			args = append(args, "logs", "--no-color", "--tail", strconv.Itoa(lines))
			args = append(args, app.Services...)
		} else if action == "start" {
			args = append(args, "up", "-d")
			args = append(args, app.Services...)
		} else {
			args = append(args, action)
			args = append(args, app.Services...)
		}
		return workspace, "docker", args, nil
	case "systemd":
		if action == "status" {
			return "", "systemctl", []string{"--user", "show", app.Unit, "--property=ActiveState,SubState,MainPID", "--no-pager"}, nil
		}
		if action == "logs" {
			return "", "journalctl", []string{"--user-unit", app.Unit, "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso"}, nil
		}
		return "", "systemctl", []string{"--user", action, app.Unit}, nil
	case "pm2":
		if action == "status" {
			return "", "pm2", []string{"describe", app.ProcessName}, nil
		}
		if action == "logs" {
			return "", "pm2", []string{"logs", app.ProcessName, "--nostream", "--lines", strconv.Itoa(lines), "--raw"}, nil
		}
		if action == "start" {
			return workspace, "pm2", []string{"start", app.EcosystemFile, "--only", app.ProcessName, "--update-env"}, nil
		}
		args := []string{action, app.ProcessName}
		if action == "restart" {
			args = append(args, "--update-env")
		}
		return "", "pm2", args, nil
	default:
		return "", "", nil, fmt.Errorf("unsupported application adapter %q", app.Adapter)
	}
}

func (s *Service) health(ctx context.Context, deploymentID int64) error {
	var enabled bool
	var endpoint, expected string
	var retries, interval int
	if err := s.db.QueryRowContext(ctx, `SELECT health_enabled,health_url,health_retries,health_retry_interval_seconds,health_expected_status FROM deployments WHERE id=?`, deploymentID).Scan(&enabled, &endpoint, &retries, &interval, &expected); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	var low, high int
	if _, err := fmt.Sscanf(expected, "%d-%d", &low, &high); err != nil {
		return err
	}
	for i := 0; i < retries; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err == nil {
			resp, e := s.healthClient.Do(req)
			if e == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				if resp.StatusCode >= low && resp.StatusCode <= high {
					return nil
				}
			}
		}
		if i+1 < retries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(interval) * time.Second):
			}
		}
	}
	return errors.New("application action completed but health check failed")
}

type commandExecutor struct{}

func (commandExecutor) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, procenv.Safe()
	var output limitedBuffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	text := strings.TrimSpace(output.String())
	if ctx.Err() != nil {
		return text, errors.New("application command timed out")
	}
	if err != nil {
		return text, fmt.Errorf("application command failed: %s", text)
	}
	return text, nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := maxOutput - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return original, nil
}
