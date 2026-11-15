package task

import "github.com/toidicode/taskflow/internal/project"

// Luật quyền trên task, p là project chứa task: chủ project làm được mọi thứ, người được giao
// xem và đổi status, user khác không thấy task. ListTasks (internal/postgres) lặp lại CanView bằng SQL.

// CanView cho biết user userID có được xem task t không.
func CanView(userID int, p project.Project, t Task) bool {
	return CanEdit(userID, p, t) || isAssignee(userID, t)
}

// CanChangeStatus cho biết user userID có được đổi status của task t không.
func CanChangeStatus(userID int, p project.Project, t Task) bool {
	return CanView(userID, p, t)
}

// CanEdit cho biết user userID có được sửa nội dung, giao lại, gắn tag và xóa task t không.
func CanEdit(userID int, p project.Project, t Task) bool {
	return userID > 0 && p.ID == t.ProjectID && project.CanAccess(userID, p)
}

// isAssignee cho biết task t có được giao cho user userID không. userID 0 (chưa đăng nhập)
// không bao giờ khớp, kể cả với task chưa giao cho ai (AssigneeID cũng là 0).
func isAssignee(userID int, t Task) bool {
	return userID > 0 && t.AssigneeID == userID
}
