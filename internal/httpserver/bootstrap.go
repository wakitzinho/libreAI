package httpserver

import (
	"net/http"
)

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	settings, err := s.effectiveDefaults(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	chats, err := s.store.ListChats(r.Context(), "", 500)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bootstrapDTO{
		Version:       s.version,
		RetentionDays: s.cfg.RetentionDays,
		Providers:     s.providers.Infos(),
		Projects:      toProjectDTOs(projects),
		Chats:         toChatDTOs(chats, s.cfg.RetentionDays),
		Defaults:      defaultsDTO{ProviderID: settings.DefaultProvider, ModelID: settings.DefaultModel},
		HiddenModels:  settings.HiddenModels,
		Limits:        limitsDTO{MaxConcurrentGenerations: s.cfg.MaxConcurrentGenerations},
	})
}
