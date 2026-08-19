package server

import "net/http"

func (s *Server) listCommitStatusDeliveries(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT q.id,q.deployment_id,q.deployment_status,q.status,q.attempts,q.last_error,q.created_at FROM commit_status_deliveries q JOIN deployments d ON d.id=q.deployment_id WHERE d.project_id=? ORDER BY q.id DESC LIMIT 100`, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "commit_status_query_failed", "Could not read commit status deliveries.")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, deploymentID int64
		var deploymentStatus, status, lastError, createdAt string
		var attempts int
		if err = rows.Scan(&id, &deploymentID, &deploymentStatus, &status, &attempts, &lastError, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "commit_status_query_failed", "Could not read commit status deliveries.")
			return
		}
		items = append(items, map[string]any{"id": id, "deploymentId": deploymentID, "deploymentStatus": deploymentStatus, "status": status, "attempts": attempts, "lastError": lastError, "createdAt": createdAt})
	}
	if err = rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "commit_status_query_failed", "Could not read commit status deliveries.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
