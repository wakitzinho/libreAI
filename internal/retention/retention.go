// Package retention deletes chats that have been inactive for the configured
// retention period. It runs at startup and on a lightweight periodic timer; no
// external scheduler is required.
package retention

import (
	"context"
	"log/slog"
	"time"

	"libreai/internal/store"
)

// Cleaner removes expired chats.
type Cleaner struct {
	store     *store.Store
	retention time.Duration
	interval  time.Duration
	logger    *slog.Logger
}

// New creates a cleaner. The default interval is six hours, which is frequent
// enough for a personal chat application and avoids constant full-table scans.
func New(s *store.Store, retentionDays int, logger *slog.Logger) *Cleaner {
	if retentionDays < 1 {
		retentionDays = 30
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Cleaner{
		store:     s,
		retention: time.Duration(retentionDays) * 24 * time.Hour,
		interval:  6 * time.Hour,
		logger:    logger,
	}
}

// Run performs an immediate cleanup (important after the server was offline),
// then repeats until ctx is cancelled.
func (c *Cleaner) Run(ctx context.Context) {
	c.Cleanup(ctx)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Cleanup(ctx)
		}
	}
}

// Cleanup deletes chats and their messages older than the retention window.
// It logs only the count, never chat content.
func (c *Cleaner) Cleanup(ctx context.Context) {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cutoff := time.Now().UTC().Add(-c.retention).UnixMilli()
	n, err := c.store.DeleteExpiredChats(callCtx, cutoff)
	if err != nil {
		c.logger.Error("chat retention cleanup failed", "error", err)
		return
	}
	if n > 0 {
		c.logger.Info("deleted expired chats", "count", n, "retention_days", int(c.retention.Hours()/24))
	}
}
