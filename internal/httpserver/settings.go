package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	settingDefaultProvider = "default_provider"
	settingDefaultModel    = "default_model"
	settingHiddenModels    = "hidden_models"
)

type appSettings struct {
	DefaultProvider string
	DefaultModel    string
	HiddenModels    []string
}

func (s *Server) loadSettings(ctx context.Context) (appSettings, error) {
	values, err := s.store.GetSettings(ctx)
	if err != nil {
		return appSettings{}, err
	}
	out := appSettings{
		DefaultProvider: strings.TrimSpace(values[settingDefaultProvider]),
		DefaultModel:    strings.TrimSpace(values[settingDefaultModel]),
	}
	if raw := values[settingHiddenModels]; raw != "" {
		var hidden []string
		if json.Unmarshal([]byte(raw), &hidden) == nil {
			out.HiddenModels = normalizeHidden(hidden)
		}
	}
	if out.HiddenModels == nil {
		out.HiddenModels = []string{}
	}
	return out, nil
}

func normalizeHidden(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || len(v) > 300 || strings.ContainsAny(v, "\r\n\x00") || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= 1000 {
			break
		}
	}
	return out
}

func (s *Server) saveSettings(ctx context.Context, values appSettings) error {
	hidden, _ := json.Marshal(values.HiddenModels)
	return s.store.SetSettings(ctx, map[string]string{
		settingDefaultProvider: values.DefaultProvider,
		settingDefaultModel:    values.DefaultModel,
		settingHiddenModels:    string(hidden),
	})
}

// effectiveDefaults validates stored defaults and falls back to the first
// usable provider/model pair.
func (s *Server) effectiveDefaults(ctx context.Context) (appSettings, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return appSettings{}, err
	}
	reg := s.registry()
	if settings.DefaultProvider != "" {
		if _, ok := reg.GetClient(settings.DefaultProvider); ok {
			if settings.DefaultModel != "" && reg.ModelKnown(settings.DefaultProvider, settings.DefaultModel) {
				return settings, nil
			}
		}
	}
	settings.DefaultProvider = ""
	settings.DefaultModel = ""
	hidden := map[string]bool{}
	for _, h := range settings.HiddenModels {
		hidden[h] = true
	}
	for _, info := range reg.Infos() {
		if !info.Configured {
			continue
		}
		for _, m := range info.Models {
			key := info.ID + "/" + m.ID
			if hidden[key] {
				continue
			}
			settings.DefaultProvider = info.ID
			settings.DefaultModel = m.ID
			return settings, nil
		}
	}
	return settings, nil
}

func (s *Server) settingsResponse(settings appSettings) settingsDTO {
	return settingsDTO{
		Defaults:      defaultsDTO{ProviderID: settings.DefaultProvider, ModelID: settings.DefaultModel},
		HiddenModels:  settings.HiddenModels,
		RetentionDays: s.cfg.RetentionDays,
		Version:       s.version,
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.effectiveDefaults(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsResponse(settings))
}

type settingsRequest struct {
	Defaults *struct {
		ProviderID string `json:"provider_id"`
		ModelID    string `json:"model_id"`
	} `json:"defaults"`
	HiddenModels *[]string `json:"hidden_models"`
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		if !s.handleRequestError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	current, err := s.loadSettings(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	reg := s.registry()
	if req.Defaults != nil {
		pid := strings.ToLower(strings.TrimSpace(req.Defaults.ProviderID))
		mid := strings.TrimSpace(req.Defaults.ModelID)
		if (pid == "") != (mid == "") {
			writeError(w, http.StatusBadRequest, "invalid_defaults", "Choose both a default provider and model, or neither.")
			return
		}
		if pid != "" {
			if _, ok := reg.GetClient(pid); !ok {
				writeError(w, http.StatusBadRequest, "provider_unavailable", "That provider is not configured or enabled.")
				return
			}
			if !reg.ModelKnown(pid, mid) {
				writeError(w, http.StatusBadRequest, "model_unavailable", "That model is not currently known. Refresh models and try again.")
				return
			}
		}
		current.DefaultProvider = pid
		current.DefaultModel = mid
	}
	if req.HiddenModels != nil {
		current.HiddenModels = normalizeHidden(*req.HiddenModels)
	}
	if err := s.saveSettings(r.Context(), current); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsResponse(current))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.registry().Infos()})
}

func (s *Server) handleRefreshModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	reg := s.registry()
	reg.Refresh(ctx, true)
	writeJSON(w, http.StatusOK, map[string]any{"providers": reg.Infos()})
}
