package httpapi

import (
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	playground "github.com/go-playground/validator/v10"
	"github.com/go-playground/validator/v10/non-standard/validators"

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

func init() {
	// binding.Validator là validator của Gin (go-playground/validator, đọc tag binding:"...").
	// Cấu hình một lần: lỗi mang tên key trong tag json, thêm rule notblank (không rỗng sau khi bỏ khoảng trắng).
	v := binding.Validator.Engine().(*playground.Validate)
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			return ""
		}
		return name
	})
	if err := v.RegisterValidation("notblank", validators.NotBlank); err != nil {
		panic(err)
	}
}

// addBindingErrors chép lỗi của tag binding vào v, dịch theo tên rule (Bài 27).
// Lỗi khác ValidationErrors (tag viết sai, dst không phải struct) được trả lại.
func (v validator) addBindingErrors(err error) error {
	verrs, ok := errors.AsType[playground.ValidationErrors](err)
	if !ok {
		return err
	}
	for _, fe := range verrs {
		v.check(false, fe.Field(), bindingMessage(fe))
	}
	return nil
}

// bindingMessage dịch một lỗi theo rule (fe.Tag) và tham số của rule (fe.Param).
func bindingMessage(fe playground.FieldError) string {
	switch fe.Tag() {
	case "required", "notblank":
		return "không được để trống"
	case "max":
		return "tối đa " + fe.Param() + " ký tự"
	case "min":
		return "tối thiểu " + fe.Param() + " ký tự"
	}
	return "không hợp lệ (" + fe.Tag() + ")"
}

// validatable là body request tự kiểm tra thêm những gì tag binding không diễn đạt được.
type validatable interface {
	validate(v validator)
}

// bindJSON đọc body vào dst, validate theo tag binding rồi method validate (nếu có).
// Body hỏng: 400/413/415, dữ liệu sai: 422. Trả về false khi đã ghi response lỗi.
func bindJSON(c *gin.Context, dst any) bool {
	if err := readJSON(c, dst); err != nil {
		bodyError(c, err)
		return false
	}
	v := validator{}
	if err := v.addBindingErrors(binding.Validator.ValidateStruct(dst)); err != nil {
		writeError(c, err)
		return false
	}
	if dv, ok := dst.(validatable); ok {
		dv.validate(v)
	}
	if len(v) > 0 {
		invalidJSON(c, v)
		return false
	}
	return true
}

// invalidJSON trả 422 kèm lỗi của từng field trong body.
func invalidJSON(c *gin.Context, v validator) {
	writeJSON(c, http.StatusUnprocessableEntity, errorResponse{Error: "dữ liệu không hợp lệ", Fields: v})
	c.Abort()
}

// badParamsJSON trả 400 kèm lỗi của từng tham số trên URL (path hoặc query).
func badParamsJSON(c *gin.Context, v validator) {
	writeJSON(c, http.StatusBadRequest, errorResponse{Error: "tham số không hợp lệ", Fields: v})
	c.Abort()
}
