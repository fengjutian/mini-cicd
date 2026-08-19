package server

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"
)

type scheduleInput struct {
	Name            string            `json:"name"`
	Environment     string            `json:"environment"`
	IntervalMinutes int               `json:"intervalMinutes"`
	Priority        int               `json:"priority"`
	CancelPrevious  bool              `json:"cancelPrevious"`
	Parameters      map[string]string `json:"parameters"`
	Enabled         *bool             `json:"enabled"`
	NextRunAt       string            `json:"nextRunAt"`
}

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,environment,interval_minutes,priority,cancel_previous,parameters_json,enabled,next_run_at,last_run_at,last_deployment_id,last_error,created_at FROM deployment_schedules WHERE project_id=? ORDER BY id`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "schedule_query_failed", "Could not read schedules.")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, env, raw, next, lastErr, created string
		var interval, priority int
		var cancel, enabled bool
		var lastRun *string
		var lastDeployment *int64
		if err = rows.Scan(&id, &name, &env, &interval, &priority, &cancel, &raw, &enabled, &next, &lastRun, &lastDeployment, &lastErr, &created); err != nil {
			writeError(w, 500, "schedule_query_failed", "Could not read schedules.")
			return
		}
		var params map[string]string
		_ = json.Unmarshal([]byte(raw), &params)
		items = append(items, map[string]any{"id": id, "name": name, "environment": env, "intervalMinutes": interval, "priority": priority, "cancelPrevious": cancel, "parameters": params, "enabled": enabled, "nextRunAt": next, "lastRunAt": lastRun, "lastDeploymentId": lastDeployment, "lastError": lastErr, "createdAt": created})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	var in scheduleInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if err := validateSchedule(&in); err != "" {
		writeError(w, 422, "invalid_schedule", err)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	next := time.Now().UTC().Add(time.Duration(in.IntervalMinutes) * time.Minute)
	if in.NextRunAt != "" {
		parsed, e := time.Parse(time.RFC3339, in.NextRunAt)
		if e != nil {
			writeError(w, 422, "invalid_schedule", "nextRunAt must be RFC3339.")
			return
		}
		next = parsed.UTC()
	}
	raw, _ := json.Marshal(in.Parameters)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO deployment_schedules(project_id,name,environment,interval_minutes,priority,cancel_previous,parameters_json,enabled,next_run_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, r.PathValue("id"), in.Name, in.Environment, in.IntervalMinutes, in.Priority, in.CancelPrevious, raw, enabled, next.Format(time.RFC3339Nano), now, now)
	if err != nil {
		writeError(w, 409, "schedule_create_failed", err.Error())
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "nextRunAt": next})
}

func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	var enabled struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &enabled); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE deployment_schedules SET enabled=?,updated_at=? WHERE id=? AND project_id=?`, enabled.Enabled, time.Now().UTC().Format(time.RFC3339Nano), r.PathValue("scheduleID"), r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "schedule_update_failed", err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		writeError(w, 404, "not_found", "Schedule was not found.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM deployment_schedules WHERE id=? AND project_id=?`, r.PathValue("scheduleID"), r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "schedule_delete_failed", err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		writeError(w, 404, "not_found", "Schedule was not found.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateSchedule(in *scheduleInput) string {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_. -]{0,63}$`).MatchString(in.Name) {
		return "Schedule name is invalid."
	}
	if in.Environment == "" {
		in.Environment = "production"
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`).MatchString(in.Environment) || in.IntervalMinutes < 1 || in.IntervalMinutes > 525600 || in.Priority < -100 || in.Priority > 100 || len(in.Parameters) > 20 {
		return "Schedule options are invalid."
	}
	v := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	for k, x := range in.Parameters {
		if !v.MatchString(k) || len(x) > 4096 {
			return "Schedule parameters are invalid."
		}
	}
	return ""
}

func (s *Server) startScheduler() {
	s.webhookWG.Add(1)
	go func() {
		defer s.webhookWG.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			s.runDueSchedule()
			select {
			case <-s.webhookCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (s *Server) runDueSchedule() {
	ctx, cancel := context.WithTimeout(s.webhookCtx, 30*time.Second)
	defer cancel()
	var id int64
	var project, environment, raw string
	var interval, priority int
	var cancelPrevious bool
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT id,project_id,environment,interval_minutes,priority,cancel_previous,parameters_json FROM deployment_schedules WHERE enabled=1 AND datetime(next_run_at)<=datetime(?) ORDER BY next_run_at,id LIMIT 1`, now.Format(time.RFC3339Nano)).Scan(&id, &project, &environment, &interval, &priority, &cancelPrevious, &raw)
	if err != nil {
		return
	}
	next := now.Add(time.Duration(interval) * time.Minute).Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE deployment_schedules SET next_run_at=?,last_run_at=?,updated_at=? WHERE id=?`, next, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), id); err != nil {
		return
	}
	if err = tx.Commit(); err != nil {
		return
	}
	d, err := s.deps.CreateForEnvironment(ctx, project, "manual", environment)
	if err != nil {
		_, _ = s.db.Exec(`UPDATE deployment_schedules SET last_error=? WHERE id=?`, err.Error(), id)
		return
	}
	var params map[string]string
	_ = json.Unmarshal([]byte(raw), &params)
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE deployments SET priority=?,parameters_json=? WHERE id=?`, priority, raw, d.ID)
	if err == nil {
		for name, value := range params {
			_, err = tx.ExecContext(ctx, `INSERT INTO deployment_variables(deployment_id,name,is_secret,plain_value,source_version) VALUES(?,?,0,?,0) ON CONFLICT(deployment_id,name) DO UPDATE SET is_secret=0,plain_value=excluded.plain_value,cipher_value=NULL,source_version=0`, d.ID, name, value)
			if err != nil {
				break
			}
		}
	}
	if err == nil && cancelPrevious {
		_, err = tx.ExecContext(ctx, `UPDATE deployments SET status='cancelled',finished_at=?,error_summary='superseded by scheduled deployment' WHERE project_id=? AND id<>? AND status='queued'`, now.Format(time.RFC3339Nano), project, d.ID)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE deployment_schedules SET last_deployment_id=?,last_error='' WHERE id=?`, d.ID, id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_, _ = s.db.Exec(`UPDATE deployment_schedules SET last_error=? WHERE id=?`, err.Error(), id)
		return
	}
	s.runner.Wake()
}
