package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/toidicode/taskflow/internal/task"
)

func TestRoutes(t *testing.T) {
	store := task.NewMemoryStore()
	if err := seed(store); err != nil {
		t.Fatal(err)
	}
	mux := newMux(store)

	tests := []struct {
		name     string
		method   string
		target   string
		wantCode int
		wantBody string // body phải chứa chuỗi này
	}{
		{"health", http.MethodGet, "/health", http.StatusOK, "ok\n"},
		{"tất cả task", http.MethodGet, "/api/v1/tasks", http.StatusOK, "#4 [p2] Viết tài liệu (todo)\n"},
		{"lọc status", http.MethodGet, "/api/v1/tasks?status=doing", http.StatusOK, "#2 [p1] Viết API danh sách task (doing)\n"},
		{"status sai", http.MethodGet, "/api/v1/tasks?status=xong", http.StatusBadRequest, `trạng thái "xong" không hợp lệ`},
		{"sai method", http.MethodPost, "/api/v1/tasks", http.StatusMethodNotAllowed, "chỉ hỗ trợ GET"},
		{"không có route", http.MethodGet, "/api/v1/projects", http.StatusNotFound, "404 page not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, muốn %d", rec.Code, tt.wantCode)
			}
			if body := rec.Body.String(); !strings.Contains(body, tt.wantBody) {
				t.Errorf("body = %q, muốn chứa %q", body, tt.wantBody)
			}
		})
	}
}

func TestListTasksHeaders(t *testing.T) {
	store := task.NewMemoryStore()
	if err := seed(store); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	listTasks(store).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tasks?status=todo", nil))

	if got := rec.Header().Get("X-Total-Count"); got != "2" {
		t.Errorf("X-Total-Count = %q, muốn %q", got, "2")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}
