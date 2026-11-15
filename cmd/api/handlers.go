package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// newMux tạo ServeMux chứa các route của API.
func newMux(projects project.Store, tasks task.Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)

	// api đăng ký route dưới tiền tố /api/v1: api("GET", "/tasks", h) là "GET /api/v1/tasks".
	api := func(method, path string, h http.Handler) {
		mux.Handle(method+" /api/v1"+path, h)
	}
	api("GET", "/projects", listProjects(projects))
	api("POST", "/projects", createProject(projects))
	api("GET", "/projects/{id}", getProject(projects))
	api("PUT", "/projects/{id}", replaceProject(projects))
	api("DELETE", "/projects/{id}", deleteProject(projects, tasks))
	api("GET", "/projects/{id}/tasks", listProjectTasks(projects, tasks))

	api("GET", "/tasks", listTasks(tasks))
	api("POST", "/tasks", createTask(projects, tasks))
	api("GET", "/tasks/{id}", getTask(tasks))
	api("PATCH", "/tasks/{id}", updateTask(projects, tasks))
	api("DELETE", "/tasks/{id}", deleteTask(tasks))
	api("POST", "/tasks/{id}/tags", addTaskTag(tasks))
	api("DELETE", "/tasks/{id}/tags/{name}", removeTaskTag(tasks))
	return mux
}

// newAPI bọc mux bằng các middleware dùng chung cho mọi route, theo thứ tự từ ngoài vào trong.
func newAPI(projects project.Store, tasks task.Store, corsOrigin string) http.Handler {
	return chain(newMux(projects, tasks),
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
