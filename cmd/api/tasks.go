package main

import (
	"errors"
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
	DueDate     optionalTime `json:"due_date"` // gửi null là xóa hạn
}

// optionalTime phân biệt ba trường hợp của một field thời gian trong body:
// không gửi (Set false), gửi null (Set true, Time là zero value) và gửi một thời điểm.
type optionalTime struct {
	Set  bool
	Time time.Time
}

// UnmarshalJSON chỉ được gọi khi body có field này, kể cả khi giá trị là null.
func (o *optionalTime) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Time = time.Time{}
		return nil
	}
	return o.Time.UnmarshalJSON(data)
}

// listTasks liệt kê mọi task, lọc được theo ?status=todo|doing|done.
func listTasks(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeTasks(w, r, store, 0)
	}
}

// writeTasks trả về các task khớp ?status= (và thuộc projectID nếu projectID khác 0).
func writeTasks(w http.ResponseWriter, r *http.Request, store task.Store, projectID int) {
	f := task.Filter{ProjectID: projectID}
	if s := r.URL.Query().Get("status"); s != "" {
		status, err := task.ParseStatus(s)
		if err != nil {
			badParamsJSON(w, validator{"status": err.Error()})
			return
		}
		f.Status = &status
	}

	tasks, err := store.List(r.Context(), f)
	if err != nil {
		storeError(w, r, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(len(tasks)))
	writeJSON(w, http.StatusOK, tasks)
}

// createTask tạo task mới từ body JSON, trả 201 kèm header Location.
func createTask(projects project.Store, store task.Store) http.HandlerFunc {
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
		if !validTask(w, r, projects, t) {
			return
		}
		if err := store.Add(r.Context(), t); err != nil {
			storeError(w, r, err)
			return
		}

		w.Header().Set("Location", fmt.Sprintf("/api/v1/tasks/%d", t.ID))
		writeJSON(w, http.StatusCreated, t)
	}
}

// validTask kiểm tra t theo quy tắc của domain (t.Validate) và kiểm tra project của t có tồn tại.
// t không hợp lệ thì ghi response 422 với lỗi của mọi field rồi trả về false.
func validTask(w http.ResponseWriter, r *http.Request, projects project.Store, t *task.Task) bool {
	v := validator{}
	if err := v.addError(t.Validate()); err != nil {
		slog.Error("validTask", "err", err) // Validate chỉ trả lỗi validate, tới đây là bug
		errorJSON(w, http.StatusInternalServerError, "lỗi máy chủ")
		return false
	}
	if t.ProjectID > 0 {
		_, err := projects.Get(r.Context(), t.ProjectID)
		switch {
		case errors.Is(err, project.ErrNotFound):
			v.check(false, "project_id", fmt.Sprintf("không có project #%d", t.ProjectID))
		case err != nil: // database lỗi: không phải lỗi của client
			storeError(w, r, err)
			return false
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
		t, err := store.Get(r.Context(), id)
		if err != nil {
			storeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

// updateTask sửa một phần task {id} (PATCH): chỉ những field có trong body.
func updateTask(projects project.Store, store task.Store) http.HandlerFunc {
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
		t, err := store.Get(r.Context(), id) // t là bản sao, sửa thoải mái
		if err != nil {
			storeError(w, r, err)
			return
		}

		if in.Title != nil {
			t.Title = strings.TrimSpace(*in.Title)
		}
		if in.Description != nil {
			t.Description = *in.Description
		}
		if in.AssigneeID != nil {
			t.AssigneeID = *in.AssigneeID
		}
		if in.DueDate.Set {
			t.DueDate = in.DueDate.Time // null thì thành zero value: không có hạn
		}
		if in.Status != nil {
			t.SetStatus(*in.Status)
		}
		if !validTask(w, r, projects, &t) { // kiểm tra cả task sau khi sửa, không chỉ field được gửi
			return
		}

		if err := store.Update(r.Context(), &t); err != nil {
			storeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

// deleteTask xóa task {id}, thành công thì trả 204 không có body.
func deleteTask(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := store.Delete(r.Context(), id); err != nil {
			storeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
