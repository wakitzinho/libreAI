package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const messageColumns = "id, chat_id, role, content, status, error, sources, created_at, updated_at"

func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	err := row.Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &m.Status, &m.Error, &m.Sources, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	return m, err
}

func listMessages(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, chatID string) ([]Message, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT "+messageColumns+" FROM messages WHERE chat_id = ? ORDER BY id", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ListMessages(ctx context.Context, chatID string) ([]Message, error) {
	return listMessages(ctx, s.db, chatID)
}

// AddUserMessage inserts a user message and records activity in one
// transaction. It does not update the title; the caller does that with
// SetTitleIfDefault.
func (s *Store) AddUserMessage(ctx context.Context, chatID, content string) (Message, error) {
	if strings.TrimSpace(content) == "" {
		return Message{}, fmt.Errorf("%w: message content is empty", ErrInvalid)
	}
	now := NowMillis()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM chats WHERE id = ?", chatID).Scan(&exists); err != nil {
		return Message{}, err
	}
	if exists == 0 {
		return Message{}, ErrNotFound
	}
	res, err := tx.ExecContext(ctx,
		"INSERT INTO messages(chat_id, role, content, status, created_at, updated_at) VALUES (?, 'user', ?, 'completed', ?, ?)",
		chatID, content, now, now)
	if err != nil {
		return Message{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Message{}, err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE chats SET updated_at = ?, last_activity_at = ? WHERE id = ?", now, now, chatID); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return Message{ID: id, ChatID: chatID, Role: "user", Content: content, Status: "completed", CreatedAt: now, UpdatedAt: now}, nil
}

// AddAssistantPlaceholder creates the assistant row before streaming starts.
// The row is updated as text arrives, so a crash leaves an interrupted
// message rather than silently losing the generation.
func (s *Store) AddAssistantPlaceholder(ctx context.Context, chatID string) (Message, error) {
	now := NowMillis()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM chats WHERE id = ?", chatID).Scan(&exists); err != nil {
		return Message{}, err
	}
	if exists == 0 {
		return Message{}, ErrNotFound
	}
	res, err := tx.ExecContext(ctx,
		"INSERT INTO messages(chat_id, role, content, status, created_at, updated_at) VALUES (?, 'assistant', '', 'streaming', ?, ?)",
		chatID, now, now)
	if err != nil {
		return Message{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Message{}, err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE chats SET updated_at = ?, last_activity_at = ? WHERE id = ?", now, now, chatID); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return Message{ID: id, ChatID: chatID, Role: "assistant", Content: "", Status: "streaming", CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) UpdateMessage(ctx context.Context, messageID int64, content, status, errText, sources string) error {
	switch status {
	case "completed", "streaming", "error", "cancelled", "interrupted":
	default:
		return fmt.Errorf("%w: invalid message status", ErrInvalid)
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE messages SET content = ?, status = ?, error = ?, sources = ?, updated_at = ? WHERE id = ?",
		content, status, errText, sources, NowMillis(), messageID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteMessage(ctx context.Context, chatID string, messageID int64) error {
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM messages WHERE id = ? AND chat_id = ?", messageID, chatID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// EditUserMessage replaces a user message, removes everything after it, and
// records activity. Only user messages may be edited.
func (s *Store) EditUserMessage(ctx context.Context, chatID string, messageID int64, content string) (Message, error) {
	if strings.TrimSpace(content) == "" {
		return Message{}, fmt.Errorf("%w: message content is empty", ErrInvalid)
	}
	now := NowMillis()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	m, err := scanMessage(tx.QueryRowContext(ctx,
		"SELECT "+messageColumns+" FROM messages WHERE id = ? AND chat_id = ?", messageID, chatID))
	if err != nil {
		return Message{}, err
	}
	if m.Role != "user" {
		return Message{}, fmt.Errorf("%w: only user messages can be edited", ErrInvalid)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE chat_id = ? AND id > ?", chatID, messageID); err != nil {
		return Message{}, err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE messages SET content = ?, status = 'completed', error = '', updated_at = ? WHERE id = ?",
		content, now, messageID); err != nil {
		return Message{}, err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE chats SET updated_at = ?, last_activity_at = ? WHERE id = ?", now, now, chatID); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	m.Content = content
	m.Status = "completed"
	m.Error = ""
	m.UpdatedAt = now
	return m, nil
}

// DeleteLastAssistant removes the final assistant message when regenerating.
// If the final message is a user message (for example after an empty failed
// generation was cleaned up), nothing is deleted.
func (s *Store) DeleteLastAssistant(ctx context.Context, chatID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id int64
	var role string
	err = tx.QueryRowContext(ctx,
		"SELECT id, role FROM messages WHERE chat_id = ? ORDER BY id DESC LIMIT 1", chatID).Scan(&id, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if role != "assistant" {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE id = ?", id); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// GetMessage returns one message scoped to a chat.
func (s *Store) GetMessage(ctx context.Context, chatID string, messageID int64) (Message, error) {
	return scanMessage(s.db.QueryRowContext(ctx,
		"SELECT "+messageColumns+" FROM messages WHERE id = ? AND chat_id = ?", messageID, chatID))
}

// GetSettings returns all small settings as a map.
func (s *Store) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSettings upserts a set of settings atomically.
func (s *Store) SetSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range values {
		if k == "" || len(k) > 100 || len(v) > 10000 {
			return fmt.Errorf("%w: invalid setting", ErrInvalid)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
			k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}
