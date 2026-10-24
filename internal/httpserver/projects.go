package httpserver

import (
	"errors"
	"net/http"

	"libreai/internal/store"
)

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": toProjectDTOs(projects)})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		if !s.handleRequestError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	p, err := s.store.CreateProject(r.Context(), req.Name)
	if err != nil {
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": toProjectDTO(p)})
}

func (s *Server) handleRenameProject(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		if !s.handleRequestError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	p, err := s.store.RenameProject(r.Context(), id, req.Name)
	if err != nil {
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": toProjectDTO(p)})
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireID(w, r)
	if !ok {
		return
	}
	moved, err := s.store.DeleteProject(r.Context(), id)
	if err != nil {
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moved_chats": moved})
}

func (s *Server) handleStoreError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "The requested item was not found.")
		return true
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
		return true
	default:
		return false
	}
}
