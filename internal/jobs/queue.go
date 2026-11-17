package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/robfig/cron/v3"
)

// Queue là hàng đợi job có tên: payload là dữ liệu JSON chứ không phải closure, nên job đi
// qua được Redis và chạy được ở process khác. Có hai bản: Local (trong bộ nhớ, Bài 36) khi
// không có Redis, và Redis (asynq) khi có.
type Queue interface {
	// Handle đăng ký một loại job, gọi trước Start.
	Handle(h Handler)
	// Enqueue đưa job loại typ với payload (mã hóa JSON) vào hàng đợi.
	Enqueue(ctx context.Context, typ string, payload any, opts ...Option) error
	// Schedule đưa job loại typ (payload rỗng) vào hàng đợi theo lịch cron, VD "0 * * * *"
	// (đầu mỗi giờ) hay "@every 30m". Gọi trước Start.
	Schedule(spec, typ string) error
	Start() error
	// Stop ngừng nhận và chạy job, chờ job đang chạy tối đa tới khi ctx hết hạn.
	Stop(ctx context.Context) error
}

// Tên các queue, theo thứ tự ưu tiên giảm dần (bản Redis lấy job theo trọng số 6:3:1).
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueLow      = "low"
)

// Handler mô tả một loại job: tên, cách chạy và các tùy chọn khi đưa vào hàng đợi.
// Bản Local chỉ dùng Type và Run (retry, timeout theo Options của Dispatcher).
type Handler struct {
	Type      string        // VD "notify:task_done:v1": đổi payload kiểu không tương thích thì tăng v
	Queue     string        // QueueCritical, QueueDefault (mặc định) hoặc QueueLow
	MaxRetry  int           // số lần chạy lại tối đa sau lần đầu (mặc định 5)
	Timeout   time.Duration // thời gian tối đa của một lần chạy (mặc định 30s)
	Retention time.Duration // giữ job đã xong trong Redis chừng này: để xem lại, và để ID chặn trùng
	Unique    time.Duration // job định kỳ: bỏ lần chạy mới khi lần trước còn chờ (tối đa chừng này)
	Run       func(ctx context.Context, payload []byte) error
}

// Option là tùy chọn của một lần Enqueue, VD ID.
type Option func(*EnqueueOptions)

// EnqueueOptions là các tùy chọn đã gom lại của một lần Enqueue.
type EnqueueOptions struct {
	ID string
}

// Collect gom opts lại, dùng trong các cài đặt Queue (kể cả hàng đợi giả trong test).
func Collect(opts []Option) EnqueueOptions {
	var o EnqueueOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// ID đặt ID cho job: bản Redis từ chối job trùng ID với job còn trong hàng đợi (kể cả job đã xong
// còn trong thời gian Retention) bằng ErrDuplicate. Bản Local bỏ qua ID.
func ID(id string) Option {
	return func(o *EnqueueOptions) { o.ID = id }
}

var (
	// ErrDuplicate được Enqueue trả về khi đã có job cùng ID (hoặc job định kỳ trước chưa xong).
	ErrDuplicate = errors.New("job trùng với job đã có")
	// SkipRetry: handler trả lỗi wrap SkipRetry thì job không được chạy lại (VD payload hỏng).
	// Chính là asynq.SkipRetry nên errors.Is dùng được với cả hai bản.
	SkipRetry = asynq.SkipRetry
)

// marshal mã hóa payload, nil thành payload rỗng.
func marshal(typ string, payload any) ([]byte, error) {
	if payload == nil {
		return nil, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("mã hóa payload của %s: %w", typ, err)
	}
	return data, nil
}

// Local là Queue chạy job ngay trong process bằng Dispatcher: mất job khi process dừng,
// mỗi bản API một hàng đợi riêng. Dùng khi không có Redis (máy dev, test).
type Local struct {
	d *Dispatcher

	mu        sync.Mutex
	handlers  map[string]Handler
	schedules []localSchedule
	quit      chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

type localSchedule struct {
	spec cron.Schedule
	typ  string
}

// NewLocal tạo Local với Dispatcher theo opts.
func NewLocal(opts Options) *Local {
	return &Local{d: New(opts), handlers: make(map[string]Handler), quit: make(chan struct{})}
}

var _ Queue = (*Local)(nil)

func (l *Local) Handle(h Handler) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handlers[h.Type] = h
}

func (l *Local) handler(typ string) (Handler, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.handlers[typ]
	if !ok {
		return Handler{}, fmt.Errorf("chưa đăng ký loại job %q", typ)
	}
	return h, nil
}

func (l *Local) Enqueue(ctx context.Context, typ string, payload any, opts ...Option) error {
	h, err := l.handler(typ)
	if err != nil {
		return err
	}
	data, err := marshal(typ, payload) // mã hóa như bản Redis: payload không mã hóa được thì lỗi ngay
	if err != nil {
		return err
	}
	return l.d.Enqueue(Job{Name: typ, Run: func(ctx context.Context) error { return h.Run(ctx, data) }})
}

func (l *Local) Schedule(spec, typ string) error {
	sched, err := ParseSchedule(spec)
	if err != nil {
		return err
	}
	if _, err := l.handler(typ); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.schedules = append(l.schedules, localSchedule{sched, typ})
	return nil
}

// ParseSchedule đọc lịch cron 5 trường (phút giờ ngày tháng thứ) hoặc "@every 1h", "@hourly"...
func ParseSchedule(spec string) (cron.Schedule, error) {
	s, err := cron.ParseStandard(spec)
	if err != nil {
		return nil, fmt.Errorf("lịch %q: %w", spec, err)
	}
	return s, nil
}

func (l *Local) Start() error {
	l.d.Start()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.schedules {
		l.wg.Go(func() { l.runSchedule(s) })
	}
	return nil
}

// runSchedule đưa job vào hàng đợi mỗi lần tới giờ, cho tới khi Stop.
func (l *Local) runSchedule(s localSchedule) {
	for {
		timer := time.NewTimer(time.Until(s.spec.Next(time.Now())))
		select {
		case <-l.quit:
			timer.Stop()
			return
		case <-timer.C:
			if err := l.Enqueue(context.Background(), s.typ, nil); err != nil {
				l.d.opts.Logger.Warn("bỏ qua lần chạy định kỳ", "job", s.typ, "err", err)
			}
		}
	}
}

func (l *Local) Stop(ctx context.Context) error {
	l.stopOnce.Do(func() { close(l.quit) })
	l.wg.Wait() // các vòng lịch thoát trước: không còn ai Enqueue vào dispatcher đã dừng
	return l.d.Stop(ctx)
}
