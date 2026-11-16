package main

import (
	"encoding"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/toidicode/taskflow/internal/cache"
	"github.com/toidicode/taskflow/internal/httpapi"
	"github.com/toidicode/taskflow/internal/ratelimit"
)

// Config là toàn bộ cấu hình của TaskFlow API.
type Config struct {
	Addr         string        // địa chỉ lắng nghe, dạng host:port
	CORSOrigin   string        // origin của frontend, rỗng là tắt CORS
	LogLevel     slog.Level    // debug, info, warn hoặc error
	LogFormat    string        // text (dễ đọc khi dev) hoặc json (cho production)
	ReadTimeout  time.Duration // đọc xong cả request
	WriteTimeout time.Duration // ghi xong response
	IdleTimeout  time.Duration // giữ kết nối keep-alive
	DatabaseURL  string        // chuỗi kết nối PostgreSQL (chứa mật khẩu), rỗng là dùng store trong bộ nhớ
	JWTSecret    string        // secret ký access token, ai có nó là ký được token của mọi user
	JWTTTL       time.Duration // thời gian sống của access token
	UploadDir    string        // thư mục lưu file upload
	Workers      int           // số worker chạy việc nền
	QueueSize    int           // số việc nền chờ tối đa
	CleanupEvery time.Duration // chu kỳ dọn file mồ côi, 0 là tắt
	RedisURL     string        // chuỗi kết nối Redis cho cache (có thể chứa mật khẩu), rỗng là không cache
	CacheTTL     time.Duration // thời gian sống của dữ liệu trong cache

	RateAuthIP     ratelimit.Limit // đăng ký, đăng nhập: mỗi IP
	RateLoginEmail ratelimit.Limit // đăng nhập: mỗi email, cộng dồn từ mọi IP
	RateUser       ratelimit.Limit // route cần đăng nhập: mỗi user

	ShutdownTimeout time.Duration // thời gian tối đa để tắt server: chờ request đang chạy và việc nền
	ShutdownDelay   time.Duration // báo /ready 503 rồi chờ chừng này trước khi ngừng nhận kết nối
	TrustedProxies  string        // IP/CIDR của reverse proxy được tin header X-Forwarded-For, cách nhau bởi dấu phẩy

	Migrate     bool // chạy migration rồi thoát, không chạy server
	HealthCheck bool // gọi /ready của server đang chạy rồi thoát với code 0 (ổn) hoặc 1
}

// minJWTSecret là độ dài tối thiểu của JWTSecret: HS256 cần khóa ít nhất 256 bit.
const minJWTSecret = 32

// defaultConfig trả về cấu hình mặc định, đủ để chạy trên máy dev.
func defaultConfig() Config {
	return Config{
		Addr:         ":8080",
		CORSOrigin:   "http://localhost:5173",
		LogLevel:     slog.LevelInfo,
		LogFormat:    "text",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
		JWTTTL:       15 * time.Minute,
		UploadDir:    "uploads",
		Workers:      4,
		QueueSize:    100,
		CleanupEvery: time.Hour,
		CacheTTL:     time.Minute,

		RateAuthIP:     ratelimit.Limit{N: 10, Period: time.Minute},
		RateLoginEmail: ratelimit.Limit{N: 5, Period: 10 * time.Minute},
		RateUser:       ratelimit.Limit{N: 600, Period: time.Minute},

		ShutdownTimeout: 8 * time.Second, // nhỏ hơn 10s mà docker stop chờ trước khi gửi SIGKILL
	}
}

// errBadFlag báo flag sai cú pháp. FlagSet đã in lỗi kèm hướng dẫn nên nơi gọi không in lại.
var errBadFlag = errors.New("flag không hợp lệ")

// loadConfig đọc cấu hình theo thứ tự ưu tiên: flag > biến môi trường > mặc định.
// lookup là hàm đọc biến môi trường (os.LookupEnv), test truyền hàm khác vào.
// Hướng dẫn (-h) và lỗi cú pháp flag được in ra output.
func loadConfig(args []string, lookup func(string) (string, bool), output io.Writer) (Config, error) {
	cfg := defaultConfig()

	// 1. biến môi trường đè lên giá trị mặc định
	env := envReader{lookup: lookup}
	env.str("TASKFLOW_ADDR", &cfg.Addr)
	env.str("TASKFLOW_CORS_ORIGIN", &cfg.CORSOrigin)
	env.level("TASKFLOW_LOG_LEVEL", &cfg.LogLevel)
	env.str("TASKFLOW_LOG_FORMAT", &cfg.LogFormat)
	env.duration("TASKFLOW_READ_TIMEOUT", &cfg.ReadTimeout)
	env.duration("TASKFLOW_WRITE_TIMEOUT", &cfg.WriteTimeout)
	env.duration("TASKFLOW_IDLE_TIMEOUT", &cfg.IdleTimeout)
	env.str("DATABASE_URL", &cfg.DatabaseURL)
	env.str("TASKFLOW_JWT_SECRET", &cfg.JWTSecret)
	env.duration("TASKFLOW_JWT_TTL", &cfg.JWTTTL)
	env.str("TASKFLOW_UPLOAD_DIR", &cfg.UploadDir)
	env.int("TASKFLOW_WORKERS", &cfg.Workers)
	env.int("TASKFLOW_QUEUE_SIZE", &cfg.QueueSize)
	env.duration("TASKFLOW_CLEANUP_INTERVAL", &cfg.CleanupEvery)
	env.str("TASKFLOW_REDIS_URL", &cfg.RedisURL)
	env.duration("TASKFLOW_CACHE_TTL", &cfg.CacheTTL)
	env.text("TASKFLOW_RATE_AUTH_IP", &cfg.RateAuthIP)
	env.text("TASKFLOW_RATE_LOGIN_EMAIL", &cfg.RateLoginEmail)
	env.text("TASKFLOW_RATE_USER", &cfg.RateUser)
	env.duration("TASKFLOW_SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout)
	env.duration("TASKFLOW_SHUTDOWN_DELAY", &cfg.ShutdownDelay)
	env.str("TASKFLOW_TRUSTED_PROXIES", &cfg.TrustedProxies)
	if err := errors.Join(env.errs...); err != nil {
		return cfg, err
	}

	// 2. flag đè lên tất cả: giá trị hiện tại của cfg làm giá trị mặc định của flag
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "địa chỉ host:port (TASKFLOW_ADDR)")
	fs.StringVar(&cfg.CORSOrigin, "cors-origin", cfg.CORSOrigin, "origin của frontend, rỗng: tắt CORS (TASKFLOW_CORS_ORIGIN)")
	fs.TextVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "debug, info, warn, error (TASKFLOW_LOG_LEVEL)")
	fs.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, "text hoặc json (TASKFLOW_LOG_FORMAT)")
	fs.DurationVar(&cfg.ReadTimeout, "read-timeout", cfg.ReadTimeout, "đọc request (TASKFLOW_READ_TIMEOUT)")
	fs.DurationVar(&cfg.WriteTimeout, "write-timeout", cfg.WriteTimeout, "ghi response (TASKFLOW_WRITE_TIMEOUT)")
	fs.DurationVar(&cfg.IdleTimeout, "idle-timeout", cfg.IdleTimeout, "keep-alive (TASKFLOW_IDLE_TIMEOUT)")
	fs.DurationVar(&cfg.JWTTTL, "jwt-ttl", cfg.JWTTTL, "thời gian sống của access token (TASKFLOW_JWT_TTL)")
	fs.StringVar(&cfg.UploadDir, "upload-dir", cfg.UploadDir, "thư mục lưu file upload (TASKFLOW_UPLOAD_DIR)")
	fs.IntVar(&cfg.Workers, "workers", cfg.Workers, "số worker chạy việc nền (TASKFLOW_WORKERS)")
	fs.IntVar(&cfg.QueueSize, "queue-size", cfg.QueueSize, "số việc nền chờ tối đa (TASKFLOW_QUEUE_SIZE)")
	fs.DurationVar(&cfg.CleanupEvery, "cleanup-interval", cfg.CleanupEvery, "chu kỳ dọn file mồ côi, 0: tắt (TASKFLOW_CLEANUP_INTERVAL)")
	fs.DurationVar(&cfg.CacheTTL, "cache-ttl", cfg.CacheTTL, "thời gian sống của dữ liệu trong cache (TASKFLOW_CACHE_TTL)")
	fs.TextVar(&cfg.RateAuthIP, "rate-auth-ip", cfg.RateAuthIP, "giới hạn đăng ký/đăng nhập mỗi IP, VD 10/1m hoặc off (TASKFLOW_RATE_AUTH_IP)")
	fs.TextVar(&cfg.RateLoginEmail, "rate-login-email", cfg.RateLoginEmail, "giới hạn đăng nhập mỗi email (TASKFLOW_RATE_LOGIN_EMAIL)")
	fs.TextVar(&cfg.RateUser, "rate-user", cfg.RateUser, "giới hạn request mỗi user đã đăng nhập (TASKFLOW_RATE_USER)")
	fs.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", cfg.ShutdownTimeout, "thời gian tối đa để tắt server (TASKFLOW_SHUTDOWN_TIMEOUT)")
	fs.DurationVar(&cfg.ShutdownDelay, "shutdown-delay", cfg.ShutdownDelay, "chờ sau khi /ready trả 503, trước khi ngừng nhận kết nối (TASKFLOW_SHUTDOWN_DELAY)")
	fs.StringVar(&cfg.TrustedProxies, "trusted-proxies", cfg.TrustedProxies, "IP/CIDR của reverse proxy, cách nhau bởi dấu phẩy (TASKFLOW_TRUSTED_PROXIES)")
	// DatabaseURL, JWTSecret, RedisURL cố ý không có flag: tham số dòng lệnh lộ ra trong ps
	// Migrate chỉ có flag: là một lệnh chạy một lần chứ không phải cấu hình lâu dài
	fs.BoolVar(&cfg.Migrate, "migrate", false, "chạy migration database rồi thoát")
	fs.BoolVar(&cfg.HealthCheck, "healthcheck", false, "kiểm tra /ready của server đang chạy rồi thoát (cho HEALTHCHECK của Docker)")
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return cfg, err
	} else if err != nil {
		return cfg, errBadFlag
	}
	if fs.NArg() > 0 {
		return cfg, fmt.Errorf("không nhận tham số %q", fs.Arg(0))
	}

	// 3. kiểm tra một lần, sai thì dừng ngay khi khởi động
	return cfg, cfg.validate()
}

// envReader đọc biến môi trường vào các field của Config và gom lỗi lại.
type envReader struct {
	lookup func(string) (string, bool)
	errs   []error
}

// str gán giá trị của biến key vào dst nếu biến được đặt, kể cả khi rỗng.
func (e *envReader) str(key string, dst *string) {
	if v, ok := e.lookup(key); ok {
		*dst = v
	}
}

func (e *envReader) int(key string, dst *int) {
	v, ok := e.lookup(key)
	if !ok {
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s=%q: cần một số nguyên", key, v))
		return
	}
	*dst = n
}

func (e *envReader) duration(key string, dst *time.Duration) {
	v, ok := e.lookup(key)
	if !ok {
		return
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s=%q: cần một khoảng thời gian như 10s, 1m30s", key, v))
		return
	}
	*dst = d
}

// text đọc biến key bằng UnmarshalText của dst, VD ratelimit.Limit.
func (e *envReader) text(key string, dst encoding.TextUnmarshaler) {
	v, ok := e.lookup(key)
	if !ok {
		return
	}
	if err := dst.UnmarshalText([]byte(v)); err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: %w", key, err))
	}
}

func (e *envReader) level(key string, dst *slog.Level) {
	v, ok := e.lookup(key)
	if !ok {
		return
	}
	if err := dst.UnmarshalText([]byte(v)); err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s=%q: chọn debug, info, warn hoặc error", key, v))
	}
}

// validate kiểm tra các giá trị mà kiểu dữ liệu không tự chặn được, trả về mọi lỗi cùng lúc.
func (c Config) validate() error {
	var errs []error
	if _, _, err := net.SplitHostPort(c.Addr); err != nil {
		errs = append(errs, fmt.Errorf("addr %q: cần dạng host:port, VD :8080", c.Addr))
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		errs = append(errs, fmt.Errorf("log-format %q: chọn text hoặc json", c.LogFormat))
	}
	if c.CORSOrigin != "" {
		u, err := url.Parse(c.CORSOrigin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			errs = append(errs, fmt.Errorf("cors-origin %q: cần dạng scheme://host[:port], không có / ở cuối", c.CORSOrigin))
		}
	}
	if c.Migrate && c.DatabaseURL == "" {
		errs = append(errs, errors.New("migrate: cần đặt DATABASE_URL"))
	}
	if !c.Migrate && !c.HealthCheck && len(c.JWTSecret) < minJWTSecret { // -migrate, -healthcheck không phát token
		errs = append(errs, fmt.Errorf("TASKFLOW_JWT_SECRET: cần ít nhất %d byte, VD tạo bằng openssl rand -base64 32", minJWTSecret))
	}
	if c.UploadDir == "" {
		errs = append(errs, errors.New("upload-dir: không được để trống"))
	}
	if c.Workers < 1 {
		errs = append(errs, fmt.Errorf("workers %d: phải lớn hơn 0", c.Workers))
	}
	if c.QueueSize < 1 {
		errs = append(errs, fmt.Errorf("queue-size %d: phải lớn hơn 0", c.QueueSize))
	}
	if c.CleanupEvery < 0 {
		errs = append(errs, fmt.Errorf("cleanup-interval %v: không được âm (0 là tắt)", c.CleanupEvery))
	}
	if _, err := httpapi.ParseTrustedProxies(c.TrustedProxies); err != nil {
		errs = append(errs, fmt.Errorf("trusted-proxies %w", err))
	}
	if c.RedisURL != "" {
		if _, err := cache.Options(c.RedisURL); err != nil { // không in URL: có thể chứa mật khẩu
			errs = append(errs, fmt.Errorf("TASKFLOW_REDIS_URL: %w", err))
		}
	}
	if c.ShutdownDelay < 0 {
		errs = append(errs, fmt.Errorf("shutdown-delay %v: không được âm", c.ShutdownDelay))
	}
	timeouts := []struct {
		name string
		d    time.Duration
	}{{"read-timeout", c.ReadTimeout}, {"write-timeout", c.WriteTimeout}, {"idle-timeout", c.IdleTimeout}, {"jwt-ttl", c.JWTTTL}, {"cache-ttl", c.CacheTTL}, {"shutdown-timeout", c.ShutdownTimeout}}
	for _, t := range timeouts {
		if t.d <= 0 {
			errs = append(errs, fmt.Errorf("%s %v: phải lớn hơn 0", t.name, t.d))
		}
	}
	return errors.Join(errs...)
}

// LogValue quyết định Config hiện ra thế nào trong log (Config cài slog.LogValuer).
// DatabaseURL, JWTSecret và RedisURL là bí mật nên chỉ cho biết đã đặt hay chưa.
func (c Config) LogValue() slog.Value {
	hidden := func(s string) string {
		if s == "" {
			return ""
		}
		return "[đã ẩn]"
	}
	return slog.GroupValue(
		slog.String("addr", c.Addr),
		slog.String("cors_origin", c.CORSOrigin),
		slog.String("log_level", c.LogLevel.String()),
		slog.String("log_format", c.LogFormat),
		slog.String("read_timeout", c.ReadTimeout.String()), // "10s" thay vì số nano giây
		slog.String("write_timeout", c.WriteTimeout.String()),
		slog.String("idle_timeout", c.IdleTimeout.String()),
		slog.String("database_url", hidden(c.DatabaseURL)),
		slog.String("jwt_secret", hidden(c.JWTSecret)),
		slog.String("jwt_ttl", c.JWTTTL.String()),
		slog.String("upload_dir", c.UploadDir),
		slog.Int("workers", c.Workers),
		slog.Int("queue_size", c.QueueSize),
		slog.String("cleanup_interval", c.CleanupEvery.String()),
		slog.String("redis_url", hidden(c.RedisURL)),
		slog.String("cache_ttl", c.CacheTTL.String()),
		slog.String("rate_auth_ip", c.RateAuthIP.String()),
		slog.String("rate_login_email", c.RateLoginEmail.String()),
		slog.String("rate_user", c.RateUser.String()),
		slog.String("shutdown_timeout", c.ShutdownTimeout.String()),
		slog.String("shutdown_delay", c.ShutdownDelay.String()),
		slog.String("trusted_proxies", c.TrustedProxies),
	)
}
