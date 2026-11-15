package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"time"
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
	Migrate      bool          // chạy migration rồi thoát, không chạy server
}

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
	// DatabaseURL cố ý không có flag: tham số dòng lệnh lộ ra trong ps
	// Migrate chỉ có flag: là một lệnh chạy một lần chứ không phải cấu hình lâu dài
	fs.BoolVar(&cfg.Migrate, "migrate", false, "chạy migration database rồi thoát")
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
	timeouts := []struct {
		name string
		d    time.Duration
	}{{"read-timeout", c.ReadTimeout}, {"write-timeout", c.WriteTimeout}, {"idle-timeout", c.IdleTimeout}}
	for _, t := range timeouts {
		if t.d <= 0 {
			errs = append(errs, fmt.Errorf("%s %v: phải lớn hơn 0", t.name, t.d))
		}
	}
	return errors.Join(errs...)
}

// LogValue quyết định Config hiện ra thế nào trong log (Config cài slog.LogValuer).
// DatabaseURL chứa mật khẩu nên chỉ cho biết đã đặt hay chưa.
func (c Config) LogValue() slog.Value {
	db := ""
	if c.DatabaseURL != "" {
		db = "[đã ẩn]"
	}
	return slog.GroupValue(
		slog.String("addr", c.Addr),
		slog.String("cors_origin", c.CORSOrigin),
		slog.String("log_level", c.LogLevel.String()),
		slog.String("log_format", c.LogFormat),
		slog.String("read_timeout", c.ReadTimeout.String()), // "10s" thay vì số nano giây
		slog.String("write_timeout", c.WriteTimeout.String()),
		slog.String("idle_timeout", c.IdleTimeout.String()),
		slog.String("database_url", db),
	)
}
