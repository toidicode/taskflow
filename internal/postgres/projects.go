package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/toidicode/taskflow/internal/project"
)

// ProjectStore lưu project trong bảng projects, cài interface project.Store.
type ProjectStore struct {
	db *sql.DB
}

var _ project.Store = (*ProjectStore)(nil)

// NewProjectStore tạo ProjectStore dùng pool db.
func NewProjectStore(db *sql.DB) *ProjectStore {
	return &ProjectStore{db: db}
}

func (s *ProjectStore) Add(ctx context.Context, p *project.Project) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	err := s.db.QueryRowContext(ctx,
		`INSERT INTO projects (name, description, owner_id) VALUES ($1, $2, $3) RETURNING id`,
		p.Name, p.Description, p.OwnerID,
	).Scan(&p.ID)
	if err != nil {
		return fmt.Errorf("thêm project: %w", err)
	}
	return nil
}

func (s *ProjectStore) Get(ctx context.Context, id int) (project.Project, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var p project.Project
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, description, owner_id FROM projects WHERE id = $1`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.OwnerID)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Project{}, fmt.Errorf("project #%d: %w", id, project.ErrNotFound)
	}
	if err != nil {
		return project.Project{}, fmt.Errorf("đọc project #%d: %w", id, err)
	}
	return p, nil
}

func (s *ProjectStore) List(ctx context.Context) ([]project.Project, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `SELECT id, name, description, owner_id FROM projects ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("liệt kê project: %w", err)
	}
	defer rows.Close()

	ps := []project.Project{}
	for rows.Next() {
		var p project.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.OwnerID); err != nil {
			return nil, fmt.Errorf("liệt kê project: %w", err)
		}
		ps = append(ps, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("liệt kê project: %w", err)
	}
	return ps, nil
}

func (s *ProjectStore) Update(ctx context.Context, p *project.Project) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	res, err := s.db.ExecContext(ctx,
		`UPDATE projects SET name = $2, description = $3, owner_id = $4 WHERE id = $1`,
		p.ID, p.Name, p.Description, p.OwnerID)
	if err != nil {
		return fmt.Errorf("sửa project #%d: %w", p.ID, err)
	}
	return affected(res, fmt.Errorf("project #%d: %w", p.ID, project.ErrNotFound))
}

func (s *ProjectStore) Delete(ctx context.Context, id int) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("xóa project #%d: %w", id, err)
	}
	return affected(res, fmt.Errorf("project #%d: %w", id, project.ErrNotFound))
}
