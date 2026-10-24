package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"libreai/internal/store"
)

func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(search) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_search", "Search query is too long.")
		return
	}
	chats, err := s.store.ListChats(r.Context(), search, 500)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": toChatDTOs(chats, s.cfg.RetentionDays)})
}

type createChatRequest struct {
	Title      string  `json:"title"`
	ProjectID  *string `json:"project_id"`
	ProviderID string  `json:"provider_id"`
	ModelID    string  `json:"model_id"`
}

func (s *Server) handleCreateChat(w http.ResponseWriter, r *http.Request) {
	var req createChatRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		if !s.handleRequestError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	providerID := strings.ToLower(strings.TrimSpace(req.ProviderID))
	modelID := strings.TrimSpace(req.ModelID)
	if (providerID == "") != (modelID == "") {
		writeError(w, http.StatusBadRequest, "invalid_selection", "Choose both a provider and a model, or neither.")
		return
	}
	if providerID == "" {
		settings, err := s.effectiveDefaults(r.Context())
		if err != nil {
			s.internalError(w, err)
			return
		}
		providerID, modelID = settings.DefaultProvider, settings.DefaultModel
	} else if err := s.validateSelection(providerID, modelID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_selection", err.Error())
		return
	}
	if req.ProjectID != nil {
		id := strings.TrimSpace(*req.ProjectID)
		if id == "" {
			req.ProjectID = nil
		} else {
			req.ProjectID = &id
		}
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "New chat"
	}
	chat, err := s.store.CreateChat(r.Context(), title, req.ProjectID, providerID, modelID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "project_not_found", "That project no longer exists.")
			return
		}
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"chat": toChatDTO(chat, s.cfg.RetentionDays)})
}

func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireID(w, r)
	if !ok {
		return
	}
	chat, err := s.store.GetChat(r.Context(), id)
	if err != nil {
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	s.writeChatDetail(w, r.Context(), chat)
}

func (s *Server) handleOpenChat(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireID(w, r)
	if !ok {
		return
	}
	chat, messages, err := s.store.OpenChat(r.Context(), id, store.NowMillis())
	if err != nil {
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	s.writeChatDetailWithMessages(w, r.Context(), chat, messages)
}

func (s *Server) writeChatDetail(w http.ResponseWriter, ctx context.Context, chat store.Chat) {
	messages, err := s.store.ListMessages(ctx, chat.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	s.writeChatDetailWithMessages(w, ctx, chat, messages)
}

func (s *Server) writeChatDetailWithMessages(w http.ResponseWriter, ctx context.Context, chat store.Chat, messages []store.Message) {
	detail := chatDetailDTO{
		Chat:     toChatDTO(chat, s.cfg.RetentionDays),
		Messages: toMessageDTOs(messages),
	}
	if chat.ProjectID != nil {
		if p, err := s.store.GetProject(ctx, *chat.ProjectID); err == nil {
			dto := toProjectDTO(p)
			detail.Project = &dto
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

type patchChatRequest struct {
	Title      *string `json:"title"`
	ProviderID *string `json:"provider_id"`
	ModelID    *string `json:"model_id"`
}

func (s *Server) handlePatchChat(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireID(w, r)
	if !ok {
		return
	}
	var req patchChatRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		if !s.handleRequestError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	if req.Title == nil && req.ProviderID == nil && req.ModelID == nil {
		writeError(w, http.StatusBadRequest, "empty_update", "No supported fields were provided.")
		return
	}
	if (req.ProviderID == nil) != (req.ModelID == nil) {
		writeError(w, http.StatusBadRequest, "invalid_selection", "Provider and model must be updated together.")
		return
	}
	var pid, mid string
	if req.ProviderID != nil {
		pid = strings.ToLower(strings.TrimSpace(*req.ProviderID))
		mid = strings.TrimSpace(*req.ModelID)
		if err := s.validateSelection(pid, mid); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_selection", err.Error())
			return
		}
	}
	chat, err := s.store.GetChat(r.Context(), id)
	if err != nil {
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	if req.Title != nil {
		chat, err = s.store.RenameChat(r.Context(), id, *req.Title)
		if err != nil {
			if !s.handleStoreError(w, err) {
				s.internalError(w, err)
			}
			return
		}
	}
	if req.ProviderID != nil {
		chat, err = s.store.SetChatModel(r.Context(), id, pid, mid)
		if err != nil {
			if !s.handleStoreError(w, err) {
				s.internalError(w, err)
			}
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat": toChatDTO(chat, s.cfg.RetentionDays)})
}

func (s *Server) handleDeleteChat(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteChat(r.Context(), id); err != nil {
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMoveChat(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireID(w, r)
	if !ok {
		return
	}
	var req struct {
		ProjectID json.RawMessage `json:"project_id"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		if !s.handleRequestError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	if len(req.ProjectID) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_project", "project_id is required (use null for unfiled).")
		return
	}
	var projectID *string
	if !bytes.Equal(bytes.TrimSpace(req.ProjectID), []byte("null")) {
		var pid string
		if err := json.Unmarshal(req.ProjectID, &pid); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_project", "project_id must be a string or null.")
			return
		}
		pid = strings.TrimSpace(pid)
		if pid == "" {
			writeError(w, http.StatusBadRequest, "invalid_project", "project_id must not be empty.")
			return
		}
		projectID = &pid
	}
	chat, err := s.store.MoveChat(r.Context(), id, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "project_not_found", "That chat or project no longer exists.")
			return
		}
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat": toChatDTO(chat, s.cfg.RetentionDays)})
}

func (s *Server) validateSelection(providerID, modelID string) error {
	if providerID == "" || modelID == "" {
		return errors.New("Choose both a provider and a model.")
	}
	if len(providerID) > 64 || len(modelID) > 200 {
		return errors.New("Provider or model identifier is too long.")
	}
	reg := s.registry()
	if _, ok := reg.GetClient(providerID); !ok {
		return errors.New("That provider is not configured or enabled.")
	}
	if !reg.ModelKnown(providerID, modelID) {
		return errors.New("That model is not currently known. Refresh models in Settings or add it to PROVIDER_*_MODELS.")
	}
	return nil
}
