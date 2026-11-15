package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/toidicode/taskflow/internal/postgres/dbgen"
	"github.com/toidicode/taskflow/internal/task"
)

// TaskStore lưu task trong bảng tasks qua code do sqlc sinh ra, cài interface task.Store.
type TaskStore struct {
	q *dbgen.Queries
}

var _ task.Store = (*TaskStore)(nil)

// NewTaskStore tạo TaskStore dùng pool db.
func NewTaskStore(db *sql.DB) *TaskStore {
	return &TaskStore{q: dbgen.New(db)}
}

func (s *TaskStore) Add(ctx context.Context, t *task.Task) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	id, err := s.q.CreateTask(ctx, dbgen.CreateTaskParams{
		ProjectID:   int64(t.ProjectID),
		Title:       t.Title,
		Description: t.Description,
		Status:      t.Status.String(),
		AssigneeID:  nullInt(t.AssigneeID),
		DueDate:     nullTime(t.DueDate),
		CompletedAt: nullTime(t.CompletedAt),
	})
	if err != nil {
		return fmt.Errorf("thêm task: %w", err)
	}
	t.ID = int(id)
	return nil
}

func (s *TaskStore) Get(ctx context.Context, id int) (task.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	row, err := s.q.GetTask(ctx, int64(id))
	if errors.Is(err, sql.ErrNoRows) {
		return task.Task{}, fmt.Errorf("task #%d: %w", id, task.ErrNotFound)
	}
	if err != nil {
		return task.Task{}, fmt.Errorf("đọc task #%d: %w", id, err)
	}
	return toTask(row)
}

func (s *TaskStore) List(ctx context.Context, f task.Filter) ([]task.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var status sql.NullString // NULL: không lọc theo trạng thái
	if f.Status != nil {
		status = sql.NullString{String: f.Status.String(), Valid: true}
	}
	rows, err := s.q.ListTasks(ctx, dbgen.ListTasksParams{ProjectID: nullInt(f.ProjectID), Status: status})
	if err != nil {
		return nil, fmt.Errorf("liệt kê task: %w", err)
	}

	tasks := make([]task.Task, 0, len(rows)) // không có task nào thì vẫn là [] chứ không phải nil
	for _, row := range rows {
		t, err := toTask(row)
		if err != nil {
			return nil, fmt.Errorf("liệt kê task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (s *TaskStore) Update(ctx context.Context, t *task.Task) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	n, err := s.q.UpdateTask(ctx, dbgen.UpdateTaskParams{
		ID:          int64(t.ID),
		ProjectID:   int64(t.ProjectID),
		Title:       t.Title,
		Description: t.Description,
		Status:      t.Status.String(),
		AssigneeID:  nullInt(t.AssigneeID),
		DueDate:     nullTime(t.DueDate),
		CompletedAt: nullTime(t.CompletedAt),
	})
	if err != nil {
		return fmt.Errorf("sửa task #%d: %w", t.ID, err)
	}
	if n == 0 { // không có hàng nào có id này
		return fmt.Errorf("task #%d: %w", t.ID, task.ErrNotFound)
	}
	return nil
}

func (s *TaskStore) Delete(ctx context.Context, id int) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	n, err := s.q.DeleteTask(ctx, int64(id))
	if err != nil {
		return fmt.Errorf("xóa task #%d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("task #%d: %w", id, task.ErrNotFound)
	}
	return nil
}

// toTask đổi một hàng của bảng tasks (model do sqlc sinh) sang task.Task của domain.
func toTask(row dbgen.Task) (task.Task, error) {
	status, err := task.ParseStatus(row.Status)
	if err != nil {
		return task.Task{}, err
	}
	// NULL cho Valid = false và giá trị là zero value: đúng quy ước "không có" của Task
	return task.Task{
		ID:          int(row.ID),
		ProjectID:   int(row.ProjectID),
		Title:       row.Title,
		Description: row.Description,
		Status:      status,
		AssigneeID:  int(row.AssigneeID.Int64),
		DueDate:     row.DueDate.Time,
		CompletedAt: row.CompletedAt.Time,
	}, nil
}
