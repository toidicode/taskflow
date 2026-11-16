// Command api là REST API của TaskFlow.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/postgres"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/storage"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
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

	if cfg.Migrate {
		if err := migrate(context.Background(), cfg.DatabaseURL); err != nil {
			slog.Error("migration", "err", err)
			os.Exit(1)
		}
		return
	}

	s, err := openStores(context.Background(), cfg)
	if err != nil {
		slog.Error("khởi tạo store", "err", err)
		os.Exit(1)
	}

	// việc nền chạy trong cùng process; Bài 38 gọi s.jobs.Stop(ctx) khi server tắt
	s.jobs = jobs.New(jobs.Options{Workers: cfg.Workers, QueueSize: cfg.QueueSize})
	s.notifier = notify.Log{Logger: slog.Default()} // chưa có email: thông báo chỉ ghi ra log
	s.jobs.Start()
	if cfg.CleanupEvery > 0 {
		s.jobs.Enqueue(cleanupJob(s)) // dọn một lần lúc khởi động, sau đó theo chu kỳ
		s.jobs.Every(cfg.CleanupEvery, cleanupJob(s))
	}

	tokens := auth.NewTokens([]byte(cfg.JWTSecret), cfg.JWTTTL)
	srv := newServer(cfg, newAPI(s, tokens, cfg.CORSOrigin))
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

// openStores dùng PostgreSQL khi có DATABASE_URL. Không có thì dùng store trong bộ nhớ
// kèm dữ liệu mẫu, đủ để chạy thử mà chưa cần database.
func openStores(ctx context.Context, cfg Config) (stores, error) {
	files, err := storage.NewDisk(cfg.UploadDir) // nội dung file luôn nằm trên đĩa, kể cả khi không có database
	if err != nil {
		return stores{}, fmt.Errorf("thư mục upload: %w", err)
	}
	if cfg.DatabaseURL == "" {
		slog.Warn("chưa đặt DATABASE_URL, dữ liệu chỉ nằm trong bộ nhớ")
		s := memoryStores(files)
		return s, seed(ctx, s.projects, s.tasks)
	}
	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return stores{}, fmt.Errorf("kết nối PostgreSQL: %w", err)
	}
	slog.Info("đã kết nối PostgreSQL")

	// schema cũ hơn code thì mọi truy vấn đều có thể lỗi: dừng ngay thay vì trả 500
	m, err := postgres.NewMigrator(db)
	if err != nil {
		db.Close()
		return stores{}, err
	}
	pending, err := m.HasPending(ctx)
	if err != nil {
		db.Close()
		return stores{}, fmt.Errorf("kiểm tra migration: %w", err)
	}
	if pending {
		db.Close()
		return stores{}, errors.New("database còn migration chưa chạy, hãy chạy với -migrate trước")
	}
	return dbStores(db, files), nil
}

// memoryStores tạo các store trong bộ nhớ, chưa có dữ liệu nào.
func memoryStores(files storage.Storage) stores {
	projects, tasks := project.NewMemoryStore(), task.NewMemoryStore()
	tasks.SetProjectFunc(projectLookup(projects)) // để lọc task theo quyền (Filter.VisibleTo)
	return stores{projects: projects, tasks: tasks, users: user.NewMemoryStore(),
		attachments: attachment.NewMemoryStore(), files: files}
}

// dbStores tạo các store lưu trong PostgreSQL qua db.
func dbStores(db *sql.DB, files storage.Storage) stores {
	return stores{
		projects:    postgres.NewProjectStore(db),
		tasks:       postgres.NewTaskStore(db),
		users:       postgres.NewUserStore(db),
		attachments: postgres.NewAttachmentStore(db),
		files:       files,
	}
}

// projectLookup cho task.MemoryStore đọc project trong store projects.
func projectLookup(projects project.Store) task.ProjectFunc {
	return func(id int) (project.Project, bool) {
		p, err := projects.Get(context.Background(), id)
		return p, err == nil
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

// seed thêm vài project và task mẫu cho store trong bộ nhớ.
func seed(ctx context.Context, projects project.Store, tasks task.Store) error {
	for _, name := range []string{"TaskFlow API", "Website giới thiệu"} {
		if err := projects.Add(ctx, project.New(name, 1)); err != nil {
			return err
		}
	}

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
		t.SetStatus(x.status)
		if err := tasks.Add(ctx, t); err != nil {
			return err
		}
	}
	return nil
}
