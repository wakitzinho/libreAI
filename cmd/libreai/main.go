// Command libreai runs the libreAI HTTP server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"libreai/internal/config"
	"libreai/internal/httpserver"
	"libreai/internal/provider"
	"libreai/internal/retention"
	"libreai/internal/store"
)

// version is replaced at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "libreai:", err)
		os.Exit(1)
	}
}

func run() error {
	envDefault := os.Getenv("ENV_FILE")
	if strings.TrimSpace(envDefault) == "" {
		envDefault = ".env"
	}
	envFile := flag.String("env", envDefault, "path to the .env configuration file")
	healthcheck := flag.Bool("healthcheck", false, "check the local /api/health endpoint and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, warnings, err := config.Load(*envFile)
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel)
	for _, warning := range warnings {
		logger.Warn("configuration warning", "detail", warning)
	}

	if *healthcheck {
		return checkHealth(cfg)
	}

	logger.Info("starting libreAI", "version", version, "go", runtime.Version(), "addr", cfg.ListenAddr())
	if len(cfg.Providers) == 0 {
		logger.Warn("no providers configured; add provider settings to the .env file and restart")
	}

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("close database", "error", err)
		}
	}()

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	recovered, err := st.RecoverInterrupted(startupCtx)
	startupCancel()
	if err != nil {
		return fmt.Errorf("recover interrupted messages: %w", err)
	}
	if recovered > 0 {
		logger.Info("marked interrupted assistant messages", "count", recovered)
	}

	registry := provider.NewRegistry(cfg, logger)
	handler, err := httpserver.New(cfg, st, registry, version, logger)
	if err != nil {
		return fmt.Errorf("create http server: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Retention runs immediately (catching up after downtime) and then every
	// six hours. Model discovery is best-effort and never blocks startup.
	go retention.New(st, cfg.RetentionDays, logger).Run(ctx)
	go func() {
		discoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		registry.Refresh(discoveryCtx, false)
	}()

	listener, err := net.Listen("tcp", cfg.ListenAddr())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddr(), err)
	}

	server := &http.Server{
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // SSE responses can legitimately outlive a fixed deadline.
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", listener.Addr().String(), "database", cfg.DatabasePath)
		errCh <- server.Serve(listener)
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutdown requested")
	}

	// Stop the signal handler so a second signal cannot interrupt the graceful
	// shutdown with an immediate exit.
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown timed out; closing", "error", err)
		_ = server.Close()
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	logger.Info("stopped")
	return nil
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv}))
}

// checkHealth requests the local health endpoint. It is used by the Docker
// health check and by service managers; it never accepts a remote URL.
func checkHealth(cfg config.Config) error {
	host := cfg.Host
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::", "[::]":
		host = "::1"
	}
	url := "http://" + net.JoinHostPort(host, fmt.Sprintf("%d", cfg.Port)) + "/api/health"
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}
