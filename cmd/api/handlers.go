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
	api("GET", "/projects/{id}", getProject(projects))
	api("GET", "/projects/{id}/tasks", listProjectTasks(projects, tasks))
	api("GET", "/tasks", listTasks(tasks))
	api("GET", "/tasks/{id}", getTask(tasks))
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
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, "lỗi máy chủ", http.StatusInternalServerError)
}

// listProjects liệt kê mọi project dạng text.
func listProjects(store *project.MemoryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projects := store.List()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Total-Count", strconv.Itoa(len(projects)))
		for _, p := range projects {
			fmt.Fprintf(w, "#%d %s (owner %d)\n", p.ID, p.Name, p.OwnerID)
		}
	}
}

// getProject trả về project có ID là wildcard {id}.
func getProject(store *project.MemoryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := store.Get(id)
		if err != nil {
			storeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "#%d %s (owner %d)\n", p.ID, p.Name, p.OwnerID)
	}
}

// listProjectTasks liệt kê task của project {id}, lọc được theo ?status= như listTasks.
func listProjectTasks(projects *project.MemoryStore, tasks task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := projects.Get(id); err != nil {
			storeError(w, err)
			return
		}
		writeTasks(w, r, tasks, id)
	}
}

// listTasks liệt kê mọi task dạng text, lọc được theo ?status=todo|doing|done.
func listTasks(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeTasks(w, r, store, 0)
	}
}

// writeTasks ghi các task khớp ?status= (và thuộc projectID nếu projectID khác 0).
func writeTasks(w http.ResponseWriter, r *http.Request, store task.Store, projectID int) {
	match := func(*task.Task) bool { return true }
	if s := r.URL.Query().Get("status"); s != "" {
		status, err := task.ParseStatus(s)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		match = func(t *task.Task) bool { return t.Status == status }
	}

	var tasks []*task.Task
	for t := range store.All() {
		if (projectID == 0 || t.ProjectID == projectID) && match(t) {
			tasks = append(tasks, t)
		}
	}

	// header phải được đặt trước khi ghi body
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Total-Count", strconv.Itoa(len(tasks)))
	for _, t := range tasks {
		fmt.Fprintf(w, "#%d [p%d] %s (%v)\n", t.ID, t.ProjectID, t.Title, t.Status)
	}
}

// getTask trả về task có ID là wildcard {id}.
func getTask(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		t, err := store.Get(id)
		if err != nil {
			storeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "#%d [p%d] %s (%v)\n", t.ID, t.ProjectID, t.Title, t.Status)
	}
}
