package httpapi

import (
	"errors"
	"net/http"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// writeError đổi lỗi của service thành response. Đây là nơi duy nhất biết lỗi nào của domain
// ứng với status code nào:
//   - *task.ValidationError (một hoặc nhiều, gộp bằng errors.Join): 422 kèm lỗi từng field;
//   - không tìm thấy: 404. Service trả lỗi này cả khi user không được xem tài nguyên,
//     để người ngoài không dò được project, task nào đang có;
//   - *task.ForbiddenError: 403, user xem được nhưng không được làm việc đang yêu cầu;
//   - xóa project còn task: 409;
//   - user của token không còn: 401.
//
// Lỗi khác (VD mất kết nối database) được ghi log kèm request ID rồi trả 500, client không thấy chi tiết lỗi.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if v := (validator{}); v.addError(err) == nil && len(v) > 0 {
		invalidJSON(w, v)
		return
	}
	var forbidden *task.ForbiddenError
	switch {
	case errors.Is(err, task.ErrNotFound) || errors.Is(err, project.ErrNotFound) || errors.Is(err, attachment.ErrNotFound):
		errorJSON(w, http.StatusNotFound, err.Error())
	case errors.As(err, &forbidden):
		errorJSON(w, http.StatusForbidden, forbidden.Reason)
	case errors.Is(err, project.ErrHasTasks):
		errorJSON(w, http.StatusConflict, err.Error())
	case errors.Is(err, user.ErrNotFound): // khóa ngoại owner_id, /me: token còn hạn nhưng user đã bị xóa
		unauthorized(w, true, "tài khoản không còn tồn tại")
	default:
		requestLogger(r).Error("store", "err", err)
		errorJSON(w, http.StatusInternalServerError, "lỗi máy chủ")
	}
}

// forbidden trả 403: user thấy tài nguyên nhưng không được làm việc này.
func forbidden(w http.ResponseWriter, message string) {
	errorJSON(w, http.StatusForbidden, message)
}
