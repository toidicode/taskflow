package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// quiet là logger bỏ hết log, để output của go test gọn.
var quiet = slog.New(slog.DiscardHandler)

// flaky trả về job lỗi fails lần đầu rồi thành công, đếm số lần chạy vào runs.
func flaky(fails int32, runs *atomic.Int32) Job {
	return Job{Name: "flaky", Run: func(ctx context.Context) error {
		if runs.Add(1) <= fails {
			return errors.New("lỗi tạm thời")
		}
		return nil
	}}
}

func TestBackoff(t *testing.T) {
	base, maxDelay := time.Second, 10*time.Second
	tests := []struct {
		attempt int
		full    time.Duration // khoảng chờ trước khi lấy ngẫu nhiên
	}{{1, 1 * time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second}, {4, 8 * time.Second}, {5, 10 * time.Second}, {50, 10 * time.Second}}
	for _, tt := range tests {
		for range 100 { // có phần ngẫu nhiên nên thử nhiều lần
			if d := backoff(tt.attempt, base, maxDelay); d < tt.full/2 || d >= tt.full {
				t.Fatalf("backoff(%d) = %v, muốn trong [%v, %v)", tt.attempt, d, tt.full/2, tt.full)
			}
		}
	}
}

func TestRetryThenSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) { // đồng hồ giả: chờ 3 giây trôi qua ngay
		var runs atomic.Int32
		d := New(Options{Workers: 1, BaseDelay: time.Second, Logger: quiet})
		d.Start()
		start := time.Now()
		if err := d.Enqueue(flaky(2, &runs)); err != nil {
			t.Fatal(err)
		}
		if err := d.Stop(context.Background()); err != nil { // chờ job chạy xong, kể cả các lần chạy lại
			t.Fatal(err)
		}
		if n := runs.Load(); n != 3 {
			t.Errorf("chạy %d lần, muốn 3", n)
		}
		// chờ lần 1 trong [0.5s, 1s), lần 2 trong [1s, 2s)
		if elapsed := time.Since(start); elapsed < 1500*time.Millisecond || elapsed >= 3*time.Second {
			t.Errorf("mất %v, muốn trong [1.5s, 3s)", elapsed)
		}
	})
}

func TestGiveUpAfterMaxAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		d := New(Options{Workers: 1, MaxAttempts: 3, Logger: quiet})
		d.Start()
		d.Enqueue(flaky(100, &runs))
		d.Stop(context.Background())
		if n := runs.Load(); n != 3 {
			t.Errorf("chạy %d lần, muốn 3", n)
		}
	})
}

func TestTimeoutAndPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var errs []error
		var runs atomic.Int32
		d := New(Options{Workers: 1, Timeout: 5 * time.Second, Logger: quiet})
		d.Start()
		start := time.Now()
		d.Enqueue(Job{Name: "slow", Run: func(ctx context.Context) error {
			switch runs.Add(1) {
			case 1: // lần 1: chờ tới khi hết timeout
				<-ctx.Done()
				errs = append(errs, ctx.Err())
				return ctx.Err()
			case 2: // lần 2: panic, không được làm chết test
				panic("nil map")
			}
			return nil
		}})
		d.Stop(context.Background())
		if n := runs.Load(); n != 3 {
			t.Errorf("chạy %d lần, muốn 3", n)
		}
		if len(errs) != 1 || !errors.Is(errs[0], context.DeadlineExceeded) {
			t.Errorf("lỗi lần 1 = %v, muốn context.DeadlineExceeded", errs)
		}
		if elapsed := time.Since(start); elapsed < 5*time.Second {
			t.Errorf("mất %v, lần 1 phải chờ đủ 5s", elapsed)
		}
	})
}

func TestQueueFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		blocking := Job{Name: "blocking", Run: func(ctx context.Context) error {
			<-release
			return nil
		}}
		d := New(Options{Workers: 1, QueueSize: 1, Logger: quiet})
		d.Start()
		if err := d.Enqueue(blocking); err != nil { // worker nhận job này
			t.Fatal(err)
		}
		// chờ worker lấy job ra khỏi hàng đợi và kẹt trong Run
		synctest.Wait()
		if err := d.Enqueue(blocking); err != nil { // nằm chờ trong hàng đợi
			t.Fatal(err)
		}
		if err := d.Enqueue(blocking); !errors.Is(err, ErrQueueFull) {
			t.Fatalf("Enqueue khi đầy = %v, muốn ErrQueueFull", err)
		}
		close(release)
		if err := d.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := d.Enqueue(blocking); !errors.Is(err, ErrStopped) {
			t.Fatalf("Enqueue sau Stop = %v, muốn ErrStopped", err)
		}
	})
}

func TestStopTimeoutAbandonsJobs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		waitForCancel := Job{Name: "wait", Run: func(ctx context.Context) error {
			runs.Add(1)
			<-ctx.Done() // chỉ dừng khi bị hủy
			return ctx.Err()
		}}
		d := New(Options{Workers: 1, Timeout: time.Hour, Logger: quiet})
		d.Start()
		d.Enqueue(waitForCancel) // đang chạy khi Stop
		d.Enqueue(waitForCancel) // còn trong hàng đợi khi Stop
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		err := d.Stop(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Stop = %v, muốn context.DeadlineExceeded", err)
		}
		if err.Error() != "bỏ dở 2 job: context deadline exceeded" {
			t.Errorf("Stop = %q", err)
		}
		if n := runs.Load(); n != 1 {
			t.Errorf("chạy %d job, muốn 1 (job thứ hai bị bỏ)", n)
		}
	})
}

func TestEvery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		d := New(Options{Logger: quiet})
		d.Start()
		d.Every(time.Minute, Job{Name: "tick", Run: func(ctx context.Context) error {
			runs.Add(1)
			return nil
		}})
		time.Sleep(3*time.Minute + time.Second)
		synctest.Wait() // chờ worker chạy xong job của phút thứ 3
		if n := runs.Load(); n != 3 {
			t.Errorf("chạy %d lần sau 3 phút, muốn 3", n)
		}
		d.Stop(context.Background())
	})
}
