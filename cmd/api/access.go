package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// Quy ước status code khi user không có quyền:
//   - 404: user không được xem tài nguyên. Trả y hệt khi tài nguyên không tồn tại,
//     để người ngoài không dò được project, task nào đang có;
//   - 403: user xem được nhưng không được làm việc đang yêu cầu (VD người được giao xóa task).

// loadProject đọc project id cho user đang đăng nhập. Không có project đó hoặc user không phải
// chủ project thì ghi cùng một response 404 rồi trả về false.
func loadProject(w http.ResponseWriter, r *http.Request, projects project.Store, id int) (project.Project, bool) {
	p, err := projects.Get(r.Context(), id)
	if err == nil && !project.CanAccess(userIDFrom(r.Context()), p) {
		err = fmt.Errorf("project #%d: %w", id, project.ErrNotFound) // có nhưng giả như không có
	}
	if err != nil {
		storeError(w, r, err)
		return project.Project{}, false
	}
	return p, true
}

// loadTask đọc task id và project chứa nó. User không được xem task (task.CanView) thì ghi
// response 404 giống hệt khi không có task rồi trả về false.
func loadTask(w http.ResponseWriter, r *http.Request, s stores, id int) (task.Task, project.Project, bool) {
	t, p, err := findTask(r.Context(), s, id)
	if err != nil {
		storeError(w, r, err)
		return task.Task{}, project.Project{}, false
	}
	return t, p, true
}

// findTask đọc task id và project chứa nó cho user đang đăng nhập. User không được xem task
// thì lỗi wrap task.ErrNotFound, y như khi không có task.
func findTask(ctx context.Context, s stores, id int) (task.Task, project.Project, error) {
	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return task.Task{}, project.Project{}, err
	}
	p, err := s.projects.Get(ctx, t.ProjectID)
	if err != nil && !errors.Is(err, project.ErrNotFound) {
		return task.Task{}, project.Project{}, err
	}
	if !task.CanView(userIDFrom(ctx), p, t) {
		return task.Task{}, project.Project{}, fmt.Errorf("task #%d: %w", id, task.ErrNotFound)
	}
	return t, p, nil
}

// loadAttachment đọc file đính kèm id cùng task và project chứa nó. User không được xem task
// thì ghi response 404 giống hệt khi không có file (không lộ cả ID của task) rồi trả về false.
func loadAttachment(w http.ResponseWriter, r *http.Request, s stores, id int) (attachment.Attachment, task.Task, project.Project, bool) {
	a, err := s.attachments.Get(r.Context(), id)
	if err != nil {
		storeError(w, r, err)
		return attachment.Attachment{}, task.Task{}, project.Project{}, false
	}
	t, p, err := findTask(r.Context(), s, a.TaskID)
	if errors.Is(err, task.ErrNotFound) {
		err = fmt.Errorf("attachment #%d: %w", id, attachment.ErrNotFound)
	}
	if err != nil {
		storeError(w, r, err)
		return attachment.Attachment{}, task.Task{}, project.Project{}, false
	}
	return a, t, p, true
}

// forbidden trả 403: user thấy tài nguyên nhưng không được làm việc này.
func forbidden(w http.ResponseWriter, message string) {
	errorJSON(w, http.StatusForbidden, message)
}
