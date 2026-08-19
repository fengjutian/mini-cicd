package server

import "net/http"

func (s *Server) listNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT n.id,n.deployment_id,n.status,n.attempts,n.last_error,n.delivered_at,n.created_at FROM notification_deliveries n JOIN deployments d ON d.id=n.deployment_id WHERE d.project_id=? ORDER BY n.id DESC LIMIT 100`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "notification_query_failed", "Could not read notification deliveries.")
		return
	}
	defer rows.Close()
	type item struct {
		ID, DeploymentID int64
		Status           string
		Attempts         int
		LastError        string
		DeliveredAt      *string
		CreatedAt        string
	}
	items := []map[string]any{}
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.ID, &x.DeploymentID, &x.Status, &x.Attempts, &x.LastError, &x.DeliveredAt, &x.CreatedAt); err != nil {
			writeError(w, 500, "notification_query_failed", "Could not read notification deliveries.")
			return
		}
		items = append(items, map[string]any{"id": x.ID, "deploymentId": x.DeploymentID, "status": x.Status, "attempts": x.Attempts, "lastError": x.LastError, "deliveredAt": x.DeliveredAt, "createdAt": x.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
