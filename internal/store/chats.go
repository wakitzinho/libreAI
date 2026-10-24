package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func scanChat(row interface{ Scan(...any) error }) (Chat, error) {
	var c Chat
	var project sql.NullString
	err := row.Scan(&c.ID, &c.Title, &project, &c.ProviderID, &c.ModelID,
		&c.CreatedAt, &c.UpdatedAt, &c.LastActivityAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Chat{}, ErrNotFound
	}
	if err != nil {
		return Chat{}, err
	}
	if project.Valid {
		v := project.String
		c.ProjectID = &v
	}
	return c, nil
}

const chatColumns = "id, title, project_id, provider_id, model_id, created_at, updated_at, last_activity_at"

func validateChatFields(title, providerID, modelID string) error {
	if title == "" || len(title) > 200 {
		return fmt.Errorf("%w: chat title must be 1-200 characters", ErrInvalid)
	}
	if len(providerID) > 64 || len(modelID) > 200 {
		return fmt.Errorf("%w: provider or model ID is too long", ErrInvalid)
	}
	if hasControl(title) || hasControl(providerID) || hasControl(modelID) {
		return fmt.Errorf("%w: chat fields contain control characters", ErrInvalid)
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func (s *Store) CreateChat(ctx context.Context, title string, projectID *string, providerID, modelID string) (Chat, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "New chat"
	}
	if err := validateChatFields(title, providerID, modelID); err != nil {
		return Chat{}, err
	}
	now := NowMillis()
	c := Chat{
		ID: NewID(), Title: title, ProjectID: projectID, ProviderID: providerID,
		ModelID: modelID, CreatedAt: now, UpdatedAt: now, LastActivityAt: now,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Chat{}, err
	}
	defer tx.Rollback()
	if projectID != nil {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE id = ?", *projectID).Scan(&exists); err != nil {
			return Chat{}, err
		}
		if exists == 0 {
			return Chat{}, fmt.Errorf("%w: project", ErrNotFound)
		}
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO chats(id, title, project_id, provider_id, model_id, created_at, updated_at, last_activity_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		c.ID, c.Title, c.ProjectID, c.ProviderID, c.ModelID, c.CreatedAt, c.UpdatedAt, c.LastActivityAt)
	if err != nil {
		return Chat{}, err
	}
	if err := tx.Commit(); err != nil {
		return Chat{}, err
	}
	return c, nil
}

func (s *Store) GetChat(ctx context.Context, id string) (Chat, error) {
	return scanChat(s.db.QueryRowContext(ctx,
		"SELECT "+chatColumns+" FROM chats WHERE id = ?", id))
}

func (s *Store) ListChats(ctx context.Context, search string, limit int) ([]Chat, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	search = strings.TrimSpace(search)
	var rows *sql.Rows
	var err error
	if search == "" {
		rows, err = s.db.QueryContext(ctx,
			"SELECT "+chatColumns+" FROM chats ORDER BY last_activity_at DESC, id LIMIT ?", limit)
	} else {
		pattern := "%" + escapeLike(search) + "%"
		rows, err = s.db.QueryContext(ctx,
			"SELECT "+chatColumns+" FROM chats WHERE title LIKE ? ESCAPE '\\' ORDER BY last_activity_at DESC, id LIMIT ?",
			pattern, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chat{}
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

func (s *Store) RenameChat(ctx context.Context, id, title string) (Chat, error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 200 || hasControl(title) {
		return Chat{}, fmt.Errorf("%w: chat title must be 1-200 characters", ErrInvalid)
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE chats SET title = ?, updated_at = ? WHERE id = ?", title, NowMillis(), id)
	if err != nil {
		return Chat{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Chat{}, ErrNotFound
	}
	return s.GetChat(ctx, id)
}

func (s *Store) SetChatModel(ctx context.Context, id, providerID, modelID string) (Chat, error) {
	if len(providerID) > 64 || len(modelID) > 200 || hasControl(providerID) || hasControl(modelID) {
		return Chat{}, fmt.Errorf("%w: invalid provider or model", ErrInvalid)
	}
	if providerID == "" || modelID == "" {
		return Chat{}, fmt.Errorf("%w: provider and model are required", ErrInvalid)
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE chats SET provider_id = ?, model_id = ?, updated_at = ? WHERE id = ?",
		providerID, modelID, NowMillis(), id)
	if err != nil {
		return Chat{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Chat{}, ErrNotFound
	}
	return s.GetChat(ctx, id)
}

func (s *Store) MoveChat(ctx context.Context, id string, projectID *string) (Chat, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Chat{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM chats WHERE id = ?", id).Scan(&exists); err != nil {
		return Chat{}, err
	}
	if exists == 0 {
		return Chat{}, ErrNotFound
	}
	if projectID != nil {
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE id = ?", *projectID).Scan(&exists); err != nil {
			return Chat{}, err
		}
		if exists == 0 {
			return Chat{}, fmt.Errorf("%w: project", ErrNotFound)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE chats SET project_id = ? WHERE id = ?", projectID, id); err != nil {
		return Chat{}, err
	}
	c, err := scanChat(tx.QueryRowContext(ctx, "SELECT "+chatColumns+" FROM chats WHERE id = ?", id))
	if err != nil {
		return Chat{}, err
	}
	if err := tx.Commit(); err != nil {
		return Chat{}, err
	}
	return c, nil
}

func (s *Store) DeleteChat(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM chats WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchChat records genuine user activity (opening a chat or sending a
// message). Background fetches must not call this.
func (s *Store) TouchChat(ctx context.Context, id string, now int64) error {
	res, err := s.db.ExecContext(ctx, "UPDATE chats SET last_activity_at = ? WHERE id = ?", now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// OpenChat records activity and returns the chat with its messages in a
// single transaction.
func (s *Store) OpenChat(ctx context.Context, id string, now int64) (Chat, []Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Chat{}, nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE chats SET last_activity_at = ? WHERE id = ?", now, id)
	if err != nil {
		return Chat{}, nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Chat{}, nil, ErrNotFound
	}
	c, err := scanChat(tx.QueryRowContext(ctx, "SELECT "+chatColumns+" FROM chats WHERE id = ?", id))
	if err != nil {
		return Chat{}, nil, err
	}
	messages, err := listMessages(ctx, tx, id)
	if err != nil {
		return Chat{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Chat{}, nil, err
	}
	return c, messages, nil
}

func (s *Store) SetTitleIfDefault(ctx context.Context, id, title, defaultTitle string) (bool, error) {
	if title == "" || len(title) > 200 || hasControl(title) {
		return false, fmt.Errorf("%w: invalid title", ErrInvalid)
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE chats SET title = ?, updated_at = ? WHERE id = ? AND title = ?",
		title, NowMillis(), id, defaultTitle)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chats WHERE id = ?", id).Scan(&exists); err != nil {
			return false, err
		}
		if exists == 0 {
			return false, ErrNotFound
		}
	}
	return n > 0, nil
}

// DeleteExpiredChats removes chats whose last activity is older than the
// cutoff. Messages are removed by ON DELETE CASCADE. Projects are untouched.
func (s *Store) DeleteExpiredChats(ctx context.Context, cutoffMillis int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "DELETE FROM chats WHERE last_activity_at < ?", cutoffMillis)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// RecoverInterrupted marks assistant messages left in streaming state by an
// unclean shutdown. It runs once at startup.
func (s *Store) RecoverInterrupted(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE messages
		 SET status = 'interrupted',
		     error = CASE WHEN error = '' THEN 'Server stopped before this response completed.' ELSE error END,
		     updated_at = ?
		 WHERE status = 'streaming'`, NowMillis())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
