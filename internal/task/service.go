package task

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/reqlog"
	"github.com/toidicode/taskflow/internal/user"
)

// Các interface dưới đây do Service (nơi dùng) khai báo và chỉ gồm những method nó cần.
// main truyền vào kiểu thật: user.Store, *jobs.Dispatcher, attachment.Files.

// Users tìm user, để kiểm tra người được giao task có thật.
type Users interface {
	Get(ctx context.Context, id int) (user.User, error)
}

// Queue là hàng đợi việc nền.
type Queue interface {
	Enqueue(j jobs.Job) error
}

// Files dọn file đính kèm khi xóa task. Không dùng thẳng attachment.Store vì internal/attachment
// đã import task (luật quyền của file dựa trên task): import ngược lại là import vòng.
type Files interface {
	// StoredNames trả về tên trong storage của các file đính kèm của task taskID.
	StoredNames(ctx context.Context, taskID int) ([]string, error)
	// Remove xóa nội dung các file, lỗi chỉ được ghi log.
	Remove(ctx context.Context, names ...string)
}

// ForbiddenError: user xem được task nhưng không được làm việc đang yêu cầu.
type ForbiddenError struct{ Reason string }

func (e *ForbiddenError) Error() string { return e.Reason }

// Service chứa các use case của task: luật quyền, kiểm tra dữ liệu, lưu, thông báo.
// Nó không biết gì về HTTP, userID là user đang đăng nhập do tầng gọi truyền vào.
type Service struct {
	tasks    Store
	projects project.Store
	users    Users
	files    Files
	queue    Queue
	notifier notify.Notifier
}

// NewService tạo Service từ các thành phần nó cần, main nối chúng lại lúc khởi động.
func NewService(tasks Store, projects project.Store, users Users, files Files, queue Queue, notifier notify.Notifier) *Service {
	return &Service{tasks: tasks, projects: projects, users: users, files: files, queue: queue, notifier: notifier}
}

// Get trả về task id và project chứa nó. User không được xem task (CanView) nhận lỗi
// wrap ErrNotFound y như khi không có task.
func (s *Service) Get(ctx context.Context, userID, id int) (Task, project.Project, error) {
	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return Task{}, project.Project{}, err
	}
	p, err := s.projects.Get(ctx, t.ProjectID)
	if err != nil && !errors.Is(err, project.ErrNotFound) {
		return Task{}, project.Project{}, err
	}
	if !CanView(userID, p, t) {
		return Task{}, project.Project{}, fmt.Errorf("task #%d: %w", id, ErrNotFound)
	}
	return t, p, nil
}

// List trả về các task thỏa f mà user userID được xem.
func (s *Service) List(ctx context.Context, userID int, f Filter) ([]Task, error) {
	f.VisibleTo = userID
	return s.tasks.List(ctx, f)
}

// HasTasks cho biết project projectID còn task nào không (cài project.Tasks, không kiểm tra quyền).
func (s *Service) HasTasks(ctx context.Context, projectID int) (bool, error) {
	ts, err := s.tasks.List(ctx, Filter{ProjectID: projectID, Limit: 1}) // chỉ cần biết còn task không
	return len(ts) > 0, err
}

// Create tạo task t trong một project của user userID và ghi ID mới vào t.ID.
// Dữ liệu sai trả về các *ValidationError (gộp bằng errors.Join).
func (s *Service) Create(ctx context.Context, userID int, t *Task) error {
	t.Title = strings.TrimSpace(t.Title)
	t.Tags = NormalizeTags(t.Tags)
	t.Status = StatusTodo
	if err := s.validate(ctx, userID, t, true); err != nil {
		return err
	}
	return s.save(ctx, t, s.tasks.Add)
}

// Changes là các thay đổi của một lần sửa task: field nil là giữ nguyên.
type Changes struct {
	Title       *string
	Description *string
	Status      *Status
	AssigneeID  *int
	DueDate     *time.Time // trỏ tới zero value là xóa hạn
	Tags        *[]string  // thay toàn bộ tag
}

// onlyStatus cho biết c không đổi gì ngoài status: việc duy nhất người được giao task làm được.
func (c Changes) onlyStatus() bool {
	return c.Title == nil && c.Description == nil && c.AssigneeID == nil && c.DueDate == nil && c.Tags == nil
}

// Update sửa task id theo c. Chủ project sửa được mọi field, người được giao chỉ đổi được status.
// Task vừa chuyển sang done thì báo cho chủ project và người được giao (chạy nền).
func (s *Service) Update(ctx context.Context, userID, id int, c Changes) (Task, error) {
	t, p, err := s.Get(ctx, userID, id) // t là bản sao, sửa thoải mái
	if err != nil {
		return Task{}, err
	}
	wasDone := t.IsDone() // trước khi sửa: chỉ báo khi task vừa chuyển sang done
	if !c.onlyStatus() && !CanEdit(userID, p, t) {
		return Task{}, &ForbiddenError{"người được giao chỉ được đổi status của task"}
	}
	if c.Status != nil && !CanChangeStatus(userID, p, t) {
		return Task{}, &ForbiddenError{"không được đổi status của task này"}
	}

	if c.Title != nil {
		t.Title = strings.TrimSpace(*c.Title)
	}
	if c.Description != nil {
		t.Description = *c.Description
	}
	if c.AssigneeID != nil {
		t.AssigneeID = *c.AssigneeID
	}
	if c.DueDate != nil {
		t.DueDate = *c.DueDate
	}
	if c.Status != nil {
		t.SetStatus(*c.Status)
	}
	if c.Tags != nil {
		t.Tags = NormalizeTags(*c.Tags)
	}
	if err := s.validate(ctx, userID, &t, false); err != nil { // kiểm tra cả task sau khi sửa
		return Task{}, err
	}
	if err := s.save(ctx, &t, s.tasks.Update); err != nil {
		return Task{}, err
	}
	if !wasDone && t.IsDone() {
		s.notifyDone(ctx, userID, t, p)
	}
	return t, nil
}

// Delete xóa task id cùng các file đính kèm. Chỉ chủ project được xóa.
func (s *Service) Delete(ctx context.Context, userID, id int) error {
	t, p, err := s.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if !CanEdit(userID, p, t) {
		return &ForbiddenError{"chỉ chủ project được xóa task"}
	}
	// đọc danh sách file trước: xóa task thì ON DELETE CASCADE xóa luôn thông tin file
	names, err := s.files.StoredNames(ctx, id)
	if err != nil {
		return err
	}
	if err := s.tasks.Delete(ctx, id); err != nil {
		return err
	}
	s.files.Remove(ctx, names...)
	return nil
}

// AddTag gắn tag name (đã chuẩn hóa) vào task id, trả về task sau khi gắn. Chỉ chủ project được gắn tag.
func (s *Service) AddTag(ctx context.Context, userID, id int, name string) (Task, error) {
	t, p, err := s.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if !CanEdit(userID, p, t) {
		return Task{}, &ForbiddenError{"chỉ chủ project được sửa tag của task"}
	}
	if !t.HasTag(name) && len(t.Tags) >= MaxTags {
		return Task{}, &ValidationError{Field: "name", Message: fmt.Sprintf("task đã có đủ %d tag", MaxTags)}
	}
	if err := s.tasks.AddTag(ctx, id, name); err != nil {
		return Task{}, err
	}
	return s.tasks.Get(ctx, id)
}

// RemoveTag gỡ tag name khỏi task id, không lỗi khi task không có tag đó. Chỉ chủ project được gỡ tag.
func (s *Service) RemoveTag(ctx context.Context, userID, id int, name string) error {
	t, p, err := s.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if !CanEdit(userID, p, t) {
		return &ForbiddenError{"chỉ chủ project được sửa tag của task"}
	}
	return s.tasks.RemoveTag(ctx, id, NormalizeTag(name))
}

// validate kiểm tra t theo quy tắc của domain (t.Validate) và kiểm tra người được giao (nếu có)
// là một user có thật. checkProject là true khi tạo task: project của t phải là project của
// userID, project của người khác bị báo giống hệt project không tồn tại.
// Lỗi database được trả về ngay, lỗi dữ liệu được gộp lại để báo mọi field một lần.
func (s *Service) validate(ctx context.Context, userID int, t *Task, checkProject bool) error {
	errs := []error{t.Validate()}
	if checkProject && t.ProjectID > 0 {
		p, err := s.projects.Get(ctx, t.ProjectID)
		switch {
		case errors.Is(err, project.ErrNotFound) || err == nil && !project.CanAccess(userID, p):
			errs = append(errs, noProject(t))
		case err != nil: // database lỗi: không phải lỗi của client
			return err
		}
	}
	if t.AssigneeID > 0 {
		_, err := s.users.Get(ctx, t.AssigneeID)
		switch {
		case errors.Is(err, user.ErrNotFound):
			errs = append(errs, noAssignee(t))
		case err != nil:
			return err
		}
	}
	return errors.Join(errs...)
}

// save gọi store (Add hoặc Update). Project hoặc user bị xóa sau lúc validate kiểm tra thì khóa
// ngoại chặn và store trả project.ErrNotFound / user.ErrNotFound: báo lỗi dữ liệu giống hệt
// khi gửi project_id, assignee_id không tồn tại từ đầu.
func (s *Service) save(ctx context.Context, t *Task, store func(context.Context, *Task) error) error {
	err := store(ctx, t)
	switch {
	case errors.Is(err, project.ErrNotFound):
		return noProject(t)
	case errors.Is(err, user.ErrNotFound):
		return noAssignee(t)
	}
	return err
}

func noProject(t *Task) error {
	return &ValidationError{Field: "project_id", Message: fmt.Sprintf("không có project #%d", t.ProjectID)}
}

func noAssignee(t *Task) error {
	return &ValidationError{Field: "assignee_id", Message: fmt.Sprintf("không có user #%d", t.AssigneeID)}
}

// notifyDone đưa việc báo "task đã xong" cho chủ project và người được giao (trừ người vừa đổi
// status) vào hàng đợi. Hàng đợi đầy thì chỉ ghi log: status đã lưu xong, mất một thông báo
// không đáng để trả lỗi cho client.
func (s *Service) notifyDone(ctx context.Context, userID int, t Task, p project.Project) {
	n := notify.TaskDone{TaskID: t.ID, Title: t.Title, DoneBy: userID}
	for _, id := range []int{p.OwnerID, t.AssigneeID} {
		if id > 0 && id != userID && !slices.Contains(n.To, id) {
			n.To = append(n.To, id)
		}
	}
	if len(n.To) == 0 { // tự làm, tự xong: không cần báo ai
		return
	}
	// job chỉ giữ n (bản sao dữ liệu), không giữ ctx của request
	err := s.queue.Enqueue(jobs.Job{Name: "notify-task-done", Run: func(ctx context.Context) error {
		return s.notifier.TaskDone(ctx, n)
	}})
	if err != nil {
		reqlog.Logger(ctx).Warn("bỏ qua thông báo task done", "task_id", t.ID, "err", err)
	}
}
