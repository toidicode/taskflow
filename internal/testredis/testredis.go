// Package testredis tạo kết nối Redis cho integration test, giống testdb cho PostgreSQL.
package testredis

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/cache"
)

// EnvURL là biến môi trường chứa URL của Redis dành cho test, VD redis://localhost:6379/15.
// Test chỉ xóa key do chính nó tạo, nhưng đừng trỏ vào Redis của production.
const EnvURL = "TASKFLOW_TEST_REDIS_URL"

// New trả về client Redis và một tiền tố key riêng cho test này (VD "test:k7qzm2xa4b:"),
// để các test chạy song song không đụng key của nhau. Khi test xong, mọi key có tiền tố đó bị xóa.
// Không đặt TASKFLOW_TEST_REDIS_URL thì test được bỏ qua.
func New(t testing.TB) (*cache.Redis, string) {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Skip("chưa đặt " + EnvURL)
	}
	rc, err := cache.NewRedis(url)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := rc.Ping(ctx); err != nil {
		rc.Close()
		t.Fatalf("kết nối Redis test: %v", err)
	}
	prefix := "test:" + strings.ToLower(rand.Text()[:10]) + ":"
	t.Cleanup(func() {
		defer rc.Close()
		// KEYS chỉ dùng trong test: nó quét toàn bộ Redis. Key của asynq có dạng asynq:{<queue>}:...
		// với tên queue có tiền tố của test (jobs.RedisOptions.QueuePrefix)
		var keys []string
		for _, pattern := range []string{prefix + "*", "asynq:{" + prefix + "*"} {
			k, err := rc.Client().Keys(ctx, pattern).Result()
			if err != nil {
				t.Errorf("dọn key %s: %v", pattern, err)
			}
			keys = append(keys, k...)
		}
		if len(keys) > 0 {
			if err := rc.Delete(ctx, keys...); err != nil {
				t.Errorf("dọn key %s*: %v", prefix, err)
			}
		}
		for _, q := range rc.Client().SMembers(ctx, "asynq:queues").Val() { // danh sách queue của asynq
			if strings.HasPrefix(q, prefix) {
				rc.Client().SRem(ctx, "asynq:queues", q)
			}
		}
	})
	return rc, prefix
}

// URL trả về URL của Redis dành cho test (rỗng khi chưa đặt), cho các client tự tạo kết nối
// như jobs.NewRedis. Gọi sau New: New đã bỏ qua test khi chưa đặt.
func URL() string {
	return os.Getenv(EnvURL)
}

// WaitSubscribers chờ tới khi channel có ít nhất n subscriber (Redis Pub/Sub không giữ message:
// gửi trước khi bên nhận kịp SUBSCRIBE là mất), tối đa 5 giây.
func WaitSubscribers(t testing.TB, rc *cache.Redis, channel string, n int64) {
	t.Helper()
	for range 500 {
		got, err := rc.Client().PubSubNumSub(context.Background(), channel).Result()
		if err == nil && got[channel] >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("channel %s chưa có đủ %d subscriber", channel, n)
}
