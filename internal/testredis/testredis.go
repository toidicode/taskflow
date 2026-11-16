// Package testredis tạo kết nối Redis cho integration test, giống testdb cho PostgreSQL.
package testredis

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"

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
		keys, err := rc.Client().Keys(ctx, prefix+"*").Result() // KEYS chỉ dùng trong test: nó quét toàn bộ Redis
		if err == nil && len(keys) > 0 {
			err = rc.Delete(ctx, keys...)
		}
		if err != nil {
			t.Errorf("dọn key %s*: %v", prefix, err)
		}
	})
	return rc, prefix
}
