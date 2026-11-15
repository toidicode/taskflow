// Package task chứa model Task và nơi lưu trữ task của TaskFlow.
package task

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Status là trạng thái của một task.
type Status int

const (
	StatusTodo Status = iota
	StatusDoing
	StatusDone
)

// String trả về tên của trạng thái, fmt dùng nó khi in một Status.
func (s Status) String() string {
	switch s {
	case StatusTodo:
		return "todo"
	case StatusDoing:
		return "doing"
	case StatusDone:
		return "done"
	}
	return "Status(" + strconv.Itoa(int(s)) + ")"
}

// ParseStatus chuyển tên trạng thái ("todo", "doing", "done") thành Status.
func ParseStatus(name string) (Status, error) {
	for _, s := range []Status{StatusTodo, StatusDoing, StatusDone} {
		if s.String() == name {
			return s, nil
		}
	}
	return 0, fmt.Errorf("trạng thái %q không hợp lệ, chọn todo, doing hoặc done", name)
}

// Task là một công việc thuộc một project.
type Task struct {
	ID          int
	ProjectID   int
	Title       string
	Description string
	Status      Status
	AssigneeID  int       // 0 nghĩa là chưa giao cho ai
	DueDate     time.Time // zero value nghĩa là không có hạn
	CompletedAt time.Time // zero value nghĩa là chưa xong
}

// ValidationError cho biết một field có giá trị không hợp lệ.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// New tạo một task mới ở trạng thái todo, ID do Store cấp khi thêm task.
// Dữ liệu không hợp lệ thì trả về các *ValidationError gộp bằng errors.Join.
func New(projectID int, title string) (*Task, error) {
	title = strings.TrimSpace(title)
	var errs []error
	if projectID <= 0 {
		errs = append(errs, &ValidationError{Field: "project_id", Message: "phải lớn hơn 0"})
	}
	if title == "" {
		errs = append(errs, &ValidationError{Field: "title", Message: "không được để trống"})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &Task{
		ProjectID: projectID,
		Title:     title,
		Status:    StatusTodo,
	}, nil
}

// Assign giao task cho người dùng có id là userID.
func (t *Task) Assign(userID int) {
	t.AssigneeID = userID
}

// Start chuyển task sang trạng thái doing.
func (t *Task) Start() {
	t.Status = StatusDoing
}

// Complete đánh dấu task đã xong và ghi lại thời điểm hoàn thành.
func (t *Task) Complete() {
	t.Status = StatusDone
	t.CompletedAt = time.Now()
}

// IsDone cho biết task đã xong hay chưa.
func (t *Task) IsDone() bool {
	return t.Status == StatusDone
}

// IsOverdue cho biết tại thời điểm now task đã quá hạn mà chưa xong hay chưa.
func (t *Task) IsOverdue(now time.Time) bool {
	return !t.IsDone() && !t.DueDate.IsZero() && now.After(t.DueDate)
}
