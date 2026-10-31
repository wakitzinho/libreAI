package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"libreai/internal/store"
)

func (s *Server) handleExportChat(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireID(w, r)
	if !ok {
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "markdown"
	}
	if format != "markdown" && format != "md" && format != "json" {
		writeError(w, http.StatusBadRequest, "invalid_format", "format must be markdown or json.")
		return
	}
	chat, err := s.store.GetChat(r.Context(), id)
	if err != nil {
		if !s.handleStoreError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	messages, err := s.store.ListMessages(r.Context(), id)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var project *store.Project
	if chat.ProjectID != nil {
		if p, err := s.store.GetProject(r.Context(), *chat.ProjectID); err == nil {
			project = &p
		}
	}
	providerName := chat.ProviderID
	if p, ok := s.registry().Provider(chat.ProviderID); ok {
		providerName = p.DisplayName
	}
	slug := fileNameSlug(chat.Title)
	if slug == "" {
		slug = "chat"
	}
	base := "libreai-" + slug + "-" + chat.ID[:8]

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+base+`.json"`)
		payload := map[string]any{
			"exported_at": time.Now().UTC().Format(time.RFC3339),
			"chat":        toChatDTO(chat, s.cfg.RetentionDays),
			"provider":    map[string]string{"id": chat.ProviderID, "name": providerName},
			"model":       chat.ModelID,
			"messages":    toMessageDTOs(messages),
		}
		if project != nil {
			payload["project"] = toProjectDTO(*project)
		} else {
			payload["project"] = nil
		}
		body, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			s.internalError(w, err)
			return
		}
		body = append(body, '\n')
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+base+`.md"`)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", chat.Title)
	fmt.Fprintf(&b, "- Exported: %s\n", time.Now().UTC().Format(time.RFC3339))
	if project != nil {
		fmt.Fprintf(&b, "- Project: %s\n", project.Name)
	}
	fmt.Fprintf(&b, "- Provider: %s (%s)\n", providerName, chat.ProviderID)
	fmt.Fprintf(&b, "- Model: %s\n", chat.ModelID)
	fmt.Fprintf(&b, "- Created: %s\n", isoTime(chat.CreatedAt))
	fmt.Fprintf(&b, "- Last activity: %s\n", isoTime(chat.LastActivityAt))
	fmt.Fprintf(&b, "- Expires: %s\n\n---\n", isoTime(chat.LastActivityAt+int64(s.cfg.RetentionDays)*24*60*60*1000))
	for _, m := range messages {
		role := "User"
		if m.Role == "assistant" {
			role = "Assistant"
		} else if m.Role == "system" {
			role = "System"
		}
		fmt.Fprintf(&b, "\n## %s · %s\n\n", role, isoTime(m.CreatedAt))
		b.WriteString(m.Content)
		if sources := parseSources(m.Sources); len(sources) > 0 {
			b.WriteString("\n\n**Sources**\n\n")
			for _, source := range sources {
				title := strings.NewReplacer("[", "(", "]", ")", "\r", " ", "\n", " ").Replace(source.Title)
				if strings.ContainsAny(source.URL, "<> \t\r\n") {
					fmt.Fprintf(&b, "- %s — %s\n", title, source.URL)
					continue
				}
				fmt.Fprintf(&b, "- [%s](<%s>)\n", title, source.URL)
			}
		}
		if m.Status != "completed" {
			fmt.Fprintf(&b, "\n\n> Status: %s", m.Status)
			if m.Error != "" {
				fmt.Fprintf(&b, " — %s", m.Error)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	_, _ = w.Write([]byte(b.String()))
}

func fileNameSlug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == ' ' || r == '-' || r == '_':
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
		if b.Len() >= 60 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
