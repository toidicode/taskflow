package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/testredis"
)

// newRedisQueue tạo jobs.Redis trên Redis của test, queue có tiền tố riêng để không đụng test khác.
func newRedisQueue(t *testing.T) (*jobs.Redis, *asynq.Inspector, string) {
	t.Helper()
	rc, prefix := testredis.New(t) // bỏ qua test khi chưa đặt TASKFLOW_TEST_REDIS_URL
	q, err := jobs.NewTestRedis(testredis.URL(), prefix, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return q, asynq.NewInspectorFromRedisClient(rc.Client()), prefix // Inspector xem trạng thái job trong Redis
}

// calls đếm số lần mỗi job chạy, theo payload.
type calls struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *calls) add(p []byte) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = make(map[string]int)
	}
	c.n[string(p)]++
	return c.n[string(p)]
}

func (c *calls) get(p string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[p]
}

// waitFor chờ tới khi cond đúng, tối đa 10 giây.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("hết thời gian chờ: %s", what)
}

func TestRedisQueue(t *testing.T) {
	q, ins, prefix := newRedisQueue(t)
	ctx := context.Background()
	var c calls
	q.Handle(jobs.Handler{Type: "test:ok:v1", Run: func(ctx context.Context, p []byte) error {
		c.add(p)
		return nil
	}})
	// lỗi 1 lần đầu rồi thành công
	q.Handle(jobs.Handler{Type: "test:flaky:v1", Run: func(ctx context.Context, p []byte) error {
		if c.add(p) == 1 {
			return errors.New("lỗi tạm thời")
		}
		return nil
	}})
	// luôn lỗi: chạy 1 + MaxRetry lần rồi vào archived
	q.Handle(jobs.Handler{Type: "test:fail:v1", MaxRetry: 1, Run: func(ctx context.Context, p []byte) error {
		c.add(p)
		return errors.New("luôn lỗi")
	}})
	// payload hỏng: SkipRetry, vào archived ngay
	q.Handle(jobs.Handler{Type: "test:skip:v1", Run: func(ctx context.Context, p []byte) error {
		c.add(p)
		return fmt.Errorf("payload hỏng: %w", jobs.SkipRetry)
	}})

	// job đưa vào trước khi có worker nào vẫn nằm chờ trong Redis
	if err := q.Enqueue(ctx, "test:ok:v1", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := q.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Stop(context.Background()) })

	// cùng ID: lần hai bị từ chối
	if err := q.Enqueue(ctx, "test:ok:v1", map[string]int{"n": 2}, jobs.ID("cung-id")); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(ctx, "test:ok:v1", map[string]int{"n": 3}, jobs.ID("cung-id")); !errors.Is(err, jobs.ErrDuplicate) {
		t.Errorf("Enqueue trùng ID = %v, muốn ErrDuplicate", err)
	}
	for _, typ := range []string{"test:flaky:v1", "test:fail:v1", "test:skip:v1"} {
		if err := q.Enqueue(ctx, typ, typ); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Enqueue(ctx, "test:khong-co:v1", nil); err == nil {
		t.Error("Enqueue loại job chưa đăng ký: không lỗi")
	}

	waitFor(t, "job chạy xong", func() bool {
		return c.get(`{"n":1}`) == 1 && c.get(`{"n":2}`) == 1 && c.get(`"test:flaky:v1"`) == 2 &&
			c.get(`"test:fail:v1"`) == 2 && c.get(`"test:skip:v1"`) == 1
	})
	if n := c.get(`{"n":3}`); n != 0 {
		t.Errorf("job trùng ID chạy %d lần", n)
	}

	waitFor(t, "hai job vào archived", func() bool {
		tasks, _ := ins.ListArchivedTasks(prefix + jobs.QueueDefault)
		return len(tasks) == 2
	})
}

func TestRedisSchedule(t *testing.T) {
	q, _, _ := newRedisQueue(t)
	var c calls
	q.Handle(jobs.Handler{Type: "test:tick:v1", Unique: time.Minute, Run: func(ctx context.Context, p []byte) error {
		c.add(p)
		return nil
	}})
	if err := q.Schedule("@every 1s", "test:tick:v1"); err != nil {
		t.Fatal(err)
	}
	if err := q.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "job định kỳ chạy 2 lần", func() bool { return c.get("") >= 2 })
	if err := q.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestRedisEnqueueTimeout: Redis nhận kết nối nhưng không bao giờ trả lời (treo). Enqueue phải
// trả lỗi sau EnqueueTimeout (mặc định 1s) chứ không treo request theo timeout mặc định của go-redis.
// Không cần Redis thật: một listener TCP không đọc, không ghi.
func TestRedisEnqueueTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn // giữ kết nối mở, không trả lời
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()

	q, err := jobs.NewRedis("redis://"+ln.Addr().String()+"/0", jobs.RedisOptions{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Stop(context.Background()) })
	q.Handle(jobs.Handler{Type: "test:hang", Run: func(context.Context, []byte) error { return nil }})

	start := time.Now()
	err = q.Enqueue(context.Background(), "test:hang", nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Enqueue vào Redis treo không trả lỗi")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Enqueue mất %v, muốn khoảng 1s (err: %v)", elapsed, err)
	}
	t.Logf("Enqueue trả lỗi sau %v: %v", elapsed.Round(10*time.Millisecond), err)
}
