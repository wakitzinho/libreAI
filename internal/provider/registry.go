package provider

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"libreai/internal/config"
)

const discoveryTTL = 10 * time.Minute

// Model is one selectable model. Manual models come from .env; discovered
// models come from the provider's /models endpoint.
type Model struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Manual bool   `json:"manual"`
}

// Info is a non-sensitive provider snapshot safe to send to the browser.
type Info struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Enabled            bool      `json:"enabled"`
	Configured         bool      `json:"configured"`
	Status             string    `json:"status"`
	DiscoverySupported bool      `json:"discovery_supported"`
	Models             []Model   `json:"models"`
	DiscoveryError     string    `json:"discovery_error,omitempty"`
	LastRefreshed      time.Time `json:"last_refreshed,omitempty"`
}

type registryProvider struct {
	cfg    config.Provider
	client *Client
}

type discoveryCache struct {
	models    []string
	fetchedAt time.Time
	lastErr   string
}

// Registry owns configured providers and their in-memory discovery cache.
type Registry struct {
	providers []*registryProvider
	byID      map[string]*registryProvider
	logger    *slog.Logger

	mu       sync.RWMutex
	cache    map[string]*discoveryCache
	refreshM sync.Mutex
}

// NewRegistry builds provider clients from trusted server-side configuration.
func NewRegistry(cfg config.Config, logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Registry{
		byID:   make(map[string]*registryProvider),
		cache:  make(map[string]*discoveryCache),
		logger: logger,
	}
	for _, p := range cfg.Providers {
		rp := &registryProvider{cfg: p}
		if p.Configured() {
			rp.client = newClient(configProvider{
				id: p.ID, name: p.DisplayName, baseURL: p.BaseURL, apiKey: p.APIKey,
				noAuth: p.NoAuth, headers: p.Headers,
			})
		}
		r.providers = append(r.providers, rp)
		r.byID[p.ID] = rp
		r.cache[p.ID] = &discoveryCache{}
	}
	return r
}

// GetClient returns a usable client for a provider ID.
func (r *Registry) GetClient(id string) (*Client, bool) {
	p, ok := r.byID[strings.ToLower(id)]
	if !ok || p.client == nil {
		return nil, false
	}
	return p.client, true
}

// Timeout returns the configured upstream timeout for a provider.
func (r *Registry) Timeout(id string, fallback time.Duration) time.Duration {
	if p, ok := r.byID[strings.ToLower(id)]; ok && p.cfg.Timeout > 0 {
		return p.cfg.Timeout
	}
	return fallback
}

// Provider returns a config snapshot (including the API key) for trusted
// server-side use only.
func (r *Registry) Provider(id string) (config.Provider, bool) {
	p, ok := r.byID[strings.ToLower(id)]
	if !ok {
		return config.Provider{}, false
	}
	return p.cfg, true
}

// Models returns the merged manual and discovered models for a provider.
func (r *Registry) Models(id string) []Model {
	id = strings.ToLower(id)
	p, ok := r.byID[id]
	if !ok {
		return nil
	}
	r.mu.RLock()
	c := r.cache[id]
	var discovered []string
	if c != nil {
		discovered = append(discovered, c.models...)
	}
	r.mu.RUnlock()

	seen := map[string]bool{}
	out := make([]Model, 0, len(p.cfg.Models)+len(discovered))
	for _, m := range p.cfg.Models {
		if !seen[m] {
			seen[m] = true
			out = append(out, Model{ID: m, Name: m, Manual: true})
		}
	}
	sort.Strings(discovered)
	for _, m := range discovered {
		if !seen[m] {
			seen[m] = true
			out = append(out, Model{ID: m, Name: m})
		}
	}
	return out
}

// ModelKnown reports whether a model is currently selectable. Providers with
// no model information at all reject generation rather than guessing.
func (r *Registry) ModelKnown(id, model string) bool {
	for _, m := range r.Models(id) {
		if m.ID == model {
			return true
		}
	}
	return false
}

// HasModelInfo reports whether any manual or discovered model is available.
func (r *Registry) HasModelInfo(id string) bool {
	return len(r.Models(id)) > 0
}

// Infos returns all provider snapshots in stable order.
func (r *Registry) Infos() []Info {
	out := make([]Info, 0, len(r.providers))
	for _, p := range r.providers {
		r.mu.RLock()
		c := r.cache[p.cfg.ID]
		var lastErr string
		var fetched time.Time
		if c != nil {
			lastErr = c.lastErr
			fetched = c.fetchedAt
		}
		r.mu.RUnlock()
		out = append(out, Info{
			ID:                 p.cfg.ID,
			Name:               p.cfg.DisplayName,
			Enabled:            p.cfg.Enabled,
			Configured:         p.cfg.Configured(),
			Status:             p.cfg.Status(),
			DiscoverySupported: p.cfg.Discovery,
			Models:             r.Models(p.cfg.ID),
			DiscoveryError:     lastErr,
			LastRefreshed:      fetched,
		})
	}
	return out
}

// Refresh discovers models. If force is false, only stale cache entries are
// refreshed. Failed discovery preserves the previous successful list and
// preserves manual models.
func (r *Registry) Refresh(ctx context.Context, force bool) {
	r.refreshM.Lock()
	defer r.refreshM.Unlock()
	for _, p := range r.providers {
		if p.client == nil || !p.cfg.Discovery {
			continue
		}
		r.mu.RLock()
		c := r.cache[p.cfg.ID]
		fresh := c != nil && !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < discoveryTTL
		r.mu.RUnlock()
		if fresh && !force {
			continue
		}
		r.refreshOneLocked(ctx, p)
	}
}

// RefreshProvider refreshes one provider's cache.
func (r *Registry) RefreshProvider(ctx context.Context, id string) {
	r.refreshM.Lock()
	defer r.refreshM.Unlock()
	p, ok := r.byID[strings.ToLower(id)]
	if !ok || p.client == nil || !p.cfg.Discovery {
		return
	}
	r.refreshOneLocked(ctx, p)
}

func (r *Registry) refreshOneLocked(ctx context.Context, p *registryProvider) {
	timeout := p.cfg.Timeout
	if timeout <= 0 || timeout > 20*time.Second {
		timeout = 15 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	models, err := p.client.Discover(callCtx)
	r.mu.Lock()
	c := r.cache[p.cfg.ID]
	if c == nil {
		c = &discoveryCache{}
		r.cache[p.cfg.ID] = c
	}
	if err != nil {
		c.lastErr = r.Redact(err.Error())
	} else {
		c.models = models
		c.fetchedAt = time.Now().UTC()
		c.lastErr = ""
	}
	count := len(models)
	lastErr := c.lastErr
	r.mu.Unlock()
	if err != nil {
		r.logger.Warn("model discovery failed", "provider", p.cfg.ID, "error", lastErr)
		return
	}
	r.logger.Info("model discovery completed", "provider", p.cfg.ID, "models", count, "duration_ms", time.Since(start).Milliseconds())
}

// Redact removes every configured API key from a string before it is logged or
// returned to a browser.
func (r *Registry) Redact(s string) string {
	for _, p := range r.providers {
		key := p.cfg.APIKey
		if key != "" {
			s = strings.ReplaceAll(s, key, "[redacted]")
		}
		for _, value := range p.cfg.Headers {
			if len(value) >= 6 {
				s = strings.ReplaceAll(s, value, "[redacted]")
			}
		}
	}
	return s
}

// Discover fetches /models from the provider.
func (c *Client) Discover(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, &Error{Kind: "config", Message: "Provider endpoint is invalid.", cause: err}
	}
	c.setHeaders(req, "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.transportError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, c.httpError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDiscoveryBody+1))
	if err != nil {
		return nil, &Error{Kind: "network", Message: "Could not read the provider model list.", cause: err}
	}
	if len(data) > maxDiscoveryBody {
		return nil, &Error{Kind: "bad_response", Message: "The provider model list was too large."}
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := jsonUnmarshal(data, &payload); err != nil {
		// Some compatible services return a bare array.
		var arr []struct {
			ID string `json:"id"`
		}
		if err2 := jsonUnmarshal(data, &arr); err2 != nil {
			return nil, &Error{Kind: "bad_response", Message: "The provider returned an invalid model list.", cause: err}
		}
		payload.Data = arr
	}
	ids := make([]string, 0, len(payload.Data)+len(payload.Models))
	appendID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 200 || hasControlChar(id) {
			return
		}
		ids = append(ids, id)
	}
	for _, m := range payload.Data {
		appendID(m.ID)
	}
	for _, m := range payload.Models {
		appendID(m.ID)
	}
	sort.Strings(ids)
	dedup := ids[:0]
	for _, id := range ids {
		if len(dedup) == 0 || dedup[len(dedup)-1] != id {
			dedup = append(dedup, id)
		}
	}
	if len(dedup) > 2000 {
		dedup = dedup[:2000]
	}
	return dedup, nil
}

func hasControlChar(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// EnsureFresh is a convenience wrapper for background warm-up.
func (r *Registry) EnsureFresh(ctx context.Context) {
	r.Refresh(ctx, false)
}

// IsCancelled reports whether err represents a cancelled upstream call.
func IsCancelled(err error) bool {
	var pe *Error
	return errors.As(err, &pe) && pe.Kind == "cancelled"
}

// IsSafeError returns a sanitized provider error string.
func SafeError(err error) string {
	if err == nil {
		return ""
	}
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Message
	}
	return "Provider request failed."
}
