package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/toidicode/taskflow/internal/realtime"
	"github.com/toidicode/taskflow/internal/task"
)

// eventsServer chạy cả API (đủ middleware, frontend ở http://localhost:5173) qua HTTP thật:
// WebSocket cần kết nối TCP thật để hijack, httptest.ResponseRecorder không làm được.
// change sửa Deps trước khi tạo handler (nil là giữ nguyên).
//
// srv.Close không chờ kết nối đã bị hijack: handler WebSocket còn chạy sau khi test kết thúc và ghi
// dòng log "request" vào slog.Default của test chạy sau (TestLogRequests, TestRecoverPanic thấy thừa
// một dòng, -race báo data race). Cleanup chờ mọi handler trả về rồi mới cho test kết thúc.
func eventsServer(t *testing.T, change func(*Deps)) (*httptest.Server, Deps) {
	t.Helper()
	d := newTestStores(t).deps(testTokens) // user #1 (An) là chủ project #1, #2
	if change != nil {
		change(&d)
	}
	h := New(d, "http://localhost:5173", nil)
	var running sync.WaitGroup
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		running.Add(1)
		defer running.Done()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(running.Wait) // đăng ký trước nên chạy sau srv.Close và sau khi các WebSocket đã đóng
	t.Cleanup(srv.Close)
	return srv, d
}

// ticketFor lấy vé WebSocket cho user userID qua POST /api/v1/realtime/tickets.
func ticketFor(t *testing.T, srv *httptest.Server, userID int) string {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/realtime/tickets", nil)
	authorize(req, userID)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out ticketResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusCreated || out.ExpiresIn != 30 {
		t.Fatalf("lấy vé: %d %+v %v", resp.StatusCode, out, err)
	}
	return out.Ticket
}

// dialEvents mở WebSocket tới project projectID bằng vé ticket, header là header thêm (VD Origin).
func dialEvents(t *testing.T, srv *httptest.Server, projectID int, ticket string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u := fmt.Sprintf("%s/api/v1/projects/%d/events?ticket=%s", srv.URL, projectID, url.QueryEscape(ticket))
	conn, resp, err := websocket.Dial(t.Context(), u, &websocket.DialOptions{HTTPClient: srv.Client(), HTTPHeader: header})
	if conn != nil {
		t.Cleanup(func() { conn.CloseNow() })
	}
	return conn, resp, err
}

// status trả về status code của handshake, 0 nếu không có response.
func status(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func TestProjectEvents(t *testing.T) {
	srv, d := eventsServer(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	conn, resp, err := dialEvents(t, srv, 1, ticketFor(t, srv, 1), nil)
	if err != nil {
		t.Fatalf("An mở WebSocket project #1: %v (status %d)", err, status(resp))
	}
	doing := task.StatusDoing
	if _, err := d.Tasks.Update(ctx, 1, 3, task.Changes{Status: &doing}); err != nil { // task #3 thuộc project #1
		t.Fatal(err)
	}
	if _, err := d.Tasks.Update(ctx, 1, 4, task.Changes{Status: &doing}); err != nil { // task #4 thuộc project #2: không gửi
		t.Fatal(err)
	}
	if err := d.Tasks.Delete(ctx, 1, 2); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`{"type":"task.status_changed","project_id":1,"task_id":3,"by":1,"task":{"id":3,"project_id":1,"title":"Viết test cho API","status":"doing"}}`,
		`{"type":"task.deleted","project_id":1,"task_id":2,"by":1}`,
	} {
		typ, msg, err := conn.Read(ctx)
		if err != nil || typ != websocket.MessageText || string(msg) != want {
			t.Errorf("message = %s (%v, %v)\n muốn %s", msg, typ, err, want)
		}
	}

	// client không được gửi gì lên: server đóng với 1008
	conn.Write(ctx, websocket.MessageText, []byte("xin chào"))
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Errorf("sau khi client gửi message: %v, muốn đóng 1008", err)
	}
}

func TestProjectEventsRejected(t *testing.T) {
	srv, _ := eventsServer(t, nil)
	evil := http.Header{"Origin": {"https://evil.example"}}
	frontend := http.Header{"Origin": {"http://localhost:5173"}}

	used := ticketFor(t, srv, 1)
	if _, _, err := dialEvents(t, srv, 1, used, frontend); err != nil { // frontend (corsOrigin) mở được
		t.Fatalf("Origin của frontend: %v", err)
	}
	tests := []struct {
		name   string
		id     int
		ticket string
		header http.Header
		want   int
	}{
		{"không có vé", 1, "", nil, http.StatusUnauthorized},
		{"vé đã dùng", 1, used, nil, http.StatusUnauthorized},
		{"vé tự chế", 1, "ABCDEFGHIJKLMNOPQRSTUVWXYZ", nil, http.StatusUnauthorized},
		{"không phải chủ project", 1, ticketFor(t, srv, 2), nil, http.StatusNotFound},
		{"không có project", 99, ticketFor(t, srv, 1), nil, http.StatusNotFound},
		{"trang web lạ (CSWSH)", 1, ticketFor(t, srv, 1), evil, http.StatusForbidden},
	}
	for _, tt := range tests {
		if _, resp, err := dialEvents(t, srv, tt.id, tt.ticket, tt.header); err == nil || status(resp) != tt.want {
			t.Errorf("%s: status %d, %v; muốn %d", tt.name, status(resp), err, tt.want)
		}
	}

	// GET thường (không phải WebSocket): 426, vé vẫn bị tiêu
	resp, err := srv.Client().Get(srv.URL + "/api/v1/projects/1/events?ticket=" + ticketFor(t, srv, 1))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired || resp.Header.Get("Upgrade") != "websocket" {
		t.Errorf("GET thường: %d, Upgrade %q; muốn 426", resp.StatusCode, resp.Header.Get("Upgrade"))
	}
}

func TestProjectEventsMaxPerUser(t *testing.T) {
	srv, _ := eventsServer(t, func(d *Deps) { d.Realtime.Hub = realtime.NewHub(realtime.Options{MaxPerUser: 1}) })
	if _, _, err := dialEvents(t, srv, 1, ticketFor(t, srv, 1), nil); err != nil {
		t.Fatal(err)
	}
	_, resp, err := dialEvents(t, srv, 2, ticketFor(t, srv, 1), nil)
	if status(resp) != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("kết nối thứ hai: %d, %v; muốn 429 kèm Retry-After", status(resp), err)
	}
}

// TestProjectEventsPing: client không trả lời ping (ở đây: không bao giờ đọc) bị server ngắt.
func TestProjectEventsPing(t *testing.T) {
	srv, d := eventsServer(t, func(d *Deps) {
		d.Realtime.PingInterval, d.Realtime.PingTimeout = 20*time.Millisecond, 50*time.Millisecond
	})
	if _, _, err := dialEvents(t, srv, 1, ticketFor(t, srv, 1), nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for d.Realtime.Hub.Len() > 0 { // client coder/websocket chỉ trả pong khi đang Read
		if time.Now().After(deadline) {
			t.Fatal("server chưa ngắt kết nối không trả lời ping")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestProjectEventsShutdown: Hub.Close (server đang tắt) làm mọi WebSocket nhận close 1001.
func TestProjectEventsShutdown(t *testing.T) {
	srv, d := eventsServer(t, nil)
	conn, _, err := dialEvents(t, srv, 1, ticketFor(t, srv, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	d.Realtime.Hub.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Errorf("Read: %v, muốn close 1001", err)
	}
	if err := d.Realtime.Hub.Wait(ctx); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

func TestLogPath(t *testing.T) {
	for in, want := range map[string]string{
		"/api/v1/projects/1/events?ticket=ABC123":    "/api/v1/projects/1/events?ticket=REDACTED",
		"/api/v1/tasks?status=todo&limit=5":          "/api/v1/tasks?status=todo&limit=5",
		"/api/v1/projects/1/events?x=1&ticket=ABC&y": "/api/v1/projects/1/events?ticket=REDACTED&x=1&y=",
	} {
		u, _ := url.Parse(in)
		if got := logPath(u); got != want {
			t.Errorf("logPath(%s) = %s, muốn %s", in, got, want)
		}
	}
}
