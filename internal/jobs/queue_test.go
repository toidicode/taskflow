package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// payload là payload mẫu của test.
type payload struct {
	TaskID int    `json:"task_id"`
	Title  string `json:"title"`
}

// recorder là handler ghi lại payload nhận được.
type recorder struct {
	mu  sync.Mutex
	got []string
}

func (r *recorder) run(ctx context.Context, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, string(data))
	return nil
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

func TestLocalEnqueue(t *testing.T) {
	ctx := context.Background()
	q := NewLocal(Options{Workers: 1, Logger: quiet})
	var rec recorder
	q.Handle(Handler{Type: "test:record:v1", Run: rec.run})
	if err := q.Start(); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(ctx, "test:record:v1", payload{7, "Viết test"}, ID("bị bỏ qua")); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(ctx, "test:khong-co:v1", nil); err == nil { // gõ sai tên: lỗi ngay khi đưa vào
		t.Error("Enqueue loại job chưa đăng ký: không lỗi")
	}
	if err := q.Enqueue(ctx, "test:record:v1", func() {}); err == nil { // không mã hóa JSON được
		t.Error("Enqueue payload là func: không lỗi")
	}
	if err := q.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.list(); len(got) != 1 || got[0] != `{"task_id":7,"title":"Viết test"}` {
		t.Errorf("handler nhận %q", got)
	}
	if err := q.Enqueue(ctx, "test:record:v1", nil); !errors.Is(err, ErrStopped) {
		t.Errorf("Enqueue sau Stop = %v, muốn ErrStopped", err)
	}
}

func TestLocalSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) { // đồng hồ giả: 3 giờ trôi qua ngay
		var runs atomic.Int32
		q := NewLocal(Options{Logger: quiet})
		q.Handle(Handler{Type: "test:tick:v1", Run: func(ctx context.Context, _ []byte) error {
			runs.Add(1)
			return nil
		}})
		if err := q.Schedule("0 * * * *", "test:tick:v1"); err != nil { // đầu mỗi giờ
			t.Fatal(err)
		}
		if err := q.Schedule("mỗi giờ", "test:tick:v1"); err == nil {
			t.Error("lịch sai: không lỗi")
		}
		if err := q.Schedule("@hourly", "test:khong-co:v1"); err == nil {
			t.Error("lịch cho loại job chưa đăng ký: không lỗi")
		}
		q.Start()
		// đồng hồ của synctest bắt đầu lúc 00:00 UTC 01/01/2000: các lần chạy lúc 01:00, 02:00, 03:00
		time.Sleep(3*time.Hour + time.Minute)
		synctest.Wait()
		if n := runs.Load(); n != 3 {
			t.Errorf("chạy %d lần sau 3 giờ, muốn 3", n)
		}
		if err := q.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSkipRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		d := New(Options{Workers: 1, Logger: quiet})
		d.Start()
		d.Enqueue(Job{Name: "payload-hong", Run: func(ctx context.Context) error {
			runs.Add(1)
			return fmt.Errorf("payload hỏng: %w", SkipRetry)
		}})
		d.Stop(context.Background())
		if n := runs.Load(); n != 1 {
			t.Errorf("chạy %d lần, muốn 1: lỗi SkipRetry không được chạy lại", n)
		}
	})
}
