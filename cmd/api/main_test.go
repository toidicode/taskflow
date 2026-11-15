package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// newTestStores tạo hai store đã có dữ liệu mẫu như khi chạy server.
func newTestStores(t *testing.T) (*project.MemoryStore, *task.MemoryStore) {
	t.Helper()
	projects, tasks := project.NewMemoryStore(), task.NewMemoryStore()
	if err := seed(projects, tasks); err != nil {
		t.Fatal(err)
	}
	return projects, tasks
}

func TestRoutes(t *testing.T) {
	mux := newMux(newTestStores(t))

	tests := []struct {
		name      string
		method    string
		target    string
		wantCode  int
		wantBody  string // body phải chứa chuỗi này
		wantAllow string // header Allow, chỉ có khi trả 405
	}{
		{"health", "GET", "/health", 200, "ok\n", ""},
		{"tất cả project", "GET", "/api/v1/projects", 200, "#2 Website giới thiệu (owner 1)\n", ""},
		{"một project", "GET", "/api/v1/projects/1", 200, "#1 TaskFlow API (owner 1)\n", ""},
		{"task của project", "GET", "/api/v1/projects/2/tasks", 200, "#4 [p2] Viết tài liệu (todo)\n", ""},
		{"task của project, lọc status", "GET", "/api/v1/projects/1/tasks?status=done", 200, "#1 [p1] Thiết kế database (done)\n", ""},
		{"project không có", "GET", "/api/v1/projects/9/tasks", 404, "project #9: không tìm thấy", ""},
		{"tất cả task", "GET", "/api/v1/tasks", 200, "#4 [p2] Viết tài liệu (todo)\n", ""},
		{"lọc status", "GET", "/api/v1/tasks?status=doing", 200, "#2 [p1] Viết API danh sách task (doing)\n", ""},
		{"status sai", "GET", "/api/v1/tasks?status=xong", 400, `trạng thái "xong" không hợp lệ`, ""},
		{"một task", "GET", "/api/v1/tasks/2", 200, "#2 [p1] Viết API danh sách task (doing)\n", ""},
		{"HEAD dùng route GET", "HEAD", "/api/v1/tasks/2", 200, "", ""},
		{"id không phải số", "GET", "/api/v1/tasks/abc", 400, `id "abc" không hợp lệ`, ""},
		{"id âm", "GET", "/api/v1/tasks/-1", 400, `id "-1" không hợp lệ`, ""},
		{"task không có", "GET", "/api/v1/tasks/99", 404, "task #99: không tìm thấy", ""},
		{"sai method", "POST", "/api/v1/tasks", 405, "Method Not Allowed", "GET, HEAD"},
		{"sai method có id", "DELETE", "/api/v1/tasks/2", 405, "Method Not Allowed", "GET, HEAD"},
		{"không có route", "GET", "/api/v1/users", 404, "404 page not found", ""},
		{"thừa dấu /", "GET", "/api/v1/tasks/2/", 404, "404 page not found", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, nil))

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, muốn %d", rec.Code, tt.wantCode)
			}
			if body := rec.Body.String(); !strings.Contains(body, tt.wantBody) {
				t.Errorf("body = %q, muốn chứa %q", body, tt.wantBody)
			}
			if got := rec.Header().Get("Allow"); got != tt.wantAllow {
				t.Errorf("Allow = %q, muốn %q", got, tt.wantAllow)
			}
		})
	}
}

func TestGetTaskPathValue(t *testing.T) {
	_, tasks := newTestStores(t)

	// gọi thẳng handler, không qua ServeMux nên phải tự đặt wildcard {id}
	req := httptest.NewRequest(http.MethodGet, "/bat-ky", nil)
	req.SetPathValue("id", "3")
	rec := httptest.NewRecorder()
	getTask(tasks).ServeHTTP(rec, req)

	if want := "#3 [p1] Viết test cho API (todo)\n"; rec.Body.String() != want {
		t.Errorf("body = %q, muốn %q", rec.Body.String(), want)
	}
}

func TestListTasksHeaders(t *testing.T) {
	_, tasks := newTestStores(t)
	rec := httptest.NewRecorder()
	listTasks(tasks).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tasks?status=todo", nil))

	if got := rec.Header().Get("X-Total-Count"); got != "2" {
		t.Errorf("X-Total-Count = %q, muốn %q", got, "2")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}
