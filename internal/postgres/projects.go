package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/toidicode/taskflow/internal/postgres/dbgen"
	"github.com/toidicode/taskflow/internal/project"
)

// ProjectStore lưu project trong bảng projects qua code do sqlc sinh ra, cài interface project.Store.
type ProjectStore struct {
	q *dbgen.Queries
}

var _ project.Store = (*ProjectStore)(nil)

// NewProjectStore tạo ProjectStore dùng pool db.
func NewProjectStore(db *sql.DB) *ProjectStore {
	return &ProjectStore{q: dbgen.New(db)}
}

func (s *ProjectStore) Add(ctx context.Context, p *project.Project) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	id, err := s.q.CreateProject(ctx, dbgen.CreateProjectParams{
		Name:        p.Name,
		Description: p.Description,
		OwnerID:     int64(p.OwnerID),
	})
	if err != nil {
		return fmt.Errorf("thêm project: %w", err)
	}
	p.ID = int(id)
	return nil
}

func (s *ProjectStore) Get(ctx context.Context, id int) (project.Project, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	row, err := s.q.GetProject(ctx, int64(id))
	if errors.Is(err, sql.ErrNoRows) {
		return project.Project{}, fmt.Errorf("project #%d: %w", id, project.ErrNotFound)
	}
	if err != nil {
		return project.Project{}, fmt.Errorf("đọc project #%d: %w", id, err)
	}
	return toProject(row), nil
}

func (s *ProjectStore) List(ctx context.Context) ([]project.Project, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := s.q.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("liệt kê project: %w", err)
	}
	ps := make([]project.Project, 0, len(rows))
	for _, row := range rows {
		ps = append(ps, toProject(row))
	}
	return ps, nil
}

func (s *ProjectStore) Update(ctx context.Context, p *project.Project) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	n, err := s.q.UpdateProject(ctx, dbgen.UpdateProjectParams{
		ID:          int64(p.ID),
		Name:        p.Name,
		Description: p.Description,
		OwnerID:     int64(p.OwnerID),
	})
	if err != nil {
		return fmt.Errorf("sửa project #%d: %w", p.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("project #%d: %w", p.ID, project.ErrNotFound)
	}
	return nil
}

func (s *ProjectStore) Delete(ctx context.Context, id int) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	n, err := s.q.DeleteProject(ctx, int64(id))
	if isForeignKeyViolation(err) { // khóa ngoại tasks.project_id không cho xóa project còn task
		return fmt.Errorf("project #%d: %w", id, project.ErrHasTasks)
	}
	if err != nil {
		return fmt.Errorf("xóa project #%d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("project #%d: %w", id, project.ErrNotFound)
	}
	return nil
}

// toProject đổi một hàng của bảng projects (model do sqlc sinh) sang project.Project.
func toProject(row dbgen.Project) project.Project {
	return project.Project{
		ID:          int(row.ID),
		Name:        row.Name,
		Description: row.Description,
		OwnerID:     int(row.OwnerID),
	}
}
