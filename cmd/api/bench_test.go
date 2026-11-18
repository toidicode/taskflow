package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/metrics"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// benchApp tạo app trong bộ nhớ có 1 project của user #1 với n task, mỗi task 3 tag, kèm metrics
// như khi chạy thật. Log request vẫn được mã hóa JSON nhưng ghi vào io.Discard.
func benchApp(b *testing.B, n int) http.Handler {
	b.Helper()
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(old) })

	ctx := context.Background()
	a := memoryApp(nil)
	a.metrics = metrics.New()
	if err := a.projects.Add(ctx, project.New("TaskFlow API", 1)); err != nil {
		b.Fatal(err)
	}
	due := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	for i := range n {
		t, err := task.New(1, fmt.Sprintf("Task số %d", i+1))
		if err != nil {
			b.Fatal(err)
		}
		t.Description = "Mô tả ngắn của task để body giống dữ liệu thật"
		t.DueDate = due
		t.Tags = []string{"backend", "go", "api"}
		if err := a.tasks.Add(ctx, t); err != nil {
			b.Fatal(err)
		}
	}
	return a.handler(testTokens, "", nil)
}

// BenchmarkListTasks đo cả đường đi của GET /api/v1/tasks?limit=100: middleware, xác thực,
// đọc store, mã hóa JSON (một trang 100 task trong 1000 task).
func BenchmarkListTasks(b *testing.B) {
	h := benchApp(b, 1000)
	token, err := testTokens.Issue(1)
	if err != nil {
		b.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/tasks?limit=100", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
	}
}
