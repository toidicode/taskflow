package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"net/netip"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/postgres"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/realtime"
	"github.com/toidicode/taskflow/internal/storage"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// stores gom các store mà test tạo ra, và có thể thay (VD s.tasks = brokenTasks{}),
// trước khi dựng handler bằng newMux hoặc newAPI. Đây là bản thu nhỏ của app trong cmd/api.
type stores struct {
	projects    project.Store
	tasks       task.Store
	users       user.Store
	attachments attachment.Store
	files       storage.Storage
	jobs        *jobs.Dispatcher
	notifier    notify.Notifier
	db          *sql.DB // nil với store trong bộ nhớ
	ready       *Readiness
	hub         *realtime.Hub
	tickets     realtime.Tickets
}

// memoryStores tạo các store trong bộ nhớ, chưa có dữ liệu nào.
func memoryStores(files storage.Storage) stores {
	projects, tasks := project.NewMemoryStore(), task.NewMemoryStore()
	tasks.SetProjectFunc(func(id int) (project.Project, bool) { // để lọc task theo quyền (Filter.VisibleTo)
		p, err := projects.Get(context.Background(), id)
		return p, err == nil
	})
	return stores{projects: projects, tasks: tasks, users: user.NewMemoryStore(),
		attachments: attachment.NewMemoryStore(), files: files, ready: NewReadiness(nil),
		hub: realtime.NewHub(realtime.Options{}), tickets: realtime.NewMemoryTickets()}
}

// dbStores tạo các store lưu trong PostgreSQL qua db.
func dbStores(db *sql.DB, files storage.Storage) stores {
	return stores{
		projects:    postgres.NewProjectStore(db),
		tasks:       postgres.NewTaskStore(db),
		users:       postgres.NewUserStore(db),
		attachments: postgres.NewAttachmentStore(db),
		files:       files,
		db:          db,
		ready:       NewReadiness(db),
		hub:         realtime.NewHub(realtime.Options{}),
		tickets:     realtime.NewMemoryTickets(),
	}
}

// deps nối các store thành service, giống app.handler trong cmd/api.
func (s stores) deps(tokens *auth.Tokens) Deps {
	tasks := task.NewService(s.tasks, s.projects, s.users,
		attachment.Files{Store: s.attachments, Storage: s.files}, s.jobs, s.notifier, s.hub)
	return Deps{
		Tasks:       tasks,
		Projects:    project.NewService(s.projects, tasks),
		Users:       s.users,
		Attachments: s.attachments,
		Files:       s.files,
		Tokens:      tokens,
		Ready:       s.ready,
		Realtime:    Realtime{Hub: s.hub, Tickets: s.tickets},
	}
}

// newMux tạo engine chỉ có các route của API (chưa có middleware dùng chung) từ s.
func newMux(s stores, tokens *auth.Tokens) *gin.Engine {
	r := newEngine(nil)
	routes(r, s.deps(tokens))
	return r
}

// newAPI tạo handler của cả API từ s, như New.
func newAPI(s stores, tokens *auth.Tokens, corsOrigin string, trusted []netip.Prefix) http.Handler {
	return New(s.deps(tokens), corsOrigin, trusted)
}

// seed thêm vài project và task mẫu, giống dữ liệu mẫu khi chạy server không có database.
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
