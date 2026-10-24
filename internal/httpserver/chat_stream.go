package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"libreai/internal/provider"
	"libreai/internal/store"
)

const maxMessageBytes = 100_000

type sendMessageRequest struct {
	Content       string `json:"content"`
	EditMessageID *int64 `json:"edit_message_id"`
	Regenerate    bool   `json:"regenerate"`
	ProviderID    string `json:"provider_id"`
	ModelID       string `json:"model_id"`
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	chatID, ok := s.requireID(w, r)
	if !ok {
		return
	}
	var req sendMessageRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		if !s.handleRequestError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if len(req.Content) > maxMessageBytes {
		writeError(w, http.StatusBadRequest, "message_too_large", "Message is too long (limit 100,000 bytes).")
		return
	}
	editMode := req.EditMessageID != nil
	if req.Regenerate && (editMode || req.Content != "") {
		writeError(w, http.StatusBadRequest, "invalid_request", "Choose one of new message, edit, or regenerate.")
		return
	}
	if editMode && req.Content == "" {
		writeError(w, http.StatusBadRequest, "empty_message", "Edited message content is required.")
		return
	}
	if !req.Regenerate && !editMode && req.Content == "" {
		writeError(w, http.StatusBadRequest, "empty_message", "Message content is required.")
		return
	}
	if req.EditMessageID != nil && *req.EditMessageID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_message", "Invalid message identifier.")
		return
	}

	chat, err := s.store.GetChat(r.Context(), chatID)
	if err != nil {
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	providerID, modelID, selectionErr := s.resolveSelection(r.Context(), chat, req)
	if selectionErr != nil {
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable", selectionErr.Error())
		return
	}

	// Only one generation per chat, and a bounded number overall.
	if !s.tryStartChat(chatID) {
		writeError(w, http.StatusConflict, "generation_in_progress", "A response is already being generated for this chat.")
		return
	}
	defer s.finishChat(chatID)
	select {
	case s.limiter <- struct{}{}:
		defer func() { <-s.limiter }()
	default:
		writeError(w, http.StatusTooManyRequests, "too_many_generations", "Too many responses are being generated. Stop another response or try again shortly.")
		return
	}

	var existedBefore bool
	switch {
	case req.Regenerate:
		messages, err := s.store.ListMessages(r.Context(), chatID)
		if err != nil {
			s.internalError(w, err)
			return
		}
		existedBefore = hasUserMessage(messages)
		if !existedBefore {
			writeError(w, http.StatusBadRequest, "nothing_to_regenerate", "This chat has no user message to regenerate from.")
			return
		}
		if _, err := s.store.DeleteLastAssistant(r.Context(), chatID); err != nil {
			s.internalError(w, err)
			return
		}
	case editMode:
		if _, err := s.store.EditUserMessage(r.Context(), chatID, *req.EditMessageID, req.Content); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "message_not_found", "The message to edit was not found in this chat.")
				return
			}
			if errors.Is(err, store.ErrInvalid) {
				writeError(w, http.StatusBadRequest, "invalid_message", err.Error())
				return
			}
			s.internalError(w, err)
			return
		}
	default:
		if _, err := s.store.AddUserMessage(r.Context(), chatID, req.Content); err != nil {
			s.internalError(w, err)
			return
		}
	}

	title := chat.Title
	if title == "New chat" {
		titleSource := req.Content
		if req.Regenerate {
			messages, _ := s.store.ListMessages(r.Context(), chatID)
			titleSource = firstUserContent(messages)
		}
		if titleSource != "" {
			newTitle := deriveTitle(titleSource)
			changed, err := s.store.SetTitleIfDefault(r.Context(), chatID, newTitle, "New chat")
			if err != nil {
				s.logger.Warn("could not set chat title", "error", s.registry().Redact(err.Error()))
			} else if changed {
				title = newTitle
			}
		}
	}

	if chat.ProviderID != providerID || chat.ModelID != modelID {
		updated, err := s.store.SetChatModel(r.Context(), chatID, providerID, modelID)
		if err != nil {
			s.internalError(w, err)
			return
		}
		chat = updated
	}

	messages, err := s.store.ListMessages(r.Context(), chatID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	contextMessages, err := buildContext(messages, s.cfg.MaxContextMessages, s.cfg.MaxContextBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "context_too_large", err.Error())
		return
	}
	if len(contextMessages) == 0 {
		writeError(w, http.StatusBadRequest, "nothing_to_generate", "There is no user message to respond to.")
		return
	}
	assistant, err := s.store.AddAssistantPlaceholder(r.Context(), chatID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	chat.Title = title
	s.streamCompletion(w, r, chat, assistant, contextMessages, providerID, modelID)
}

func (s *Server) resolveSelection(ctx context.Context, chat store.Chat, req sendMessageRequest) (string, string, error) {
	reqProvider := strings.ToLower(strings.TrimSpace(req.ProviderID))
	reqModel := strings.TrimSpace(req.ModelID)
	if (reqProvider == "") != (reqModel == "") {
		return "", "", errors.New("Choose both a provider and a model, or neither.")
	}
	providerID, modelID := reqProvider, reqModel
	if providerID == "" {
		providerID, modelID = chat.ProviderID, chat.ModelID
	}
	if providerID == "" {
		settings, err := s.effectiveDefaults(ctx)
		if err != nil {
			return "", "", errors.New("Could not load default provider settings.")
		}
		providerID, modelID = settings.DefaultProvider, settings.DefaultModel
	}
	if providerID == "" || modelID == "" {
		return "", "", errors.New("No provider or model is selected. Configure a provider in Settings and choose a model.")
	}
	if len(providerID) > 64 || len(modelID) > 200 {
		return "", "", errors.New("Provider or model identifier is too long.")
	}
	reg := s.registry()
	if _, ok := reg.GetClient(providerID); !ok {
		return "", "", errors.New("The selected provider is not configured or enabled. Check the .env settings.")
	}
	if !reg.ModelKnown(providerID, modelID) {
		return "", "", errors.New("The selected model is not currently known. Refresh models in Settings or add it to PROVIDER_*_MODELS.")
	}
	return providerID, modelID, nil
}

func hasUserMessage(messages []store.Message) bool {
	for _, m := range messages {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			return true
		}
	}
	return false
}

func firstUserContent(messages []store.Message) string {
	for _, m := range messages {
		if m.Role == "user" {
			return m.Content
		}
	}
	return ""
}

func deriveTitle(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	if len(runes) == 0 {
		return "New chat"
	}
	if len(runes) > 48 {
		return string(runes[:48]) + "..."
	}
	return string(runes)
}

// buildContext keeps the newest completed text messages within hard limits.
// Failed, interrupted, and cancelled assistant messages are not sent as
// context, but remain visible in the chat.
func buildContext(messages []store.Message, maxMessages, maxBytes int) ([]provider.ChatMessage, error) {
	if maxMessages <= 0 {
		maxMessages = 200
	}
	if maxBytes <= 0 {
		maxBytes = 400_000
	}
	filtered := make([]provider.ChatMessage, 0, len(messages))
	for _, m := range messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if m.Role == "assistant" && m.Status != "completed" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		filtered = append(filtered, provider.ChatMessage{Role: m.Role, Content: m.Content})
	}
	total := 0
	for _, m := range filtered {
		total += len(m.Content)
	}
	start := 0
	for start < len(filtered) && (len(filtered)-start > maxMessages || total > maxBytes) {
		total -= len(filtered[start].Content)
		start++
	}
	filtered = filtered[start:]
	for len(filtered) > 0 && filtered[0].Role != "user" {
		total -= len(filtered[0].Content)
		filtered = filtered[1:]
	}
	if len(filtered) == 0 {
		return nil, errors.New("There is no user message to send.")
	}
	if total > maxBytes {
		return nil, errors.New("The conversation context is too large to send. Start a new chat or shorten the message.")
	}
	if filtered[len(filtered)-1].Role != "user" {
		return nil, errors.New("The last message is not a user message.")
	}
	return filtered, nil
}

func (s *Server) tryStartChat(chatID string) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if _, busy := s.active[chatID]; busy {
		return false
	}
	s.active[chatID] = struct{}{}
	return true
}

func (s *Server) finishChat(chatID string) {
	s.activeMu.Lock()
	delete(s.active, chatID)
	s.activeMu.Unlock()
}

func (s *Server) streamCompletion(w http.ResponseWriter, r *http.Request, chat store.Chat, assistant store.Message, messages []provider.ChatMessage, providerID, modelID string) {
	reg := s.registry()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)

	meta := map[string]any{
		"chat_id":              chat.ID,
		"assistant_message_id": assistant.ID,
		"title":                chat.Title,
		"provider_id":          providerID,
		"model_id":             modelID,
	}
	if err := writeSSE(w, rc, "meta", meta); err != nil {
		s.persistFinal(assistant.ID, "", "cancelled", "Connection closed before generation started.")
		return
	}

	client, ok := reg.GetClient(providerID)
	if !ok {
		_ = writeSSE(w, rc, "error", map[string]any{"code": "provider_unavailable", "message": "Provider is no longer available.", "message_id": assistant.ID, "status": "error"})
		s.persistFinal(assistant.ID, "", "error", "Provider is no longer available.")
		return
	}

	timeout := reg.Timeout(providerID, s.cfg.UpstreamTimeout)
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	var content strings.Builder
	var writeErr error
	lastPersist := time.Now()
	onDelta := func(text string) error {
		content.WriteString(text)
		if err := writeSSE(w, rc, "delta", map[string]string{"text": text}); err != nil {
			writeErr = err
			return err
		}
		if time.Since(lastPersist) >= 900*time.Millisecond {
			lastPersist = time.Now()
			s.persistProgress(assistant.ID, content.String())
		}
		return nil
	}

	result, genErr := client.StreamChat(ctx, modelID, messages, onDelta)
	text := content.String()
	status := "completed"
	errorMessage := ""
	code := ""
	switch {
	case genErr == nil && text == "":
		status = "error"
		code = "empty_response"
		errorMessage = "The provider returned an empty response."
	case genErr == nil:
		status = "completed"
	case writeErr != nil || r.Context().Err() != nil || provider.IsCancelled(genErr):
		status = "cancelled"
		code = "cancelled"
		errorMessage = "Generation was stopped."
	case provider.SafeError(genErr) != "":
		status = "error"
		code = "provider_error"
		errorMessage = provider.SafeError(genErr)
	default:
		status = "error"
		code = "provider_error"
		errorMessage = "The provider request failed."
	}

	if genErr != nil && status != "cancelled" {
		s.logger.Warn("upstream generation failed", "provider", providerID, "model", modelID,
			"error", reg.Redact(genErr.Error()))
	}

	if !s.persistFinal(assistant.ID, text, status, errorMessage) {
		status = "error"
		code = "persist_failed"
		errorMessage = "The response could not be saved to the database."
	}

	if status == "completed" {
		_ = writeSSE(w, rc, "done", map[string]any{
			"message_id":     assistant.ID,
			"status":         "completed",
			"title":          chat.Title,
			"finish_reason":  result.FinishReason,
			"content_length": len(text),
		})
		return
	}
	_ = writeSSE(w, rc, "error", map[string]any{
		"code":       code,
		"message":    errorMessage,
		"message_id": assistant.ID,
		"status":     status,
	})
}

func (s *Server) persistProgress(messageID int64, content string) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := s.store.UpdateMessage(ctx, messageID, content, "streaming", ""); err != nil {
		s.logger.Warn("could not persist streaming progress", "error", s.registry().Redact(err.Error()))
	}
}

func (s *Server) persistFinal(messageID int64, content, status, errorMessage string) bool {
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		err := s.store.UpdateMessage(ctx, messageID, content, status, errorMessage)
		cancel()
		if err == nil {
			return true
		}
		s.logger.Error("could not persist final assistant message", "attempt", attempt+1, "error", s.registry().Redact(err.Error()))
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

func writeSSE(w http.ResponseWriter, rc *http.ResponseController, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	if err := rc.Flush(); err != nil {
		return err
	}
	return nil
}
