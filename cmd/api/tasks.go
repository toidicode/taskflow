package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// createTaskInput là body của POST /tasks.
type createTaskInput struct {
	ProjectID   int       `json:"project_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	AssigneeID  int       `json:"assignee_id"`
	DueDate     time.Time `json:"due_date"`
}

// updateTaskInput là body của PATCH /tasks/{id}: field nào nil là client không gửi, giữ nguyên.
type updateTaskInput struct {
	Title       *string      `json:"title"`
	Description *string      `json:"description"`
	Status      *task.Status `json:"status"`
	AssigneeID  *int         `json:"assignee_id"`
	DueDate     *time.Time   `json:"due_date"`
}

// listTasks liệt kê mọi task, lọc được theo ?status=todo|doing|done.
func listTasks(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeTasks(w, r, store, 0)
	}
}

// writeTasks trả về các task khớp ?status= (và thuộc projectID nếu projectID khác 0).
func writeTasks(w http.ResponseWriter, r *http.Request, store task.Store, projectID int) {
	match := func(*task.Task) bool { return true }
	if s := r.URL.Query().Get("status"); s != "" {
		status, err := task.ParseStatus(s)
		if err != nil {
			badParamsJSON(w, validator{"status": err.Error()})
			return
		}
		match = func(t *task.Task) bool { return t.Status == status }
	}

	tasks := []*task.Task{} // không phải nil slice: không có task thì trả [] thay vì null
	for t := range store.All() {
		if (projectID == 0 || t.ProjectID == projectID) && match(t) {
			tasks = append(tasks, t)
		}
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(len(tasks)))
	writeJSON(w, http.StatusOK, tasks)
}

// createTask tạo task mới từ body JSON, trả 201 kèm header Location.
func createTask(projects *project.MemoryStore, store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in createTaskInput
		if err := readJSON(w, r, &in); err != nil {
			bodyError(w, err)
			return
		}
		t := &task.Task{
			ProjectID:   in.ProjectID,
			Title:       strings.TrimSpace(in.Title),
			Description: in.Description,
			Status:      task.StatusTodo,
			AssigneeID:  in.AssigneeID,
			DueDate:     in.DueDate,
		}
		if !validTask(w, projects, t) {
			return
		}
		store.Add(t)

		w.Header().Set("Location", fmt.Sprintf("/api/v1/tasks/%d", t.ID))
		writeJSON(w, http.StatusCreated, t)
	}
}

// validTask kiểm tra t theo quy tắc của domain (t.Validate) và kiểm tra project của t có tồn tại.
// t không hợp lệ thì ghi response 422 với lỗi của mọi field rồi trả về false.
func validTask(w http.ResponseWriter, projects *project.MemoryStore, t *task.Task) bool {
	v := validator{}
	if err := v.addError(t.Validate()); err != nil {
		slog.Error("validTask", "err", err) // Validate chỉ trả lỗi validate, tới đây là bug
		errorJSON(w, http.StatusInternalServerError, "lỗi máy chủ")
		return false
	}
	if t.ProjectID > 0 {
		if _, err := projects.Get(t.ProjectID); err != nil {
			v.check(false, "project_id", fmt.Sprintf("không có project #%d", t.ProjectID))
		}
	}
	if len(v) > 0 {
		invalidJSON(w, v)
		return false
	}
	return true
}

// getTask trả về task có ID là wildcard {id}.
func getTask(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		t, err := store.Get(id)
		if err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

// updateTask sửa một phần task {id} (PATCH): chỉ những field có trong body.
func updateTask(projects *project.MemoryStore, store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var in updateTaskInput
		if err := readJSON(w, r, &in); err != nil {
			bodyError(w, err)
			return
		}
		old, err := store.Get(id)
		if err != nil {
			storeError(w, err)
			return
		}

		t := *old // sửa trên bản sao, request khác có thể đang đọc *old
		if in.Title != nil {
			t.Title = strings.TrimSpace(*in.Title)
		}
		if in.Description != nil {
			t.Description = *in.Description
		}
		if in.AssigneeID != nil {
			t.AssigneeID = *in.AssigneeID
		}
		if in.DueDate != nil {
			t.DueDate = *in.DueDate
		}
		if in.Status != nil {
			t.SetStatus(*in.Status)
		}
		if !validTask(w, projects, &t) { // kiểm tra cả task sau khi sửa, không chỉ field được gửi
			return
		}

		if err := store.Update(&t); err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, &t)
	}
}

// deleteTask xóa task {id}, thành công thì trả 204 không có body.
func deleteTask(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := store.Delete(id); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
