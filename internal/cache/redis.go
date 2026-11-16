// Package cache chứa các nơi lưu cache dạng key → []byte có thời hạn:
// Redis cho production, Memory cho test.
package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// go-redis tự ghi log lỗi kết nối ra stderr bằng package log, mỗi request một dòng khi Redis chết.
// Lỗi đó đã được CachedStore ghi WARN kèm request_id, nên log riêng của go-redis chỉ để ở mức DEBUG.
func init() {
	redis.SetLogger(slogLogger{})
}

type slogLogger struct{}

func (slogLogger) Printf(ctx context.Context, format string, v ...any) {
	slog.DebugContext(ctx, "go-redis: "+fmt.Sprintf(format, v...))
}

// Redis lưu cache trong Redis qua go-redis. Một *Redis dùng chung cho mọi goroutine:
// bên trong redis.Client có sẵn pool kết nối.
type Redis struct {
	client *redis.Client
}

// Options đọc url dạng redis://[:mật-khẩu@]host:port/số-db rồi đặt timeout ngắn cho những gì url không ghi.
// Cache chỉ là phần thêm: Redis chậm hay chết thì phải bỏ qua thật nhanh rồi đọc database,
// không để request chờ 5 giây theo mặc định của go-redis.
func Options(url string) (*redis.Options, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	if opt.DialTimeout == 0 {
		opt.DialTimeout = 500 * time.Millisecond
	}
	if opt.ReadTimeout == 0 {
		opt.ReadTimeout = 200 * time.Millisecond
		opt.WriteTimeout = 200 * time.Millisecond
	}
	if opt.PoolTimeout == 0 {
		opt.PoolTimeout = 300 * time.Millisecond // chờ kết nối rảnh trong pool
	}
	if opt.MaxRetries == 0 {
		opt.MaxRetries = -1 // không thử lại lệnh lỗi: đọc database còn nhanh hơn
	}
	opt.DialerRetries = 1            // mặc định thử kết nối 5 lần mỗi lần cần kết nối mới
	opt.ContextTimeoutEnabled = true // lệnh dừng khi context của request bị hủy
	return opt, nil
}

// NewRedis tạo client cho url (xem Options). Client kết nối khi có lệnh đầu tiên,
// nên NewRedis không lỗi khi Redis chưa chạy; gọi Ping để kiểm tra.
func NewRedis(url string) (*Redis, error) {
	opt, err := Options(url)
	if err != nil {
		return nil, err
	}
	return &Redis{client: redis.NewClient(opt)}, nil
}

// Get trả về giá trị của key; ok là false khi key không có hoặc đã hết hạn.
func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) { // redis.Nil: key không tồn tại, không phải lỗi
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// Set ghi key với thời hạn ttl.
func (r *Redis) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return r.client.Set(ctx, key, val, ttl).Err()
}

// Delete xóa các key, key không tồn tại thì bỏ qua.
func (r *Redis) Delete(ctx context.Context, keys ...string) error {
	return r.client.Del(ctx, keys...).Err()
}

// Ping kiểm tra kết nối tới Redis.
func (r *Redis) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Client trả về redis.Client bên dưới, cho những việc Redis làm được ngoài cache (VD test dọn key).
func (r *Redis) Client() *redis.Client {
	return r.client
}

// Close đóng mọi kết nối trong pool.
func (r *Redis) Close() error {
	return r.client.Close()
}
