package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/toidicode/taskflow/internal/task"
)

// TaskStore lưu task trong bảng tasks, cài interface task.Store.
type TaskStore struct {
	db *sql.DB
}

var _ task.Store = (*TaskStore)(nil)

// NewTaskStore tạo TaskStore dùng pool db.
func NewTaskStore(db *sql.DB) *TaskStore {
	return &TaskStore{db: db}
}

func (s *TaskStore) Add(ctx context.Context, t *task.Task) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	// RETURNING id trả về ID do PostgreSQL cấp ngay trong câu INSERT
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO tasks (project_id, title, description, status, assignee_id, due_date, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		t.ProjectID, t.Title, t.Description, t.Status.String(),
		nullInt(t.AssigneeID), nullTime(t.DueDate), nullTime(t.CompletedAt),
	).Scan(&t.ID)
	if err != nil {
		return fmt.Errorf("thêm task: %w", err)
	}
	return nil
}

func (s *TaskStore) Get(ctx context.Context, id int) (task.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, title, description, status, assignee_id, due_date, completed_at
		 FROM tasks WHERE id = $1`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return task.Task{}, fmt.Errorf("task #%d: %w", id, task.ErrNotFound)
	}
	if err != nil {
		return task.Task{}, fmt.Errorf("đọc task #%d: %w", id, err)
	}
	return t, nil
}

func (s *TaskStore) List(ctx context.Context, f task.Filter) ([]task.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var status sql.NullString // NULL: không lọc theo trạng thái
	if f.Status != nil {
		status = sql.NullString{String: f.Status.String(), Valid: true}
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, title, description, status, assignee_id, due_date, completed_at
		 FROM tasks
		 WHERE ($1::bigint IS NULL OR project_id = $1)
		   AND ($2::text IS NULL OR status = $2)
		 ORDER BY id`,
		nullInt(f.ProjectID), status)
	if err != nil {
		return nil, fmt.Errorf("liệt kê task: %w", err)
	}
	defer rows.Close() // trả kết nối về pool, kể cả khi return sớm vì lỗi

	tasks := []task.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("liệt kê task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil { // lỗi xảy ra giữa chừng, khi đang đọc các hàng
		return nil, fmt.Errorf("liệt kê task: %w", err)
	}
	return tasks, nil
}

func (s *TaskStore) Update(ctx context.Context, t *task.Task) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks
		 SET project_id = $2, title = $3, description = $4, status = $5,
		     assignee_id = $6, due_date = $7, completed_at = $8
		 WHERE id = $1`,
		t.ID, t.ProjectID, t.Title, t.Description, t.Status.String(),
		nullInt(t.AssigneeID), nullTime(t.DueDate), nullTime(t.CompletedAt))
	if err != nil {
		return fmt.Errorf("sửa task #%d: %w", t.ID, err)
	}
	return affected(res, fmt.Errorf("task #%d: %w", t.ID, task.ErrNotFound))
}

func (s *TaskStore) Delete(ctx context.Context, id int) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("xóa task #%d: %w", id, err)
	}
	return affected(res, fmt.Errorf("task #%d: %w", id, task.ErrNotFound))
}

// scanTask đọc một hàng gồm các cột theo đúng thứ tự trong SELECT của TaskStore.
func scanTask(row scanner) (task.Task, error) {
	var (
		t         task.Task
		status    string
		assignee  sql.NullInt64 // cột có thể NULL phải scan vào kiểu nhận được NULL
		due, done sql.NullTime
	)
	err := row.Scan(&t.ID, &t.ProjectID, &t.Title, &t.Description, &status, &assignee, &due, &done)
	if err != nil {
		return task.Task{}, err
	}
	if t.Status, err = task.ParseStatus(status); err != nil {
		return task.Task{}, err
	}
	// NULL cho Valid = false và giá trị là zero value: đúng quy ước "không có" của Task
	t.AssigneeID = int(assignee.Int64)
	t.DueDate = due.Time
	t.CompletedAt = done.Time
	return t, nil
}
