package server

import (
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func (s *Server) systemStorage(w http.ResponseWriter, r *http.Request) {
	parts := map[string]int64{"database": 0, "repositories": 0, "workspaces": 0, "logs": 0, "backups": 0}
	paths := map[string]string{"database": s.cfg.DatabasePath, "repositories": s.cfg.DataDir + string(os.PathSeparator) + "repositories", "workspaces": s.cfg.DataDir + string(os.PathSeparator) + "workspaces", "logs": s.cfg.DataDir + string(os.PathSeparator) + "logs", "backups": s.cfg.DataDir + string(os.PathSeparator) + "backups"}
	for key, root := range paths {
		_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				if info, e := d.Info(); e == nil {
					parts[key] += info.Size()
				}
			}
			return nil
		})
	}
	var deployments, projects int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM deployments`).Scan(&deployments)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM projects WHERE archived_at IS NULL`).Scan(&projects)
	var total int64
	for _, n := range parts {
		total += n
	}
	writeJSON(w, 200, map[string]any{"dataDirectory": s.cfg.DataDir, "totalBytes": total, "parts": parts, "deployments": deployments, "projects": projects})
}

func (s *Server) systemChecks(w http.ResponseWriter, r *http.Request) {
	type check struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	}
	checks := []check{}
	if err := s.db.PingContext(r.Context()); err != nil {
		checks = append(checks, check{"database", "error", err.Error()})
	} else {
		checks = append(checks, check{"database", "ok", "SQLite connection is healthy"})
	}
	if path, err := exec.LookPath("git"); err != nil {
		checks = append(checks, check{"git", "error", "git executable was not found"})
	} else {
		out, e := exec.CommandContext(r.Context(), path, "--version").CombinedOutput()
		status := "ok"
		if e != nil {
			status = "error"
		}
		checks = append(checks, check{"git", status, strings.TrimSpace(string(out))})
	}
	if _, err := exec.LookPath(s.cfg.Shell); err != nil {
		checks = append(checks, check{"shell", "error", s.cfg.Shell + " was not found"})
	} else {
		checks = append(checks, check{"shell", "ok", s.cfg.Shell})
	}
	testFile, err := os.CreateTemp(s.cfg.DataDir, ".write-check-")
	if err != nil {
		checks = append(checks, check{"data_directory", "error", err.Error()})
	} else {
		name := testFile.Name()
		_ = testFile.Close()
		_ = os.Remove(name)
		checks = append(checks, check{"data_directory", "ok", s.cfg.DataDir})
	}
	secretStatus, secretDetail := "ok", "master key configured"
	if !s.box.Available() {
		secretStatus, secretDetail = "warning", "master key is not configured; secrets cannot be stored"
	}
	checks = append(checks, check{"secret_encryption", secretStatus, secretDetail})
	runnerStatus, runnerDetail := "warning", "in-process Local Runner; use MINICICD_RUNNER_ENDPOINT in production"
	if s.cfg.RunnerEndpoint != "" {
		if info, err := os.Stat(s.cfg.RunnerEndpoint); err == nil && (info.Mode()&os.ModeSocket) != 0 {
			runnerStatus, runnerDetail = "ok", "isolated Runner socket: "+s.cfg.RunnerEndpoint
		} else {
			runnerStatus, runnerDetail = "error", "isolated Runner socket is unavailable: "+s.cfg.RunnerEndpoint
		}
	}
	checks = append(checks, check{"runner_isolation", runnerStatus, runnerDetail})
	overall := "ok"
	for _, c := range checks {
		if c.Status == "error" {
			overall = "error"
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": overall, "os": runtime.GOOS, "arch": runtime.GOARCH, "checks": checks})
}
