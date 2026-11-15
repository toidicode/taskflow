package main

import (
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

// NewTask tạo một task mới ở trạng thái todo.
func NewTask(id, projectID int, title string) *Task {
	return &Task{
		ID:        id,
		ProjectID: projectID,
		Title:     strings.TrimSpace(title),
		Status:    StatusTodo,
	}
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
