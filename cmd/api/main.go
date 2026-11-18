// Command api là REST API của TaskFlow.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/cache"
	"github.com/toidicode/taskflow/internal/httpapi"
	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/metrics"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/notifyrpc"
	"github.com/toidicode/taskflow/internal/postgres"
	"github.com/toidicode/taskflow/internal/telemetry"
)

func main() {
	cfg, err := loadConfig(os.Args[1:], os.LookupEnv, os.Stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		os.Exit(0) // -h: đã in hướng dẫn
	case errors.Is(err, errBadFlag):
		os.Exit(2) // đã in lỗi và hướng dẫn
	case err != nil:
		fmt.Fprintf(os.Stderr, "cấu hình không hợp lệ:\n%v\n", err)
		os.Exit(2)
	}

	if cfg.HealthCheck {
		os.Exit(healthcheck(cfg.Addr))
	}

	logger := newLogger(os.Stderr, cfg)
	slog.SetDefault(logger) // các hàm slog.Info, slog.Error... và package log đều đi qua logger này
	slog.Info("đã đọc cấu hình", "config", cfg)

	if cfg.Migrate {
		if err := migrate(context.Background(), cfg.DatabaseURL); err != nil {
			slog.Error("migration", "err", err)
			os.Exit(1)
		}
		return
	}

	// Gin mặc định ở chế độ debug: in từng route và cảnh báo ra stdout lúc khởi động.
	// TaskFlow chạy chế độ release, trừ khi đặt GIN_MODE (VD GIN_MODE=debug để xem route).
	if _, ok := os.LookupEnv(gin.EnvGinMode); !ok {
		gin.SetMode(gin.ReleaseMode)
	}
	if err := run(cfg); err != nil {
		slog.Error("server dừng vì lỗi", "err", err)
		os.Exit(1)
	}
	slog.Info("đã tắt server")
}

// run khởi động API, chờ SIGINT (Ctrl+C) hoặc SIGTERM (docker stop) rồi tắt server an toàn.
// Các defer chạy theo thứ tự ngược: database được đóng sau cùng, khi không còn ai dùng nó.
func run(cfg Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// tracing chỉ bật khi có OTEL_EXPORTER_OTLP_ENDPOINT. defer đầu tiên nên chạy sau cùng:
	// span của lúc tắt server cũng được gửi đi
	shutdownTracing, err := telemetry.Setup(ctx, "taskflow-api", buildVersion())
	if err != nil {
		return fmt.Errorf("tracing: %w", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(sctx); err != nil {
			slog.Warn("không gửi hết được trace", "err", err)
		}
	}()

	a, err := openApp(ctx, cfg)
	if err != nil {
		return fmt.Errorf("khởi tạo store: %w", err)
	}
	a.metrics = metrics.New()
	if a.db != nil {
		defer func() {
			a.db.Close()
			slog.Info("đã đóng kết nối database")
		}()
	}

	var rc *cache.Redis // nil khi không có Redis
	if cfg.RedisURL != "" {
		rc, err = connectRedis(ctx, cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		cached := useCache(a, rc, cfg.CacheTTL)
		a.metrics.Cache("project", func() (int64, int64, int64) {
			st := cached.Stats()
			return st.Hits, st.Misses, st.Errors
		})
		defer func() { // chạy trước khi đóng database: defer chạy theo thứ tự ngược
			st := cached.Stats()
			slog.Info("thống kê cache project", "hits", st.Hits, "misses", st.Misses, "errors", st.Errors)
			rc.Close()
		}()
		receive := a.useRedisRealtime(rc, "taskflow:realtime:") // event đi qua Redis Pub/Sub tới mọi bản API
		go receive(ctx)
		slog.Info("bật cache project, rate limit và realtime qua Redis", "cache_ttl", cfg.CacheTTL)
	} else {
		slog.Info("chưa đặt TASKFLOW_REDIS_URL: không cache, rate limit đếm trong bộ nhớ")
	}
	a.rateLimit = rateLimit(rc, cfg)

	ln, err := net.Listen("tcp", cfg.Addr) // mở cổng trước: cổng đã bị chiếm thì báo lỗi ngay
	if err != nil {
		return err
	}

	// việc nền chạy trong cùng process (worker và scheduler), serve dừng nó sau khi HTTP server đã tắt.
	// Có Redis thì job nằm trong Redis: không mất khi process dừng, mọi bản API dùng chung.
	if cfg.RedisURL != "" {
		// asynq chờ job đang chạy tối đa nửa thời gian tắt: lúc nó dừng, HTTP server đã dùng một phần,
		// phần còn lại đủ để trả job chưa xong về hàng đợi trước khi hết TASKFLOW_SHUTDOWN_TIMEOUT
		q, err := jobs.NewRedis(cfg.RedisURL, jobs.RedisOptions{
			Concurrency: cfg.Workers, ShutdownTimeout: cfg.ShutdownTimeout / 2})
		if err != nil {
			return fmt.Errorf("hàng đợi: %w", err)
		}
		a.queue = q
		a.metrics.Queue(q.Stats) // số job pending, retry, archived... của từng queue
		slog.Info("hàng đợi job trong Redis (asynq)")
	} else {
		a.queue = jobs.NewLocal(jobs.Options{Workers: cfg.Workers, QueueSize: cfg.QueueSize})
		slog.Warn("chưa đặt TASKFLOW_REDIS_URL: hàng đợi job trong bộ nhớ, mất job khi tắt server")
	}
	if cfg.NotifierAddr != "" {
		// một ClientConn cho cả process, dùng chung cho mọi worker; đóng sau khi serve đã dừng hàng đợi
		conn, err := notifyrpc.Dial(cfg.NotifierAddr, cfg.NotifierToken)
		if err != nil {
			return fmt.Errorf("notifier: %w", err)
		}
		defer conn.Close()
		a.notifier = notifyrpc.NewNotifier(conn)
		slog.Info("gửi thông báo qua notifier gRPC", "addr", cfg.NotifierAddr)
	} else {
		a.notifier = notify.Log{Logger: slog.Default()} // không có notifier: thông báo chỉ ghi ra log
	}
	if err := a.startJobs(cfg.CleanupEvery, cfg.RemindAt); err != nil {
		return fmt.Errorf("hàng đợi: %w", err)
	}

	if cfg.MetricsAddr != "" {
		stopMetrics, err := serveMetrics(cfg.MetricsAddr, a.metrics.Handler())
		if err != nil {
			return fmt.Errorf("metrics: %w", err)
		}
		defer stopMetrics() // sau khi serve trả về: Prometheus vẫn lấy được metric trong lúc tắt
	}

	a.webDir = cfg.WebDir
	tokens := auth.NewTokens([]byte(cfg.JWTSecret), cfg.JWTTTL)
	trusted, _ := httpapi.ParseTrustedProxies(cfg.TrustedProxies) // đã kiểm tra trong loadConfig
	srv := newServer(cfg, a.handler(tokens, cfg.CORSOrigin, trusted))
	slog.Info("TaskFlow API lắng nghe", "addr", cfg.Addr, "version", buildVersion())

	go func() {
		<-ctx.Done()
		stop() // từ giờ SIGINT/SIGTERM lại có tác dụng mặc định: tín hiệu thứ hai tắt ngay process
	}()
	return serve(ctx, srv, ln, a, cfg.ShutdownDelay, cfg.ShutdownTimeout)
}

// newLogger tạo logger ghi ra w theo định dạng và mức log trong cfg.
func newLogger(w io.Writer, cfg Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	var h slog.Handler = slog.NewTextHandler(w, opts)
	if cfg.LogFormat == "json" {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(telemetry.LogHandler{Handler: h}) // log ghi với ctx của một span có thêm trace_id, span_id
}

// newServer tạo http.Server cho handler h với các timeout trong cfg.
func newServer(cfg Config, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second, // đọc xong header
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		// lỗi nội bộ của net/http (VD TLS handshake) cũng ghi qua slog
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}
}

// migrate chạy mọi migration chưa chạy trên database ở url và ghi log từng migration.
func migrate(ctx context.Context, url string) error {
	db, err := postgres.Open(ctx, url)
	if err != nil {
		return fmt.Errorf("kết nối PostgreSQL: %w", err)
	}
	defer db.Close()

	m, err := postgres.NewMigrator(db)
	if err != nil {
		return err
	}
	results, err := m.Up(ctx) // chỉ chạy các migration chưa có trong goose_db_version
	if err != nil {
		return err // migration hỏng thì err là *goose.PartialError, nêu rõ file nào
	}
	for _, r := range results {
		slog.Info("đã chạy migration", "file", r.Source.Path, "duration", r.Duration)
	}
	version, err := m.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	slog.Info("database đã ở phiên bản mới nhất", "version", version)
	return nil
}
