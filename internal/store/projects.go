package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return p, err
}

func (s *Store) CreateProject(ctx context.Context, name string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return Project{}, fmt.Errorf("%w: project name must be 1-100 characters", ErrInvalid)
	}
	now := NowMillis()
	p := Project{ID: NewID(), Name: name, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO projects(id, name, created_at, updated_at) VALUES (?, ?, ?, ?)",
		p.ID, p.Name, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, name, created_at, updated_at FROM projects ORDER BY name COLLATE NOCASE, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProject(ctx context.Context, id string) (Project, error) {
	return scanProject(s.db.QueryRowContext(ctx,
		"SELECT id, name, created_at, updated_at FROM projects WHERE id = ?", id))
}

func (s *Store) RenameProject(ctx context.Context, id, name string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return Project{}, fmt.Errorf("%w: project name must be 1-100 characters", ErrInvalid)
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE projects SET name = ?, updated_at = ? WHERE id = ?", name, NowMillis(), id)
	if err != nil {
		return Project{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Project{}, ErrNotFound
	}
	return s.GetProject(ctx, id)
}

// DeleteProject removes a project and moves its chats to the unfiled area. It
// never deletes chats implicitly.
func (s *Store) DeleteProject(ctx context.Context, id string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE id = ?", id).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, ErrNotFound
	}
	res, err := tx.ExecContext(ctx, "UPDATE chats SET project_id = NULL WHERE project_id = ?", id)
	if err != nil {
		return 0, err
	}
	moved, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return moved, nil
}
