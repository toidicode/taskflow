// Package jobs chạy việc nền (background job) ngay trong process của API: một hàng đợi
// trong bộ nhớ (buffered channel) và một nhóm worker cố định, có retry, timeout và dừng sạch.
// Job trong hàng đợi mất hết khi process dừng hoặc crash, nên chỉ dùng cho việc mất cũng
// không sao hoặc sẽ được làm lại (VD việc định kỳ).
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

// Job là một việc nền. Run phải dừng khi ctx bị hủy: hết thời gian của một lần chạy
// (Options.Timeout) hoặc Stop bỏ dở các job còn lại.
type Job struct {
	Name string                          // tên để ghi log, VD "notify-task-done"
	Run  func(ctx context.Context) error // trả lỗi (hoặc panic) thì được chạy lại sau một khoảng chờ
}

// Options cấu hình Dispatcher, field nào bằng 0 thì dùng giá trị mặc định.
type Options struct {
	Workers     int           // số job chạy cùng lúc (mặc định 4)
	QueueSize   int           // số job chờ tối đa trong hàng đợi (mặc định 100)
	MaxAttempts int           // số lần chạy tối đa của một job, tính cả lần đầu (mặc định 5)
	Timeout     time.Duration // thời gian tối đa của một lần chạy (mặc định 30s)
	BaseDelay   time.Duration // khoảng chờ trước lần chạy lại đầu tiên, gấp đôi sau mỗi lần (mặc định 1s)
	MaxDelay    time.Duration // khoảng chờ tối đa giữa hai lần chạy (mặc định 1m)
	Logger      *slog.Logger  // mặc định slog.Default()
}

var (
	// ErrQueueFull được Enqueue trả về khi hàng đợi đã đầy.
	ErrQueueFull = errors.New("hàng đợi job đã đầy")
	// ErrStopped được Enqueue trả về sau khi đã gọi Stop.
	ErrStopped = errors.New("dispatcher đã dừng")
)

// Dispatcher nhận job qua Enqueue và chạy chúng bằng Options.Workers worker.
type Dispatcher struct {
	opts      Options
	queue     chan Job
	ctx       context.Context // context cha của mọi job, bị hủy khi Stop bỏ dở
	cancel    context.CancelFunc
	quit      chan struct{}  // đóng khi Stop: các vòng lặp của Every thoát
	wg        sync.WaitGroup // các worker và vòng lặp của Every
	abandoned atomic.Int64   // số job bị bỏ dở vì Stop hết thời gian chờ

	mu      sync.RWMutex // Enqueue giữ RLock khi gửi để Stop không đóng queue giữa chừng
	stopped bool
}

// New tạo Dispatcher, gọi Start để các worker bắt đầu chạy.
func New(opts Options) *Dispatcher {
	opts.Workers = orDefault(opts.Workers, 4)
	opts.QueueSize = orDefault(opts.QueueSize, 100)
	opts.MaxAttempts = orDefault(opts.MaxAttempts, 5)
	opts.Timeout = orDefault(opts.Timeout, 30*time.Second)
	opts.BaseDelay = orDefault(opts.BaseDelay, time.Second)
	opts.MaxDelay = orDefault(opts.MaxDelay, time.Minute)
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{
		opts:   opts,
		queue:  make(chan Job, opts.QueueSize),
		ctx:    ctx,
		cancel: cancel,
		quit:   make(chan struct{}),
	}
}

// orDefault trả về v, hoặc def khi v không lớn hơn 0.
func orDefault[T int | time.Duration](v, def T) T {
	if v > 0 {
		return v
	}
	return def
}

// Start chạy các worker, chỉ gọi một lần.
func (d *Dispatcher) Start() {
	for range d.opts.Workers {
		d.wg.Go(d.worker)
	}
}

// Enqueue đưa j vào hàng đợi rồi trả về ngay, không chờ j chạy. Hàng đợi đầy thì trả
// ErrQueueFull, đã Stop thì trả ErrStopped: nơi gọi tự quyết bỏ qua hay báo lỗi.
func (d *Dispatcher) Enqueue(j Job) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.stopped {
		return ErrStopped
	}
	select {
	case d.queue <- j:
		return nil
	default: // không chờ chỗ trống: request đang đợi phía sau
		return ErrQueueFull
	}
}

// Every đưa j vào hàng đợi sau mỗi khoảng interval cho tới khi Stop. Job định kỳ cũng có
// retry, timeout như mọi job khác; hàng đợi đầy thì bỏ lần này, chờ lần sau.
func (d *Dispatcher) Every(interval time.Duration, j Job) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.wg.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-d.quit:
				return
			case <-ticker.C:
				if err := d.Enqueue(j); errors.Is(err, ErrQueueFull) {
					d.opts.Logger.Warn("bỏ qua lần chạy định kỳ", "job", j.Name, "err", err)
				}
			}
		}
	})
}

// Stop ngừng nhận job mới và chờ các worker làm hết những job đã có trong hàng đợi.
// Nếu ctx hết hạn trước, Stop hủy context của các job đang chạy, bỏ các job còn lại
// và trả lỗi kèm số job bị bỏ dở (không job nào bị bỏ dở thì vẫn trả nil).
func (d *Dispatcher) Stop(ctx context.Context) error {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return ErrStopped
	}
	d.stopped = true
	close(d.quit)
	close(d.queue) // worker làm nốt các job còn trong queue rồi thoát khỏi vòng range
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		d.cancel()
		return nil
	case <-ctx.Done():
		d.cancel() // job đang chạy nhận ctx.Done(), job đang chờ retry thôi chờ
		<-done     // các worker bỏ nhanh những job còn lại rồi thoát
		if n := d.abandoned.Load(); n > 0 {
			return fmt.Errorf("bỏ dở %d job: %w", n, ctx.Err())
		}
		return nil // ctx hết hạn đúng lúc các job vừa xong: không mất job nào
	}
}

// worker lấy job từ hàng đợi cho tới khi Stop đóng hàng đợi.
func (d *Dispatcher) worker() {
	for j := range d.queue {
		if d.ctx.Err() != nil { // Stop đã hết thời gian chờ: không chạy nữa
			d.abandoned.Add(1)
			continue
		}
		d.process(j)
	}
}

// process chạy j, lỗi thì chờ theo backoff rồi chạy lại, tối đa MaxAttempts lần.
func (d *Dispatcher) process(j Job) {
	log := d.opts.Logger.With("job", j.Name)
	for attempt := 1; ; attempt++ {
		start := time.Now()
		err := d.runOnce(j)
		if err == nil {
			log.Debug("job xong", "attempt", attempt, "duration", time.Since(start))
			return
		}
		if d.ctx.Err() != nil {
			d.abandoned.Add(1)
			log.Warn("bỏ dở job vì dispatcher dừng", "attempt", attempt, "err", err)
			return
		}
		if attempt >= d.opts.MaxAttempts {
			log.Error("job thất bại, không chạy lại nữa", "attempt", attempt, "err", err)
			return
		}
		delay := backoff(attempt, d.opts.BaseDelay, d.opts.MaxDelay)
		log.Warn("job lỗi, sẽ chạy lại", "attempt", attempt, "retry_in", delay, "err", err)
		select {
		case <-time.After(delay):
		case <-d.ctx.Done():
			d.abandoned.Add(1)
			log.Warn("bỏ dở job vì dispatcher dừng", "attempt", attempt)
			return
		}
	}
}

// runOnce chạy j một lần với timeout. Panic trong job thành lỗi thay vì làm chết cả process.
func (d *Dispatcher) runOnce(j Job) (err error) {
	ctx, cancel := context.WithTimeout(d.ctx, d.opts.Timeout)
	defer cancel()
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v", v)
		}
	}()
	return j.Run(ctx)
}

// backoff trả về khoảng chờ sau lần chạy lỗi thứ attempt: base·2^(attempt-1), tối đa maxDelay,
// rồi lấy ngẫu nhiên trong [d/2, d) (jitter) để các job cùng lỗi không cùng chạy lại một lúc.
func backoff(attempt int, base, maxDelay time.Duration) time.Duration {
	d := base
	for i := 1; i < attempt && d < maxDelay; i++ {
		d *= 2
	}
	d = min(d, maxDelay)
	if half := d / 2; half > 0 {
		return half + rand.N(half)
	}
	return d
}
