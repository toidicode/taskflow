// Package reqlog gắn request ID vào context, để tầng HTTP lẫn tầng service ghi log
// cùng một request_id mà không phải truyền logger qua từng hàm.
package reqlog

import (
	"context"
	"log/slog"
)

// key là kiểu key cho context value, không exported nên package khác không tạo được key trùng.
type key struct{}

// WithID trả về context con mang request ID id.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, key{}, id)
}

// ID đọc request ID từ context, trả về "-" khi không có (VD job chạy nền, test).
func ID(ctx context.Context) string {
	if id, ok := ctx.Value(key{}).(string); ok {
		return id
	}
	return "-"
}

// Logger trả về logger mặc định gắn sẵn request_id của ctx.
func Logger(ctx context.Context) *slog.Logger {
	return slog.With("request_id", ID(ctx))
}

// Valid chỉ chấp nhận ID từ 1 tới 64 ký tự chữ, số, '-' hoặc '_', để bên gọi
// (header X-Request-ID, metadata gRPC) không chèn được ký tự lạ (VD xuống dòng) vào log.
func Valid(id string) bool {
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

// Handler bọc một slog.Handler: bản ghi nào được ghi với ctx mang request ID
// (logger.InfoContext(ctx, ...)) thì có thêm request_id, không cần tạo logger cho từng request.
type Handler struct {
	slog.Handler
}

func (h Handler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := ctx.Value(key{}).(string); ok {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return Handler{h.Handler.WithAttrs(attrs)}
}

func (h Handler) WithGroup(name string) slog.Handler {
	return Handler{h.Handler.WithGroup(name)}
}
