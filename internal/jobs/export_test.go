package jobs

import (
	"log/slog"
	"time"
)

// NewTestRedis tạo Redis cho test: queue có tiền tố prefix, hỏi Redis mỗi 100ms và chạy lại
// sau vài chục mili giây thay vì vài giây, để test không phải chờ lâu.
func NewTestRedis(url, prefix string, logger *slog.Logger) (*Redis, error) {
	return NewRedis(url, RedisOptions{
		Concurrency: 2, BaseDelay: 20 * time.Millisecond, MaxDelay: 100 * time.Millisecond,
		ShutdownTimeout: time.Second, QueuePrefix: prefix, Logger: logger,
		pollInterval: 100 * time.Millisecond,
	})
}
