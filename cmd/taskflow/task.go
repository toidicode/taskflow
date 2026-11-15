package main

import (
	"errors"
	"strconv"
	"strings"
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

// Task là một công việc thuộc một project.
type Task struct {
	ID          int
	ProjectID   int
	Title       string
	Description string
	Status      Status
	AssigneeID  int // 0 nghĩa là chưa giao cho ai
}

// ValidationError cho biết một field có giá trị không hợp lệ.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// NewTask tạo một task mới ở trạng thái todo, ID do TaskStore cấp khi thêm task.
// Dữ liệu không hợp lệ thì trả về các *ValidationError gộp bằng errors.Join.
func NewTask(projectID int, title string) (*Task, error) {
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

// Complete đánh dấu task đã xong.
func (t *Task) Complete() {
	t.Status = StatusDone
}

// IsDone cho biết task đã xong hay chưa.
func (t *Task) IsDone() bool {
	return t.Status == StatusDone
}
