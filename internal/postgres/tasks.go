package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/toidicode/taskflow/internal/postgres/dbgen"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// TaskStore lưu task trong bảng tasks (tag trong tags và task_tags) qua code do sqlc sinh ra,
// cài interface task.Store.
type TaskStore struct {
	db *sql.DB // để mở transaction
	q  *dbgen.Queries
}

var _ task.Store = (*TaskStore)(nil)

// NewTaskStore tạo TaskStore dùng pool db.
func NewTaskStore(db *sql.DB) *TaskStore {
	return &TaskStore{db: db, q: dbgen.New(db)}
}

// Add thêm task và các tag của nó trong cùng một transaction:
// một câu lỗi thì không có gì được ghi lại, kể cả task.
// Không có project t.ProjectID (VD vừa bị xóa) thì lỗi wrap project.ErrNotFound.
func (s *TaskStore) Add(ctx context.Context, t *task.Task) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var id int64
	err := withTx(ctx, s.db, func(tx *sql.Tx) error {
		q := s.q.WithTx(tx) // Queries chạy trên tx thay vì trên pool
		var err error
		id, err = q.CreateTask(ctx, dbgen.CreateTaskParams{
			ProjectID:   int64(t.ProjectID),
			Title:       t.Title,
			Description: t.Description,
			Status:      t.Status.String(),
			AssigneeID:  nullInt(t.AssigneeID),
			DueDate:     nullTime(t.DueDate),
			CompletedAt: nullTime(t.CompletedAt),
		})
		if isForeignKeyViolation(err) { // khóa ngoại tasks.project_id: không có project này
			return project.ErrNotFound
		}
		if err != nil {
			return err
		}
		return setTags(ctx, q, id, t.Tags)
	})
	if errors.Is(err, project.ErrNotFound) {
		return fmt.Errorf("project #%d: %w", t.ProjectID, err)
	}
	if err != nil {
		return fmt.Errorf("thêm task: %w", err)
	}
	t.ID = int(id) // chỉ ghi ID khi transaction đã commit
	return nil
}

// setTags thay toàn bộ tag của task taskID bằng names. q phải chạy trong một transaction.
func setTags(ctx context.Context, q *dbgen.Queries, taskID int64, names []string) error {
	if err := q.DeleteTaskTags(ctx, taskID); err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	tagIDs, err := q.UpsertTags(ctx, names)
	if err != nil {
		return err
	}
	return q.AddTaskTags(ctx, dbgen.AddTaskTagsParams{TaskID: taskID, TagIds: tagIDs})
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
	tasks, err := s.withTags(ctx, []dbgen.Task{row})
	if err != nil {
		return task.Task{}, fmt.Errorf("đọc task #%d: %w", id, err)
	}
	return tasks[0], nil
}

func (s *TaskStore) List(ctx context.Context, f task.Filter) ([]task.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	params := dbgen.ListTasksParams{
		ProjectID: nullInt(f.ProjectID),
		Tag:       sql.NullString{String: f.Tag, Valid: f.Tag != ""},
		AfterID:   int64(f.AfterID),
		MaxRows:   sql.NullInt32{Int32: int32(f.Limit), Valid: f.Limit > 0}, // NULL: không có LIMIT
	}
	if f.Status != nil { // NULL: không lọc theo trạng thái
		params.Status = sql.NullString{String: f.Status.String(), Valid: true}
	}
	rows, err := s.q.ListTasks(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("liệt kê task: %w", err)
	}
	tasks, err := s.withTags(ctx, rows)
	if err != nil {
		return nil, fmt.Errorf("liệt kê task: %w", err)
	}
	return tasks, nil
}

// withTags đổi các hàng của bảng tasks sang task.Task kèm tag của chúng.
// Tag của mọi task được đọc bằng một câu truy vấn (task_id = ANY($1)), không phải mỗi task một câu.
func (s *TaskStore) withTags(ctx context.Context, rows []dbgen.Task) ([]task.Task, error) {
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	tagRows, err := s.q.ListTaskTags(ctx, ids)
	if err != nil {
		return nil, err
	}
	tags := make(map[int64][]string, len(rows)) // task_id → tên các tag
	for _, tr := range tagRows {
		tags[tr.TaskID] = append(tags[tr.TaskID], tr.Name)
	}

	tasks := make([]task.Task, 0, len(rows)) // không có task nào thì vẫn là [] chứ không phải nil
	for _, row := range rows {
		t, err := toTask(row)
		if err != nil {
			return nil, err
		}
		t.Tags = tags[row.ID]
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// Update sửa task và thay toàn bộ tag của nó bằng t.Tags trong cùng một transaction.
// Không có project t.ProjectID (VD vừa bị xóa) thì lỗi wrap project.ErrNotFound.
func (s *TaskStore) Update(ctx context.Context, t *task.Task) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	err := withTx(ctx, s.db, func(tx *sql.Tx) error {
		q := s.q.WithTx(tx)
		n, err := q.UpdateTask(ctx, dbgen.UpdateTaskParams{
			ID:          int64(t.ID),
			ProjectID:   int64(t.ProjectID),
			Title:       t.Title,
			Description: t.Description,
			Status:      t.Status.String(),
			AssigneeID:  nullInt(t.AssigneeID),
			DueDate:     nullTime(t.DueDate),
			CompletedAt: nullTime(t.CompletedAt),
		})
		if isForeignKeyViolation(err) {
			return project.ErrNotFound
		}
		if err != nil {
			return err
		}
		if n == 0 { // không có hàng nào có id này
			return task.ErrNotFound
		}
		return setTags(ctx, q, int64(t.ID), t.Tags)
	})
	if errors.Is(err, task.ErrNotFound) {
		return fmt.Errorf("task #%d: %w", t.ID, err)
	}
	if errors.Is(err, project.ErrNotFound) {
		return fmt.Errorf("project #%d: %w", t.ProjectID, err)
	}
	if err != nil {
		return fmt.Errorf("sửa task #%d: %w", t.ID, err)
	}
	return nil
}

func (s *TaskStore) Delete(ctx context.Context, id int) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	// task_tags có ON DELETE CASCADE: liên kết với tag bị xóa theo task
	n, err := s.q.DeleteTask(ctx, int64(id))
	if err != nil {
		return fmt.Errorf("xóa task #%d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("task #%d: %w", id, task.ErrNotFound)
	}
	return nil
}

// AddTag tạo tag name nếu chưa có rồi gắn vào task, hai bước trong một transaction.
func (s *TaskStore) AddTag(ctx context.Context, taskID int, name string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	err := withTx(ctx, s.db, func(tx *sql.Tx) error {
		q := s.q.WithTx(tx)
		tagIDs, err := q.UpsertTags(ctx, []string{name})
		if err != nil {
			return err
		}
		err = q.AddTaskTags(ctx, dbgen.AddTaskTagsParams{TaskID: int64(taskID), TagIds: tagIDs})
		if isForeignKeyViolation(err) { // không có task taskID: tag vừa tạo cũng bị rollback
			return task.ErrNotFound
		}
		return err
	})
	if errors.Is(err, task.ErrNotFound) {
		return fmt.Errorf("task #%d: %w", taskID, err)
	}
	if err != nil {
		return fmt.Errorf("gắn tag cho task #%d: %w", taskID, err)
	}
	return nil
}

func (s *TaskStore) RemoveTag(ctx context.Context, taskID int, name string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	n, err := s.q.RemoveTaskTag(ctx, dbgen.RemoveTaskTagParams{TaskID: int64(taskID), Name: name})
	if err != nil {
		return fmt.Errorf("gỡ tag của task #%d: %w", taskID, err)
	}
	if n > 0 {
		return nil
	}
	// không xóa được hàng nào: task không có tag này (không sao), hoặc không có task này
	_, err = s.q.GetTask(ctx, int64(taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task #%d: %w", taskID, task.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("gỡ tag của task #%d: %w", taskID, err)
	}
	return nil
}

// toTask đổi một hàng của bảng tasks (model do sqlc sinh) sang task.Task của domain, chưa có tag.
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
