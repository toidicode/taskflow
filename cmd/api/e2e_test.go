package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/testdb"
)

// client gọi API qua HTTP thật, như một ứng dụng frontend, với token của một user.
type client struct {
	t     *testing.T
	srv   *httptest.Server
	token string // rỗng: không gửi Authorization
}

// do gửi request tới server, kiểm tra status là want rồi trả về body của response.
func (c *client) do(method, path, contentType string, body io.Reader, want int) []byte {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.srv.URL+path, body)
	if err != nil {
		c.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.srv.Client().Do(req) // client của httptest.Server, tự đóng kết nối khi server đóng
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: status %d, muốn %d, body %s", method, path, resp.StatusCode, want, data)
	}
	if resp.Header.Get("X-Request-ID") == "" { // có nghĩa là request đã đi qua middleware thật
		c.t.Errorf("%s %s: thiếu header X-Request-ID", method, path)
	}
	return data
}

// call gửi in dưới dạng JSON (nil là không có body) rồi giải mã response thành T.
func call[T any](c *client, method, path string, in any, want int) T {
	c.t.Helper()
	var body io.Reader
	contentType := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			c.t.Fatal(err)
		}
		body, contentType = bytes.NewReader(b), "application/json"
	}
	var out T
	if data := c.do(method, path, contentType, body, want); len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			c.t.Fatalf("%s %s: response không đọc được thành %T: %v", method, path, out, err)
		}
	}
	return out
}

// signUp đăng ký user rồi đăng nhập, trả về client mang access token của user đó.
func signUp(t *testing.T, srv *httptest.Server, name, email, password string) *client {
	t.Helper()
	anon := &client{t: t, srv: srv}
	call[struct{}](anon, "POST", "/api/v1/auth/register",
		map[string]string{"email": email, "name": name, "password": password}, http.StatusCreated)
	tok := call[tokenResponse](anon, "POST", "/api/v1/auth/login",
		map[string]string{"email": email, "password": password}, http.StatusOK)
	return &client{t: t, srv: srv, token: tok.AccessToken}
}

// TestEndToEnd chạy một kịch bản đầy đủ qua HTTP thật: server httptest, http.Client và mọi middleware.
// Cùng kịch bản chạy với store trong bộ nhớ và với PostgreSQL (khi có TASKFLOW_TEST_DATABASE_URL).
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("test end-to-end chậm (bcrypt), bỏ qua khi chạy go test -short")
	}
	backends := []struct {
		name string
		open func(t *testing.T) *app
	}{
		{"memory", func(t *testing.T) *app { return memoryApp(newTestFiles(t)) }},
		{"postgres", func(t *testing.T) *app { return dbApp(testdb.New(t), newTestFiles(t)) }},
	}
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			logs := captureLog(t) // log của server không lẫn vào output của go test
			s := withTestJobs(t, b.open(t))
			srv := httptest.NewServer(s.handler(testTokens, "http://localhost:5173", nil))
			t.Cleanup(func() { // đăng ký trước nên chạy sau srv.Close
				if t.Failed() {
					t.Logf("log của server:\n%s", logs)
				}
			})
			t.Cleanup(srv.Close)

			an := signUp(t, srv, "An", "an@example.com", "mat-khau-cua-an")
			binh := signUp(t, srv, "Bình", "binh@example.com", "mat-khau-cua-binh")

			p := call[project.Project](an, "POST", "/api/v1/projects", map[string]any{"name": "TaskFlow API"}, http.StatusCreated)
			tk := call[task.Task](an, "POST", "/api/v1/tasks",
				map[string]any{"project_id": p.ID, "title": "Viết test end-to-end", "assignee_id": 2}, http.StatusCreated)

			// Bình (người được giao) đính kèm file rồi chuyển task sang done
			const content = "Báo cáo: test đã chạy qua HTTP thật\n"
			data := binh.do("POST", fmt.Sprintf("/api/v1/tasks/%d/attachments", tk.ID),
				"multipart/form-data; boundary="+testBoundary, strings.NewReader(uploadBody("file", "bao-cao.txt", content)),
				http.StatusCreated)
			var a attachment.Attachment
			if err := json.Unmarshal(data, &a); err != nil || a.Name != "bao-cao.txt" || a.UploadedBy != 2 {
				t.Fatalf("upload: %+v, %v", a, err)
			}
			done := call[task.Task](binh, "PATCH", fmt.Sprintf("/api/v1/tasks/%d", tk.ID), map[string]any{"status": "done"}, http.StatusOK)
			if done.Status != task.StatusDone || done.CompletedAt.IsZero() {
				t.Errorf("sau PATCH: %+v", done)
			}

			// An (chủ project) thấy task đã xong và tải được đúng file Bình gửi
			list := call[page[task.Task]](an, "GET", "/api/v1/tasks?status=done", nil, http.StatusOK)
			if len(list.Data) != 1 || list.Data[0].ID != tk.ID {
				t.Errorf("task done của An: %+v", list.Data)
			}
			if got := an.do("GET", fmt.Sprintf("/api/v1/attachments/%d", a.ID), "", nil, http.StatusOK); string(got) != content {
				t.Errorf("tải file: %q, muốn %q", got, content)
			}

			// chờ worker gửi xong thông báo bằng Stop, không đoán thời gian bằng time.Sleep
			if err := s.jobs.Stop(t.Context()); err != nil {
				t.Fatal(err)
			}
			sent := s.notifier.(*recordNotifier).sent
			if len(sent) != 1 || sent[0].TaskID != tk.ID || sent[0].DoneBy != 2 || !slices.Equal(sent[0].To, []int{1}) {
				t.Errorf("thông báo: %+v, muốn một thông báo task #%d cho An", sent, tk.ID)
			}

			// server thật hỗ trợ đặt deadline cho từng request (Bài 35): không có cảnh báo nào trong log
			for _, e := range logEntries(t, logs) {
				if e["level"] != "INFO" {
					t.Errorf("log %v: %v", e["level"], e["msg"])
				}
			}
		})
	}
}

// TestTLS chạy API sau HTTPS với HTTP/2, chứng chỉ tự ký do httptest tạo.
func TestTLS(t *testing.T) {
	captureLog(t)
	srv := httptest.NewUnstartedServer(newTestApp(t).handler(testTokens, "http://localhost:5173", nil))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/health") // client đã tin chứng chỉ của server
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Proto != "HTTP/2.0" || resp.TLS == nil {
		t.Errorf("status %d, %s, TLS %v", resp.StatusCode, resp.Proto, resp.TLS != nil)
	}
}
