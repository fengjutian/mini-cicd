package server

import (
	"net/http"
	"time"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339Nano)
	var projects, running, succeeded, failed int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM projects WHERE archived_at IS NULL`).Scan(&projects); err != nil {
		writeError(w, 500, "internal_error", "Could not load dashboard.")
		return
	}
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM deployments WHERE status IN ('preparing','running','cancelling')`).Scan(&running)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM deployments WHERE status='succeeded' AND finished_at>=?`, since).Scan(&succeeded)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM deployments WHERE status IN ('failed','timed_out') AND finished_at>=?`, since).Scan(&failed)
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects, "runningDeployments": running, "succeededLast24Hours": succeeded, "failedLast24Hours": failed})
}
