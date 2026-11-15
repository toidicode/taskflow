package attachment

import (
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// Luật quyền trên file đính kèm, dựa trên luật của task (internal/task/policy.go):
// ai xem được task thì xem, tải về và đính kèm file được; xóa file thì chỉ chủ project
// hoặc chính người đã upload file đó.

// CanUpload cho biết user userID có được đính kèm file vào task t không.
func CanUpload(userID int, p project.Project, t task.Task) bool {
	return task.CanView(userID, p, t)
}

// CanDelete cho biết user userID có được xóa file a của task t không.
func CanDelete(userID int, p project.Project, t task.Task, a Attachment) bool {
	if a.TaskID != t.ID {
		return false
	}
	return task.CanEdit(userID, p, t) || task.CanView(userID, p, t) && a.UploadedBy == userID
}
