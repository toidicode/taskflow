package httpapi

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/reqlog"
)

// ctxKey là kiểu key cho giá trị gắn vào gin.Context bằng c.Set. Kiểu không exported nên
// package khác không thể tạo ra key trùng với key của package này.
type ctxKey int

const (
	userIDKey ctxKey = iota // ID của user đã đăng nhập, do requireAuth gắn vào
)

// requestID gắn cho mỗi request một ID: lấy từ header X-Request-ID nếu hợp lệ,
// không thì tự sinh. ID được trả lại trong header của response và lưu vào context của
// request (không phải gin.Context): service nhận c.Request.Context() nên log cũng có ID.
func requestID(c *gin.Context) {
	id := c.GetHeader("X-Request-ID")
	if !validRequestID(id) {
		id = rand.Text()
	}
	c.Header("X-Request-ID", id)
	c.Request = c.Request.WithContext(reqlog.WithID(c.Request.Context(), id))
	c.Next()
}

// validRequestID chỉ chấp nhận ID từ 1 tới 64 ký tự chữ, số, '-' hoặc '_',
// để client không chèn được ký tự lạ (VD xuống dòng) vào log.
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

// requestLogger trả về logger mặc định gắn sẵn request_id của request,
// mọi dòng log về request này đều có cùng ID.
func requestLogger(c *gin.Context) *slog.Logger {
	return reqlog.Logger(c.Request.Context())
}

// logRequests ghi một dòng log cho mỗi request: IP client, method, path, status, số byte, thời gian xử lý.
// Lỗi 5xx ghi ở mức ERROR, 4xx ở mức WARN, còn lại INFO.
func logRequests(c *gin.Context) {
	start := time.Now()
	c.Next() // chạy các middleware phía sau và handler, xong mới ghi log

	status := c.Writer.Status() // c.Writer của Gin tự nhớ status và số byte, không cần bọc ResponseWriter
	level := slog.LevelInfo
	switch {
	case status >= 500:
		level = slog.LevelError
	case status >= 400:
		level = slog.LevelWarn
	}
	requestLogger(c).LogAttrs(c.Request.Context(), level, "request",
		slog.String("client_ip", c.ClientIP()),
		slog.String("method", c.Request.Method),
		slog.String("path", c.Request.URL.RequestURI()),
		slog.Int("status", status),
		slog.Int("size", max(c.Writer.Size(), 0)), // Size() là -1 khi chưa ghi byte nào
		slog.Duration("duration", time.Since(start)),
	)
}

// recoverPanic bắt panic của middleware và handler phía sau, ghi log kèm stack trace rồi trả 500 dạng JSON.
func recoverPanic(c *gin.Context) {
	defer func() {
		err := recover()
		if err == nil {
			return
		}
		if err == http.ErrAbortHandler {
			panic(err) // panic có chủ đích để hủy response, trả lại cho net/http
		}
		requestLogger(c).Error("panic", "err", err, "stack", string(debug.Stack()))
		c.Header("Connection", "close")
		errorJSON(c, http.StatusInternalServerError, "lỗi máy chủ")
	}()
	c.Next()
}

// cors cho phép trình duyệt từ origin gọi API. origin rỗng thì không làm gì.
func cors(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Add("Vary", "Origin") // response khác nhau tùy Origin, cache phải tách riêng
		if origin == "" || c.GetHeader("Origin") != origin {
			return // không gọi c.Next: Gin tự chạy tiếp handler sau khi middleware return
		}
		h.Set("Access-Control-Allow-Origin", origin)

		// preflight: trình duyệt hỏi trước khi gửi request "không đơn giản"
		if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			h.Set("Access-Control-Max-Age", "600")  // trình duyệt nhớ kết quả 10 phút
			c.AbortWithStatus(http.StatusNoContent) // trả lời luôn, không tới route nào
			return
		}
		// cho JavaScript đọc các header này (mặc định chỉ đọc được vài header cơ bản),
		// kể cả các header rate limit mà openapi.yaml hứa với frontend
		h.Set("Access-Control-Expose-Headers", "Location, X-Request-ID, X-Total-Count, Retry-After, RateLimit, RateLimit-Policy")
	}
}
