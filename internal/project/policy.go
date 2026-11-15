package project

// CanAccess cho biết user userID có được xem, sửa và xóa project p không: chỉ chủ project.
// Người được giao task trong p chỉ thấy task đó (task.CanView), không thấy project.
func CanAccess(userID int, p Project) bool {
	return userID > 0 && p.OwnerID == userID
}
