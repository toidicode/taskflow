package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/cache"
	"github.com/toidicode/taskflow/internal/testredis"
)

// TestRedis chạy cùng kịch bản với TestMemory trên Redis thật (TASKFLOW_TEST_REDIS_URL).
func TestRedis(t *testing.T) {
	t.Parallel()
	rc, prefix := testredis.New(t)
	r := NewRedis(rc.Client(), prefix)
	limiterSteps(t, r, "ip:203.0.113.7", false)

	// key tự hết hạn khi bucket đầy lại: không có key nào nằm mãi trong Redis
	ttl := rc.Client().PTTL(context.Background(), prefix+"ip:203.0.113.7").Val()
	if ttl <= 59*time.Second || ttl > time.Minute {
		t.Errorf("PTTL = %v, muốn gần 1 phút", ttl)
	}
}

// TestRedisRefill: token được thêm theo thời gian thật của Redis.
func TestRedisRefill(t *testing.T) {
	t.Parallel()
	rc, prefix := testredis.New(t)
	r := NewRedis(rc.Client(), prefix)
	ctx, lim := context.Background(), Limit{N: 2, Period: 400 * time.Millisecond}
	r.Allow(ctx, "k", lim)
	r.Allow(ctx, "k", lim)
	res, err := r.Allow(ctx, "k", lim)
	if err != nil || res.Allowed || res.RetryAfter <= 0 || res.RetryAfter > 200*time.Millisecond {
		t.Fatalf("request 3: %+v, %v; muốn bị chặn, chờ tối đa 200ms", res, err)
	}
	time.Sleep(res.RetryAfter + 5*time.Millisecond)
	if res, err := r.Allow(ctx, "k", lim); err != nil || !res.Allowed {
		t.Errorf("sau RetryAfter: %+v, %v; muốn được đi", res, err)
	}
}

// TestRedisConcurrent: 50 request song song, như 50 request tới nhiều bản API cùng lúc:
// script chạy nguyên khối nên đúng 10 request được đi.
func TestRedisConcurrent(t *testing.T) {
	t.Parallel()
	rc, prefix := testredis.New(t)
	r := NewRedis(rc.Client(), prefix)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	for range 50 {
		wg.Go(func() {
			res, err := r.Allow(context.Background(), "user:1", Limit{N: 10, Period: time.Minute})
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if res.Allowed {
				allowed++
			}
		})
	}
	wg.Wait()
	if allowed != 10 {
		t.Errorf("%d request được đi, muốn 10", allowed)
	}
}

// TestRedisDown: Redis không chạy thì Allow trả lỗi nhanh, nơi gọi tự quyết cho qua hay chặn.
func TestRedisDown(t *testing.T) {
	rc, err := cache.NewRedis("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	start := time.Now()
	if _, err := NewRedis(rc.Client(), "").Allow(context.Background(), "k", Limit{N: 1, Period: time.Second}); err == nil {
		t.Fatal("Redis không chạy mà không có lỗi")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("mất %v mới báo lỗi", d)
	}
}
