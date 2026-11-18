package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// maxBodyBytes giới hạn kích thước body JSON của request: 1 MB.
const maxBodyBytes = 1 << 20

// errUnsupportedMediaType được trả về khi request không gửi Content-Type: application/json.
var errUnsupportedMediaType = errors.New("Content-Type phải là application/json")

// bufPool giữ các buffer đã dùng xong để response sau dùng lại, thay vì mỗi response cấp phát
// một buffer mới cỡ cả body (Bài 52). Pool có thể bị GC dọn bất cứ lúc nào: chỉ là chỗ để tái sử dụng.
var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// maxPooledBuf: buffer lớn hơn mức này (một response rất lớn) không được trả về pool,
// để một response hiếm hoi không giữ mãi một khối nhớ lớn.
const maxPooledBuf = 256 << 10

// writeJSON mã hóa v thành JSON rồi ghi ra response với status code cho trước.
// Khác c.JSON của Gin: Content-Type đúng "application/json", body kết thúc bằng xuống dòng như
// từ Bài 25, và lỗi mã hóa thành 500 thay vì một response dở dang.
func writeJSON(c *gin.Context, status int, v any) {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= maxPooledBuf {
			bufPool.Put(buf) // c.Data đã chép xong buf.Bytes() vào response, không ai giữ nó nữa
		}
	}()
	// mã hóa trước khi ghi: lỗi thì vẫn kịp đổi sang 500 vì chưa gửi header nào.
	// Encode thêm '\n' vào cuối, kết quả giống hệt json.Marshal + '\n' như trước
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		slog.Error("writeJSON", "err", err)
		c.Data(http.StatusInternalServerError, "application/json", []byte(`{"error":"lỗi máy chủ"}`+"\n"))
		return
	}
	c.Data(status, "application/json", buf.Bytes())
}

// errorResponse là định dạng lỗi chung của API. Fields chỉ có ở lỗi validate:
// {"error": "dữ liệu không hợp lệ", "fields": {"title": "không được để trống"}}.
type errorResponse struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields,omitempty"`
}

// errorJSON trả lỗi không gắn với field nào: {"error": "..."}, rồi gọi c.Abort để
// các handler còn lại trong chuỗi (VD handler sau requireAuth) không chạy nữa.
func errorJSON(c *gin.Context, status int, message string) {
	writeJSON(c, status, errorResponse{Error: message})
	c.Abort()
}

// readJSON đọc body của request (một JSON object) vào dst, trả lỗi có thông báo dễ hiểu cho client.
func readJSON(c *gin.Context, dst any) error {
	mediaType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if mediaType != "application/json" {
		return errUnsupportedMediaType
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return err // bodyError trả 413 cho lỗi này
		case errors.Is(err, io.EOF):
			return errors.New("body rỗng, cần một JSON object")
		case errors.As(err, &syntaxErr):
			return fmt.Errorf("JSON sai cú pháp ở byte thứ %d", syntaxErr.Offset)
		case errors.Is(err, io.ErrUnexpectedEOF):
			return errors.New("JSON bị cắt cụt, thiếu phần kết thúc")
		case errors.As(err, &typeErr) && typeErr.Field != "":
			return fmt.Errorf("field %q sai kiểu, cần %v", typeErr.Field, typeErr.Type)
		case errors.As(err, &typeErr):
			return errors.New("body phải là một JSON object")
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			return fmt.Errorf("không hỗ trợ field %s", strings.TrimPrefix(err.Error(), "json: unknown field "))
		default:
			return err // VD lỗi từ UnmarshalText của task.Status
		}
	}

	// body chỉ được chứa đúng một giá trị JSON
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body chỉ được chứa một JSON object")
	}
	return nil
}

// bodyError trả lỗi của readJSON với status code phù hợp: 415, 413 hoặc 400.
func bodyError(c *gin.Context, err error) {
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, errUnsupportedMediaType):
		errorJSON(c, http.StatusUnsupportedMediaType, err.Error())
	case errors.As(err, &maxErr):
		errorJSON(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("body vượt quá %d byte", maxErr.Limit))
	default:
		errorJSON(c, http.StatusBadRequest, err.Error())
	}
}
