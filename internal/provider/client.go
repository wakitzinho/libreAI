// Package provider implements the OpenAI-compatible Chat Completions client,
// optional model discovery, and in-memory discovery caching.
package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	maxErrorBody      = 8 << 10
	maxDiscoveryBody  = 2 << 20
	maxCompletionBody = 4 << 20
	maxDeltaBytes     = 2 << 20
)

// ChatMessage is a single prompt message sent upstream.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Error is a safe, user-presentable provider error. It may wrap a lower-level
// cause for server logs, but Error() never contains credentials.
type Error struct {
	Kind       string // cancelled, timeout, network, auth, rate_limit, bad_request, upstream, bad_response, config
	StatusCode int
	RetryAfter string
	Message    string
	cause      error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }

// StreamResult describes how an upstream generation ended.
type StreamResult struct {
	FinishReason string
}

// Client talks to one configured OpenAI-compatible provider.
type Client struct {
	id      string
	name    string
	baseURL string
	apiKey  string
	noAuth  bool
	headers map[string]string
	http    *http.Client
}

func newClient(p configProvider) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 2
	transport.MaxIdleConns = 8
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 35 * time.Second
	return &Client{
		id:      p.id,
		name:    p.name,
		baseURL: p.baseURL,
		apiKey:  p.apiKey,
		noAuth:  p.noAuth,
		headers: p.headers,
		http:    &http.Client{Transport: transport},
	}
}

// configProvider avoids an import cycle in tests while letting config.Provider
// be converted by the registry.
type configProvider struct {
	id, name, baseURL, apiKey string
	noAuth                    bool
	headers                   map[string]string
}

func (c *Client) ID() string   { return c.id }
func (c *Client) Name() string { return c.name }

func (c *Client) setHeaders(req *http.Request, accept string) {
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "libreAI")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
}

func (c *Client) sanitize(s string) string {
	s = strings.TrimSpace(s)
	if c.apiKey != "" {
		s = strings.ReplaceAll(s, c.apiKey, "[redacted]")
	}
	// Custom headers may also carry credentials. Redact values long enough to
	// be plausible secrets while avoiding replacing very short common words.
	for _, value := range c.headers {
		if len(value) >= 6 {
			s = strings.ReplaceAll(s, value, "[redacted]")
		}
	}
	// Keep provider-controlled text on one safe line.
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// StreamChat opens a streaming Chat Completions request and invokes onDelta for
// each text fragment. It never retries and never falls back to another model.
func (c *Client) StreamChat(ctx context.Context, model string, messages []ChatMessage, onDelta func(string) error) (StreamResult, error) {
	if c.baseURL == "" {
		return StreamResult{}, &Error{Kind: "config", Message: "Provider endpoint is not configured."}
	}
	if strings.TrimSpace(model) == "" {
		return StreamResult{}, &Error{Kind: "config", Message: "No model was selected."}
	}
	body := chatRequest{Model: model, Messages: messages, Stream: true}
	payload, err := jsonMarshal(body)
	if err != nil {
		return StreamResult{}, &Error{Kind: "config", Message: "Could not prepare the provider request.", cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return StreamResult{}, &Error{Kind: "config", Message: "Provider endpoint is invalid.", cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req, "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return StreamResult{}, c.transportError(ctx, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return StreamResult{}, c.httpError(resp)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") && !strings.Contains(contentType, "event-stream") {
		return c.readNonStreaming(resp.Body, onDelta)
	}
	return c.readStream(ctx, resp.Body, onDelta)
}

func (c *Client) transportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return &Error{Kind: "timeout", Message: "The provider request timed out.", cause: err}
		}
		return &Error{Kind: "cancelled", Message: "Generation was cancelled.", cause: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &Error{Kind: "timeout", Message: "The provider request timed out.", cause: err}
	}
	return &Error{Kind: "network", Message: "Could not reach the provider. Check its endpoint and your network connection.", cause: err}
}

func (c *Client) httpError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	upstream := extractErrorMessage(body)
	upstream = c.sanitize(upstream)
	e := &Error{StatusCode: resp.StatusCode, RetryAfter: strings.TrimSpace(resp.Header.Get("Retry-After"))}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		e.Kind = "auth"
		e.Message = "The provider rejected its API key. Check provider configuration in .env and restart libreAI."
	case http.StatusTooManyRequests:
		e.Kind = "rate_limit"
		e.Message = "The provider rate limit was reached. Try again shortly."
		if e.RetryAfter != "" {
			e.Message += " (Retry-After: " + c.sanitize(e.RetryAfter) + ")"
		}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		e.Kind = "bad_request"
		e.Message = "The provider rejected the request."
		if upstream != "" {
			e.Message += " " + upstream
		}
	case http.StatusNotFound:
		e.Kind = "bad_request"
		e.Message = "The provider could not find that model or endpoint."
		if upstream != "" {
			e.Message += " " + upstream
		}
	default:
		e.Kind = "upstream"
		e.Message = fmt.Sprintf("The provider returned an error (HTTP %d).", resp.StatusCode)
		if upstream != "" && resp.StatusCode >= 500 {
			e.Message += " " + upstream
		}
	}
	return e
}

func (c *Client) readNonStreaming(body io.Reader, onDelta func(string) error) (StreamResult, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxCompletionBody+1))
	if err != nil {
		return StreamResult{}, &Error{Kind: "network", Message: "Could not read the provider response.", cause: err}
	}
	if len(data) > maxCompletionBody {
		return StreamResult{}, &Error{Kind: "bad_response", Message: "The provider response was too large."}
	}
	var resp chatResponse
	if err := jsonUnmarshal(data, &resp); err != nil {
		return StreamResult{}, &Error{Kind: "bad_response", Message: "The provider returned an invalid response.", cause: err}
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return StreamResult{}, &Error{Kind: "upstream", Message: "Provider error: " + c.sanitize(resp.Error.Message)}
	}
	if len(resp.Choices) == 0 {
		return StreamResult{}, &Error{Kind: "bad_response", Message: "The provider returned no conversation choices."}
	}
	choice := resp.Choices[0]
	if choice.Message.Content == "" {
		return StreamResult{}, &Error{Kind: "bad_response", Message: "The provider returned an empty response."}
	}
	if err := onDelta(choice.Message.Content); err != nil {
		return StreamResult{}, err
	}
	finish := ""
	if choice.FinishReason != nil {
		finish = *choice.FinishReason
	}
	return StreamResult{FinishReason: finish}, nil
}

func (c *Client) readStream(ctx context.Context, body io.Reader, onDelta func(string) error) (StreamResult, error) {
	decoder := NewSSEDecoder(body, 1<<20)
	var total int
	finished := false
	finishReason := ""
	for {
		event, err := decoder.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return StreamResult{}, c.transportError(ctx, ctxErr)
			}
			return StreamResult{}, &Error{Kind: "bad_response", Message: "The provider sent malformed streaming data.", cause: err}
		}
		data := strings.TrimSpace(event.Data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			finished = true
			break
		}
		var chunk chatStreamChunk
		if err := jsonUnmarshal([]byte(data), &chunk); err != nil {
			return StreamResult{}, &Error{Kind: "bad_response", Message: "The provider sent malformed streaming data.", cause: err}
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return StreamResult{}, &Error{Kind: "upstream", Message: "Provider error: " + c.sanitize(chunk.Error.Message)}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				total += len(choice.Delta.Content)
				if total > maxDeltaBytes {
					return StreamResult{}, &Error{Kind: "bad_response", Message: "The provider response was too large."}
				}
				if err := onDelta(choice.Delta.Content); err != nil {
					return StreamResult{}, err
				}
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finished = true
				finishReason = *choice.FinishReason
			}
		}
		if finished {
			break
		}
		if err := ctx.Err(); err != nil {
			return StreamResult{}, c.transportError(ctx, err)
		}
	}
	if !finished {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return StreamResult{}, c.transportError(ctx, ctxErr)
		}
		return StreamResult{}, &Error{Kind: "bad_response", Message: "The provider stream ended before a completion signal. The response may be incomplete."}
	}
	return StreamResult{FinishReason: finishReason}, nil
}

func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var wrapper struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := jsonUnmarshal(body, &wrapper); err == nil {
		if wrapper.Error.Message != "" {
			return wrapper.Error.Message
		}
		if wrapper.Message != "" {
			return wrapper.Message
		}
		if wrapper.Detail != "" {
			return wrapper.Detail
		}
	}
	return ""
}
