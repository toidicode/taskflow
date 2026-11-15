package main

import (
	"errors"
	"fmt"
	"net/http"

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
	t, err := s.tasks.Get(r.Context(), id)
	if err != nil {
		storeError(w, r, err)
		return task.Task{}, project.Project{}, false
	}
	p, err := s.projects.Get(r.Context(), t.ProjectID)
	if err != nil && !errors.Is(err, project.ErrNotFound) {
		storeError(w, r, err)
		return task.Task{}, project.Project{}, false
	}
	if !task.CanView(userIDFrom(r.Context()), p, t) {
		storeError(w, r, fmt.Errorf("task #%d: %w", id, task.ErrNotFound))
		return task.Task{}, project.Project{}, false
	}
	return t, p, true
}

// forbidden trả 403: user thấy tài nguyên nhưng không được làm việc này.
func forbidden(w http.ResponseWriter, message string) {
	errorJSON(w, http.StatusForbidden, message)
}
