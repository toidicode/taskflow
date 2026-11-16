package httpapi

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// projectInput là body của POST /projects và PUT /projects/:id, validate bằng tag binding.
// Không có owner_id: chủ project là user đang đăng nhập.
type projectInput struct {
	Name        string `json:"name" binding:"notblank,max=100"`
	Description string `json:"description" binding:"max=2000"`
}

// listProjects liệt kê các project của user đang đăng nhập.
func listProjects(projects *project.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		list, err := projects.List(c.Request.Context(), userIDFrom(c))
		if err != nil {
			writeError(c, err)
			return
		}
		c.Header("X-Total-Count", strconv.Itoa(len(list)))
		writeJSON(c, http.StatusOK, list)
	}
}

// createProject tạo project mới từ body JSON, trả 201 kèm header Location.
func createProject(projects *project.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in projectInput
		if !bindJSON(c, &in) {
			return
		}
		p, err := projects.Create(c.Request.Context(), userIDFrom(c), in.Name, in.Description)
		if err != nil {
			writeError(c, err)
			return
		}
		c.Header("Location", fmt.Sprintf("/api/v1/projects/%d", p.ID))
		writeJSON(c, http.StatusCreated, p)
	}
}

// getProject trả về project có ID là wildcard :id.
func getProject(projects *project.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		p, err := projects.Get(c.Request.Context(), userIDFrom(c), id)
		if err != nil {
			writeError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, p)
	}
}

// replaceProject thay tên và mô tả của project :id bằng body JSON (PUT), giữ nguyên chủ project.
func replaceProject(projects *project.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		var in projectInput
		if !bindJSON(c, &in) {
			return
		}
		p, err := projects.Replace(c.Request.Context(), userIDFrom(c), id, in.Name, in.Description)
		if err != nil {
			writeError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, p)
	}
}

// deleteProject xóa project :id khi project không còn task nào, trả 204; còn task thì 409.
func deleteProject(projects *project.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		if err := projects.Delete(c.Request.Context(), userIDFrom(c), id); err != nil {
			writeError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// listProjectTasks liệt kê task của project :id, lọc được theo ?status= như listTasks.
func listProjectTasks(projects *project.Service, tasks *task.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		if _, err := projects.Get(c.Request.Context(), userIDFrom(c), id); err != nil {
			writeError(c, err)
			return
		}
		writeTasks(c, tasks, id)
	}
}
