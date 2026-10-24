// Package httpserver exposes the JSON API, SSE streaming endpoint, and embedded
// frontend from a single Go HTTP server.
package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"libreai/internal/config"
	"libreai/internal/provider"
	"libreai/internal/store"
	"libreai/web"
)

// Server is the application's HTTP handler.
type Server struct {
	cfg       config.Config
	store     *store.Store
	providers *provider.Registry
	logger    *slog.Logger
	version   string
	mux       *http.ServeMux
	assets    map[string]asset
	limiter   chan struct{}

	activeMu sync.Mutex
	active   map[string]struct{}
}

type asset struct {
	data        []byte
	contentType string
	etag        string
}

// New builds the HTTP server and preloads all embedded frontend assets.
func New(cfg config.Config, st *store.Store, providers *provider.Registry, version string, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:       cfg,
		store:     st,
		providers: providers,
		logger:    logger,
		version:   version,
		mux:       http.NewServeMux(),
		assets:    map[string]asset{},
		limiter:   make(chan struct{}, cfg.MaxConcurrentGenerations),
		active:    map[string]struct{}{},
	}
	if err := s.loadAssets(); err != nil {
		return nil, err
	}
	s.routes()
	return s, nil
}

func (s *Server) loadAssets() error {
	err := fs.WalkDir(web.FS, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(web.FS, name)
		if err != nil {
			return err
		}
		urlPath := "/" + strings.TrimPrefix(name, "./")
		urlPath = path.Clean(urlPath)
		ct := mime.TypeByExtension(path.Ext(name))
		switch strings.ToLower(path.Ext(name)) {
		case ".js":
			ct = "text/javascript; charset=utf-8"
		case ".css":
			ct = "text/css; charset=utf-8"
		case ".svg":
			ct = "image/svg+xml"
		case ".html":
			ct = "text/html; charset=utf-8"
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		sum := sha256.Sum256(data)
		s.assets[urlPath] = asset{data: data, contentType: ct, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
		return nil
	})
	if err != nil {
		return fmt.Errorf("load embedded frontend: %w", err)
	}
	if _, ok := s.assets["/index.html"]; !ok {
		return errors.New("embedded frontend is missing index.html")
	}
	return nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/bootstrap", s.handleBootstrap)
	s.mux.HandleFunc("GET /api/models", s.handleModels)
	s.mux.HandleFunc("POST /api/models/refresh", s.handleRefreshModels)
	s.mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	s.mux.HandleFunc("PUT /api/settings", s.handlePutSettings)

	s.mux.HandleFunc("GET /api/projects", s.handleListProjects)
	s.mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	s.mux.HandleFunc("PATCH /api/projects/{id}", s.handleRenameProject)
	s.mux.HandleFunc("DELETE /api/projects/{id}", s.handleDeleteProject)

	s.mux.HandleFunc("GET /api/chats", s.handleListChats)
	s.mux.HandleFunc("POST /api/chats", s.handleCreateChat)
	s.mux.HandleFunc("GET /api/chats/{id}", s.handleGetChat)
	s.mux.HandleFunc("PATCH /api/chats/{id}", s.handlePatchChat)
	s.mux.HandleFunc("DELETE /api/chats/{id}", s.handleDeleteChat)
	s.mux.HandleFunc("POST /api/chats/{id}/open", s.handleOpenChat)
	s.mux.HandleFunc("POST /api/chats/{id}/move", s.handleMoveChat)
	s.mux.HandleFunc("POST /api/chats/{id}/messages", s.handleSendMessage)
	s.mux.HandleFunc("GET /api/chats/{id}/export", s.handleExportChat)

	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "API endpoint not found.")
	})
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "API endpoint not found.")
	})
}

// Handler returns the fully wrapped HTTP handler. API routes live in their own
// mux so the API catch-all and the static-file catch-all cannot conflict under
// Go's precise ServeMux pattern rules.
func (s *Server) Handler() http.Handler {
	root := http.NewServeMux()
	root.Handle("/api/", s.mux)
	root.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "API endpoint not found.")
	})
	root.HandleFunc("/", s.handleStatic)
	// Outer to inner: panic recovery, security headers, origin checks, logging.
	return s.recoverPanic(s.securityHeaders(s.originCheck(s.logRequests(root))))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := timeoutContext(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		s.logger.Error("health check database failure", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "version": s.version})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	requestPath := r.URL.Path
	if requestPath == "/" {
		requestPath = "/index.html"
	}
	a, ok := s.assets[requestPath]
	if !ok {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("ETag", a.etag)
	w.Header().Set("Cache-Control", "no-cache")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, a.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(a.data)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(a.data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type errorBody struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: apiError{Code: code, Message: message}})
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.logger.Error("request failed", "error", s.providers.Redact(err.Error()))
	writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
}

// --- middlewares -----------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		// Deliberately log only the path (no query or bodies) and no headers.
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path,
			"status", status, "duration_ms", time.Since(start).Milliseconds(), "bytes", sw.bytes)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; object-src 'none'; media-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
			writeError(w, http.StatusForbidden, "cross_origin", "Cross-origin requests are not allowed.")
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			writeError(w, http.StatusForbidden, "cross_origin", "Invalid request origin.")
			return
		}
		allowed := []string{r.Host}
		if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
			allowed = append(allowed, strings.TrimSpace(strings.Split(xfh, ",")[0]))
		}
		for _, host := range allowed {
			if host != "" && strings.EqualFold(u.Host, host) {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeError(w, http.StatusForbidden, "cross_origin", "Cross-origin requests are not allowed.")
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("panic serving request", "path", r.URL.Path, "panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- request helpers -------------------------------------------------------

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	ct := r.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil || mediaType != "application/json" {
		return &requestError{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "Content-Type must be application/json."}
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return &requestError{Status: http.StatusRequestEntityTooLarge, Code: "body_too_large", Message: "Request body is too large."}
		}
		return &requestError{Status: http.StatusBadRequest, Code: "invalid_json", Message: "Invalid JSON request body."}
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return &requestError{Status: http.StatusBadRequest, Code: "invalid_json", Message: "Request body must contain one JSON value."}
	}
	return nil
}

type requestError struct {
	Status  int
	Code    string
	Message string
}

func (e *requestError) Error() string { return e.Message }

func (s *Server) handleRequestError(w http.ResponseWriter, err error) bool {
	var re *requestError
	if errors.As(err, &re) {
		writeError(w, re.Status, re.Code, re.Message)
		return true
	}
	return false
}

func (s *Server) requireID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, http.StatusBadRequest, "invalid_id", "Invalid identifier.")
		return "", false
	}
	return id, true
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func timeoutContext(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

func marshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
