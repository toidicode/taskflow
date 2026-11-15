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

// send gửi request tới h, body khác rỗng thì gửi kèm Content-Type: application/json.
func send(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
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
		{"tất cả project", "GET", "/api/v1/projects", 200, `{"id":2,"name":"Website giới thiệu","owner_id":1}]`, ""},
		{"một project", "GET", "/api/v1/projects/1", 200, `{"id":1,"name":"TaskFlow API","owner_id":1}` + "\n", ""},
		{"task của project", "GET", "/api/v1/projects/2/tasks", 200, `[{"id":4,"project_id":2,"title":"Viết tài liệu","status":"todo"}]`, ""},
		{"task của project, lọc status", "GET", "/api/v1/projects/1/tasks?status=done", 200, `"title":"Thiết kế database","status":"done","completed_at":"`, ""},
		{"lọc không có task nào", "GET", "/api/v1/projects/2/tasks?status=done", 200, "[]\n", ""},
		{"project không có", "GET", "/api/v1/projects/9/tasks", 404, `{"error":"project #9: không tìm thấy"}`, ""},
		{"tất cả task", "GET", "/api/v1/tasks", 200, `"title":"Viết tài liệu","status":"todo"}]`, ""},
		{"lọc status", "GET", "/api/v1/tasks?status=doing", 200, `[{"id":2,"project_id":1,"title":"Viết API danh sách task","status":"doing"}]`, ""},
		{"status sai", "GET", "/api/v1/tasks?status=xong", 400, `{"error":"trạng thái \"xong\" không hợp lệ`, ""},
		{"một task", "GET", "/api/v1/tasks/2", 200, `{"id":2,"project_id":1,"title":"Viết API danh sách task","status":"doing"}` + "\n", ""},
		{"HEAD dùng route GET", "HEAD", "/api/v1/tasks/2", 200, "", ""},
		{"id không phải số", "GET", "/api/v1/tasks/abc", 400, `{"error":"id \"abc\" không hợp lệ`, ""},
		{"task không có", "GET", "/api/v1/tasks/99", 404, `{"error":"task #99: không tìm thấy"}`, ""},
		{"xóa task không có", "DELETE", "/api/v1/tasks/99", 404, `{"error":"task #99: không tìm thấy"}`, ""},
		{"xóa project còn task", "DELETE", "/api/v1/projects/1", 409, `{"error":"project #1 vẫn còn task`, ""},
		{"sai method", "PUT", "/api/v1/tasks", 405, "Method Not Allowed", "GET, HEAD, POST"},
		{"sai method có id", "PUT", "/api/v1/tasks/2", 405, "Method Not Allowed", "DELETE, GET, HEAD, PATCH"},
		{"không có route", "GET", "/api/v1/users", 404, "404 page not found", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := send(mux, tt.method, tt.target, "")

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

func TestReadJSONErrors(t *testing.T) {
	mux := newMux(newTestStores(t))

	tests := []struct {
		name     string
		body     string
		wantCode int
		wantErr  string // body lỗi phải chứa chuỗi này
	}{
		{"body rỗng", "", 400, "body rỗng"},
		{"sai cú pháp", `{"title": "Viết API",}`, 400, "JSON sai cú pháp ở byte thứ 24"},
		{"bị cắt cụt", `{"title": "Viết API"`, 400, "JSON bị cắt cụt"},
		{"sai kiểu", `{"project_id": "1"}`, 400, `field \"project_id\" sai kiểu, cần int`},
		{"không phải object", `[1, 2]`, 400, "body phải là một JSON object"},
		{"field lạ", `{"projectId": 1}`, 400, `không hỗ trợ field \"projectId\"`},
		{"thừa dữ liệu", `{"project_id": 1, "title": "a"} {}`, 400, "chỉ được chứa một JSON object"},
		{"quá lớn", `{"title": "` + strings.Repeat("a", maxBodyBytes) + `"}`, 413, "body vượt quá 1048576 byte"},
		{"lỗi validate", `{"project_id": 0, "title": " "}`, 400, `project_id: phải lớn hơn 0\ntitle: không được để trống`},
		{"project không có", `{"project_id": 9, "title": "Viết API"}`, 400, "project_id: không có project #9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json; charset=utf-8")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, muốn %d", rec.Code, tt.wantCode)
			}
			if body := rec.Body.String(); !strings.Contains(body, tt.wantErr) {
				t.Errorf("body = %q, muốn chứa %q", body, tt.wantErr)
			}
		})
	}

	t.Run("sai Content-Type", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("status = %d, muốn 415", rec.Code)
		}
	})
}

func TestTaskCRUD(t *testing.T) {
	mux := newMux(newTestStores(t))

	// các bước chạy lần lượt trên cùng một store
	rec := send(mux, "POST", "/api/v1/tasks", `{"project_id": 2, "title": " Viết README ", "due_date": "2026-11-30T17:00:00+07:00"}`)
	if rec.Code != http.StatusCreated || rec.Header().Get("Location") != "/api/v1/tasks/5" {
		t.Fatalf("POST: %d, Location %q, body %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if want := `{"id":5,"project_id":2,"title":"Viết README","status":"todo","due_date":"2026-11-30T17:00:00+07:00"}` + "\n"; rec.Body.String() != want {
		t.Errorf("POST body = %s, muốn %s", rec.Body, want)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}

	rec = send(mux, "PATCH", "/api/v1/tasks/5", `{"status": "done", "assignee_id": 2}`)
	if body := rec.Body.String(); rec.Code != 200 || !strings.Contains(body, `"status":"done","assignee_id":2,`) ||
		!strings.Contains(body, `"completed_at":"`) || !strings.Contains(body, `"title":"Viết README"`) {
		t.Errorf("PATCH: %d %s", rec.Code, body)
	}
	rec = send(mux, "PATCH", "/api/v1/tasks/5", `{"status": "xong"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `trạng thái \"xong\" không hợp lệ`) {
		t.Errorf("PATCH status sai: %d %s", rec.Code, rec.Body)
	}
	rec = send(mux, "PATCH", "/api/v1/tasks/5", `{"title": ""}`)
	if rec.Code != 400 {
		t.Errorf("PATCH title rỗng: %d %s", rec.Code, rec.Body)
	}

	rec = send(mux, "DELETE", "/api/v1/tasks/5", "")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("DELETE: %d %q", rec.Code, rec.Body)
	}
	if rec = send(mux, "GET", "/api/v1/tasks/5", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET sau DELETE: %d", rec.Code)
	}
}

func TestProjectCRUD(t *testing.T) {
	mux := newMux(newTestStores(t))

	rec := send(mux, "POST", "/api/v1/projects", `{"name": "App mobile", "owner_id": 2}`)
	if rec.Code != http.StatusCreated || rec.Header().Get("Location") != "/api/v1/projects/3" ||
		rec.Body.String() != `{"id":3,"name":"App mobile","owner_id":2}`+"\n" {
		t.Fatalf("POST: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}

	rec = send(mux, "PUT", "/api/v1/projects/3", `{"name": "App mobile v2", "description": "iOS và Android", "owner_id": 2}`)
	if want := `{"id":3,"name":"App mobile v2","description":"iOS và Android","owner_id":2}` + "\n"; rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("PUT: %d %s", rec.Code, rec.Body)
	}
	if rec = send(mux, "PUT", "/api/v1/projects/9", `{"name": "x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("PUT project không có: %d", rec.Code)
	}

	if rec = send(mux, "DELETE", "/api/v1/projects/3", ""); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE: %d %s", rec.Code, rec.Body)
	}
	if rec = send(mux, "GET", "/api/v1/projects/3", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET sau DELETE: %d", rec.Code)
	}
}

func TestGetTaskPathValue(t *testing.T) {
	_, tasks := newTestStores(t)

	// gọi thẳng handler, không qua ServeMux nên phải tự đặt wildcard {id}
	req := httptest.NewRequest(http.MethodGet, "/bat-ky", nil)
	req.SetPathValue("id", "3")
	rec := httptest.NewRecorder()
	getTask(tasks).ServeHTTP(rec, req)

	if want := `{"id":3,"project_id":1,"title":"Viết test cho API","status":"todo"}` + "\n"; rec.Body.String() != want {
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
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestWriteJSONError(t *testing.T) {
	// Status 7 không có tên nên MarshalText trả lỗi: writeJSON phải đổi sang 500
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, &task.Task{ID: 1, Status: 7})
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != `{"error":"lỗi máy chủ"}`+"\n" {
		t.Errorf("writeJSON lỗi: %d %q", rec.Code, rec.Body)
	}
}
