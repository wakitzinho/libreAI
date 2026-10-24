package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"libreai/internal/config"
)

func testRegistry(t *testing.T, baseURL string, models []string) *Registry {
	t.Helper()
	cfg := config.Config{Providers: []config.Provider{{
		ID: "mock", DisplayName: "Mock", BaseURL: baseURL,
		APIKey: "sk-very-secret", Enabled: true, Discovery: true, Models: models,
	}}}
	return NewRegistry(cfg, nil)
}

func TestSSEDecoder(t *testing.T) {
	input := ": comment\r\nevent: message\r\ndata: first\r\ndata: second\r\nid: 7\r\n\r\ndata: {\"x\":1}\n\n"
	dec := NewSSEDecoder(strings.NewReader(input), 1024)
	first, err := dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first.Event != "message" || first.Data != "first\nsecond" || first.ID != "7" {
		t.Fatalf("first event = %+v", first)
	}
	second, err := dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	if second.Event != "" || second.Data != `{"x":1}` {
		t.Fatalf("second event = %+v", second)
	}
	if _, err := dec.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final error = %v", err)
	}
}

func TestSSEDecoderFinalEventWithoutBlankLine(t *testing.T) {
	dec := NewSSEDecoder(strings.NewReader("data: tail"), 1024)
	ev, err := dec.Next()
	if err != nil || ev.Data != "tail" {
		t.Fatalf("event = %+v, %v", ev, err)
	}
	if _, err := dec.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final error = %v", err)
	}
}

func TestSSEDecoderLimits(t *testing.T) {
	dec := NewSSEDecoder(strings.NewReader("data: "+strings.Repeat("x", 100)+"\n\n"), 50)
	if _, err := dec.Next(); err == nil {
		t.Fatal("expected data size error")
	}
	dec = NewSSEDecoder(strings.NewReader("data: "+strings.Repeat("x", 100)+"\n\n"), 1024)
	// The line limit is separate from the event data limit; a small maxLine
	// should reject an oversized line even when the event would fit.
	dec.maxLine = 20
	if _, err := dec.Next(); err == nil {
		t.Fatal("expected line size error")
	}
}

func mockChatServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestStreamChatParsesSSEAndSendsRequest(t *testing.T) {
	var gotAuth, gotAccept, gotPath, gotBody string
	var gotContentLength int64
	srv := mockChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		gotContentLength = r.ContentLength
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range []string{
			`{"choices":[{"delta":{"content":"Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		} {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
			flusher.Flush()
		}
	})
	reg := testRegistry(t, srv.URL, nil)
	client, ok := reg.GetClient("mock")
	if !ok {
		t.Fatal("client missing")
	}
	var got strings.Builder
	result, err := client.StreamChat(context.Background(), "model-1",
		[]ChatMessage{{Role: "user", Content: "hi"}},
		func(s string) error { got.WriteString(s); return nil })
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if got.String() != "Hello" || result.FinishReason != "stop" {
		t.Fatalf("stream = %q finish=%q", got.String(), result.FinishReason)
	}
	if gotAuth != "Bearer sk-very-secret" || gotAccept != "text/event-stream" || gotPath != "/chat/completions" {
		t.Fatalf("headers/path = %q %q %q", gotAuth, gotAccept, gotPath)
	}
	if gotContentLength <= 0 {
		t.Fatalf("request Content-Length = %d; providers that do not support chunked bodies would reject it", gotContentLength)
	}
	if !strings.Contains(gotBody, `"stream":true`) || !strings.Contains(gotBody, `"model":"model-1"`) || !strings.Contains(gotBody, `"content":"hi"`) {
		t.Fatalf("request body = %s", gotBody)
	}
}

func TestStreamChatNonStreamingJSONFallback(t *testing.T) {
	srv := mockChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"JSON reply"},"finish_reason":"stop"}]}`)
	})
	client, _ := testRegistry(t, srv.URL, nil).GetClient("mock")
	var got string
	result, err := client.StreamChat(context.Background(), "m", nil, func(s string) error { got += s; return nil })
	if err != nil || got != "JSON reply" || result.FinishReason != "stop" {
		t.Fatalf("result = %+v, text=%q, err=%v", result, got, err)
	}
}

func TestStreamChatErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		wantKind   string
		wantText   string
	}{
		{"auth", http.StatusUnauthorized, `{"error":{"message":"invalid sk-very-secret"}}`, "", "auth", "rejected its API key"},
		{"rate", http.StatusTooManyRequests, `{"error":{"message":"slow"}}`, "30", "rate_limit", "Retry-After: 30"},
		{"bad request", http.StatusBadRequest, `{"error":{"message":"bad model"}}`, "", "bad_request", "bad model"},
		{"not found", http.StatusNotFound, `{}`, "", "bad_request", "could not find"},
		{"upstream", http.StatusBadGateway, `{"message":"upstream down"}`, "", "upstream", "HTTP 502"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			srv := mockChatServer(t, func(w http.ResponseWriter, r *http.Request) {
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			client, _ := testRegistry(t, srv.URL, nil).GetClient("mock")
			_, err := client.StreamChat(context.Background(), "m", nil, func(string) error { return nil })
			var pe *Error
			if !errors.As(err, &pe) {
				t.Fatalf("error type = %T %v", err, err)
			}
			if pe.Kind != tt.wantKind || !strings.Contains(pe.Message, tt.wantText) {
				t.Fatalf("error = %+v", pe)
			}
			if strings.Contains(pe.Error(), "sk-very-secret") {
				t.Fatalf("error leaked API key: %s", pe.Error())
			}
		})
	}
}

func TestStreamChatMalformedAndIncomplete(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"malformed json", "data: not-json\n\n"},
		{"no completion signal", `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n\n"},
		{"error object", `data: {"error":{"message":"provider exploded"}}` + "\n\n"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			srv := mockChatServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tt.body)
			})
			client, _ := testRegistry(t, srv.URL, nil).GetClient("mock")
			_, err := client.StreamChat(context.Background(), "m", nil, func(string) error { return nil })
			if err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestStreamChatCancellation(t *testing.T) {
	started := make(chan struct{})
	srv := mockChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		flusher.Flush()
		close(started)
		<-r.Context().Done()
	})
	client, _ := testRegistry(t, srv.URL, nil).GetClient("mock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()
	_, err := client.StreamChat(ctx, "m", nil, func(string) error { return nil })
	if err == nil {
		t.Fatal("expected cancellation")
	}
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != "cancelled" {
		t.Fatalf("error = %#v", err)
	}
}

func TestDiscoveryAndCache(t *testing.T) {
	requests := 0
	srv := mockChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer sk-very-secret" {
			t.Errorf("missing auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"z-model"},{"id":"a-model"},{"id":"z-model"}]}`)
	})
	reg := testRegistry(t, srv.URL, []string{"manual-model"})
	ctx := context.Background()
	reg.Refresh(ctx, false)
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
	models := reg.Models("mock")
	if len(models) != 3 {
		t.Fatalf("models = %#v", models)
	}
	ids := map[string]bool{}
	for _, m := range models {
		ids[m.ID] = true
	}
	if !ids["manual-model"] || !ids["a-model"] || !ids["z-model"] {
		t.Fatalf("models = %#v", models)
	}
	// A fresh cache is not refreshed again.
	reg.Refresh(ctx, false)
	if requests != 1 {
		t.Fatalf("fresh cache refreshed: %d requests", requests)
	}
	// Forced refresh does.
	reg.Refresh(ctx, true)
	if requests != 2 {
		t.Fatalf("forced refresh requests = %d", requests)
	}
}

func TestDiscoveryFailurePreservesManualModels(t *testing.T) {
	srv := mockChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	})
	reg := testRegistry(t, srv.URL, []string{"manual-model"})
	reg.Refresh(context.Background(), true)
	infos := reg.Infos()
	if len(infos) != 1 {
		t.Fatalf("infos = %#v", infos)
	}
	if infos[0].DiscoveryError == "" {
		t.Fatal("expected discovery error")
	}
	if len(infos[0].Models) != 1 || infos[0].Models[0].ID != "manual-model" {
		t.Fatalf("manual models lost: %#v", infos[0].Models)
	}
	if !reg.ModelKnown("mock", "manual-model") || reg.ModelKnown("mock", "unknown") {
		t.Fatal("ModelKnown mismatch")
	}
}

func TestHeadersAndTimeout(t *testing.T) {
	cfg := config.Config{Providers: []config.Provider{{
		ID: "mock", DisplayName: "Mock", BaseURL: "https://example.invalid/v1",
		APIKey: "key", Enabled: true, Headers: map[string]string{"X-Custom": "yes"},
	}}}
	reg := NewRegistry(cfg, nil)
	client, _ := reg.GetClient("mock")
	req, _ := http.NewRequest(http.MethodGet, "https://example.invalid/v1/models", nil)
	client.setHeaders(req, "application/json")
	if req.Header.Get("X-Custom") != "yes" || req.Header.Get("Authorization") != "Bearer key" {
		t.Fatalf("headers = %#v", req.Header)
	}
	if reg.Timeout("mock", time.Minute) != time.Minute {
		t.Fatal("unexpected fallback timeout")
	}
}

func TestRedact(t *testing.T) {
	reg := testRegistry(t, "https://example.invalid", nil)
	got := reg.Redact("upstream said sk-very-secret is bad")
	if strings.Contains(got, "sk-very-secret") || !strings.Contains(got, "[redacted]") {
		t.Fatalf("redact = %q", got)
	}
}

func TestCustomHeaderValueRedaction(t *testing.T) {
	client := newClient(configProvider{
		baseURL: "https://example.invalid",
		headers: map[string]string{"X-Api-Key": "header-secret-value"},
	})
	got := client.sanitize("provider echoed header-secret-value")
	if strings.Contains(got, "header-secret-value") || !strings.Contains(got, "[redacted]") {
		t.Fatalf("sanitize = %q", got)
	}
	reg := NewRegistry(config.Config{Providers: []config.Provider{{
		ID: "mock", BaseURL: "https://example.invalid", Enabled: true,
		Headers: map[string]string{"X-Api-Key": "header-secret-value"},
	}}}, nil)
	if got := reg.Redact("log said header-secret-value"); strings.Contains(got, "header-secret-value") {
		t.Fatalf("registry redact = %q", got)
	}
}

func TestClientConfigValidation(t *testing.T) {
	client := newClient(configProvider{})
	if _, err := client.StreamChat(context.Background(), "m", nil, nil); err == nil {
		t.Fatal("expected missing base URL error")
	}
	client = newClient(configProvider{baseURL: "https://example.invalid"})
	if _, err := client.StreamChat(context.Background(), "", nil, nil); err == nil {
		t.Fatal("expected missing model error")
	}
}

func TestChatRequestShape(t *testing.T) {
	payload, err := json.Marshal(chatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["stream"] != true || decoded["model"] != "m" {
		t.Fatalf("request = %#v", decoded)
	}
	if len(decoded) != 3 {
		t.Fatalf("unexpected request fields: %#v", decoded)
	}
}
