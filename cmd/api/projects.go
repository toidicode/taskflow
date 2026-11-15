package main

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
type projectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	OwnerID     int    `json:"owner_id"`
}

// validate kiểm tra body của project, cài interface validatable.
func (in *projectInput) validate(v validator) {
	name := strings.TrimSpace(in.Name)
	v.check(name != "", "name", "không được để trống")
	v.check(utf8.RuneCountInString(name) <= 100, "name", "tối đa 100 ký tự")
	v.check(utf8.RuneCountInString(in.Description) <= 2000, "description", "tối đa 2000 ký tự")
	v.check(in.OwnerID > 0, "owner_id", "phải lớn hơn 0")
}

// listProjects liệt kê mọi project.
func listProjects(store project.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projects, err := store.List(r.Context())
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
		p := project.New(in.Name, in.OwnerID)
		p.Description = in.Description
		if err := store.Add(r.Context(), p); err != nil {
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
		p, err := store.Get(r.Context(), id)
		if err != nil {
			storeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// replaceProject thay toàn bộ nội dung project {id} bằng body JSON (PUT).
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
		p := project.New(in.Name, in.OwnerID)
		p.ID, p.Description = id, in.Description
		if err := store.Update(r.Context(), p); err != nil {
			storeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// deleteProject xóa project {id} khi project không còn task nào, trả 204.
func deleteProject(projects project.Store, tasks task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if _, err := projects.Get(r.Context(), id); err != nil {
			storeError(w, r, err)
			return
		}
		ts, err := tasks.List(r.Context(), task.Filter{ProjectID: id})
		if err != nil {
			storeError(w, r, err)
			return
		}
		if len(ts) > 0 {
			errorJSON(w, http.StatusConflict, fmt.Sprintf("project #%d vẫn còn task, hãy xóa task trước", id))
			return
		}
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
		if _, err := projects.Get(r.Context(), id); err != nil {
			storeError(w, r, err)
			return
		}
		writeTasks(w, r, tasks, id)
	}
}
