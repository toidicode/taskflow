package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// testJWTSecret là secret đủ 32 byte cho các test cần cấu hình hợp lệ.
const testJWTSecret = "secret-chi-dung-trong-test-0123456789"

// envMap giả lập biến môi trường cho loadConfig.
func envMap(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		env   map[string]string
		check func(c Config) bool
	}{
		{"mặc định", nil, map[string]string{"TASKFLOW_JWT_SECRET": testJWTSecret}, func(c Config) bool {
			want := defaultConfig()
			want.JWTSecret = testJWTSecret // secret không có giá trị mặc định
			return c == want && c.JWTTTL == 15*time.Minute
		}},
		{"env đè mặc định", nil, map[string]string{
			"TASKFLOW_ADDR": ":9000", "TASKFLOW_LOG_LEVEL": "debug", "TASKFLOW_LOG_FORMAT": "json",
			"TASKFLOW_READ_TIMEOUT": "3s", "DATABASE_URL": "postgres://u:p@db/taskflow",
			"TASKFLOW_JWT_SECRET": testJWTSecret, "TASKFLOW_JWT_TTL": "5m", "TASKFLOW_UPLOAD_DIR": "/var/lib/taskflow",
		}, func(c Config) bool {
			return c.Addr == ":9000" && c.LogLevel == slog.LevelDebug && c.LogFormat == "json" &&
				c.ReadTimeout == 3*time.Second && c.DatabaseURL == "postgres://u:p@db/taskflow" &&
				c.JWTTTL == 5*time.Minute && c.UploadDir == "/var/lib/taskflow"
		}},
		{"flag đè env", []string{"-addr", ":7000", "-log-level", "warn", "-jwt-ttl", "1h"},
			map[string]string{"TASKFLOW_ADDR": ":9000", "TASKFLOW_LOG_LEVEL": "debug", "TASKFLOW_JWT_SECRET": testJWTSecret, "TASKFLOW_JWT_TTL": "5m"},
			func(c Config) bool { return c.Addr == ":7000" && c.LogLevel == slog.LevelWarn && c.JWTTTL == time.Hour }},
		{"env rỗng tắt CORS", nil, map[string]string{"TASKFLOW_CORS_ORIGIN": "", "TASKFLOW_JWT_SECRET": testJWTSecret},
			func(c Config) bool { return c.CORSOrigin == "" }},
		{"việc nền", []string{"-cleanup-interval", "0"},
			map[string]string{"TASKFLOW_WORKERS": "8", "TASKFLOW_QUEUE_SIZE": "500", "TASKFLOW_JWT_SECRET": testJWTSecret},
			func(c Config) bool { return c.Workers == 8 && c.QueueSize == 500 && c.CleanupEvery == 0 }},
		{"-migrate không cần secret", []string{"-migrate"}, map[string]string{"DATABASE_URL": "postgres://u:p@db/taskflow"},
			func(c Config) bool { return c.Migrate }},
		{"tắt server", []string{"-shutdown-timeout", "20s"},
			map[string]string{"TASKFLOW_SHUTDOWN_TIMEOUT": "5s", "TASKFLOW_SHUTDOWN_DELAY": "3s", "TASKFLOW_JWT_SECRET": testJWTSecret},
			func(c Config) bool { return c.ShutdownTimeout == 20*time.Second && c.ShutdownDelay == 3*time.Second }},
		{"-healthcheck không cần secret", []string{"-healthcheck"}, map[string]string{"TASKFLOW_ADDR": ":9000"},
			func(c Config) bool { return c.HealthCheck && c.Addr == ":9000" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := loadConfig(tt.args, envMap(tt.env), io.Discard)
			if err != nil {
				t.Fatalf("lỗi: %v", err)
			}
			if !tt.check(c) {
				t.Errorf("cấu hình sai: %+v", c)
			}
		})
	}
}

func TestLoadConfigErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want []string // các đoạn phải có trong thông báo lỗi
	}{
		{"env sai kiểu", nil, map[string]string{"TASKFLOW_READ_TIMEOUT": "10", "TASKFLOW_LOG_LEVEL": "verbose"},
			[]string{`TASKFLOW_READ_TIMEOUT="10"`, `TASKFLOW_LOG_LEVEL="verbose"`}},
		{"giá trị sai", []string{"-addr", "8080", "-log-format", "xml", "-cors-origin", "http://localhost:5173/", "-idle-timeout", "0s", "-jwt-ttl", "0s"},
			nil, []string{`addr "8080"`, `log-format "xml"`, `cors-origin "http://localhost:5173/"`, "idle-timeout 0s", "jwt-ttl 0s"}},
		{"thiếu secret", nil, nil, []string{"TASKFLOW_JWT_SECRET: cần ít nhất 32 byte"}},
		{"secret quá ngắn", nil, map[string]string{"TASKFLOW_JWT_SECRET": "secret"}, []string{"TASKFLOW_JWT_SECRET: cần ít nhất 32 byte"}},
		{"upload-dir rỗng", []string{"-upload-dir", ""}, map[string]string{"TASKFLOW_JWT_SECRET": testJWTSecret},
			[]string{"upload-dir: không được để trống"}},
		{"việc nền sai", []string{"-workers", "0", "-cleanup-interval", "-1m"},
			map[string]string{"TASKFLOW_QUEUE_SIZE": "nhiều", "TASKFLOW_JWT_SECRET": testJWTSecret},
			[]string{`TASKFLOW_QUEUE_SIZE="nhiều": cần một số nguyên`}},
		{"workers và cleanup sai", []string{"-workers", "0", "-cleanup-interval", "-1m"},
			map[string]string{"TASKFLOW_JWT_SECRET": testJWTSecret},
			[]string{"workers 0: phải lớn hơn 0", "cleanup-interval -1m0s: không được âm"}},
		{"thừa tham số", []string{"serve"}, nil, []string{`không nhận tham số "serve"`}},
		{"-migrate thiếu DATABASE_URL", []string{"-migrate"}, nil, []string{"migrate: cần đặt DATABASE_URL"}},
		{"tắt server sai", []string{"-shutdown-timeout", "0s", "-shutdown-delay", "-1s"},
			map[string]string{"TASKFLOW_JWT_SECRET": testJWTSecret},
			[]string{"shutdown-timeout 0s: phải lớn hơn 0", "shutdown-delay -1s: không được âm"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(tt.args, envMap(tt.env), io.Discard)
			if err == nil {
				t.Fatal("không có lỗi")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("lỗi %q thiếu %q", err, w)
				}
			}
		})
	}

	// flag sai cú pháp: FlagSet in lỗi ra output, loadConfig trả errBadFlag
	var out bytes.Buffer
	_, err := loadConfig([]string{"-write-timeout", "abc"}, envMap(nil), &out)
	if !errors.Is(err, errBadFlag) || !strings.Contains(out.String(), `invalid value "abc" for flag -write-timeout`) {
		t.Errorf("flag sai: err = %v, output = %q", err, out.String())
	}

	// -h không phải lỗi cấu hình: in hướng dẫn, main thoát với code 0
	out.Reset()
	_, err = loadConfig([]string{"-h"}, envMap(nil), &out)
	if !errors.Is(err, flag.ErrHelp) || !strings.Contains(out.String(), "TASKFLOW_LOG_LEVEL") {
		t.Errorf("-h: err = %v, output = %q", err, out.String())
	}
}

func TestConfigLogValue(t *testing.T) {
	cfg := defaultConfig()
	cfg.DatabaseURL = "postgres://taskflow:mat-khau-bi-mat@db:5432/taskflow"
	cfg.JWTSecret = "jwt-secret-bi-mat-jwt-secret-bi-mat"

	var buf bytes.Buffer
	newLogger(&buf, Config{LogFormat: "json"}).Info("đã đọc cấu hình", "config", cfg)

	if strings.Contains(buf.String(), "bi-mat") {
		t.Errorf("log lộ mật khẩu: %s", buf.String())
	}
	for _, want := range []string{`"addr":":8080"`, `"database_url":"[đã ẩn]"`, `"jwt_secret":"[đã ẩn]"`, `"jwt_ttl":"15m0s"`, `"upload_dir":"uploads"`, `"workers":4`, `"cleanup_interval":"1h0m0s"`, `"shutdown_timeout":"8s"`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log thiếu %s: %s", want, buf.String())
		}
	}
}

func TestNewLoggerLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, Config{LogLevel: slog.LevelWarn, LogFormat: "text"})
	logger.Info("bị bỏ qua")
	logger.Warn("được ghi", "n", 1)

	got := buf.String()
	if strings.Contains(got, "bị bỏ qua") || !strings.Contains(got, `level=WARN msg="được ghi" n=1`) {
		t.Errorf("log = %q", got)
	}
}
