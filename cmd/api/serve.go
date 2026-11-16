package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// serve phục vụ srv trên ln cho tới khi ctx bị hủy (nhận SIGINT/SIGTERM), rồi tắt theo thứ tự:
// báo /ready 503, ngừng nhận kết nối và chờ các request đang chạy, dừng việc nền.
// Cả hai bước chờ dùng chung thời hạn timeout. Đóng database là việc của nơi gọi, sau khi serve trả về.
func serve(ctx context.Context, srv *http.Server, ln net.Listener, s stores, delay, timeout time.Duration) error {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc: // server dừng mà chưa ai gọi Shutdown: lỗi thật
		return err
	case <-ctx.Done():
	}
	slog.Info("bắt đầu tắt server", "cause", context.Cause(ctx), "timeout", timeout)

	// 1. load balancer hỏi /ready sẽ nhận 503 và thôi gửi request mới tới đây
	s.ready.stopping.Store(true)
	time.Sleep(delay)

	sctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 2. đóng listener, đóng kết nối rảnh, chờ request đang chạy xong
	var errs []error
	if err := srv.Shutdown(sctx); err != nil {
		srv.Close() // hết thời gian: cắt luôn các kết nối còn lại
		errs = append(errs, fmt.Errorf("HTTP server: %w", err))
	}
	<-errc // Serve đã trả về http.ErrServerClosed
	slog.Info("đã dừng HTTP server")

	// 3. không còn handler nào Enqueue được nữa: dừng việc nền với thời gian còn lại
	if err := s.jobs.Stop(sctx); err != nil {
		errs = append(errs, fmt.Errorf("việc nền: %w", err))
	}
	slog.Info("đã dừng việc nền")
	return errors.Join(errs...)
}

// readiness trả lời /ready: server có nên nhận request mới không.
type readiness struct {
	db       *sql.DB     // nil khi dùng store trong bộ nhớ
	stopping atomic.Bool // true từ lúc bắt đầu tắt server
}

func (rd *readiness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if rd.stopping.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "đang tắt")
		return
	}
	if rd.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := rd.db.PingContext(ctx); err != nil {
			requestLogger(r).Warn("ready: database không phản hồi", "err", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "database không phản hồi")
			return
		}
	}
	fmt.Fprintln(w, "ok")
}

// healthcheck gọi /ready của server đang lắng nghe ở addr, trả về exit code cho HEALTHCHECK của Docker:
// 0 là sẵn sàng, 1 là không. Image distroless không có curl hay shell nên chính binary làm việc này.
func healthcheck(addr string) int {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	if host == "" { // ":8080" là lắng nghe mọi địa chỉ, gọi qua localhost
		host = "localhost"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/ready")
	if err != nil {
		fmt.Println(err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Println("/ready:", resp.Status)
		return 1
	}
	return 0
}
