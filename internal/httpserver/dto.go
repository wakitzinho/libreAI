package httpserver

import (
	"time"

	"libreai/internal/provider"
	"libreai/internal/store"
)

type projectDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type chatDTO struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	ProjectID      *string `json:"project_id"`
	ProviderID     string  `json:"provider_id"`
	ModelID        string  `json:"model_id"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	LastActivityAt string  `json:"last_activity_at"`
	ExpiresAt      string  `json:"expires_at"`
}

type messageDTO struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
}

type chatDetailDTO struct {
	Chat     chatDTO      `json:"chat"`
	Messages []messageDTO `json:"messages"`
	Project  *projectDTO  `json:"project,omitempty"`
}

type defaultsDTO struct {
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
}

type bootstrapDTO struct {
	Version       string          `json:"version"`
	RetentionDays int             `json:"retention_days"`
	Providers     []provider.Info `json:"providers"`
	Projects      []projectDTO    `json:"projects"`
	Chats         []chatDTO       `json:"chats"`
	Defaults      defaultsDTO     `json:"defaults"`
	HiddenModels  []string        `json:"hidden_models"`
	Limits        limitsDTO       `json:"limits"`
}

type limitsDTO struct {
	MaxConcurrentGenerations int `json:"max_concurrent_generations"`
}

type settingsDTO struct {
	Defaults      defaultsDTO `json:"defaults"`
	HiddenModels  []string    `json:"hidden_models"`
	RetentionDays int         `json:"retention_days"`
	Version       string      `json:"version"`
}

func isoTime(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func toProjectDTO(p store.Project) projectDTO {
	return projectDTO{ID: p.ID, Name: p.Name, CreatedAt: isoTime(p.CreatedAt), UpdatedAt: isoTime(p.UpdatedAt)}
}

func toChatDTO(c store.Chat, retentionDays int) chatDTO {
	expires := c.LastActivityAt + (int64(retentionDays) * 24 * 60 * 60 * 1000)
	return chatDTO{
		ID:             c.ID,
		Title:          c.Title,
		ProjectID:      c.ProjectID,
		ProviderID:     c.ProviderID,
		ModelID:        c.ModelID,
		CreatedAt:      isoTime(c.CreatedAt),
		UpdatedAt:      isoTime(c.UpdatedAt),
		LastActivityAt: isoTime(c.LastActivityAt),
		ExpiresAt:      isoTime(expires),
	}
}

func toMessageDTO(m store.Message) messageDTO {
	return messageDTO{
		ID: m.ID, Role: m.Role, Content: m.Content, Status: m.Status,
		Error: m.Error, CreatedAt: isoTime(m.CreatedAt),
	}
}

func toMessageDTOs(messages []store.Message) []messageDTO {
	out := make([]messageDTO, 0, len(messages))
	for _, m := range messages {
		out = append(out, toMessageDTO(m))
	}
	return out
}

func toProjectDTOs(projects []store.Project) []projectDTO {
	out := make([]projectDTO, 0, len(projects))
	for _, p := range projects {
		out = append(out, toProjectDTO(p))
	}
	return out
}

func toChatDTOs(chats []store.Chat, retentionDays int) []chatDTO {
	out := make([]chatDTO, 0, len(chats))
	for _, c := range chats {
		out = append(out, toChatDTO(c, retentionDays))
	}
	return out
}
