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
