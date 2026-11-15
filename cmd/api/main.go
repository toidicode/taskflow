// Command api là REST API của TaskFlow.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
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

	logger := newLogger(os.Stderr, cfg)
	slog.SetDefault(logger) // các hàm slog.Info, slog.Error... và package log đều đi qua logger này
	slog.Info("đã đọc cấu hình", "config", cfg)

	projects := project.NewMemoryStore()
	tasks := task.NewMemoryStore()
	if err := seed(projects, tasks); err != nil {
		slog.Error("tạo dữ liệu mẫu", "err", err)
		os.Exit(1)
	}

	srv := newServer(cfg, newAPI(projects, tasks, cfg.CORSOrigin))
	slog.Info("TaskFlow API lắng nghe", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server dừng", "err", err)
		os.Exit(1)
	}
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

// seed thêm vài project và task mẫu. Từ Bài 29 dữ liệu sẽ nằm trong PostgreSQL.
func seed(projects *project.MemoryStore, tasks task.Store) error {
	projects.Add(project.New("TaskFlow API", 1)) // project #1
	projects.Add(project.New("Website giới thiệu", 1))

	samples := []struct {
		projectID int
		title     string
		status    task.Status
	}{
		{1, "Thiết kế database", task.StatusDone},
		{1, "Viết API danh sách task", task.StatusDoing},
		{1, "Viết test cho API", task.StatusTodo},
		{2, "Viết tài liệu", task.StatusTodo},
	}
	for _, x := range samples {
		t, err := task.New(x.projectID, x.title)
		if err != nil {
			return err
		}
		switch x.status {
		case task.StatusDoing:
			t.Start()
		case task.StatusDone:
			t.Complete()
		}
		tasks.Add(t)
	}
	return nil
}
