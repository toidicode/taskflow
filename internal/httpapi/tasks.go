package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/task"
)

// createTaskInput là body của POST /tasks.
type createTaskInput struct {
	ProjectID   int       `json:"project_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	AssigneeID  int       `json:"assignee_id"`
	DueDate     time.Time `json:"due_date"`
	Tags        []string  `json:"tags"`
}

// updateTaskInput là body của PATCH /tasks/:id: field nào nil là client không gửi, giữ nguyên.
type updateTaskInput struct {
	Title       *string      `json:"title"`
	Description *string      `json:"description"`
	Status      *task.Status `json:"status"`
	AssigneeID  *int         `json:"assignee_id"`
	DueDate     optionalTime `json:"due_date"` // gửi null là xóa hạn
	Tags        *[]string    `json:"tags"`     // gửi danh sách là thay toàn bộ tag, [] là xóa hết
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

// listTasks liệt kê task theo trang, lọc được theo ?status=todo|doing|done và ?tag=.
func listTasks(tasks *task.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		writeTasks(c, tasks, 0)
	}
}

// writeTasks trả về một trang các task mà user đang đăng nhập được xem, khớp ?status=, ?tag=
// (và thuộc projectID nếu projectID khác 0), phân trang bằng ?limit= và ?cursor=.
func writeTasks(c *gin.Context, tasks *task.Service, projectID int) {
	me := userIDFrom(c)
	if me == 0 { // route quên requireAuth: Filter.VisibleTo = 0 sẽ trả về task của mọi người
		unauthorized(c, false, "cần đăng nhập: thiếu access token")
		return
	}
	v := validator{}
	f := task.Filter{ProjectID: projectID, Tag: task.NormalizeTag(c.Query("tag"))}
	if s := c.Query("status"); s != "" {
		status, err := task.ParseStatus(s)
		if err != nil {
			v.check(false, "status", err.Error())
		} else {
			f.Status = &status
		}
	}
	limit, afterID := parsePage(c, v)
	if len(v) > 0 {
		badParamsJSON(c, v)
		return
	}
	f.AfterID, f.Limit = afterID, limit+1 // lấy thừa một task để biết còn trang sau không

	list, err := tasks.List(c.Request.Context(), me, f)
	if err != nil {
		writeError(c, err)
		return
	}
	p := page[task.Task]{Data: list}
	if len(list) > limit {
		p.Data = list[:limit]
		p.NextCursor = encodeCursor(p.Data[limit-1].ID) // trang sau bắt đầu sau task cuối của trang này
	}
	writeJSON(c, http.StatusOK, p)
}

// createTask tạo task mới từ body JSON trong một project của user đang đăng nhập, trả 201 kèm header Location.
func createTask(tasks *task.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in createTaskInput
		if err := readJSON(c, &in); err != nil {
			bodyError(c, err)
			return
		}
		t := &task.Task{
			ProjectID:   in.ProjectID,
			Title:       in.Title,
			Description: in.Description,
			AssigneeID:  in.AssigneeID,
			DueDate:     in.DueDate,
			Tags:        in.Tags,
		}
		if err := tasks.Create(c.Request.Context(), userIDFrom(c), t); err != nil {
			writeError(c, err)
			return
		}
		c.Header("Location", fmt.Sprintf("/api/v1/tasks/%d", t.ID))
		writeJSON(c, http.StatusCreated, t)
	}
}

// getTask trả về task có ID là tham số :id.
func getTask(tasks *task.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		t, _, err := tasks.Get(c.Request.Context(), userIDFrom(c), id)
		if err != nil {
			writeError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, t)
	}
}

// updateTask sửa một phần task :id (PATCH): chỉ những field có trong body.
func updateTask(tasks *task.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		var in updateTaskInput
		if err := readJSON(c, &in); err != nil {
			bodyError(c, err)
			return
		}
		t, err := tasks.Update(c.Request.Context(), userIDFrom(c), id, in.changes())
		if err != nil {
			writeError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, t)
	}
}

// changes đổi body PATCH thành task.Changes của service.
func (in *updateTaskInput) changes() task.Changes {
	c := task.Changes{Title: in.Title, Description: in.Description, Status: in.Status, AssigneeID: in.AssigneeID, Tags: in.Tags}
	if in.DueDate.Set {
		c.DueDate = &in.DueDate.Time // null thì là zero value: xóa hạn
	}
	return c
}

// deleteTask xóa task :id cùng các file đính kèm, thành công thì trả 204 không có body.
func deleteTask(tasks *task.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		if err := tasks.Delete(c.Request.Context(), userIDFrom(c), id); err != nil {
			writeError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// tagInput là body của POST /tasks/:id/tags.
type tagInput struct {
	Name string `json:"name"`
}

// validate chuẩn hóa tên tag rồi kiểm tra, cài interface validatable.
func (in *tagInput) validate(v validator) {
	in.Name = task.NormalizeTag(in.Name)
	msg := task.CheckTag(in.Name)
	v.check(msg == "", "name", msg)
}

// addTaskTag gắn tag trong body vào task :id, trả về task sau khi gắn.
func addTaskTag(tasks *task.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		var in tagInput
		if !bindJSON(c, &in) {
			return
		}
		t, err := tasks.AddTag(c.Request.Context(), userIDFrom(c), id, in.Name)
		if err != nil {
			writeError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, t)
	}
}

// removeTaskTag gỡ tag :name khỏi task :id, trả 204 kể cả khi task không có tag đó.
func removeTaskTag(tasks *task.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		if err := tasks.RemoveTag(c.Request.Context(), userIDFrom(c), id, c.Param("name")); err != nil {
			writeError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
