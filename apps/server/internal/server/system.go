package server

import (
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

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
	overall := "ok"
	for _, c := range checks {
		if c.Status == "error" {
			overall = "error"
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": overall, "os": runtime.GOOS, "arch": runtime.GOARCH, "checks": checks})
}
