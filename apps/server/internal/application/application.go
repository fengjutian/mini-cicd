package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	db   *sql.DB
	exec Executor
}

func New(db *sql.DB) *Service                                { return &Service{db: db, exec: commandExecutor{}} }
func NewWithExecutor(db *sql.DB, executor Executor) *Service { return &Service{db: db, exec: executor} }

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
		} else {
			args = append(args, "logs", "--no-color", "--tail", strconv.Itoa(lines))
			args = append(args, app.Services...)
		}
		return workspace, "docker", args, nil
	case "systemd":
		if action == "status" {
			return "", "systemctl", []string{"--user", "show", app.Unit, "--property=ActiveState,SubState,MainPID", "--no-pager"}, nil
		}
		return "", "journalctl", []string{"--user-unit", app.Unit, "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso"}, nil
	case "pm2":
		if action == "status" {
			return "", "pm2", []string{"describe", app.ProcessName}, nil
		}
		return "", "pm2", []string{"logs", app.ProcessName, "--nostream", "--lines", strconv.Itoa(lines), "--raw"}, nil
	default:
		return "", "", nil, fmt.Errorf("unsupported application adapter %q", app.Adapter)
	}
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
