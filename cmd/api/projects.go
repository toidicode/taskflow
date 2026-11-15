package main

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// projectInput là body của POST /projects và PUT /projects/{id}.
type projectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	OwnerID     int    `json:"owner_id"`
}

// listProjects liệt kê mọi project.
func listProjects(store *project.MemoryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projects := store.List()
		w.Header().Set("X-Total-Count", strconv.Itoa(len(projects)))
		writeJSON(w, http.StatusOK, projects)
	}
}

// createProject tạo project mới từ body JSON, trả 201 kèm header Location.
func createProject(store *project.MemoryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in projectInput
		if err := readJSON(w, r, &in); err != nil {
			bodyError(w, err)
			return
		}
		p := project.New(in.Name, in.OwnerID)
		p.Description = in.Description
		store.Add(p)

		w.Header().Set("Location", fmt.Sprintf("/api/v1/projects/%d", p.ID))
		writeJSON(w, http.StatusCreated, p)
	}
}

// getProject trả về project có ID là wildcard {id}.
func getProject(store *project.MemoryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		p, err := store.Get(id)
		if err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// replaceProject thay toàn bộ nội dung project {id} bằng body JSON (PUT).
func replaceProject(store *project.MemoryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		var in projectInput
		if err := readJSON(w, r, &in); err != nil {
			bodyError(w, err)
			return
		}
		p := project.New(in.Name, in.OwnerID)
		p.ID, p.Description = id, in.Description
		if err := store.Update(p); err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// deleteProject xóa project {id} khi project không còn task nào, trả 204.
func deleteProject(projects *project.MemoryStore, tasks task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := projects.Get(id); err != nil {
			storeError(w, err)
			return
		}
		for t := range tasks.All() {
			if t.ProjectID == id {
				errorJSON(w, http.StatusConflict, fmt.Sprintf("project #%d vẫn còn task, hãy xóa task trước", id))
				return
			}
		}
		if err := projects.Delete(id); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// listProjectTasks liệt kê task của project {id}, lọc được theo ?status= như listTasks.
func listProjectTasks(projects *project.MemoryStore, tasks task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := projects.Get(id); err != nil {
			storeError(w, err)
			return
		}
		writeTasks(w, r, tasks, id)
	}
}
