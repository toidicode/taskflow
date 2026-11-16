package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/jobs"
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
	if err := s.jobs.Enqueue(jobs.Job{Name: "muộn"}); !errors.Is(err, jobs.ErrStopped) {
		t.Errorf("Enqueue sau khi tắt: %v, muốn ErrStopped", err)
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
