package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// slowServer chạy serve trên một cổng ngẫu nhiên với route GET /slow: báo started khi bắt đầu
// rồi chờ release mới trả "xong". Hủy ctx là giả lập nhận SIGTERM; kết quả của serve gửi vào kênh trả về.
func slowServer(t *testing.T, ctx context.Context, s *app, timeout time.Duration) (addr string, started, release chan struct{}, done chan error) {
	t.Helper()
	started, release, done = make(chan struct{}), make(chan struct{}), make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, "xong")
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- serve(ctx, newServer(defaultConfig(), mux), ln, s, 0, timeout) }()
	return ln.Addr().String(), started, release, done
}

// get gửi GET tới url trong goroutine riêng, kết quả (body hoặc lỗi) gửi vào kênh trả về.
func get(url string) chan string {
	res := make(chan string, 1)
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			res <- "lỗi: " + err.Error()
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			res <- "lỗi: " + err.Error()
			return
		}
		res <- string(b)
	}()
	return res
}

func TestServeWaitsForRequests(t *testing.T) {
	captureLog(t)
	s := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, started, release, done := slowServer(t, ctx, s, 5*time.Second)

	res := get("http://" + addr + "/slow")
	<-started
	cancel() // như nhận SIGTERM khi /slow đang chạy

	// listener đóng ngay: kết nối mới bị từ chối, trong khi /slow vẫn đang chạy
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("server vẫn nhận kết nối mới sau khi bắt đầu tắt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec := httptest.NewRecorder()
	s.ready.ServeHTTP(rec, httptest.NewRequest("GET", "/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/ready = %d, muốn 503: chưa chuyển sang đang tắt", rec.Code)
	}
	select {
	case err := <-done:
		t.Fatalf("serve trả về khi request còn chạy: %v", err)
	default:
	}

	close(release)
	if body := <-res; body != "xong" {
		t.Errorf("/slow: %q, muốn request đang chạy được làm xong", body)
	}
	if err := <-done; err != nil {
		t.Errorf("serve: %v", err)
	}
	if err := s.queue.Enqueue(context.Background(), notify.TypeTaskDone, nil); !errors.Is(err, jobs.ErrStopped) {
		t.Errorf("Enqueue sau khi tắt: %v, muốn ErrStopped", err)
	}
}

// TestServeClosesWebSockets: WebSocket sống qua WriteTimeout của server (net/http xóa deadline khi
// hijack), và khi tắt server thì client nhận close 1001 thay vì bị cắt ngang.
func TestServeClosesWebSockets(t *testing.T) {
	captureLog(t)
	a := newTestApp(t)
	bg := context.Background()
	p := project.New("TaskFlow API", 1)
	if err := a.projects.Add(bg, p); err != nil {
		t.Fatal(err)
	}
	ticket, _ := a.tickets.Issue(bg, 1)

	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.WriteTimeout = 100 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- serve(ctx, newServer(cfg, a.handler(testTokens, "", nil)), ln, a, 0, 5*time.Second) }()

	u := fmt.Sprintf("ws://%s/api/v1/projects/%d/events?ticket=%s", ln.Addr(), p.ID, ticket)
	conn, _, err := websocket.Dial(bg, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	time.Sleep(300 * time.Millisecond) // quá WriteTimeout 100ms
	a.hub.Publish(bg, task.Event{Type: task.EventDeleted, ProjectID: p.ID, TaskID: 9, By: 1})
	rctx, rcancel := context.WithTimeout(bg, 5*time.Second)
	defer rcancel()
	if _, msg, err := conn.Read(rctx); err != nil {
		t.Fatalf("sau WriteTimeout: %v, muốn kết nối còn sống", err)
	} else if want := `{"type":"task.deleted","project_id":1,"task_id":9,"by":1}`; string(msg) != want {
		t.Errorf("message = %s", msg)
	}

	cancel() // như nhận SIGTERM
	if _, _, err := conn.Read(rctx); websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Errorf("khi tắt server: %v, muốn close 1001", err)
	}
	if err := <-done; err != nil {
		t.Errorf("serve: %v", err)
	}
}

func TestServeShutdownTimeout(t *testing.T) {
	captureLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, started, release, done := slowServer(t, ctx, newTestApp(t), 100*time.Millisecond)
	defer close(release) // handler chỉ được thả ra khi test xong

	res := get("http://" + addr + "/slow")
	<-started
	cancel()

	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("serve: %v, muốn lỗi hết thời gian", err)
	}
	if body := <-res; body == "xong" {
		t.Error("request quá hạn vẫn được trả lời, muốn kết nối bị cắt")
	}
}

func TestHealthcheck(t *testing.T) {
	s := newTestApp(t)
	srv := httptest.NewServer(s.ready)
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	if code := healthcheck(":" + port); code != 0 { // giống TASKFLOW_ADDR=:8080 trong container
		t.Errorf("server sẵn sàng: exit code %d, muốn 0", code)
	}
	s.ready.SetStopping()
	if code := healthcheck(srv.Listener.Addr().String()); code != 1 {
		t.Errorf("server đang tắt: exit code %d, muốn 1", code)
	}
	srv.Close()
	if code := healthcheck(srv.Listener.Addr().String()); code != 1 {
		t.Errorf("server đã tắt: exit code %d, muốn 1", code)
	}
}

// TestPprof: addPprof đăng ký đủ các profile, kể cả CPU profile và goroutineleak (Go 1.27).
func TestPprof(t *testing.T) {
	mux := http.NewServeMux()
	addPprof(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for path, want := range map[string]string{
		"/debug/pprof/":                  "goroutineleak",
		"/debug/pprof/goroutine?debug=1": "goroutine profile: total",
		"/debug/pprof/heap?debug=1":      "heap profile:",
		"/debug/pprof/mutex?debug=1":     "--- mutex:",
		"/debug/pprof/profile?seconds=1": "", // protobuf nén gzip, chỉ kiểm tra có dữ liệu
		"/debug/pprof/trace?seconds=0.1": "",
		"/debug/pprof/cmdline":           "",
	} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(body) == 0 || !strings.Contains(string(body), want) {
			t.Errorf("GET %s: %d, %d byte, muốn có %q", path, resp.StatusCode, len(body), want)
		}
	}
}
