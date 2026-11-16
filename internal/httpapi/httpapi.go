// Package httpapi là tầng HTTP của TaskFlow API: route, middleware, đọc request và ghi response.
// Luật nghiệp vụ nằm trong các service (task.Service, project.Service), handler chỉ đọc input,
// gọi service rồi đổi kết quả hoặc lỗi thành response.
package httpapi

import (
	"fmt"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/storage"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// Deps là những gì tầng HTTP cần, main tạo ra và truyền vào.
type Deps struct {
	Tasks       *task.Service
	Projects    *project.Service
	Users       user.Store
	Attachments attachment.Store // thông tin file đính kèm
	Files       storage.Storage  // nội dung file đính kèm
	Tokens      *auth.Tokens
	Ready       *Readiness // trả lời /ready
}

// routes tạo ServeMux chứa các route của API. Route dưới /api/v1 cần access token,
// trừ đăng ký và đăng nhập.
func routes(d Deps) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health) // liveness: process còn sống, không hỏi database
	mux.Handle("GET /ready", d.Ready)     // readiness: nhận request được không (database, đang tắt)
	mux.Handle("POST /api/v1/auth/register", register(d.Users))
	mux.Handle("POST /api/v1/auth/login", login(d.Users, d.Tokens))

	// api đăng ký route cần đăng nhập dưới tiền tố /api/v1: api("GET", "/tasks", h) là "GET /api/v1/tasks".
	// Mặc định là phải đăng nhập, route công khai phải đăng ký riêng ở trên.
	authed := requireAuth(d.Tokens)
	api := func(method, path string, h http.Handler) {
		mux.Handle(method+" /api/v1"+path, authed(h))
	}
	api("GET", "/me", me(d.Users))

	api("GET", "/projects", listProjects(d.Projects))
	api("POST", "/projects", createProject(d.Projects))
	api("GET", "/projects/{id}", getProject(d.Projects))
	api("PUT", "/projects/{id}", replaceProject(d.Projects))
	api("DELETE", "/projects/{id}", deleteProject(d.Projects))
	api("GET", "/projects/{id}/tasks", listProjectTasks(d.Projects, d.Tasks))

	api("GET", "/tasks", listTasks(d.Tasks))
	api("POST", "/tasks", createTask(d.Tasks))
	api("GET", "/tasks/{id}", getTask(d.Tasks))
	api("PATCH", "/tasks/{id}", updateTask(d.Tasks))
	api("DELETE", "/tasks/{id}", deleteTask(d.Tasks))
	api("POST", "/tasks/{id}/tags", addTaskTag(d.Tasks))
	api("DELETE", "/tasks/{id}/tags/{name}", removeTaskTag(d.Tasks))

	api("POST", "/tasks/{id}/attachments", uploadAttachment(d))
	api("GET", "/tasks/{id}/attachments", listAttachments(d))
	api("GET", "/attachments/{id}", downloadAttachment(d))
	api("DELETE", "/attachments/{id}", deleteAttachment(d))
	return mux
}

// New tạo handler của cả API: các route bọc trong middleware dùng chung, theo thứ tự từ ngoài vào trong.
// trusted là các proxy tin cậy (VD Caddy) được phép báo IP client qua X-Forwarded-For.
func New(d Deps, corsOrigin string, trusted []netip.Prefix) http.Handler {
	return chain(routes(d),
		requestID,         // ngoài cùng: các middleware sau đều đọc được ID
		clientIP(trusted), // trước logRequests để log có IP thật của client
		logRequests,       // log cả response 500 do recoverPanic trả về
		recoverPanic,      // bắt panic của mọi thứ phía trong
		cors(corsOrigin),
	)
}

// health cho biết server còn sống, dùng cho Docker/load balancer kiểm tra.
func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

// pathID đọc wildcard {id} trên path và đổi sang số nguyên dương.
// id sai thì ghi response 400 và trả về false.
func pathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	s := r.PathValue("id")
	id, err := strconv.Atoi(s)
	if err != nil || id < 1 {
		badParamsJSON(w, validator{"id": fmt.Sprintf("cần một số nguyên dương, nhận %q", s)})
		return 0, false
	}
	return id, true
}
