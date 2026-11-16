package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/storage"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode) // không in route và cảnh báo của chế độ debug ra output của go test
	m.Run()
}

// Các helper nhỏ dưới đây giống bản trong internal/httpapi: test của hai package không dùng chung được
// hàm trong file _test.go của nhau.

// testTokens phát và kiểm tra token trong test, secret này chỉ dùng cho test.
var testTokens = auth.NewTokens([]byte("secret-chi-dung-trong-test-0123456789"), 15*time.Minute)

// newTestApp tạo app với store trong bộ nhớ, việc nền và recordNotifier, chưa có dữ liệu nào.
func newTestApp(t *testing.T) *app {
	t.Helper()
	return withTestJobs(t, memoryApp(newTestFiles(t)))
}

// newTestFiles tạo storage trong thư mục tạm, go test tự xóa khi test xong.
func newTestFiles(t *testing.T) *storage.Disk {
	t.Helper()
	files, err := storage.NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	return files
}

// withTestJobs gắn vào a một dispatcher một worker (dừng khi test xong) và một recordNotifier.
func withTestJobs(t *testing.T, a *app) *app {
	t.Helper()
	a.jobs = jobs.New(jobs.Options{Workers: 1, Logger: slog.New(slog.DiscardHandler)})
	a.jobs.Start()
	t.Cleanup(func() { a.jobs.Stop(context.Background()) })
	a.notifier = &recordNotifier{}
	return a
}

// recordNotifier ghi lại các thông báo thay vì gửi, dùng được từ nhiều goroutine.
type recordNotifier struct {
	mu   sync.Mutex
	sent []notify.TaskDone
}

func (r *recordNotifier) TaskDone(ctx context.Context, n notify.TaskDone) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, n)
	return nil
}

// captureLog thay logger mặc định bằng logger JSON ghi vào buffer cho tới hết test.
// Test dùng captureLog không được chạy song song vì logger mặc định là biến dùng chung.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// logEntries đọc từng dòng log JSON trong buf thành map.
func logEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	dec := json.NewDecoder(buf)
	for dec.More() {
		var e map[string]any
		if err := dec.Decode(&e); err != nil {
			t.Fatalf("log không phải JSON: %v", err)
		}
		entries = append(entries, e)
	}
	return entries
}

// testBoundary là boundary cố định của body multipart trong test.
const testBoundary = "taskflow-test-boundary"

// uploadBody tạo body multipart/form-data có một file trong field field.
func uploadBody(field, filename, content string) string {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	w.SetBoundary(testBoundary)
	part, _ := w.CreateFormFile(field, filename)
	io.WriteString(part, content)
	w.Close()
	return b.String()
}

// page là body của các API trả về danh sách có phân trang.
type page[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor"`
}

// tokenResponse là body trả về khi đăng nhập thành công (chỉ field test cần).
type tokenResponse struct {
	AccessToken string `json:"access_token"`
}
