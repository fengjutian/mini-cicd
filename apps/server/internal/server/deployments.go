package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request) {
	environment := r.URL.Query().Get("environment")
	if environment == "" {
		environment = "production"
	}
	var input struct {
		Priority       int               `json:"priority"`
		CancelPrevious bool              `json:"cancelPrevious"`
		Parameters     map[string]string `json:"parameters"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	if input.Priority < -100 || input.Priority > 100 || len(input.Parameters) > 20 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_deployment_options", "Priority must be between -100 and 100 and parameters are limited to 20.")
		return
	}
	variable := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	for name, value := range input.Parameters {
		if !variable.MatchString(name) || len(value) > 4096 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_parameter", "Deployment parameter names or values are invalid.")
			return
		}
	}
	d, err := s.deps.CreateForEnvironment(r.Context(), r.PathValue("id"), "manual", environment)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	raw, _ := json.Marshal(input.Parameters)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `UPDATE deployments SET priority=?,parameters_json=? WHERE id=?`, input.Priority, raw, d.ID); err != nil {
		s.deploymentError(w, err)
		return
	}
	for name, value := range input.Parameters {
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO deployment_variables(deployment_id,name,is_secret,plain_value,source_version) VALUES(?,?,0,?,0) ON CONFLICT(deployment_id,name) DO UPDATE SET is_secret=0,plain_value=excluded.plain_value,cipher_value=NULL,source_version=0`, d.ID, name, value); err != nil {
			s.deploymentError(w, err)
			return
		}
	}
	if input.CancelPrevious {
		if _, err = tx.ExecContext(r.Context(), `UPDATE deployments SET status='cancelled',finished_at=?,error_summary='superseded by newer deployment' WHERE project_id=? AND id<>? AND status='queued'`, time.Now().UTC().Format(time.RFC3339Nano), d.ProjectID, d.ID); err != nil {
			s.deploymentError(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		s.deploymentError(w, err)
		return
	}
	d, _ = s.deps.Get(r.Context(), d.ID)
	s.runner.Wake()
	writeJSON(w, http.StatusCreated, d)
}
func (s *Server) approveDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentID(w, r)
	if !ok {
		return
	}
	u := r.Context().Value(userKey).(user)
	d, err := s.deps.Approve(r.Context(), id, u.Username)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	s.runner.Wake()
	writeJSON(w, http.StatusOK, d)
}
func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.List(r.Context(), r.PathValue("id"))
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentID(w, r)
	if !ok {
		return
	}
	d, err := s.deps.Get(r.Context(), id)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
func (s *Server) deploymentSteps(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentID(w, r)
	if !ok {
		return
	}
	items, err := s.deps.Steps(r.Context(), id)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) redeployDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentID(w, r)
	if !ok {
		return
	}
	source, err := s.deps.Get(r.Context(), id)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	d, err := s.deps.CreateAtCommit(r.Context(), source.ProjectID, "redeploy", source.CommitSHA)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	s.runner.Wake()
	writeJSON(w, http.StatusCreated, d)
}
func (s *Server) rollbackDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentID(w, r)
	if !ok {
		return
	}
	d, err := s.deps.Rollback(r.Context(), id)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	s.runner.Wake()
	writeJSON(w, http.StatusCreated, d)
}
func (s *Server) cancelDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentID(w, r)
	if !ok {
		return
	}
	if err := s.deps.Cancel(r.Context(), id); err != nil {
		s.deploymentError(w, err)
		return
	}
	// Short-circuit the runner's 250ms cancellation poll so the in-flight
	// step's context is cancelled as soon as the API call returns.
	s.runner.CancelByID(id)
	s.runner.Wake()
	d, err := s.deps.Get(r.Context(), id)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
func (s *Server) deploymentLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentID(w, r)
	if !ok {
		return
	}
	d, err := s.deps.Get(r.Context(), id)
	if err != nil {
		s.deploymentError(w, err)
		return
	}
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	ch, unsubscribe := s.logs.Subscribe(id)
	defer unsubscribe()
	history, err := s.logs.ReadAfter(d.ProjectID, id, after)
	if err != nil {
		writeError(w, 500, "log_error", "Could not read deployment logs.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "stream_unsupported", "Streaming is unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	writeEvent := func(e any, seq int64) {
		raw, _ := json.Marshal(e)
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", seq, raw)
		flusher.Flush()
	}
	for _, e := range history {
		writeEvent(e, e.Sequence)
		after = e.Sequence
	}
	if terminal(d.Status) {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, open := <-ch:
			if !open {
				return
			}
			if e.Sequence > after {
				writeEvent(e, e.Sequence)
				after = e.Sequence
			}
		case <-ticker.C:
			current, err := s.deps.Get(r.Context(), id)
			if err != nil || terminal(current.Status) {
				fmt.Fprint(w, ": complete\n\n")
				flusher.Flush()
				return
			}
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
func deploymentID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, 400, "invalid_id", "Deployment ID is invalid.")
		return 0, false
	}
	return id, true
}
func (s *Server) deploymentError(w http.ResponseWriter, err error) {
	if err == sql.ErrNoRows {
		writeError(w, 404, "not_found", "Deployment or project was not found.")
		return
	}
	writeError(w, 422, "deployment_error", err.Error())
}
func terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled" || status == "timed_out"
}
