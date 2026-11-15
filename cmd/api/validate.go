package main

import (
	"errors"
	"net/http"

	"github.com/toidicode/taskflow/internal/task"
)

// validator gom lỗi validate theo từng field: key là tên field trong JSON, value là thông báo lỗi.
type validator map[string]string

// check ghi lỗi message cho field khi ok là false. Mỗi field chỉ giữ lỗi đầu tiên.
func (v validator) check(ok bool, field, message string) {
	if _, exists := v[field]; !ok && !exists {
		v[field] = message
	}
}

// addError chép mọi *task.ValidationError trong err vào v, kể cả các lỗi được gộp bằng
// errors.Join. Lỗi nào không phải lỗi validate thì được trả lại cho nơi gọi.
func (v validator) addError(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var rest []error
		for _, e := range joined.Unwrap() {
			rest = append(rest, v.addError(e))
		}
		return errors.Join(rest...)
	}
	if ve, ok := errors.AsType[*task.ValidationError](err); ok {
		v.check(false, ve.Field, ve.Message)
		return nil
	}
	return err
}

// validatable là body request tự kiểm tra được dữ liệu của mình.
type validatable interface {
	validate(v validator)
}

// decodeValid đọc body JSON vào dst rồi gọi dst.validate. Body hỏng trả 400/413/415,
// dữ liệu sai trả 422. Trả về false khi đã ghi response lỗi, handler chỉ cần return.
func decodeValid(w http.ResponseWriter, r *http.Request, dst validatable) bool {
	if err := readJSON(w, r, dst); err != nil {
		bodyError(w, err)
		return false
	}
	v := validator{}
	dst.validate(v)
	if len(v) > 0 {
		invalidJSON(w, v)
		return false
	}
	return true
}

// invalidJSON trả 422 kèm lỗi của từng field trong body.
func invalidJSON(w http.ResponseWriter, v validator) {
	writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: "dữ liệu không hợp lệ", Fields: v})
}

// badParamsJSON trả 400 kèm lỗi của từng tham số trên URL (path hoặc query).
func badParamsJSON(w http.ResponseWriter, v validator) {
	writeJSON(w, http.StatusBadRequest, errorResponse{Error: "tham số không hợp lệ", Fields: v})
}
