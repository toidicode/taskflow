package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// stores gom các store mà handler cần.
type stores struct {
	projects project.Store
	tasks    task.Store
	users    user.Store
}

// newMux tạo ServeMux chứa các route của API. Route dưới /api/v1 cần access token,
// trừ đăng ký và đăng nhập.
func newMux(s stores, tokens *auth.Tokens) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.Handle("POST /api/v1/auth/register", register(s.users))
	mux.Handle("POST /api/v1/auth/login", login(s.users, tokens))

	// api đăng ký route cần đăng nhập dưới tiền tố /api/v1: api("GET", "/tasks", h) là "GET /api/v1/tasks".
	// Mặc định là phải đăng nhập, route công khai phải đăng ký riêng ở trên.
	authed := requireAuth(tokens)
	api := func(method, path string, h http.Handler) {
		mux.Handle(method+" /api/v1"+path, authed(h))
	}
	api("GET", "/me", me(s.users))

	api("GET", "/projects", listProjects(s.projects))
	api("POST", "/projects", createProject(s.projects))
	api("GET", "/projects/{id}", getProject(s.projects))
	api("PUT", "/projects/{id}", replaceProject(s.projects))
	api("DELETE", "/projects/{id}", deleteProject(s.projects, s.tasks))
	api("GET", "/projects/{id}/tasks", listProjectTasks(s.projects, s.tasks))

	api("GET", "/tasks", listTasks(s.tasks))
	api("POST", "/tasks", createTask(s))
	api("GET", "/tasks/{id}", getTask(s))
	api("PATCH", "/tasks/{id}", updateTask(s))
	api("DELETE", "/tasks/{id}", deleteTask(s))
	api("POST", "/tasks/{id}/tags", addTaskTag(s))
	api("DELETE", "/tasks/{id}/tags/{name}", removeTaskTag(s))
	return mux
}

// newAPI bọc mux bằng các middleware dùng chung cho mọi route, theo thứ tự từ ngoài vào trong.
func newAPI(s stores, tokens *auth.Tokens, corsOrigin string) http.Handler {
	return chain(newMux(s, tokens),
		requestID,    // ngoài cùng: các middleware sau đều đọc được ID
		logRequests,  // log cả response 500 do recoverPanic trả về
		recoverPanic, // bắt panic của mọi thứ phía trong
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

// storeError trả 404 khi err là lỗi không tìm thấy, 409 khi xóa project còn task. Lỗi khác
// (VD mất kết nối database) được ghi log kèm request ID rồi trả 500, client không thấy chi tiết lỗi.
func storeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, task.ErrNotFound) || errors.Is(err, project.ErrNotFound):
		errorJSON(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, project.ErrHasTasks):
		errorJSON(w, http.StatusConflict, err.Error())
		return
	}
	requestLogger(r).Error("store", "err", err)
	errorJSON(w, http.StatusInternalServerError, "lỗi máy chủ")
}
