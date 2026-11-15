package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
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

// updateTaskInput là body của PATCH /tasks/{id}: field nào nil là client không gửi, giữ nguyên.
type updateTaskInput struct {
	Title       *string      `json:"title"`
	Description *string      `json:"description"`
	Status      *task.Status `json:"status"`
	AssigneeID  *int         `json:"assignee_id"`
	DueDate     optionalTime `json:"due_date"` // gửi null là xóa hạn
	Tags        *[]string    `json:"tags"`     // gửi danh sách là thay toàn bộ tag, [] là xóa hết
}

// onlyStatus cho biết body không đổi gì ngoài status: việc duy nhất người được giao task làm được.
func (in *updateTaskInput) onlyStatus() bool {
	return in.Title == nil && in.Description == nil && in.AssigneeID == nil && !in.DueDate.Set && in.Tags == nil
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
func listTasks(store task.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeTasks(w, r, store, 0)
	}
}

// writeTasks trả về một trang các task mà user đang đăng nhập được xem, khớp ?status=, ?tag=
// (và thuộc projectID nếu projectID khác 0), phân trang bằng ?limit= và ?cursor=.
func writeTasks(w http.ResponseWriter, r *http.Request, store task.Store, projectID int) {
	me := userIDFrom(r.Context())
	if me == 0 { // route quên bọc requireAuth: Filter.VisibleTo = 0 sẽ trả về task của mọi người
		unauthorized(w, false, "cần đăng nhập: thiếu access token")
		return
	}
	query, v := r.URL.Query(), validator{}
	f := task.Filter{ProjectID: projectID, Tag: task.NormalizeTag(query.Get("tag")), VisibleTo: me}
	if s := query.Get("status"); s != "" {
		status, err := task.ParseStatus(s)
		if err != nil {
			v.check(false, "status", err.Error())
		} else {
			f.Status = &status
		}
	}
	limit, afterID := parsePage(query, v)
	if len(v) > 0 {
		badParamsJSON(w, v)
		return
	}
	f.AfterID, f.Limit = afterID, limit+1 // lấy thừa một task để biết còn trang sau không

	tasks, err := store.List(r.Context(), f)
	if err != nil {
		storeError(w, r, err)
		return
	}
	p := page[task.Task]{Data: tasks}
	if len(tasks) > limit {
		p.Data = tasks[:limit]
		p.NextCursor = encodeCursor(p.Data[limit-1].ID) // trang sau bắt đầu sau task cuối của trang này
	}
	writeJSON(w, http.StatusOK, p)
}

// createTask tạo task mới từ body JSON trong một project của user đang đăng nhập, trả 201 kèm header Location.
func createTask(s stores) http.HandlerFunc {
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
			Tags:        task.NormalizeTags(in.Tags),
		}
		if !validTask(w, r, s, t, true) {
			return
		}
		if err := s.tasks.Add(r.Context(), t); err != nil {
			saveTaskError(w, r, t, err)
			return
		}

		w.Header().Set("Location", fmt.Sprintf("/api/v1/tasks/%d", t.ID))
		writeJSON(w, http.StatusCreated, t)
	}
}

// validTask kiểm tra t theo quy tắc của domain (t.Validate) và kiểm tra người được giao (nếu có)
// là một user có thật. checkProject là true khi tạo task: project của t phải là project của user
// đang đăng nhập, project của người khác bị báo giống hệt project không tồn tại.
// t không hợp lệ thì ghi response 422 với lỗi của mọi field rồi trả về false.
func validTask(w http.ResponseWriter, r *http.Request, s stores, t *task.Task, checkProject bool) bool {
	v := validator{}
	if err := v.addError(t.Validate()); err != nil {
		slog.Error("validTask", "err", err) // Validate chỉ trả lỗi validate, tới đây là bug
		errorJSON(w, http.StatusInternalServerError, "lỗi máy chủ")
		return false
	}
	if checkProject && t.ProjectID > 0 {
		p, err := s.projects.Get(r.Context(), t.ProjectID)
		switch {
		case errors.Is(err, project.ErrNotFound) || err == nil && !project.CanAccess(userIDFrom(r.Context()), p):
			v.check(false, "project_id", fmt.Sprintf("không có project #%d", t.ProjectID))
		case err != nil: // database lỗi: không phải lỗi của client
			storeError(w, r, err)
			return false
		}
	}
	if t.AssigneeID > 0 {
		_, err := s.users.Get(r.Context(), t.AssigneeID)
		switch {
		case errors.Is(err, user.ErrNotFound):
			v.check(false, "assignee_id", fmt.Sprintf("không có user #%d", t.AssigneeID))
		case err != nil:
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

// saveTaskError ghi response cho lỗi của store.Add/Update. Project hoặc user bị xóa sau lúc validTask
// kiểm tra thì khóa ngoại chặn và store trả project.ErrNotFound / user.ErrNotFound: trả 422 giống hệt
// khi gửi project_id, assignee_id không tồn tại từ đầu (không phải 404, vì task trên path vẫn có).
func saveTaskError(w http.ResponseWriter, r *http.Request, t *task.Task, err error) {
	switch {
	case errors.Is(err, project.ErrNotFound):
		invalidJSON(w, validator{"project_id": fmt.Sprintf("không có project #%d", t.ProjectID)})
	case errors.Is(err, user.ErrNotFound):
		invalidJSON(w, validator{"assignee_id": fmt.Sprintf("không có user #%d", t.AssigneeID)})
	default:
		storeError(w, r, err)
	}
}

// getTask trả về task có ID là wildcard {id}.
func getTask(s stores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		t, _, ok := loadTask(w, r, s, id)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

// updateTask sửa một phần task {id} (PATCH): chỉ những field có trong body.
// Chủ project sửa được mọi field, người được giao chỉ đổi được status.
func updateTask(s stores) http.HandlerFunc {
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
		t, p, ok := loadTask(w, r, s, id) // t là bản sao, sửa thoải mái
		if !ok {
			return
		}
		me := userIDFrom(r.Context())
		if !in.onlyStatus() && !task.CanEdit(me, p, t) {
			forbidden(w, "người được giao chỉ được đổi status của task")
			return
		}
		if in.Status != nil && !task.CanChangeStatus(me, p, t) {
			forbidden(w, "không được đổi status của task này")
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
		if in.Tags != nil {
			t.Tags = task.NormalizeTags(*in.Tags)
		}
		if !validTask(w, r, s, &t, false) { // kiểm tra cả task sau khi sửa, không chỉ field được gửi
			return
		}

		if err := s.tasks.Update(r.Context(), &t); err != nil {
			saveTaskError(w, r, &t, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

// deleteTask xóa task {id}, thành công thì trả 204 không có body. Chỉ chủ project được xóa.
func deleteTask(s stores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		t, p, ok := loadTask(w, r, s, id)
		if !ok {
			return
		}
		if !task.CanEdit(userIDFrom(r.Context()), p, t) {
			forbidden(w, "chỉ chủ project được xóa task")
			return
		}
		if err := s.tasks.Delete(r.Context(), id); err != nil {
			storeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// tagInput là body của POST /tasks/{id}/tags.
type tagInput struct {
	Name string `json:"name"`
}

// validate chuẩn hóa tên tag rồi kiểm tra, cài interface validatable.
func (in *tagInput) validate(v validator) {
	in.Name = task.NormalizeTag(in.Name)
	msg := task.CheckTag(in.Name)
	v.check(msg == "", "name", msg)
}

// addTaskTag gắn tag trong body vào task {id}, trả về task sau khi gắn. Chỉ chủ project được gắn tag.
func addTaskTag(s stores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var in tagInput
		if !decodeValid(w, r, &in) {
			return
		}
		t, p, ok := loadTask(w, r, s, id)
		if !ok {
			return
		}
		if !task.CanEdit(userIDFrom(r.Context()), p, t) {
			forbidden(w, "chỉ chủ project được sửa tag của task")
			return
		}
		if !t.HasTag(in.Name) && len(t.Tags) >= task.MaxTags {
			invalidJSON(w, validator{"name": fmt.Sprintf("task đã có đủ %d tag", task.MaxTags)})
			return
		}
		if err := s.tasks.AddTag(r.Context(), id, in.Name); err != nil {
			storeError(w, r, err)
			return
		}
		t, err := s.tasks.Get(r.Context(), id)
		if err != nil {
			storeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

// removeTaskTag gỡ tag {name} khỏi task {id}, trả 204 kể cả khi task không có tag đó.
// Chỉ chủ project được gỡ tag.
func removeTaskTag(s stores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		t, p, ok := loadTask(w, r, s, id)
		if !ok {
			return
		}
		if !task.CanEdit(userIDFrom(r.Context()), p, t) {
			forbidden(w, "chỉ chủ project được sửa tag của task")
			return
		}
		if err := s.tasks.RemoveTag(r.Context(), id, task.NormalizeTag(r.PathValue("name"))); err != nil {
			storeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
