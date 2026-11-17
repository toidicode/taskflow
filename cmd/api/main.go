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
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/postgres"
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

	a, err := openApp(ctx, cfg)
	if err != nil {
		return fmt.Errorf("khởi tạo store: %w", err)
	}
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

	// việc nền chạy trong cùng process, serve dừng nó sau khi HTTP server đã tắt
	a.jobs = jobs.New(jobs.Options{Workers: cfg.Workers, QueueSize: cfg.QueueSize})
	a.notifier = notify.Log{Logger: slog.Default()} // chưa có email: thông báo chỉ ghi ra log
	a.jobs.Start()
	if cfg.CleanupEvery > 0 {
		a.jobs.Enqueue(cleanupJob(a)) // dọn một lần lúc khởi động, sau đó theo chu kỳ
		a.jobs.Every(cfg.CleanupEvery, cleanupJob(a))
	}

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
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
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
