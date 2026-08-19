package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/application"
)

func (s *Server) applicationStatus(w http.ResponseWriter, r *http.Request) {
	result, err := s.applications.Status(r.Context(), r.PathValue("id"))
	if err != nil {
		s.applicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) applicationLogs(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("lines")
	lines, err := strconv.Atoi(raw)
	if raw != "" && (err != nil || lines < 1 || lines > 2000) {
		writeError(w, http.StatusBadRequest, "invalid_lines", "Lines must be between 1 and 2000.")
		return
	}
	result, err := s.applications.Logs(r.Context(), r.PathValue("id"), lines)
	if err != nil {
		s.applicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) applicationError(w http.ResponseWriter, err error) {
	if errors.Is(err, application.ErrNotConfigured) {
		writeError(w, http.StatusConflict, "application_not_configured", err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "application_command_failed", err.Error())
}
