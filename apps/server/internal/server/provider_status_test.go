package server

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

func TestListCommitStatusDeliveriesIsProjectScoped(t *testing.T) {
	handler := newTestServer(t)
	s := handler.(*Server)
	setup := requestJSON(t, handler, http.MethodPost, "/api/v1/setup", map[string]string{"email": "owner@example.com", "username": "owner", "password": "correct horse battery staple", "confirmPassword": "correct horse battery staple"})
	cookie := setup.Result().Cookies()[0]
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range []string{"visible", "hidden"} {
		if _, err := s.db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,created_at,updated_at) VALUES(?,?,?,'https://example/repo','main',?,?)`, id, id, id, now, now); err != nil {
			t.Fatal(err)
		}
		res, err := s.db.Exec(`INSERT INTO deployments(project_id,status,trigger_type,branch,commit_sha,commit_status_config_json,created_at) VALUES(?,'succeeded','manual','main','0123456789012345678901234567890123456789','{"enabled":true}',?)`, id, now)
		if err != nil {
			t.Fatal(err)
		}
		deploymentID, _ := res.LastInsertId()
		if _, err = s.db.Exec(`UPDATE commit_status_deliveries SET status='failed',last_error=? WHERE deployment_id=?`, id+"-error", deploymentID); err != nil {
			t.Fatal(err)
		}
	}

	rec := requestJSON(t, handler, http.MethodGet, "/api/v1/projects/visible/commit-status-deliveries", nil, cookie)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("visible-error")) || bytes.Contains(rec.Body.Bytes(), []byte("hidden-error")) {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
}
