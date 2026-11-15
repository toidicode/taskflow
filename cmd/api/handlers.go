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
func newMux(projects *project.MemoryStore, tasks task.Store) *http.ServeMux {
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
	api("PATCH", "/tasks/{id}", updateTask(tasks))
	api("DELETE", "/tasks/{id}", deleteTask(tasks))
	return mux
}

// health cho biết server còn sống, dùng cho Docker/load balancer kiểm tra.
func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

// pathID đọc wildcard {id} trên path và đổi sang số nguyên dương.
func pathID(r *http.Request) (int, error) {
	s := r.PathValue("id")
	id, err := strconv.Atoi(s)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("id %q không hợp lệ, cần một số nguyên dương", s)
	}
	return id, nil
}

// storeError trả 404 khi err là lỗi không tìm thấy, các lỗi khác là 500.
func storeError(w http.ResponseWriter, err error) {
	if errors.Is(err, task.ErrNotFound) || errors.Is(err, project.ErrNotFound) {
		errorJSON(w, http.StatusNotFound, err.Error())
		return
	}
	errorJSON(w, http.StatusInternalServerError, "lỗi máy chủ")
}
