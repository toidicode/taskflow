package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/cache"
	"github.com/toidicode/taskflow/internal/httpapi"
	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/postgres"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/ratelimit"
	"github.com/toidicode/taskflow/internal/realtime"
	"github.com/toidicode/taskflow/internal/storage"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// app gom mọi thành phần của TaskFlow API. run tạo nó một lần lúc khởi động rồi nối các thành
// phần với nhau bằng tay (dependency injection thủ công): không cần framework, đọc code là thấy
// cái gì phụ thuộc vào cái gì.
type app struct {
	db          *sql.DB // nil khi dùng store trong bộ nhớ; run đóng khi tắt server
	projects    project.Store
	tasks       task.Store
	users       user.Store
	attachments attachment.Store   // thông tin file đính kèm
	files       storage.Storage    // nội dung file đính kèm
	jobs        *jobs.Dispatcher   // việc nền
	notifier    notify.Notifier    // gửi thông báo (trong job)
	ready       *httpapi.Readiness // trả lời /ready, serve bật trạng thái đang tắt
	rateLimit   httpapi.RateLimit  // giới hạn số request, Limiter nil là tắt
	hub         *realtime.Hub      // kết nối WebSocket của bản API này
	events      task.Publisher     // nơi task.Service gửi event: hub, hoặc Redis khi chạy nhiều bản
	tickets     realtime.Tickets   // vé mở WebSocket
}

// handler tạo các service từ store rồi tạo handler HTTP của cả API.
func (a *app) handler(tokens *auth.Tokens, corsOrigin string, trusted []netip.Prefix) http.Handler {
	tasks := task.NewService(a.tasks, a.projects, a.users,
		attachment.Files{Store: a.attachments, Storage: a.files}, a.jobs, a.notifier, a.events)
	projects := project.NewService(a.projects, tasks) // *task.Service có HasTasks: thỏa project.Tasks
	return httpapi.New(httpapi.Deps{
		Tasks:       tasks,
		Projects:    projects,
		Users:       a.users,
		Attachments: a.attachments,
		Files:       a.files,
		Tokens:      tokens,
		Ready:       a.ready,
		RateLimit:   a.rateLimit,
		Realtime:    httpapi.Realtime{Hub: a.hub, Tickets: a.tickets},
	}, corsOrigin, trusted)
}

// openApp dùng PostgreSQL khi có DATABASE_URL. Không có thì dùng store trong bộ nhớ
// kèm dữ liệu mẫu, đủ để chạy thử mà chưa cần database.
func openApp(ctx context.Context, cfg Config) (*app, error) {
	files, err := storage.NewDisk(cfg.UploadDir) // nội dung file luôn nằm trên đĩa, kể cả khi không có database
	if err != nil {
		return nil, fmt.Errorf("thư mục upload: %w", err)
	}
	if cfg.DatabaseURL == "" {
		slog.Warn("chưa đặt DATABASE_URL, dữ liệu chỉ nằm trong bộ nhớ")
		a := memoryApp(files)
		return a, seed(ctx, a.projects, a.tasks)
	}
	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("kết nối PostgreSQL: %w", err)
	}
	slog.Info("đã kết nối PostgreSQL")

	// schema cũ hơn code thì mọi truy vấn đều có thể lỗi: dừng ngay thay vì trả 500
	m, err := postgres.NewMigrator(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	pending, err := m.HasPending(ctx)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("kiểm tra migration: %w", err)
	}
	if pending {
		db.Close()
		return nil, errors.New("database còn migration chưa chạy, hãy chạy với -migrate trước")
	}
	return dbApp(db, files), nil
}

// memoryApp tạo app với các store trong bộ nhớ, chưa có dữ liệu nào.
func memoryApp(files storage.Storage) *app {
	projects, tasks := project.NewMemoryStore(), task.NewMemoryStore()
	tasks.SetProjectFunc(projectLookup(projects)) // để lọc task theo quyền (Filter.VisibleTo)
	a := &app{projects: projects, tasks: tasks, users: user.NewMemoryStore(),
		attachments: attachment.NewMemoryStore(), files: files, ready: httpapi.NewReadiness(nil)}
	a.useLocalRealtime()
	return a
}

// dbApp tạo app với các store lưu trong PostgreSQL qua db.
func dbApp(db *sql.DB, files storage.Storage) *app {
	a := &app{
		projects:    postgres.NewProjectStore(db),
		tasks:       postgres.NewTaskStore(db),
		users:       postgres.NewUserStore(db),
		attachments: postgres.NewAttachmentStore(db),
		files:       files,
		db:          db,
		ready:       httpapi.NewReadiness(db),
	}
	a.useLocalRealtime()
	return a
}

// useLocalRealtime cho a một Hub nhận event thẳng từ task.Service và vé trong bộ nhớ: đủ khi chạy một bản API.
func (a *app) useLocalRealtime() {
	a.hub = realtime.NewHub(realtime.Options{})
	a.events = a.hub
	a.tickets = realtime.NewMemoryTickets()
}

// useRedisRealtime chuyển event và vé sang Redis để nhiều bản API dùng chung. Trả về hàm nhận event
// từ Redis, chạy trong goroutine riêng cho tới khi ctx bị hủy.
func (a *app) useRedisRealtime(rc *cache.Redis, prefix string) func(ctx context.Context) {
	broker := realtime.NewRedis(rc.Client(), prefix+"events", a.hub)
	a.events = broker
	a.tickets = realtime.NewRedisTickets(rc.Client(), prefix+"ticket:")
	return broker.Run
}

// connectRedis tạo client Redis cho url, dùng chung cho cache và rate limit. Redis chưa chạy lúc
// khởi động thì chỉ cảnh báo: go-redis tự kết nối khi Redis lên lại, trong lúc đó cache đọc thẳng
// database còn rate limit đếm trong bộ nhớ (xem httpapi.RateLimit).
func connectRedis(ctx context.Context, url string) (*cache.Redis, error) {
	rc, err := cache.NewRedis(url)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := rc.Ping(pctx); err != nil {
		slog.Warn("chưa kết nối được Redis", "err", err)
	} else {
		slog.Info("đã kết nối Redis")
	}
	return rc, nil
}

// useCache bọc store project của a bằng cache trong Redis.
func useCache(a *app, rc *cache.Redis, ttl time.Duration) *project.CachedStore {
	cached := project.NewCachedStore(a.projects, rc, project.CacheOptions{TTL: ttl})
	a.projects = cached // mọi nơi nhận a.projects (project.Service, task.Service) đi qua cache
	return cached
}

// rateLimit chọn nơi đếm request: Redis khi có, để mọi bản API đếm chung; không thì bộ nhớ.
// Bộ nhớ cũng là Fallback cho route đăng nhập, đăng ký khi Redis lỗi.
func rateLimit(rc *cache.Redis, cfg Config) httpapi.RateLimit {
	mem := ratelimit.NewMemory()
	rl := httpapi.RateLimit{Limiter: mem, Fallback: mem, Limits: httpapi.Limits{
		AuthIP: cfg.RateAuthIP, LoginEmail: cfg.RateLoginEmail, User: cfg.RateUser,
	}, EmailSecret: emailSecret(cfg.JWTSecret)}
	if rc != nil {
		rl.Limiter = ratelimit.NewRedis(rc.Client(), "taskflow:ratelimit:")
	}
	return rl
}

// emailSecret suy ra khóa HMAC cho key email từ TASKFLOW_JWT_SECRET: không cần thêm một bí mật
// để cấu hình, và khóa này khác hẳn khóa ký token. Đổi JWT secret thì mọi bucket email về đầy.
func emailSecret(jwtSecret string) []byte {
	mac := hmac.New(sha256.New, []byte(jwtSecret))
	mac.Write([]byte("taskflow:ratelimit:email"))
	return mac.Sum(nil)
}

// projectLookup cho task.MemoryStore đọc project trong store projects.
func projectLookup(projects project.Store) task.ProjectFunc {
	return func(id int) (project.Project, bool) {
		p, err := projects.Get(context.Background(), id)
		return p, err == nil
	}
}

// orphanMinAge: file mồ côi phải cũ hơn chừng này mới bị xóa, lớn hơn nhiều so với
// thời gian tối đa của một lần upload (5 phút).
const orphanMinAge = time.Hour

// cleanupJob là job xóa file mồ côi trong thư mục upload (file không có hàng attachments nào).
func cleanupJob(a *app) jobs.Job {
	return jobs.Job{Name: "clean-orphan-files", Run: func(ctx context.Context) error {
		n, err := attachment.CleanOrphans(ctx, a.attachments, a.files, time.Now().Add(-orphanMinAge))
		slog.Info("đã dọn file mồ côi", "removed", n)
		return err
	}}
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
