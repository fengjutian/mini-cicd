package server

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/project"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
)

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	items, err := s.projects.List(r.Context())
	if err != nil {
		s.projectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	item, err := s.projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.projectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in project.Input
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := s.projects.Create(r.Context(), in)
	if err != nil {
		s.projectError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	var in project.Input
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := s.projects.Update(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.projectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) archiveProject(w http.ResponseWriter, r *http.Request) {
	if err := s.projects.Archive(r.Context(), r.PathValue("id")); err != nil {
		s.projectError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) listVariables(w http.ResponseWriter, r *http.Request) {
	items, err := s.projects.ListVariables(r.Context(), r.PathValue("id"))
	if err != nil {
		s.projectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) putVariable(w http.ResponseWriter, r *http.Request) {
	var in project.VariableInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	in.Name = r.PathValue("name")
	item, err := s.projects.PutVariable(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.projectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) deleteVariable(w http.ResponseWriter, r *http.Request) {
	if err := s.projects.DeleteVariable(r.Context(), r.PathValue("id"), r.PathValue("name")); err != nil {
		s.projectError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) projectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not_found", "Project or variable was not found.")
	case errors.Is(err, secret.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "secret_unavailable", err.Error())
	default:
		if containsConstraint(err) {
			writeError(w, http.StatusConflict, "conflict", "A project or variable with that identifier already exists.")
		} else {
			writeError(w, http.StatusUnprocessableEntity, "invalid_project", err.Error())
		}
	}
}
func containsConstraint(err error) bool {
	return err != nil && (isUniqueError(err) || containsFold(err.Error(), "constraint"))
}
func containsFold(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if equalFoldASCII(value[i:i+len(part)], part) {
			return true
		}
	}
	return false
}
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 32
		}
		if y >= 'A' && y <= 'Z' {
			y += 32
		}
		if x != y {
			return false
		}
	}
	return true
}
