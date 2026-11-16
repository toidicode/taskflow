package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
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
func listProjects(projects *project.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := projects.List(r.Context(), userIDFrom(r.Context()))
		if err != nil {
			writeError(w, r, err)
			return
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(len(list)))
		writeJSON(w, http.StatusOK, list)
	}
}

// createProject tạo project mới từ body JSON, trả 201 kèm header Location.
func createProject(projects *project.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in projectInput
		if !decodeValid(w, r, &in) {
			return
		}
		p, err := projects.Create(r.Context(), userIDFrom(r.Context()), in.Name, in.Description)
		if err != nil {
			writeError(w, r, err)
			return
		}
		w.Header().Set("Location", fmt.Sprintf("/api/v1/projects/%d", p.ID))
		writeJSON(w, http.StatusCreated, p)
	}
}

// getProject trả về project có ID là wildcard {id}.
func getProject(projects *project.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		p, err := projects.Get(r.Context(), userIDFrom(r.Context()), id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// replaceProject thay tên và mô tả của project {id} bằng body JSON (PUT), giữ nguyên chủ project.
func replaceProject(projects *project.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var in projectInput
		if !decodeValid(w, r, &in) {
			return
		}
		p, err := projects.Replace(r.Context(), userIDFrom(r.Context()), id, in.Name, in.Description)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// deleteProject xóa project {id} khi project không còn task nào, trả 204; còn task thì 409.
func deleteProject(projects *project.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := projects.Delete(r.Context(), userIDFrom(r.Context()), id); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// listProjectTasks liệt kê task của project {id}, lọc được theo ?status= như listTasks.
func listProjectTasks(projects *project.Service, tasks *task.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if _, err := projects.Get(r.Context(), userIDFrom(r.Context()), id); err != nil {
			writeError(w, r, err)
			return
		}
		writeTasks(w, r, tasks, id)
	}
}
