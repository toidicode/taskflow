package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// addUsers thêm các user (không có mật khẩu) vào s, ID nối tiếp user đã có.
func addUsers(t *testing.T, s stores, names ...string) {
	t.Helper()
	for _, name := range names {
		u := &user.User{Email: user.NormalizeEmail(name) + "@example.com", Name: name}
		if err := s.users.Add(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
}

// newAccessMux tạo mux cho test phân quyền:
//   - user #1 An: chủ project #1, #2 (task #1..#4 của dữ liệu mẫu);
//   - user #2 Bình: được giao task #3 (project #1, có tag "go");
//   - user #3 Chi: chủ project #3 với task #5.
func newAccessMux(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	s := newTestStores(t)
	addUsers(t, s, "Binh", "Chi")

	t3, err := s.tasks.Get(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	t3.AssigneeID, t3.Tags = 2, []string{"go"}
	if err := s.tasks.Update(ctx, &t3); err != nil {
		t.Fatal(err)
	}
	p := project.New("Dự án của Chi", 3)
	if err := s.projects.Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.tasks.Add(ctx, &task.Task{ProjectID: p.ID, Title: "Task của Chi"}); err != nil {
		t.Fatal(err)
	}
	return newMux(s, testTokens)
}

// TestAccess chạy từng request với ba vai: chủ project (An), người được giao task #3 (Bình)
// và người ngoài (Chi). Mỗi lần chạy dùng store mới, request trước không ảnh hưởng request sau.
func TestAccess(t *testing.T) {
	roles := []struct {
		name   string
		userID int
	}{{"chủ project", 1}, {"người được giao", 2}, {"người ngoài", 3}}

	tests := []struct {
		name   string
		method string
		target string
		body   string
		want   [3]int // status code theo thứ tự của roles
	}{
		{"xem project", "GET", "/api/v1/projects/1", "", [3]int{200, 404, 404}},
		{"sửa project", "PUT", "/api/v1/projects/1", `{"name": "TaskFlow API v2"}`, [3]int{200, 404, 404}},
		{"xóa project còn task", "DELETE", "/api/v1/projects/2", "", [3]int{409, 404, 404}},
		{"task của project", "GET", "/api/v1/projects/1/tasks", "", [3]int{200, 404, 404}},
		{"tạo task trong project", "POST", "/api/v1/tasks", `{"project_id": 1, "title": "Viết docs"}`, [3]int{201, 422, 422}},
		{"xem task", "GET", "/api/v1/tasks/3", "", [3]int{200, 200, 404}},
		{"bắt đầu task", "PATCH", "/api/v1/tasks/3", `{"status": "doing"}`, [3]int{200, 200, 404}},
		{"hoàn thành task", "PATCH", "/api/v1/tasks/3", `{"status": "done"}`, [3]int{200, 200, 404}},
		{"sửa title", "PATCH", "/api/v1/tasks/3", `{"title": "Viết test"}`, [3]int{200, 403, 404}},
		{"giao lại", "PATCH", "/api/v1/tasks/3", `{"assignee_id": 3}`, [3]int{200, 403, 404}},
		{"status kèm field khác", "PATCH", "/api/v1/tasks/3", `{"status": "done", "due_date": null}`, [3]int{200, 403, 404}},
		{"gắn tag", "POST", "/api/v1/tasks/3/tags", `{"name": "test"}`, [3]int{200, 403, 404}},
		{"gỡ tag", "DELETE", "/api/v1/tasks/3/tags/go", "", [3]int{204, 403, 404}},
		{"xóa task", "DELETE", "/api/v1/tasks/3", "", [3]int{204, 403, 404}},
	}
	for _, tt := range tests {
		for i, role := range roles {
			t.Run(tt.name+"/"+role.name, func(t *testing.T) {
				rec := sendAs(newAccessMux(t), role.userID, tt.method, tt.target, tt.body)
				if rec.Code != tt.want[i] {
					t.Errorf("%s %s: %d %s, muốn %d", tt.method, tt.target, rec.Code, rec.Body, tt.want[i])
				}
			})
		}
	}
}

// TestAccessHidesExistence: người ngoài nhận đúng response của tài nguyên không tồn tại.
func TestAccessHidesExistence(t *testing.T) {
	mux := newAccessMux(t)
	for _, tt := range []struct{ target, want string }{
		{"/api/v1/tasks/3", `{"error":"task #3: không tìm thấy"}`},
		{"/api/v1/projects/1", `{"error":"project #1: không tìm thấy"}`},
	} {
		if rec := sendAs(mux, 3, "GET", tt.target, ""); rec.Body.String() != tt.want+"\n" {
			t.Errorf("GET %s: %d %s, muốn %s", tt.target, rec.Code, rec.Body, tt.want)
		}
	}
	// project của người khác và project không tồn tại: cùng một lỗi 422
	for _, id := range []string{"1", "99"} {
		rec := sendAs(mux, 3, "POST", "/api/v1/tasks", `{"project_id": `+id+`, "title": "x"}`)
		want := `{"error":"dữ liệu không hợp lệ","fields":{"project_id":"không có project #` + id + `"}}` + "\n"
		if rec.Code != http.StatusUnprocessableEntity || rec.Body.String() != want {
			t.Errorf("POST task vào project #%s: %d %s", id, rec.Code, rec.Body)
		}
	}
}

// TestListVisibility: danh sách chỉ có những gì user được xem, lọc trong store chứ không phải trong handler.
func TestListVisibility(t *testing.T) {
	mux := newAccessMux(t)
	tests := []struct {
		userID   int
		target   string
		wantTask []int
	}{
		{1, "/api/v1/tasks", []int{1, 2, 3, 4}},
		{2, "/api/v1/tasks", []int{3}},
		{3, "/api/v1/tasks", []int{5}},
		{1, "/api/v1/tasks?status=todo&limit=1", []int{3}}, // lọc + phân trang vẫn đúng
		{2, "/api/v1/tasks?tag=go", []int{3}},
		{4, "/api/v1/tasks", []int{}}, // user chưa có gì
	}
	for _, tt := range tests {
		rec := sendAs(mux, tt.userID, "GET", tt.target, "")
		p := decodePage(t, rec)
		ids := []int{}
		for _, tk := range p.Data {
			ids = append(ids, tk.ID)
		}
		if !slices.Equal(ids, tt.wantTask) {
			t.Errorf("user #%d GET %s: task %v, muốn %v", tt.userID, tt.target, ids, tt.wantTask)
		}
	}

	for _, tt := range []struct {
		userID int
		want   string
	}{
		{1, `[{"id":1,"name":"TaskFlow API","owner_id":1},{"id":2,"name":"Website giới thiệu","owner_id":1}]`},
		{2, `[]`},
		{3, `[{"id":3,"name":"Dự án của Chi","owner_id":3}]`},
	} {
		if rec := sendAs(mux, tt.userID, "GET", "/api/v1/projects", ""); rec.Body.String() != tt.want+"\n" {
			t.Errorf("user #%d GET /projects: %s, muốn %s", tt.userID, rec.Body, tt.want)
		}
	}
}

// TestAssigneeMustExist: assignee_id phải là một user có thật.
func TestAssigneeMustExist(t *testing.T) {
	mux := newAccessMux(t)
	want := `{"error":"dữ liệu không hợp lệ","fields":{"assignee_id":"không có user #9"}}` + "\n"
	for _, req := range []struct{ method, target, body string }{
		{"POST", "/api/v1/tasks", `{"project_id": 1, "title": "Viết docs", "assignee_id": 9}`},
		{"PATCH", "/api/v1/tasks/1", `{"assignee_id": 9}`},
	} {
		rec := send(mux, req.method, req.target, req.body)
		if rec.Code != http.StatusUnprocessableEntity || rec.Body.String() != want {
			t.Errorf("%s %s: %d %s", req.method, req.target, rec.Code, rec.Body)
		}
	}
	if rec := send(mux, "PATCH", "/api/v1/tasks/1", `{"assignee_id": 2}`); rec.Code != http.StatusOK {
		t.Errorf("giao cho user #2: %d %s", rec.Code, rec.Body)
	}
}

// decodePage đọc body của response 200 thành một trang task.
func decodePage(t *testing.T, rec *httptest.ResponseRecorder) page[task.Task] {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var p page[task.Task]
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestWriteTasksNeedsUser:handler danh sách không bao giờ trả về task của mọi người
// khi request không có user (route bị đăng ký thiếu requireAuth).
func TestWriteTasksNeedsUser(t *testing.T) {
	rec := sendAs(listTasks(newTestStores(t).tasks), 0, "GET", "/api/v1/tasks", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("không có user: %d %s, muốn 401", rec.Code, rec.Body)
	}
}
