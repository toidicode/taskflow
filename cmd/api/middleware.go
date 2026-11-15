package main

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// middleware nhận một handler và trả về handler mới bọc quanh nó.
type middleware func(http.Handler) http.Handler

// chain bọc h bằng các middleware, middleware đứng đầu danh sách nằm ngoài cùng:
// chain(h, a, b) chính là a(b(h)), request đi qua a rồi b rồi mới tới h.
func chain(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// ctxKey là kiểu key cho context value. Kiểu không exported nên package khác
// không thể tạo ra key trùng với key của package này.
type ctxKey int

const requestIDKey ctxKey = iota

// requestID gắn cho mỗi request một ID: lấy từ header X-Request-ID nếu hợp lệ,
// không thì tự sinh. ID được lưu vào context và trả lại trong header của response.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = rand.Text()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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

// requestIDFrom đọc request ID từ context, trả về "-" khi không có.
func requestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return "-"
}

// statusRecorder bọc ResponseWriter để ghi lại status code và số byte của body.
type statusRecorder struct {
	http.ResponseWriter
	status int
	size   int
}

func (rec *statusRecorder) WriteHeader(code int) {
	if rec.status == 0 {
		rec.status = code
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK // Write không có WriteHeader trước nghĩa là 200
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.size += n
	return n, err
}

// Unwrap trả về ResponseWriter gốc để http.ResponseController
// vẫn gọi được Flush, SetWriteDeadline... qua lớp bọc này.
func (rec *statusRecorder) Unwrap() http.ResponseWriter {
	return rec.ResponseWriter
}

// requestLogger trả về logger mặc định gắn sẵn request_id của r,
// mọi dòng log về request này đều có cùng ID.
func requestLogger(r *http.Request) *slog.Logger {
	return slog.With("request_id", requestIDFrom(r.Context()))
}

// logRequests ghi một dòng log cho mỗi request: method, path, status, số byte, thời gian xử lý.
// Lỗi 5xx ghi ở mức ERROR, 4xx ở mức WARN, còn lại INFO.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK // handler không ghi gì cả
		}

		level := slog.LevelInfo
		switch {
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status >= 400:
			level = slog.LevelWarn
		}
		requestLogger(r).LogAttrs(r.Context(), level, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.RequestURI()),
			slog.Int("status", rec.status),
			slog.Int("size", rec.size),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

// recoverPanic bắt panic của handler phía sau, ghi log kèm stack trace rồi trả 500 dạng JSON.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			err := recover()
			if err == nil {
				return
			}
			if err == http.ErrAbortHandler {
				panic(err) // panic có chủ đích để hủy response, trả lại cho net/http
			}
			requestLogger(r).Error("panic", "err", err, "stack", string(debug.Stack()))
			w.Header().Set("Connection", "close")
			errorJSON(w, http.StatusInternalServerError, "lỗi máy chủ")
		}()
		next.ServeHTTP(w, r)
	})
}

// cors cho phép trình duyệt từ origin gọi API. origin rỗng thì không làm gì.
func cors(origin string) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Add("Vary", "Origin") // response khác nhau tùy Origin, cache phải tách riêng
			if origin == "" || r.Header.Get("Origin") != origin {
				next.ServeHTTP(w, r)
				return
			}
			h.Set("Access-Control-Allow-Origin", origin)

			// preflight: trình duyệt hỏi trước khi gửi request "không đơn giản"
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				h.Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")
				h.Set("Access-Control-Max-Age", "600") // trình duyệt nhớ kết quả 10 phút
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// cho JavaScript đọc các header này (mặc định chỉ đọc được vài header cơ bản)
			h.Set("Access-Control-Expose-Headers", "Location, X-Request-ID, X-Total-Count")
			next.ServeHTTP(w, r)
		})
	}
}
