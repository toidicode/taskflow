package main

import (
	"fmt"
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
			errorJSON(w, http.StatusBadRequest, err.Error())
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
		t, err := task.New(in.ProjectID, in.Title)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error()) // Bài 27 chuẩn hóa lỗi validate
			return
		}
		if _, err := projects.Get(in.ProjectID); err != nil {
			errorJSON(w, http.StatusBadRequest, fmt.Sprintf("project_id: không có project #%d", in.ProjectID))
			return
		}
		t.Description, t.AssigneeID, t.DueDate = in.Description, in.AssigneeID, in.DueDate
		store.Add(t)

		w.Header().Set("Location", fmt.Sprintf("/api/v1/tasks/%d", t.ID))
		writeJSON(w, http.StatusCreated, t)
	}
}

// getTask trả về task có ID là wildcard {id}.
func getTask(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
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
func updateTask(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
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
			if t.Title == "" {
				errorJSON(w, http.StatusBadRequest, "title: không được để trống")
				return
			}
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
		id, err := pathID(r)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := store.Delete(id); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
