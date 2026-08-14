package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/auth"
)

type auditResponse struct {
	http.ResponseWriter
	status int
}

func (w *auditResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *auditResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.ResponseWriter.Write(p)
}
func (w *auditResponse) Flush() {
	if w.status == 0 {
		w.status = 200
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) audit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		var userID, username string
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			_ = s.db.QueryRowContext(r.Context(), `SELECT u.id,u.username FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id_hash=?`, auth.HashSessionToken(cookie.Value)).Scan(&userID, &username)
		}
		recorder := &auditResponse{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = 200
		}
		action := strings.ToLower(r.Method) + " " + r.Pattern
		if r.Pattern == "" {
			action = strings.ToLower(r.Method) + " " + r.URL.Path
		}
		_, err := s.db.ExecContext(context.Background(), `INSERT INTO audit_events(user_id,username,action,target,status_code,ip_address,user_agent,created_at) VALUES(NULLIF(?,''),?,?,?,?,?,?,?)`, userID, username, action, r.URL.Path, status, s.clientIP(r), r.UserAgent(), time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			s.logger.Warn("write audit event", "error", err)
		}
	})
}

func (s *Server) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,COALESCE(user_id,''),username,action,target,status_code,ip_address,user_agent,created_at FROM audit_events ORDER BY id DESC LIMIT 500`)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not list audit events.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var uid, name, action, target, ip, agent, created string
		var status int
		if rows.Scan(&id, &uid, &name, &action, &target, &status, &ip, &agent, &created) != nil {
			writeError(w, 500, "internal_error", "Could not list audit events.")
			return
		}
		items = append(items, map[string]any{"id": id, "userId": uid, "username": name, "action": action, "target": target, "statusCode": status, "ipAddress": ip, "userAgent": agent, "createdAt": created})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
