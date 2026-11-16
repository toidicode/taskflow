package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/cache"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/testredis"
)

// TestRedis chạy với Redis thật (TASKFLOW_TEST_REDIS_URL), không có thì bỏ qua.
func TestRedis(t *testing.T) {
	t.Parallel()
	rc, prefix := testredis.New(t)
	ctx := context.Background()
	key := prefix + "k"

	if _, ok, err := rc.Get(ctx, key); ok || err != nil {
		t.Fatalf("key chưa có: ok = %v, err = %v (redis.Nil phải thành miss, không phải lỗi)", ok, err)
	}
	if err := rc.Set(ctx, key, []byte(`{"id":1}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	b, ok, err := rc.Get(ctx, key)
	if err != nil || !ok || string(b) != `{"id":1}` {
		t.Fatalf("Get: %q, %v, %v", b, ok, err)
	}
	if ttl := rc.Client().PTTL(ctx, key).Val(); ttl <= 59*time.Second || ttl > time.Minute {
		t.Errorf("TTL = %v, muốn gần 1 phút", ttl)
	}
	if err := rc.Delete(ctx, key, prefix+"khong-co"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := rc.Get(ctx, key); ok {
		t.Error("sau Delete key vẫn còn")
	}
}

// TestCachedStoreRedis: CachedStore trên Redis thật, đọc lần hai trúng cache, ghi thì cache được xóa.
func TestCachedStoreRedis(t *testing.T) {
	t.Parallel()
	rc, prefix := testredis.New(t)
	ctx := context.Background()
	s := project.NewCachedStore(project.NewMemoryStore(), rc, project.CacheOptions{Prefix: prefix})

	s.Add(ctx, project.New("TaskFlow API", 1))
	for range 2 {
		if ps, err := s.List(ctx, 1); err != nil || len(ps) != 1 {
			t.Fatalf("List: %v, %v", ps, err)
		}
	}
	if st := s.Stats(); st.Hits != 1 || st.Misses != 1 {
		t.Errorf("sau 2 lần List: %+v, muốn 1 hit 1 miss", st)
	}
	if n := rc.Client().Exists(ctx, prefix+"projects:v1:owner:1").Val(); n != 1 {
		t.Errorf("key danh sách không có trong Redis")
	}

	s.Add(ctx, project.New("Website", 1))
	if ps, _ := s.List(ctx, 1); len(ps) != 2 {
		t.Errorf("sau Add: %d project, muốn 2 (cache phải bị xóa)", len(ps))
	}
}

// TestRedisDown: Redis không chạy thì lệnh lỗi nhanh và CachedStore vẫn trả dữ liệu từ store.
// Không cần Redis thật: cổng 1 trên máy test không có ai lắng nghe.
func TestRedisDown(t *testing.T) {
	rc, err := cache.NewRedis("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	ctx := context.Background()
	s := project.NewCachedStore(project.NewMemoryStore(), rc, project.CacheOptions{})

	start := time.Now()
	if err := s.Add(ctx, project.New("TaskFlow API", 1)); err != nil {
		t.Fatal(err)
	}
	ps, err := s.List(ctx, 1)
	if err != nil || len(ps) != 1 {
		t.Fatalf("List khi Redis chết: %v, %v", ps, err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("mất %v, cache lỗi phải bỏ qua nhanh", d)
	}
	if st := s.Stats(); st.Errors != 2 { // xóa sau Add, đọc (đọc lỗi thì không ghi)
		t.Errorf("Stats = %+v, muốn 2 lỗi", st)
	}
}
