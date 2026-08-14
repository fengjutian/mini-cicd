package server

import (
	"encoding/hex"
	"net/http"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/auth"
)

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
		ConfirmPassword string `json:"confirmPassword"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if input.NewPassword != input.ConfirmPassword {
		writeError(w, 422, "password_mismatch", "Passwords do not match.")
		return
	}
	u := r.Context().Value(userKey).(user)
	var currentHash string
	if err := s.db.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id=?`, u.ID).Scan(&currentHash); err != nil || !auth.VerifyPassword(currentHash, input.CurrentPassword) {
		writeError(w, 401, "invalid_password", "Current password is incorrect.")
		return
	}
	newHash, err := auth.HashPassword(input.NewPassword)
	if err != nil {
		writeError(w, 422, "invalid_password", err.Error())
		return
	}
	cookie, _ := r.Cookie(sessionCookie)
	current := auth.HashSessionToken(cookie.Value)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not change password.")
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `UPDATE users SET password_hash=? WHERE id=?`, newHash, u.ID); err == nil {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=? AND id_hash<>?`, u.ID, current)
	}
	if err != nil || tx.Commit() != nil {
		writeError(w, 500, "internal_error", "Could not change password.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	u := r.Context().Value(userKey).(user)
	cookie, _ := r.Cookie(sessionCookie)
	current := hex.EncodeToString(auth.HashSessionToken(cookie.Value))
	rows, err := s.db.QueryContext(r.Context(), `SELECT lower(hex(id_hash)),created_at,expires_at,last_seen_at,ip_address,user_agent FROM sessions WHERE user_id=? AND expires_at>? ORDER BY last_seen_at DESC`, u.ID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		writeError(w, 500, "internal_error", "Could not list sessions.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, created, expires, seen, ip, agent string
		if rows.Scan(&id, &created, &expires, &seen, &ip, &agent) != nil {
			writeError(w, 500, "internal_error", "Could not list sessions.")
			return
		}
		items = append(items, map[string]any{"id": id, "createdAt": created, "expiresAt": expires, "lastSeenAt": seen, "ipAddress": ip, "userAgent": agent, "current": id == current})
	}
	writeJSON(w, 200, items)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) != 64 {
		writeError(w, 400, "invalid_session", "Session ID is invalid.")
		return
	}
	u := r.Context().Value(userKey).(user)
	result, err := s.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=? AND lower(hex(id_hash))=lower(?)`, u.ID, id)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not revoke session.")
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		writeError(w, 404, "not_found", "Session was not found.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
