package retention

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"libreai/internal/store"
)

func TestCleanupDeletesOnlyExpiredChats(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "libreai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	old, _ := st.CreateChat(ctx, "Old chat", nil, "", "")
	recent, _ := st.CreateChat(ctx, "Recent chat", nil, "", "")
	if _, err := st.AddUserMessage(ctx, old.ID, "message"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(ctx, old.ID, time.Now().UTC().Add(-31*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	New(st, 30, logger).Cleanup(ctx)
	if _, err := st.GetChat(ctx, old.ID); err == nil {
		t.Fatal("expired chat was not deleted")
	}
	if _, err := st.GetChat(ctx, recent.ID); err != nil {
		t.Fatalf("recent chat was deleted: %v", err)
	}
}

func TestRunStopsWithContext(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "libreai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		New(st, 30, logger).Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cleaner did not stop")
	}
}
