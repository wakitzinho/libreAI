package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "nested", "libreai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func TestProjectCRUDAndDeleteMovesChats(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	p, err := s.CreateProject(ctx, "  Work  ")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.Name != "Work" || p.ID == "" {
		t.Fatalf("project = %+v", p)
	}
	projects, err := s.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects = %#v, %v", projects, err)
	}
	renamed, err := s.RenameProject(ctx, p.ID, "Home lab")
	if err != nil || renamed.Name != "Home lab" {
		t.Fatalf("RenameProject = %+v, %v", renamed, err)
	}

	chat, err := s.CreateChat(ctx, "Chat", &p.ID, "prov", "model")
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	if chat.ProjectID == nil || *chat.ProjectID != p.ID {
		t.Fatalf("chat project = %v", chat.ProjectID)
	}

	moved, err := s.DeleteProject(ctx, p.ID)
	if err != nil || moved != 1 {
		t.Fatalf("DeleteProject = %d, %v", moved, err)
	}
	chat, err = s.GetChat(ctx, chat.ID)
	if err != nil {
		t.Fatalf("GetChat after project delete: %v", err)
	}
	if chat.ProjectID != nil {
		t.Fatalf("chat project = %v, want nil", *chat.ProjectID)
	}
	if _, err := s.GetProject(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted project error = %v", err)
	}
}

func TestInvalidInputs(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateProject(ctx, "   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty project error = %v", err)
	}
	if _, err := s.CreateProject(ctx, string(make([]byte, 101))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long project error = %v", err)
	}
	missing := "ffffffffffffffffffffffffffffffff"
	if _, err := s.CreateChat(ctx, "x", &missing, "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("create chat with missing project error = %v", err)
	}
	chat, err := s.CreateChat(ctx, "x", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUserMessage(ctx, chat.ID, "   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty message error = %v", err)
	}
	if _, err := s.CreateChat(ctx, "bad\x00title", nil, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("control character title error = %v", err)
	}
}

func TestChatCRUDSearchAndCascade(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	first, err := s.CreateChat(ctx, "Alpha 100%", nil, "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateChat(ctx, "Beta_underscore", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Title != "Alpha 100%" || second.Title != "Beta_underscore" {
		t.Fatalf("unexpected titles")
	}

	list, err := s.ListChats(ctx, "100%", 10)
	if err != nil || len(list) != 1 || list[0].ID != first.ID {
		t.Fatalf("literal wildcard search = %#v, %v", list, err)
	}
	list, err = s.ListChats(ctx, "%", 10)
	if err != nil || len(list) != 1 || list[0].ID != first.ID {
		t.Fatalf("percent search = %#v, %v", list, err)
	}
	list, err = s.ListChats(ctx, "_", 10)
	if err != nil || len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("underscore search = %#v, %v", list, err)
	}

	if _, err := s.RenameChat(ctx, first.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty rename error = %v", err)
	}
	renamed, err := s.RenameChat(ctx, first.ID, "Renamed")
	if err != nil || renamed.Title != "Renamed" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	updated, err := s.SetChatModel(ctx, first.ID, "p2", "m2")
	if err != nil || updated.ProviderID != "p2" || updated.ModelID != "m2" {
		t.Fatalf("SetChatModel = %+v, %v", updated, err)
	}
	if _, err := s.SetChatModel(ctx, first.ID, "", "m"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty provider error = %v", err)
	}

	if _, err := s.AddUserMessage(ctx, first.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAssistantPlaceholder(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteChat(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetChat(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted chat error = %v", err)
	}
	if err := s.DeleteChat(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete error = %v", err)
	}
}

func TestMoveChat(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	p, _ := s.CreateProject(ctx, "Project")
	chat, _ := s.CreateChat(ctx, "Chat", nil, "", "")
	moved, err := s.MoveChat(ctx, chat.ID, &p.ID)
	if err != nil || moved.ProjectID == nil || *moved.ProjectID != p.ID {
		t.Fatalf("MoveChat = %+v, %v", moved, err)
	}
	unfiled, err := s.MoveChat(ctx, chat.ID, nil)
	if err != nil || unfiled.ProjectID != nil {
		t.Fatalf("MoveChat nil = %+v, %v", unfiled, err)
	}
	if _, err := s.MoveChat(ctx, chat.ID, ptr("ffffffffffffffffffffffffffffffff")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("move to missing project error = %v", err)
	}
}

func ptr(s string) *string { return &s }

func TestMessagesEditRegenerateAndRecover(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	chat, _ := s.CreateChat(ctx, "Chat", nil, "", "")

	u1, err := s.AddUserMessage(ctx, chat.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	a1, err := s.AddAssistantPlaceholder(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Status != "streaming" {
		t.Fatalf("placeholder status = %q", a1.Status)
	}
	if err := s.UpdateMessage(ctx, a1.ID, "answer one", "completed", "", `[{"title":"Example","url":"https://example.com"}]`); err != nil {
		t.Fatal(err)
	}
	u2, _ := s.AddUserMessage(ctx, chat.ID, "second")
	a2, _ := s.AddAssistantPlaceholder(ctx, chat.ID)
	if err := s.UpdateMessage(ctx, a2.ID, "partial", "cancelled", "stopped", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMessage(ctx, a2.ID, "partial", "bogus", "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid status error = %v", err)
	}
	stored, err := s.GetMessage(ctx, chat.ID, a1.ID)
	if err != nil || !strings.Contains(stored.Sources, "https://example.com") {
		t.Fatalf("stored sources = %+v, %v", stored, err)
	}

	// Editing the first user message removes everything after it.
	if _, err := s.EditUserMessage(ctx, chat.ID, a1.ID, "assistant edit"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("editing assistant error = %v", err)
	}
	edited, err := s.EditUserMessage(ctx, chat.ID, u1.ID, "first edited")
	if err != nil || edited.Content != "first edited" {
		t.Fatalf("EditUserMessage = %+v, %v", edited, err)
	}
	msgs, err := s.ListMessages(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != u1.ID {
		t.Fatalf("messages after edit = %+v", msgs)
	}

	// Regeneration deletes only a trailing assistant message.
	a3, _ := s.AddAssistantPlaceholder(ctx, chat.ID)
	if err := s.UpdateMessage(ctx, a3.ID, "answer", "completed", "", ""); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.DeleteLastAssistant(ctx, chat.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteLastAssistant = %v, %v", deleted, err)
	}
	deleted, err = s.DeleteLastAssistant(ctx, chat.ID)
	if err != nil || deleted {
		t.Fatalf("DeleteLastAssistant on user tail = %v, %v", deleted, err)
	}

	// A crash leaves streaming rows; recovery marks them interrupted.
	streaming, _ := s.AddAssistantPlaceholder(ctx, chat.ID)
	if _, err := s.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.GetMessage(ctx, chat.ID, streaming.ID)
	if err != nil || recovered.Status != "interrupted" || recovered.Error == "" {
		t.Fatalf("recovered = %+v, %v", recovered, err)
	}

	_ = u2
}

func TestOpenChatTouchesActivity(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	chat, _ := s.CreateChat(ctx, "Chat", nil, "", "")
	old := chat.LastActivityAt - 10_000
	if err := s.TouchChat(ctx, chat.ID, old); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetChat(ctx, chat.ID)
	if err != nil || got.LastActivityAt != old {
		t.Fatalf("TouchChat = %+v, %v", got, err)
	}
	if err := s.TouchChat(ctx, "ffffffffffffffffffffffffffffffff", old); !errors.Is(err, ErrNotFound) {
		t.Fatalf("touch missing error = %v", err)
	}
	opened, messages, err := s.OpenChat(ctx, chat.ID, old+5000)
	if err != nil || opened.LastActivityAt != old+5000 || len(messages) != 0 {
		t.Fatalf("OpenChat = %+v, %#v, %v", opened, messages, err)
	}
	if _, _, err := s.OpenChat(ctx, "ffffffffffffffffffffffffffffffff", old); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open missing error = %v", err)
	}
}

func TestSetTitleIfDefault(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	chat, _ := s.CreateChat(ctx, "New chat", nil, "", "")
	changed, err := s.SetTitleIfDefault(ctx, chat.ID, "Derived title", "New chat")
	if err != nil || !changed {
		t.Fatalf("SetTitleIfDefault = %v, %v", changed, err)
	}
	changed, err = s.SetTitleIfDefault(ctx, chat.ID, "Another title", "New chat")
	if err != nil || changed {
		t.Fatalf("second SetTitleIfDefault = %v, %v", changed, err)
	}
	if _, err := s.SetTitleIfDefault(ctx, chat.ID, "", "New chat"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid title error = %v", err)
	}
}

func TestRetentionCleanupKeepsProjects(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	p, _ := s.CreateProject(ctx, "Keep me")
	old, _ := s.CreateChat(ctx, "Old", &p.ID, "", "")
	recent, _ := s.CreateChat(ctx, "Recent", &p.ID, "", "")
	if _, err := s.AddUserMessage(ctx, old.ID, "old message"); err != nil {
		t.Fatal(err)
	}
	oldTime := NowMillis() - 31*24*60*60*1000
	if err := s.TouchChat(ctx, old.ID, oldTime); err != nil {
		t.Fatal(err)
	}

	deleted, err := s.DeleteExpiredChats(ctx, NowMillis()-30*24*60*60*1000)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteExpiredChats = %d, %v", deleted, err)
	}
	if _, err := s.GetChat(ctx, old.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old chat survived: %v", err)
	}
	if _, err := s.GetChat(ctx, recent.ID); err != nil {
		t.Fatalf("recent chat deleted: %v", err)
	}
	if _, err := s.GetProject(ctx, p.ID); err != nil {
		t.Fatalf("project deleted: %v", err)
	}
	msgs, err := s.ListMessages(ctx, old.ID)
	if err != nil {
		t.Fatalf("ListMessages orphan check: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("orphaned messages = %d", len(msgs))
	}
}

func TestSettings(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.SetSettings(ctx, map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSettings(ctx, map[string]string{"a": "3"}); err != nil {
		t.Fatal(err)
	}
	values, err := s.GetSettings(ctx)
	if err != nil || values["a"] != "3" || values["b"] != "2" {
		t.Fatalf("settings = %#v, %v", values, err)
	}
	if err := s.SetSettings(ctx, map[string]string{"": "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty setting key error = %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				chat, err := s.CreateChat(ctx, "Concurrent", nil, "", "")
				if err != nil {
					errs <- err
					return
				}
				if _, err := s.AddUserMessage(ctx, chat.ID, "hello"); err != nil {
					errs <- err
					return
				}
				if _, err := s.ListChats(ctx, "", 20); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent operation: %v", err)
	}
	list, err := s.ListChats(ctx, "", 100)
	if err != nil || len(list) != workers*5 {
		t.Fatalf("chats = %d, %v", len(list), err)
	}
}

func TestMessageSourcesMigrationFromOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "libreai.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	chat, err := s.CreateChat(ctx, "Old chat", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.AddUserMessage(ctx, chat.ID, "kept content")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a database created before migration 0002.
	if _, err := s.db.Exec("ALTER TABLE messages DROP COLUMN sources"); err != nil {
		t.Fatalf("drop sources: %v", err)
	}
	if _, err := s.db.Exec("DELETE FROM schema_migrations WHERE version = 2"); err != nil {
		t.Fatalf("reset migration: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen after migration: %v", err)
	}
	defer s.Close()
	messages, err := s.ListMessages(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "kept content" || messages[0].Sources != "" {
		t.Fatalf("migrated messages = %+v", messages)
	}
	if err := s.UpdateMessage(ctx, msg.ID, "kept content", "completed", "", `[{"title":"T","url":"https://example.com"}]`); err != nil {
		t.Fatalf("UpdateMessage after migration: %v", err)
	}
	stored, err := s.GetMessage(ctx, chat.ID, msg.ID)
	if err != nil || !strings.Contains(stored.Sources, "https://example.com") {
		t.Fatalf("stored message = %+v, %v", stored, err)
	}
}
