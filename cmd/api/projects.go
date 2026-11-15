package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// projectInput là body của POST /projects và PUT /projects/{id}.
// Không có owner_id: chủ project là user đang đăng nhập.
type projectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// validate kiểm tra body của project, cài interface validatable.
func (in *projectInput) validate(v validator) {
	name := strings.TrimSpace(in.Name)
	v.check(name != "", "name", "không được để trống")
	v.check(utf8.RuneCountInString(name) <= 100, "name", "tối đa 100 ký tự")
	v.check(utf8.RuneCountInString(in.Description) <= 2000, "description", "tối đa 2000 ký tự")
}

// listProjects liệt kê các project của user đang đăng nhập.
func listProjects(store project.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projects, err := store.List(r.Context(), userIDFrom(r.Context()))
		if err != nil {
			storeError(w, r, err)
			return
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(len(projects)))
		writeJSON(w, http.StatusOK, projects)
	}
}

// createProject tạo project mới từ body JSON, trả 201 kèm header Location.
func createProject(store project.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in projectInput
		if !decodeValid(w, r, &in) {
			return
		}
		p := project.New(in.Name, userIDFrom(r.Context())) // chủ project là người tạo
		p.Description = in.Description
		err := store.Add(r.Context(), p)
		if errors.Is(err, user.ErrNotFound) { // khóa ngoại owner_id: token còn hạn nhưng user đã bị xóa
			unauthorized(w, true, "tài khoản không còn tồn tại")
			return
		}
		if err != nil {
			storeError(w, r, err)
			return
		}

		w.Header().Set("Location", fmt.Sprintf("/api/v1/projects/%d", p.ID))
		writeJSON(w, http.StatusCreated, p)
	}
}

// getProject trả về project có ID là wildcard {id}.
func getProject(store project.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		p, ok := loadProject(w, r, store, id)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// replaceProject thay tên và mô tả của project {id} bằng body JSON (PUT), giữ nguyên chủ project.
func replaceProject(store project.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var in projectInput
		if !decodeValid(w, r, &in) {
			return
		}
		old, ok := loadProject(w, r, store, id)
		if !ok {
			return
		}
		p := project.New(in.Name, old.OwnerID)
		p.ID, p.Description = id, in.Description
		if err := store.Update(r.Context(), p); err != nil {
			storeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// deleteProject xóa project {id} khi project không còn task nào, trả 204; còn task thì 409.
func deleteProject(projects project.Store, tasks task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if _, ok := loadProject(w, r, projects, id); !ok {
			return
		}
		ts, err := tasks.List(r.Context(), task.Filter{ProjectID: id, Limit: 1}) // chỉ cần biết còn task không
		if err != nil {
			storeError(w, r, err)
			return
		}
		if len(ts) > 0 {
			storeError(w, r, fmt.Errorf("project #%d: %w", id, project.ErrHasTasks))
			return
		}
		// task được thêm ngay sau lúc kiểm tra thì khóa ngoại của PostgreSQL vẫn chặn: store trả ErrHasTasks
		if err := projects.Delete(r.Context(), id); err != nil {
			storeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// listProjectTasks liệt kê task của project {id}, lọc được theo ?status= như listTasks.
func listProjectTasks(projects project.Store, tasks task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if _, ok := loadProject(w, r, projects, id); !ok {
			return
		}
		writeTasks(w, r, tasks, id)
	}
}
