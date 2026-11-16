package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/storage"
)

// testBoundary là boundary của body multipart do uploadBody tạo, sendAs nhận ra body này nhờ nó.
const testBoundary = "taskflow-test-boundary"

// uploadBody tạo body multipart/form-data có một file tên filename với nội dung content trong field.
func uploadBody(field, filename, content string) string {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	w.SetBoundary(testBoundary)
	part, _ := w.CreateFormFile(field, filename) // Content-Type của part: application/octet-stream
	io.WriteString(part, content)
	w.Close()
	return b.String()
}

// newUploadStores tạo store mẫu với storage trong thư mục <tạm>/uploads, trả về cả thư mục đó.
func newUploadStores(t *testing.T) (stores, string) {
	t.Helper()
	s := newTestStores(t)
	dir := filepath.Join(t.TempDir(), "uploads")
	files, err := storage.NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	s.files = files
	return s, dir
}

// storedFiles trả về tên các file trong dir.
func storedFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// pngData là một "ảnh PNG": 8 byte chữ ký PNG là đủ để DetectContentType nhận ra.
var pngData = "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 1000)

func TestUploadDownload(t *testing.T) {
	s, dir := newUploadStores(t)
	mux := newMux(s, testTokens)

	rec := send(mux, "POST", "/api/v1/tasks/1/attachments", uploadBody("file", "Ảnh chụp màn hình.png", pngData))
	if rec.Code != http.StatusCreated || rec.Header().Get("Location") != "/api/v1/attachments/1" {
		t.Fatalf("upload: %d %s, Location %q", rec.Code, rec.Body, rec.Header().Get("Location"))
	}
	var a attachment.Attachment
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if a.Name != "Ảnh chụp màn hình.png" || a.ContentType != "image/png" || a.Size != int64(len(pngData)) || a.UploadedBy != 1 {
		t.Errorf("attachment = %+v", a)
	}
	// trên đĩa chỉ có một file, tên ngẫu nhiên 26 ký tự chứ không phải tên client gửi
	if files := storedFiles(t, dir); len(files) != 1 || len(files[0]) != 26 {
		t.Errorf("file trên đĩa: %q", files)
	}

	rec = send(mux, "GET", "/api/v1/attachments/1", "")
	if rec.Code != http.StatusOK || rec.Body.String() != pngData {
		t.Fatalf("download: %d, %d byte", rec.Code, rec.Body.Len())
	}
	h := rec.Header()
	if h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("header: %v", h)
	}
	_, params, err := mime.ParseMediaType(h.Get("Content-Disposition"))
	if err != nil || params["filename"] != "Ảnh chụp màn hình.png" {
		t.Errorf("Content-Disposition = %q (%v)", h.Get("Content-Disposition"), err)
	}

	// http.ServeContent hỗ trợ Range: tải tiếp file đang tải dở
	req := httptest.NewRequest("GET", "/api/v1/attachments/1", nil)
	authorize(req, 1)
	req.Header.Set("Range", "bytes=0-7")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != pngData[:8] {
		t.Errorf("Range: %d %q", rec.Code, rec.Body)
	}

	rec = send(mux, "GET", "/api/v1/tasks/1/attachments", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"Ảnh chụp màn hình.png","content_type":"image/png"`) {
		t.Errorf("danh sách: %d %s", rec.Code, rec.Body)
	}
}

func TestUploadErrors(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode int
		wantBody string // body phải chứa chuỗi này
	}{
		{"không phải multipart", `{"file": "a.txt"}`, 415, "multipart/form-data"},
		{"thiếu field file", uploadBody("tep", "a.txt", "xin chào"), 422, `"file":"cần một file trong field \"file\""`},
		{"file rỗng", uploadBody("file", "a.txt", ""), 422, `"file":"file rỗng"`},
		{"HTML đặt tên .png", uploadBody("file", "anh.png", "<!DOCTYPE html><script>alert(1)</script>"), 422,
			"không nhận file text/html; charset=utf-8"},
		{"file chạy được", uploadBody("file", "tool.pdf", "\x7fELF\x02\x01\x01"), 422, "không nhận file application/octet-stream"},
		{"body bị cắt cụt", strings.TrimSuffix(uploadBody("file", "a.txt", "xin chào"), "--"+testBoundary+"--\r\n"), 400,
			"body multipart không hợp lệ"},
		{"file quá 10 MB", uploadBody("file", "big.txt", strings.Repeat("a", maxUploadBytes+1)), 413, "file tối đa 10 MB"},
		{"body quá lớn", uploadBody("file", "big.txt", strings.Repeat("a", maxUploadBody)), 413, "file tối đa 10 MB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := newUploadStores(t)
			rec := send(newMux(s, testTokens), "POST", "/api/v1/tasks/1/attachments", tt.body)
			if rec.Code != tt.wantCode || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("%d %s, muốn %d %s", rec.Code, rec.Body, tt.wantCode, tt.wantBody)
			}
			if files := storedFiles(t, dir); len(files) != 0 {
				t.Errorf("còn file trên đĩa: %q", files)
			}
		})
	}
}

// TestUploadTooLargeStream: client không gửi Content-Length (chunked), body chỉ bị
// http.MaxBytesReader chặn khi đang đọc giữa chừng.
func TestUploadTooLargeStream(t *testing.T) {
	s, dir := newUploadStores(t)
	body := uploadBody("file", "big.txt", strings.Repeat("a", maxUploadBody))
	req := httptest.NewRequest("POST", "/api/v1/tasks/1/attachments", io.MultiReader(strings.NewReader(body)))
	req.ContentLength = -1 // không biết trước kích thước
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+testBoundary)
	authorize(req, 1)
	rec := httptest.NewRecorder()
	newMux(s, testTokens).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("%d %s, muốn 413", rec.Code, rec.Body)
	}
	if files := storedFiles(t, dir); len(files) != 0 {
		t.Errorf("còn file trên đĩa: %q", files)
	}
}

// TestUploadFileName: tên file của client chỉ được lưu làm thông tin, không bao giờ thành đường dẫn.
func TestUploadFileName(t *testing.T) {
	s, dir := newUploadStores(t)
	mux := newMux(s, testTokens)
	tests := []struct{ filename, want string }{
		{"../../etc/passwd", "passwd"}, // Part.FileName() đã bỏ phần thư mục
		{`..\..\x.txt`, ".._.._x.txt"}, // trên Linux \ không phải dấu phân cách: CleanName thay bằng _
		{"..", "file"},
	}
	for _, tt := range tests {
		rec := send(mux, "POST", "/api/v1/tasks/1/attachments", uploadBody("file", tt.filename, "xin chào"))
		var a attachment.Attachment
		json.Unmarshal(rec.Body.Bytes(), &a)
		if rec.Code != http.StatusCreated || a.Name != tt.want {
			t.Errorf("filename %q: %d %s, muốn name %q", tt.filename, rec.Code, rec.Body, tt.want)
		}
	}
	// mọi file đều nằm trong dir với tên ngẫu nhiên, ngoài dir không có gì mới
	for _, name := range storedFiles(t, dir) {
		if len(name) != 26 {
			t.Errorf("file %q trong thư mục upload", name)
		}
	}
	if outside := storedFiles(t, filepath.Dir(dir)); len(outside) != 1 {
		t.Errorf("ngoài thư mục upload có: %q", outside)
	}
}

func TestDeleteAttachmentFiles(t *testing.T) {
	s, dir := newUploadStores(t)
	mux := newMux(s, testTokens)
	upload := func(taskID string) {
		t.Helper()
		if rec := send(mux, "POST", "/api/v1/tasks/"+taskID+"/attachments", uploadBody("file", "a.txt", "xin chào")); rec.Code != http.StatusCreated {
			t.Fatalf("upload: %d %s", rec.Code, rec.Body)
		}
	}

	// xóa một file: mất cả hàng lẫn file trên đĩa
	upload("1")
	if rec := send(mux, "DELETE", "/api/v1/attachments/1", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("xóa file: %d %s", rec.Code, rec.Body)
	}
	if rec := send(mux, "GET", "/api/v1/attachments/1", ""); rec.Code != http.StatusNotFound {
		t.Errorf("tải file đã xóa: %d", rec.Code)
	}
	if files := storedFiles(t, dir); len(files) != 0 {
		t.Errorf("còn file sau khi xóa: %q", files)
	}

	// xóa task: file của task đó cũng bị xóa, file của task khác thì không
	upload("1")
	upload("1")
	upload("2")
	if rec := send(mux, "DELETE", "/api/v1/tasks/1", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("xóa task: %d %s", rec.Code, rec.Body)
	}
	if files := storedFiles(t, dir); len(files) != 1 {
		t.Errorf("sau khi xóa task #1 còn %d file, muốn 1 (của task #2)", len(files))
	}
	if list, _ := s.attachments.List(context.Background(), 2); len(list) != 1 {
		t.Errorf("file của task #2: %+v", list)
	}
}
