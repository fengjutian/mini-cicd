package server

import (
	"fmt"
	"net/http"
)

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	statuses := []string{"queued", "preparing", "running", "succeeded", "failed", "cancelled", "timed_out"}
	for _, status := range statuses {
		var count int64
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE status=?`, status).Scan(&count)
		fmt.Fprintf(w, "minicicd_deployments_total{status=%q} %d\n", status, count)
	}
	var hits, misses, statusFailures, notificationFailures int64
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE cache_hit=1`).Scan(&hits)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE cache_hit=0`).Scan(&misses)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM commit_status_deliveries WHERE status='failed'`).Scan(&statusFailures)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM notification_deliveries WHERE status='failed'`).Scan(&notificationFailures)
	fmt.Fprintf(w, "minicicd_pipeline_cache_total{result=\"hit\"} %d\nminicicd_pipeline_cache_total{result=\"miss\"} %d\n", hits, misses)
	fmt.Fprintf(w, "minicicd_delivery_failures{kind=\"commit_status\"} %d\nminicicd_delivery_failures{kind=\"notification\"} %d\n", statusFailures, notificationFailures)
}
