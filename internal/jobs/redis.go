package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// RedisOptions cấu hình Redis, field bằng 0 thì dùng mặc định.
type RedisOptions struct {
	Concurrency     int           // số job chạy cùng lúc trong process này (mặc định 4)
	BaseDelay       time.Duration // chờ trước lần chạy lại đầu tiên, gấp đôi sau mỗi lần (mặc định 10s)
	MaxDelay        time.Duration // chờ tối đa giữa hai lần chạy (mặc định 10m)
	ShutdownTimeout time.Duration // Stop chờ job đang chạy chừng này rồi trả job về hàng đợi (mặc định 8s)
	EnqueueTimeout  time.Duration // một lần Enqueue chờ Redis tối đa chừng này (mặc định 1s)
	QueuePrefix     string        // tiền tố tên queue trong Redis, test dùng để không đụng nhau
	Logger          *slog.Logger  // mặc định slog.Default()

	pollInterval time.Duration // test: hỏi Redis job mới, job hẹn giờ, job chạy lại thường hơn (mặc định 1s, 5s)
}

// Redis là Queue lưu job trong Redis bằng asynq: job còn nguyên khi process dừng hay crash,
// mọi bản API dùng chung một hàng đợi. Mỗi bản vừa đưa job vào (Client) vừa chạy job
// (Server) và đưa job định kỳ vào theo lịch (Scheduler).
type Redis struct {
	rdb      *redis.Client
	client   *asynq.Client
	server   *asynq.Server
	sched    *asynq.Scheduler
	inspect  *asynq.Inspector // đọc số job trong từng queue cho metrics
	mux      *asynq.ServeMux
	handlers map[string]Handler
	opts     RedisOptions
}

var _ Queue = (*Redis)(nil)

// NewRedis tạo Redis kết nối tới url (VD redis://localhost:6379/0). Client Redis là của riêng
// hàng đợi, với timeout mặc định của go-redis: không dùng timeout 200ms của cache. Riêng Enqueue
// (gọi trong request) bị giới hạn bởi EnqueueTimeout.
func NewRedis(url string, opts RedisOptions) (*Redis, error) {
	ropts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis url: %w", err)
	}
	ropts.ContextTimeoutEnabled = true // go-redis dừng đọc/ghi khi ctx hết hạn, không chỉ khi hết ReadTimeout
	opts.Concurrency = orDefault(opts.Concurrency, 4)
	opts.BaseDelay = orDefault(opts.BaseDelay, 10*time.Second)
	opts.MaxDelay = orDefault(opts.MaxDelay, 10*time.Minute)
	opts.ShutdownTimeout = orDefault(opts.ShutdownTimeout, 8*time.Second)
	opts.EnqueueTimeout = orDefault(opts.EnqueueTimeout, time.Second)
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	rdb := redis.NewClient(ropts)
	r := &Redis{rdb: rdb, client: asynq.NewClientFromRedisClient(rdb), mux: asynq.NewServeMux(),
		inspect: asynq.NewInspectorFromRedisClient(rdb), handlers: make(map[string]Handler), opts: opts}
	logger := asynqLogger{opts.Logger.With("component", "asynq")}
	r.server = asynq.NewServerFromRedisClient(rdb, asynq.Config{
		Concurrency: opts.Concurrency,
		Queues: map[string]int{ // trọng số: critical được lấy gấp đôi default, gấp sáu low
			r.queue(QueueCritical): 6,
			r.queue(QueueDefault):  3,
			r.queue(QueueLow):      1,
		},
		RetryDelayFunc: func(n int, err error, t *asynq.Task) time.Duration {
			return backoff(n+1, opts.BaseDelay, opts.MaxDelay) // n: số lần đã chạy lại
		},
		ErrorHandler:             asynq.ErrorHandlerFunc(r.logFailure),
		TaskCheckInterval:        opts.pollInterval,
		DelayedTaskCheckInterval: opts.pollInterval,
		ShutdownTimeout:          opts.ShutdownTimeout,
		Logger:                   logger,
		LogLevel:                 asynq.WarnLevel,
	})
	// Scheduler có kết nối riêng: bản FromRedisClient của asynq v0.26.0 ghi log ERROR (thừa, không hỏng gì)
	// "redis connection is shared..." mỗi lần Scheduler.Shutdown vì nó cố đóng kết nối dùng chung.
	r.sched = asynq.NewScheduler(redisConnOpt{ropts}, &asynq.SchedulerOpts{
		Location: time.Local,
		Logger:   logger,
		LogLevel: asynq.WarnLevel,
		PostEnqueueFunc: func(info *asynq.TaskInfo, err error) {
			switch {
			case errors.Is(err, asynq.ErrDuplicateTask): // bản API khác vừa đưa vào, hoặc lần trước chưa xong
				opts.Logger.Debug("bỏ qua lần chạy định kỳ: job trước chưa xong", "err", err)
			case err != nil:
				opts.Logger.Warn("không đưa được job định kỳ vào hàng đợi", "err", err)
			default:
				opts.Logger.Info("đã đưa job định kỳ vào hàng đợi", "job", info.Type, "id", info.ID)
			}
		},
	})
	return r, nil
}

// queue trả về tên thật trong Redis của queue name.
func (r *Redis) queue(name string) string {
	if name == "" {
		name = QueueDefault
	}
	return r.opts.QueuePrefix + name
}

func (r *Redis) Handle(h Handler) {
	r.handlers[h.Type] = h
	r.mux.HandleFunc(h.Type, func(ctx context.Context, t *asynq.Task) error {
		id, _ := asynq.GetTaskID(ctx)
		return run(ctx, h, id, t.Headers(), t.Payload())
	})
}

// options đổi tùy chọn của h sang option của asynq.
func (r *Redis) options(h Handler) []asynq.Option {
	opts := []asynq.Option{
		asynq.Queue(r.queue(h.Queue)),
		asynq.MaxRetry(orDefault(h.MaxRetry, 5)),
		asynq.Timeout(orDefault(h.Timeout, 30*time.Second)),
	}
	if h.Retention > 0 {
		opts = append(opts, asynq.Retention(h.Retention))
	}
	return opts
}

func (r *Redis) Enqueue(ctx context.Context, typ string, payload any, opts ...Option) error {
	h, ok := r.handlers[typ]
	if !ok {
		return fmt.Errorf("chưa đăng ký loại job %q", typ)
	}
	data, err := marshal(typ, payload)
	if err != nil {
		return err
	}
	aopts := r.options(h)
	if id := Collect(opts).ID; id != "" {
		aopts = append(aopts, asynq.TaskID(id))
	}
	ctx, span, headers := startSend(ctx, typ)
	// Redis treo thì request gọi Enqueue chỉ chậm thêm tối đa EnqueueTimeout rồi nhận lỗi
	ctx, cancel := context.WithTimeout(ctx, r.opts.EnqueueTimeout)
	defer cancel()
	// header của task (asynq v0.26) nằm cạnh payload trong Redis: trace context đi theo job
	// mà không đổi định dạng payload
	_, err = r.client.EnqueueContext(ctx, asynq.NewTaskWithHeaders(typ, data, headers), aopts...)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		err = fmt.Errorf("%s: %w", typ, ErrDuplicate)
	}
	endSpan(span, err)
	return err
}

func (r *Redis) Schedule(spec, typ string) error {
	h, ok := r.handlers[typ]
	if !ok {
		return fmt.Errorf("chưa đăng ký loại job %q", typ)
	}
	if _, err := ParseSchedule(spec); err != nil {
		return err
	}
	opts := r.options(h)
	if h.Unique > 0 {
		// chạy N bản API là N scheduler cùng đưa job vào lúc tới giờ: Unique giữ lại một
		// (cùng loại, cùng payload, cùng queue) cho tới khi nó chạy xong hoặc hết Unique
		opts = append(opts, asynq.Unique(h.Unique))
	}
	_, err := r.sched.Register(spec, asynq.NewTask(typ, nil), opts...)
	return err
}

func (r *Redis) Start() error {
	if err := r.server.Start(r.mux); err != nil {
		return fmt.Errorf("asynq server: %w", err)
	}
	if err := r.sched.Start(); err != nil {
		r.server.Shutdown()
		return fmt.Errorf("asynq scheduler: %w", err)
	}
	return nil
}

// Stop dừng scheduler, rồi server: ngừng lấy job mới, chờ job đang chạy tối đa ShutdownTimeout,
// job chưa xong được trả về hàng đợi để bản API khác lấy ngay. Vì vậy ShutdownTimeout phải ngắn hơn
// thời gian ctx còn lại. ctx hết hạn trước thì Stop trả lỗi mà không chờ nữa: job đang chạy dở vẫn
// không mất, nhưng phải chờ recoverer (1-2 phút sau khi hết lease) và mất một lượt chạy lại.
func (r *Redis) Stop(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		r.sched.Shutdown()
		r.server.Shutdown()
		r.client.Close() // client dùng chung rdb: không đóng kết nối
		r.rdb.Close()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("asynq chưa dừng xong: %w", ctx.Err())
	}
}

// QueueStats là số job theo trạng thái của một queue.
type QueueStats struct {
	Queue                                                  string // QueueCritical, QueueDefault, QueueLow
	Pending, Active, Scheduled, Retry, Archived, Completed int
}

// Stats đọc số job theo trạng thái của các queue của r. Queue chưa từng có job thì không có trong kết quả.
func (r *Redis) Stats() ([]QueueStats, error) {
	existing, err := r.inspect.Queues()
	if err != nil {
		return nil, err
	}
	var out []QueueStats
	for _, name := range []string{QueueCritical, QueueDefault, QueueLow} {
		if !slices.Contains(existing, r.queue(name)) {
			continue
		}
		info, err := r.inspect.GetQueueInfo(r.queue(name))
		if err != nil {
			return nil, err
		}
		out = append(out, QueueStats{Queue: name, Pending: info.Pending, Active: info.Active,
			Scheduled: info.Scheduled, Retry: info.Retry, Archived: info.Archived, Completed: info.Completed})
	}
	return out, nil
}

// Ping kiểm tra kết nối tới Redis của hàng đợi.
func (r *Redis) Ping(ctx context.Context) error {
	return r.rdb.Ping(ctx).Err()
}

// logFailure ghi log mỗi lần job lỗi: còn được chạy lại thì WARN, hết lượt (hoặc SkipRetry)
// thì ERROR, job nằm lại trong archived để xem và chạy lại bằng tay.
func (r *Redis) logFailure(ctx context.Context, t *asynq.Task, err error) {
	id, _ := asynq.GetTaskID(ctx)
	retried, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	log := r.opts.Logger.With("job", t.Type(), "id", id, "retried", retried, "err", err)
	if retried >= maxRetry || errors.Is(err, asynq.SkipRetry) {
		log.Error("job thất bại, chuyển vào archived")
		return
	}
	log.Warn("job lỗi, sẽ chạy lại")
}

// redisConnOpt cho asynq tạo client từ *redis.Options đã đọc từ URL: giữ mọi tùy chọn trong URL
// (pool, timeout, client_name...), asynq.RedisClientOpt chỉ có vài field.
type redisConnOpt struct{ o *redis.Options }

func (c redisConnOpt) MakeRedisClient() any {
	o := *c.o // bản sao: NewClient sửa field mặc định trong Options
	return redis.NewClient(&o)
}

// asynqLogger chuyển log của asynq sang slog.
type asynqLogger struct{ l *slog.Logger }

func (a asynqLogger) Debug(args ...any) { a.l.Debug(fmt.Sprint(args...)) }
func (a asynqLogger) Info(args ...any)  { a.l.Info(fmt.Sprint(args...)) }
func (a asynqLogger) Warn(args ...any)  { a.l.Warn(fmt.Sprint(args...)) }
func (a asynqLogger) Error(args ...any) { a.l.Error(fmt.Sprint(args...)) }
func (a asynqLogger) Fatal(args ...any) {
	a.l.Error(fmt.Sprint(args...))
	os.Exit(1)
}
