package main

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/toidicode/taskflow/internal/task"
)

// newMux tạo ServeMux chứa các route của API.
func newMux(store task.Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", health)
	mux.Handle("/api/v1/tasks", listTasks(store))
	return mux
}

// health cho biết server còn sống, dùng cho Docker/load balancer kiểm tra.
func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

// listTasks trả về handler liệt kê task dạng text, lọc được theo ?status=todo|doing|done.
func listTasks(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "chỉ hỗ trợ GET", http.StatusMethodNotAllowed)
			return
		}

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
			if match(t) {
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
}
