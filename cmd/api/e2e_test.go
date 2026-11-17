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
	"time"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/cache"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/ratelimit"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/testdb"
	"github.com/toidicode/taskflow/internal/testredis"
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

// riêng đăng nhập theo email chỉ 2 lần mỗi giờ để thử 429 (với 2 lần mỗi phút, token hồi lại sau 30 giây:
// một lần chạy -race dưới tải nặng từng thấy lần đoán thứ ba được đi qua).
var e2eLimits = Config{
	RateAuthIP:     ratelimit.Limit{N: 10, Period: time.Minute},
	RateLoginEmail: ratelimit.Limit{N: 2, Period: time.Hour},
	RateUser:       ratelimit.Limit{N: 100, Period: time.Minute},
}

// TestEndToEnd chạy một kịch bản đầy đủ qua HTTP thật: server httptest, http.Client và mọi middleware.
// Cùng kịch bản chạy với store trong bộ nhớ, với PostgreSQL (khi có TASKFLOW_TEST_DATABASE_URL),
// và hai bản có cache project: cache trong bộ nhớ, Redis thật (khi có thêm TASKFLOW_TEST_REDIS_URL).
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
		{"memory+cache", func(t *testing.T) *app {
			a := memoryApp(newTestFiles(t))
			a.projects = project.NewCachedStore(a.projects, cache.NewMemory(), project.CacheOptions{})
			return a
		}},
		{"postgres+redis", func(t *testing.T) *app { // cache và rate limit đều trong Redis
			rc, prefix := testredis.New(t)
			a := dbApp(testdb.New(t), newTestFiles(t))
			a.projects = project.NewCachedStore(a.projects, rc, project.CacheOptions{Prefix: prefix})
			a.rateLimit = rateLimit(nil, e2eLimits)
			a.rateLimit.Limiter = ratelimit.NewRedis(rc.Client(), prefix+"ratelimit:")
			return a
		}},
	}
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			logs := captureLog(t) // log của server không lẫn vào output của go test
			s := withTestJobs(t, b.open(t))
			if s.rateLimit.Limiter == nil {
				s.rateLimit = rateLimit(nil, e2eLimits) // đếm trong bộ nhớ
			}
			srv := httptest.NewServer(s.handler(testTokens, "http://localhost:5173", nil))
			t.Cleanup(func() { // đăng ký trước nên chạy sau srv.Close
				if t.Failed() {
					t.Logf("log của server:\n%s", logs)
				}
			})
			t.Cleanup(srv.Close)
			checkContract(t, srv) // mọi request, response bên dưới phải khớp api/openapi.yaml

			an := signUp(t, srv, "An", "an@example.com", "mat-khau-cua-an")
			binh := signUp(t, srv, "Bình", "binh@example.com", "mat-khau-cua-binh")

			p := call[project.Project](an, "POST", "/api/v1/projects", map[string]any{"name": "TaskFlow API"}, http.StatusCreated)

			// đọc danh sách (bản có cache: lần sau trúng cache), rồi tạo, đổi tên, xóa project:
			// mỗi lần ghi phải thấy ngay ở lần đọc kế tiếp
			names := func() []string {
				var out []string
				for _, x := range call[[]project.Project](an, "GET", "/api/v1/projects", nil, http.StatusOK) {
					out = append(out, x.Name)
				}
				return out
			}
			names()
			p2 := call[project.Project](an, "POST", "/api/v1/projects", map[string]any{"name": "Website"}, http.StatusCreated)
			call[project.Project](an, "PUT", fmt.Sprintf("/api/v1/projects/%d", p2.ID), map[string]any{"name": "Website mới"}, http.StatusOK)
			if got := names(); !slices.Equal(got, []string{"TaskFlow API", "Website mới"}) {
				t.Errorf("sau khi tạo và đổi tên: %q", got)
			}
			if got := call[project.Project](an, "GET", fmt.Sprintf("/api/v1/projects/%d", p2.ID), nil, http.StatusOK); got.Name != "Website mới" {
				t.Errorf("GET project #%d: %q", p2.ID, got.Name)
			}
			an.do("DELETE", fmt.Sprintf("/api/v1/projects/%d", p2.ID), "", nil, http.StatusNoContent)
			an.do("GET", fmt.Sprintf("/api/v1/projects/%d", p2.ID), "", nil, http.StatusNotFound)
			if got := names(); !slices.Equal(got, []string{"TaskFlow API"}) {
				t.Errorf("sau khi xóa: %q", got)
			}
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

			// dò mật khẩu của một email: hai lần sai thì 401, lần thứ ba bị chặn theo email
			anon := &client{t: t, srv: srv}
			guess := map[string]string{"email": "khach@example.com", "password": "doan-mo"}
			call[struct{}](anon, "POST", "/api/v1/auth/login", guess, http.StatusUnauthorized)
			call[struct{}](anon, "POST", "/api/v1/auth/login", guess, http.StatusUnauthorized)
			resp, err := srv.Client().Post(srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"email":"khach@example.com","password":"x"}`))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
				t.Errorf("lần đoán thứ ba: %d, Retry-After %q; muốn 429 có Retry-After", resp.StatusCode, resp.Header.Get("Retry-After"))
			}

			// server thật hỗ trợ đặt deadline cho từng request (Bài 35), cache, rate limit không lỗi: không có
			// cảnh báo nào trong log, ngoài dòng log WARN của các request 404, 401, 429 cố ý gửi ở trên
			for _, e := range logEntries(t, logs) {
				if st := e["status"]; e["msg"] == "request" && (st == 404.0 || st == 401.0 || st == 429.0) {
					continue
				}
				if e["level"] == "WARN" || e["level"] == "ERROR" {
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
