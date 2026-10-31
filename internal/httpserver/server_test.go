package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"libreai/internal/config"
	"libreai/internal/provider"
	"libreai/internal/store"
)

type testEnv struct {
	server    *Server
	api       *httptest.Server
	store     *store.Store
	provider  *httptest.Server
	baseURL   string
	apiServer *http.Server
}

func newTestEnv(t *testing.T, providerHandler http.HandlerFunc, models ...string) *testEnv {
	t.Helper()
	return newTestEnvWithConfig(t, providerHandler, nil, models...)
}

func newTestEnvWithConfig(t *testing.T, providerHandler http.HandlerFunc, mutate func(*config.Config), models ...string) *testEnv {
	t.Helper()
	if len(models) == 0 {
		models = []string{"test-model"}
	}
	providerSrv := httptest.NewServer(providerHandler)
	t.Cleanup(providerSrv.Close)

	st, err := store.Open(filepath.Join(t.TempDir(), "libreai.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Config{
		Host: "127.0.0.1", Port: 8080,
		DatabasePath:             filepath.Join(t.TempDir(), "unused.db"),
		RetentionDays:            30,
		UpstreamTimeout:          5 * time.Second,
		MaxConcurrentGenerations: 2,
		MaxRequestBodyBytes:      1 << 20,
		MaxContextMessages:       200,
		MaxContextBytes:          400_000,
		ShutdownTimeout:          5 * time.Second,
		LogLevel:                 "error",
		Providers: []config.Provider{{
			ID: "mock", DisplayName: "Mock Provider",
			BaseURL: providerSrv.URL, APIKey: "sk-super-secret",
			Enabled: true, Discovery: true, Models: models,
		}},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := provider.NewRegistry(cfg, logger)
	srv, err := New(cfg, st, reg, "test-version", logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	api := httptest.NewServer(srv.Handler())
	t.Cleanup(api.Close)
	return &testEnv{server: srv, api: api, store: st, provider: providerSrv, baseURL: api.URL}
}

func (e *testEnv) request(t *testing.T, method, path string, body string, headers map[string]string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.baseURL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

type providersResponse struct {
	Providers []provider.Info `json:"providers"`
}

type chatEnvelope struct {
	Chat chatDTO `json:"chat"`
}

type projectEnvelope struct {
	Project projectDTO `json:"project"`
}

type exportResponse struct {
	Chat struct {
		ID string `json:"id"`
	} `json:"chat"`
	Messages []messageDTO `json:"messages"`
	Provider struct {
		Name string `json:"name"`
	} `json:"provider"`
}

func readJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return v
}

type sseEvent struct {
	Event string
	Data  map[string]any
}

func readSSE(t *testing.T, resp *http.Response) []sseEvent {
	t.Helper()
	defer resp.Body.Close()
	var events []sseEvent
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	event := ""
	var data []string
	flush := func() {
		if len(data) == 0 {
			event = ""
			return
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &payload); err != nil {
			t.Fatalf("bad SSE JSON %q: %v", strings.Join(data, "\n"), err)
		}
		events = append(events, sseEvent{Event: event, Data: payload})
		event = ""
		data = nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch key {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read SSE: %v", err)
	}
	flush()
	return events
}

func TestHealthAndStaticAssets(t *testing.T) {
	env := newTestEnv(t, func(http.ResponseWriter, *http.Request) {})
	resp := env.request(t, "GET", "/api/health", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing security header")
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q", csp)
	}
	var health map[string]string
	health = readJSON[map[string]string](t, resp)
	if health["status"] != "ok" || health["version"] != "test-version" {
		t.Fatalf("health = %#v", health)
	}

	for path, contentType := range map[string]string{
		"/":                   "text/html; charset=utf-8",
		"/index.html":         "text/html; charset=utf-8",
		"/css/app.css":        "text/css; charset=utf-8",
		"/js/app.js":          "text/javascript; charset=utf-8",
		"/assets/favicon.svg": "image/svg+xml",
	} {
		resp := env.request(t, "GET", path, "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != contentType {
			t.Fatalf("%s content type = %q, want %q", path, got, contentType)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if len(body) == 0 {
			t.Fatalf("%s is empty", path)
		}
	}

	// ETag revalidation.
	resp = env.request(t, "GET", "/css/app.css", "", nil)
	etag := resp.Header.Get("ETag")
	resp.Body.Close()
	if etag == "" {
		t.Fatal("missing ETag")
	}
	resp = env.request(t, "GET", "/css/app.css", "", map[string]string{"If-None-Match": etag})
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidation status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, "GET", "/missing", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestBootstrapDoesNotLeakSecrets(t *testing.T) {
	env := newTestEnv(t, func(http.ResponseWriter, *http.Request) {})
	resp := env.request(t, "GET", "/api/bootstrap", "", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "sk-super-secret") {
		t.Fatalf("bootstrap leaked API key: %s", body)
	}
	if strings.Contains(string(body), env.provider.URL) {
		t.Fatalf("bootstrap leaked provider URL: %s", body)
	}
	var boot bootstrapDTO
	if err := json.Unmarshal(body, &boot); err != nil {
		t.Fatal(err)
	}
	if boot.Version != "test-version" || boot.RetentionDays != 30 {
		t.Fatalf("bootstrap = %+v", boot)
	}
	if len(boot.Providers) != 1 || boot.Providers[0].Name != "Mock Provider" || !boot.Providers[0].Configured {
		t.Fatalf("providers = %+v", boot.Providers)
	}
	if boot.Defaults.ProviderID != "mock" || boot.Defaults.ModelID != "test-model" {
		t.Fatalf("defaults = %+v", boot.Defaults)
	}
	if boot.Limits.MaxConcurrentGenerations != 2 {
		t.Fatalf("limits = %+v", boot.Limits)
	}
}

type upstreamChatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Stream bool `json:"stream"`
}

func TestProjectChatStreamPersistenceAndExport(t *testing.T) {
	var chatRequests int32
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&chatRequests, 1)
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-super-secret" {
			t.Errorf("missing auth")
		}
		var req upstreamChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		if req.Model != "test-model" || !req.Stream || len(req.Messages) != 1 || req.Messages[0].Content != "Hello there" {
			t.Errorf("unexpected upstream request: %+v", req)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range []string{
			`{"choices":[{"delta":{"content":"Hello"}}]}`,
			`{"choices":[{"delta":{"content":" world"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
	})

	// Project CRUD.
	resp := env.request(t, "POST", "/api/projects", `{"name":"Work"}`, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d", resp.StatusCode)
	}
	projectResp := readJSON[struct {
		Project projectDTO `json:"project"`
	}](t, resp)
	projectID := projectResp.Project.ID

	// Chat creation.
	resp = env.request(t, "POST", "/api/chats", `{"project_id":"`+projectID+`"}`, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create chat status = %d", resp.StatusCode)
	}
	chatResp := readJSON[struct {
		Chat chatDTO `json:"chat"`
	}](t, resp)
	chatID := chatResp.Chat.ID
	if chatResp.Chat.ProjectID == nil || *chatResp.Chat.ProjectID != projectID {
		t.Fatalf("chat project = %v", chatResp.Chat.ProjectID)
	}
	before, err := env.store.GetChat(context.Background(), chatID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)

	// Stream a message.
	resp = env.request(t, "POST", "/api/chats/"+chatID+"/messages", `{"content":"Hello there"}`, nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("message status = %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}
	events := readSSE(t, resp)
	if len(events) != 4 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Event != "meta" || events[1].Event != "delta" || events[2].Event != "delta" || events[3].Event != "done" {
		t.Fatalf("event sequence = %+v", events)
	}
	if events[0].Data["title"] != "Hello there" {
		t.Fatalf("meta title = %v", events[0].Data["title"])
	}
	if events[1].Data["text"] != "Hello" || events[2].Data["text"] != " world" {
		t.Fatalf("deltas = %+v", events)
	}
	if events[3].Data["status"] != "completed" || events[3].Data["finish_reason"] != "stop" {
		t.Fatalf("done = %+v", events[3].Data)
	}
	if atomic.LoadInt32(&chatRequests) != 1 {
		t.Fatalf("upstream requests = %d", chatRequests)
	}

	// The conversation is persisted and activity was recorded.
	resp = env.request(t, "GET", "/api/chats/"+chatID, "", nil)
	detail := readJSON[chatDetailDTO](t, resp)
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %+v", detail.Messages)
	}
	if detail.Messages[0].Role != "user" || detail.Messages[0].Content != "Hello there" {
		t.Fatalf("user message = %+v", detail.Messages[0])
	}
	if detail.Messages[1].Role != "assistant" || detail.Messages[1].Content != "Hello world" || detail.Messages[1].Status != "completed" {
		t.Fatalf("assistant message = %+v", detail.Messages[1])
	}
	if detail.Chat.Title != "Hello there" || detail.Chat.ModelID != "test-model" {
		t.Fatalf("chat = %+v", detail.Chat)
	}
	if detail.Chat.LastActivityAt == "" {
		t.Fatal("missing activity timestamp")
	}
	after, _ := env.store.GetChat(context.Background(), chatID)
	if after.LastActivityAt <= before.LastActivityAt {
		t.Fatalf("activity not updated: %d -> %d", before.LastActivityAt, after.LastActivityAt)
	}

	// Opening a chat records activity as well.
	time.Sleep(5 * time.Millisecond)
	resp = env.request(t, "POST", "/api/chats/"+chatID+"/open", "", nil)
	opened := readJSON[chatDetailDTO](t, resp)
	if len(opened.Messages) != 2 {
		t.Fatalf("open messages = %d", len(opened.Messages))
	}
	openedChat, _ := env.store.GetChat(context.Background(), chatID)
	if openedChat.LastActivityAt <= after.LastActivityAt {
		t.Fatal("open did not update activity")
	}

	// Rename and model selection.
	resp = env.request(t, "PATCH", "/api/chats/"+chatID, `{"title":"Renamed"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename status = %d", resp.StatusCode)
	}
	renamed := readJSON[struct {
		Chat chatDTO `json:"chat"`
	}](t, resp)
	if renamed.Chat.Title != "Renamed" {
		t.Fatalf("renamed = %+v", renamed.Chat)
	}
	resp = env.request(t, "PATCH", "/api/chats/"+chatID, `{"provider_id":"mock","model_id":"unknown-model"}`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown model status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Sending must also reject a model that is not configured or discovered,
	// before any user message is persisted or the provider is contacted.
	resp = env.request(t, "POST", "/api/chats/"+chatID+"/messages",
		`{"content":"should not be sent","provider_id":"mock","model_id":"unknown-model"}`, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unknown model send status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	if atomic.LoadInt32(&chatRequests) != 1 {
		t.Fatalf("unknown model reached upstream: requests = %d", chatRequests)
	}
	detailResponse := env.request(t, "GET", "/api/chats/"+chatID, "", nil)
	unchanged := readJSON[chatDetailDTO](t, detailResponse)
	if len(unchanged.Messages) != 2 {
		t.Fatalf("rejected send persisted messages: %+v", unchanged.Messages)
	}

	// Move out of the project (project itself remains).
	resp = env.request(t, "POST", "/api/chats/"+chatID+"/move", `{"project_id":null}`, nil)
	moved := readJSON[struct {
		Chat chatDTO `json:"chat"`
	}](t, resp)
	if moved.Chat.ProjectID != nil {
		t.Fatalf("chat still in project: %v", *moved.Chat.ProjectID)
	}

	// Export Markdown.
	resp = env.request(t, "GET", "/api/chats/"+chatID+"/export?format=markdown", "", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/markdown") {
		t.Fatalf("markdown export status = %d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	md, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"# Renamed", "## User", "Hello there", "## Assistant", "Hello world"} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("markdown export missing %q:\n%s", want, md)
		}
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), `filename="libreai-renamed-`) {
		t.Fatalf("content disposition = %q", resp.Header.Get("Content-Disposition"))
	}

	// Export JSON.
	resp = env.request(t, "GET", "/api/chats/"+chatID+"/export?format=json", "", nil)
	exported := readJSON[exportResponse](t, resp)
	if exported.Chat.ID != chatID || len(exported.Messages) != 2 || exported.Provider.Name != "Mock Provider" {
		t.Fatalf("json export = %+v", exported)
	}

	resp = env.request(t, "GET", "/api/chats/"+chatID+"/export?format=xml", "", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid format status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Delete.
	resp = env.request(t, "DELETE", "/api/chats/"+chatID, "", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.request(t, "GET", "/api/chats/"+chatID, "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted chat status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, "DELETE", "/api/projects/"+projectID, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete project status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRefreshModelsAndSettings(t *testing.T) {
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"discovered-a"},{"id":"discovered-b"}]}`)
	})

	resp := env.request(t, "POST", "/api/models/refresh", "", nil)
	refreshed := readJSON[providersResponse](t, resp)
	if len(refreshed.Providers) != 1 {
		t.Fatalf("providers = %+v", refreshed.Providers)
	}
	ids := map[string]bool{}
	for _, m := range refreshed.Providers[0].Models {
		ids[m.ID] = true
	}
	if !ids["test-model"] || !ids["discovered-a"] || !ids["discovered-b"] {
		t.Fatalf("models = %+v", refreshed.Providers[0].Models)
	}

	// Save defaults and hidden models.
	resp = env.request(t, "PUT", "/api/settings", `{"defaults":{"provider_id":"mock","model_id":"discovered-a"},"hidden_models":["mock/discovered-b","mock/discovered-b",""]}`, nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("save settings status = %d: %s", resp.StatusCode, body)
	}
	saved := readJSON[settingsDTO](t, resp)
	if saved.Defaults.ProviderID != "mock" || saved.Defaults.ModelID != "discovered-a" {
		t.Fatalf("saved defaults = %+v", saved.Defaults)
	}
	if len(saved.HiddenModels) != 1 || saved.HiddenModels[0] != "mock/discovered-b" {
		t.Fatalf("hidden = %#v", saved.HiddenModels)
	}

	resp = env.request(t, "GET", "/api/settings", "", nil)
	loaded := readJSON[settingsDTO](t, resp)
	if loaded.Defaults.ModelID != "discovered-a" || len(loaded.HiddenModels) != 1 {
		t.Fatalf("loaded settings = %+v", loaded)
	}

	resp = env.request(t, "GET", "/api/bootstrap", "", nil)
	boot := readJSON[bootstrapDTO](t, resp)
	if boot.Defaults.ModelID != "discovered-a" || len(boot.HiddenModels) != 1 {
		t.Fatalf("bootstrap settings = %+v", boot)
	}

	// Invalid settings are rejected.
	for _, body := range []string{
		`{"defaults":{"provider_id":"mock","model_id":""}}`,
		`{"defaults":{"provider_id":"nope","model_id":"m"}}`,
		`{"defaults":{"provider_id":"mock","model_id":"nope"}}`,
	} {
		resp = env.request(t, "PUT", "/api/settings", body, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("settings %s status = %d", body, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestCancellationPersistsPartialResponse(t *testing.T) {
	started := make(chan struct{})
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first \"}}]}\n\n")
		flusher.Flush()
		close(started)
		<-r.Context().Done()
	})

	resp := env.request(t, "POST", "/api/chats", `{}`, nil)
	created := readJSON[struct {
		Chat chatDTO `json:"chat"`
	}](t, resp)
	chatID := created.Chat.ID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", env.baseURL+"/api/chats/"+chatID+"/messages", strings.NewReader(`{"content":"go"}`))
	req.Header.Set("Content-Type", "application/json")
	streamResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(streamResp.Body)
	var assistantID int64
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &payload); err == nil {
				if id, ok := payload["assistant_message_id"].(float64); ok {
					assistantID = int64(id)
				}
				if payload["text"] == "first " {
					go func() {
						<-started
						cancel()
					}()
					break
				}
			}
		}
	}
	// Wait until the server has persisted the cancellation.
	deadline := time.Now().Add(5 * time.Second)
	var msg store.Message
	for time.Now().Before(deadline) {
		msg, err = env.store.GetMessage(context.Background(), chatID, assistantID)
		if err == nil && msg.Status == "cancelled" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	streamResp.Body.Close()
	if err != nil || msg.Status != "cancelled" {
		t.Fatalf("message = %+v, err=%v", msg, err)
	}
	if msg.Content != "first " {
		t.Fatalf("partial content = %q", msg.Content)
	}
}

func TestProviderErrorIsReportedAndPersisted(t *testing.T) {
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key sk-super-secret"}}`)
	})
	resp := env.request(t, "POST", "/api/chats", `{}`, nil)
	created := readJSON[struct {
		Chat chatDTO `json:"chat"`
	}](t, resp)
	chatID := created.Chat.ID
	resp = env.request(t, "POST", "/api/chats/"+chatID+"/messages", `{"content":"go"}`, nil)
	events := readSSE(t, resp)
	if len(events) < 2 {
		t.Fatalf("events = %+v", events)
	}
	last := events[len(events)-1]
	if last.Event != "error" || last.Data["status"] != "error" {
		t.Fatalf("final event = %+v", last)
	}
	if msg, _ := last.Data["message"].(string); strings.Contains(msg, "sk-super-secret") || !strings.Contains(msg, "rejected its API key") {
		t.Fatalf("unsafe error message = %q", msg)
	}
	detailResp := env.request(t, "GET", "/api/chats/"+chatID, "", nil)
	detail := readJSON[chatDetailDTO](t, detailResp)
	if len(detail.Messages) != 2 || detail.Messages[1].Status != "error" || detail.Messages[1].Content != "" {
		t.Fatalf("persisted messages = %+v", detail.Messages)
	}
}

func TestValidationAndOriginChecks(t *testing.T) {
	env := newTestEnv(t, func(http.ResponseWriter, *http.Request) {})

	resp := env.request(t, "POST", "/api/projects", `{"name":"x"}`, map[string]string{"Content-Type": "text/plain"})
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("content type status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, "POST", "/api/projects", `{"name":"x"`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, "POST", "/api/projects", `{"name":"x","extra":true}`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, "POST", "/api/projects", `{"name":"x"}`, map[string]string{"Origin": "https://evil.example"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, "POST", "/api/projects", `{"name":"x"}`, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, "POST", "/api/projects", `{"name":"x"}`, map[string]string{"Origin": env.baseURL})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("same-origin status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, "GET", "/api/chats/notavalidid", "", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid ID status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, "GET", "/api/unknown", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown API status = %d", resp.StatusCode)
	}
	var apiErr errorBody
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil || apiErr.Error.Code != "not_found" {
		t.Fatalf("unknown API body = %+v, %v", apiErr, err)
	}

	resp = env.request(t, "POST", "/api/chats", `{}`, nil)
	created := readJSON[struct {
		Chat chatDTO `json:"chat"`
	}](t, resp)
	large := strings.Repeat("x", 100_001)
	resp = env.request(t, "POST", "/api/chats/"+created.Chat.ID+"/messages", `{"content":"`+large+`"}`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized message status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestConcurrentGenerationConflict(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-release
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	})
	resp := env.request(t, "POST", "/api/chats", `{}`, nil)
	created := readJSON[struct {
		Chat chatDTO `json:"chat"`
	}](t, resp)
	chatID := created.Chat.ID

	firstDone := make(chan struct{})
	var firstResp *http.Response
	var firstErr error
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	go func() {
		defer close(firstDone)
		req, _ := http.NewRequest("POST", env.baseURL+"/api/chats/"+chatID+"/messages", strings.NewReader(`{"content":"one"}`))
		req.Header.Set("Content-Type", "application/json")
		firstResp, firstErr = http.DefaultClient.Do(req)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first generation did not start")
	}
	resp = env.request(t, "POST", "/api/chats/"+chatID+"/messages", `{"content":"two"}`, nil)
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("second generation status = %d: %s", resp.StatusCode, body)
	}
	resp.Body.Close()
	close(release)
	released = true
	<-firstDone
	if firstErr != nil {
		t.Fatalf("first request: %v", firstErr)
	}
	events := readSSE(t, firstResp)
	if len(events) < 2 || events[len(events)-1].Event != "done" {
		t.Fatalf("first stream did not complete: %+v", events)
	}
}

func TestMissingChat(t *testing.T) {
	env := newTestEnv(t, func(http.ResponseWriter, *http.Request) {})
	resp := env.request(t, "GET", "/api/chats/ffffffffffffffffffffffffffffffff", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing chat status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func createTestChat(t *testing.T, env *testEnv) chatDTO {
	t.Helper()
	resp := env.request(t, "POST", "/api/chats", `{}`, nil)
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create chat status = %d: %s", resp.StatusCode, body)
	}
	return readJSON[chatEnvelope](t, resp).Chat
}

func TestWebSearchInjectsResultsAndPersistsSources(t *testing.T) {
	var searchQueries []string
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer pplx-test" {
			t.Errorf("search authorization = %q", got)
		}
		var req struct {
			Query      string `json:"query"`
			MaxResults int    `json:"max_results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode search request: %v", err)
		}
		if req.MaxResults != 3 {
			t.Errorf("search max_results = %d", req.MaxResults)
		}
		searchQueries = append(searchQueries, req.Query)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Go downloads","url":"https://go.dev/dl/","snippet":"Go 1.27 is the current release.","date":"2026-10-01"}]}`))
	}))
	defer searchSrv.Close()

	var upstreamPrompt string
	env := newTestEnvWithConfig(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req upstreamChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if len(req.Messages) > 0 {
			upstreamPrompt = req.Messages[len(req.Messages)-1].Content
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Go 1.27\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}, func(cfg *config.Config) {
		cfg.SearchEnabled = true
		cfg.SearchBaseURL = searchSrv.URL
		cfg.SearchAPIKey = "pplx-test"
		cfg.SearchMaxResults = 3
		cfg.SearchTimeout = 5 * time.Second
		cfg.SearchMaxBytes = 12000
	})

	chat := createTestChat(t, env)
	resp := env.request(t, "POST", "/api/chats/"+chat.ID+"/messages",
		`{"content":"what is the latest Go release?","web_search":true}`, nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("message status = %d: %s", resp.StatusCode, body)
	}
	events := readSSE(t, resp)
	if len(events) != 5 {
		t.Fatalf("events = %+v", events)
	}
	want := []string{"meta", "search", "search", "delta", "done"}
	for i, name := range want {
		if events[i].Event != name {
			t.Fatalf("event %d = %q, want %q (%+v)", i, events[i].Event, name, events)
		}
	}
	if events[1].Data["status"] != "searching" || events[2].Data["status"] != "done" {
		t.Fatalf("search events = %+v", events[1:3])
	}
	results, ok := events[2].Data["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("search results = %#v", events[2].Data["results"])
	}
	if len(searchQueries) != 1 || searchQueries[0] != "what is the latest Go release?" {
		t.Fatalf("search queries = %v", searchQueries)
	}

	for _, want := range []string{
		`<web_search_results query="what is the latest Go release?">`,
		"[1] Go downloads (2026-10-01)",
		"URL: https://go.dev/dl/",
		"Go 1.27 is the current release.",
		"what is the latest Go release?",
	} {
		if !strings.Contains(upstreamPrompt, want) {
			t.Fatalf("upstream prompt missing %q:\n%s", want, upstreamPrompt)
		}
	}
	if !strings.HasSuffix(upstreamPrompt, "what is the latest Go release?") {
		t.Fatalf("user message is not last in the prompt:\n%s", upstreamPrompt)
	}

	resp = env.request(t, "GET", "/api/chats/"+chat.ID, "", nil)
	detail := readJSON[chatDetailDTO](t, resp)
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %+v", detail.Messages)
	}
	if detail.Messages[0].Content != "what is the latest Go release?" {
		t.Fatalf("stored user content = %q", detail.Messages[0].Content)
	}
	if len(detail.Messages[1].Sources) != 1 || detail.Messages[1].Sources[0].URL != "https://go.dev/dl/" {
		t.Fatalf("stored sources = %+v", detail.Messages[1].Sources)
	}
}

func TestWebSearchFailureDoesNotAbortGeneration(t *testing.T) {
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"search is down"}`)
	}))
	defer searchSrv.Close()

	env := newTestEnvWithConfig(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Plain answer\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}, func(cfg *config.Config) {
		cfg.SearchEnabled = true
		cfg.SearchBaseURL = searchSrv.URL
		cfg.SearchAPIKey = "pplx-test"
		cfg.SearchMaxResults = 5
		cfg.SearchTimeout = 5 * time.Second
		cfg.SearchMaxBytes = 12000
	})

	chat := createTestChat(t, env)
	resp := env.request(t, "POST", "/api/chats/"+chat.ID+"/messages",
		`{"content":"hello","web_search":true}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("message status = %d", resp.StatusCode)
	}
	events := readSSE(t, resp)
	if len(events) != 5 || events[2].Event != "search" || events[2].Data["status"] != "error" {
		t.Fatalf("events = %+v", events)
	}
	if events[3].Event != "delta" || events[3].Data["text"] != "Plain answer" || events[4].Event != "done" {
		t.Fatalf("generation did not continue: %+v", events)
	}
	if msg, _ := events[2].Data["message"].(string); !strings.Contains(msg, "HTTP 500") {
		t.Fatalf("search error message = %q", msg)
	}

	resp = env.request(t, "GET", "/api/chats/"+chat.ID, "", nil)
	detail := readJSON[chatDetailDTO](t, resp)
	if len(detail.Messages) != 2 || len(detail.Messages[1].Sources) != 0 {
		t.Fatalf("stored messages = %+v", detail.Messages)
	}
}

func TestWebSearchRejectedWhenNotConfigured(t *testing.T) {
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream provider should not be contacted")
		http.NotFound(w, r)
	})
	chat := createTestChat(t, env)
	resp := env.request(t, "POST", "/api/chats/"+chat.ID+"/messages",
		`{"content":"hello","web_search":true}`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "web_search_unavailable") {
		t.Fatalf("body = %s", body)
	}
}
