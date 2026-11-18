package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"
)

// serve phục vụ srv trên ln cho tới khi ctx bị hủy (nhận SIGINT/SIGTERM), rồi tắt theo thứ tự:
// báo /ready 503, ngừng nhận kết nối và chờ các request đang chạy, dừng việc nền.
// Cả hai bước chờ dùng chung thời hạn timeout. Đóng database là việc của nơi gọi, sau khi serve trả về.
func serve(ctx context.Context, srv *http.Server, ln net.Listener, a *app, delay, timeout time.Duration) error {
	// Shutdown không chờ, cũng không đóng các kết nối đã bị hijack như WebSocket: nó chỉ gọi
	// các hàm đăng ký ở đây (trong goroutine riêng). Hub báo mọi WebSocket gửi close frame rồi kết thúc.
	srv.RegisterOnShutdown(a.hub.Close)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc: // server dừng mà chưa ai gọi Shutdown: lỗi thật
		return err
	case <-ctx.Done():
	}
	slog.Info("bắt đầu tắt server", "cause", context.Cause(ctx), "timeout", timeout)

	// 1. load balancer hỏi /ready sẽ nhận 503 và thôi gửi request mới tới đây
	a.ready.SetStopping()
	time.Sleep(delay)

	sctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 2. đóng listener, đóng kết nối rảnh, chờ request đang chạy xong
	var errs []error
	if err := srv.Shutdown(sctx); err != nil {
		srv.Close() // hết thời gian: cắt luôn các kết nối còn lại
		errs = append(errs, fmt.Errorf("HTTP server: %w", err))
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) { // bình thường Serve trả về ErrServerClosed
		errs = append(errs, fmt.Errorf("HTTP server: %w", err))
	}
	// chờ các WebSocket gửi close frame xong (Hub.Close đã được Shutdown gọi)
	if err := a.hub.Wait(sctx); err != nil {
		errs = append(errs, fmt.Errorf("WebSocket: %w", err))
	}
	slog.Info("đã dừng HTTP server")

	// 3. không còn handler nào Enqueue được nữa: dừng việc nền với thời gian còn lại
	if err := a.queue.Stop(sctx); err != nil {
		errs = append(errs, fmt.Errorf("việc nền: %w", err))
	}
	slog.Info("đã dừng việc nền")
	return errors.Join(errs...)
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

// serveMetrics phục vụ h ở http://addr/metrics trên một cổng riêng: Prometheus gọi vào trong mạng
// nội bộ, cổng này không đi qua Caddy nên người ngoài không đọc được. withPprof thêm các profile
// của net/http/pprof dưới /debug/pprof/ trên cùng cổng đó. Trả về hàm đóng server.
func serveMetrics(addr string, h http.Handler, withPprof bool) (stop func(), err error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", h)
	if withPprof {
		addPprof(mux)
	}
	// không có WriteTimeout: /debug/pprof/profile?seconds=30 ghi response sau 30 giây
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server metrics dừng", "err", err)
		}
	}()
	slog.Info("metrics cho Prometheus", "addr", ln.Addr().String(), "pprof", withPprof)
	return func() { srv.Close() }, nil
}

// addPprof đăng ký các handler của net/http/pprof lên mux. Import net/http/pprof cũng tự đăng ký
// chúng lên http.DefaultServeMux, nhưng TaskFlow không phục vụ mux toàn cục đó ở cổng nào.
func addPprof(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index) // trang danh sách + heap, goroutine, block, mutex...
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile) // CPU profile, mặc định 30 giây
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace) // execution trace
	// block và mutex profile rỗng nếu không bật: trung bình một mẫu cho mỗi 1ms goroutine bị chặn,
	// một mẫu cho mỗi 100 lần tranh chấp mutex. Thưa như vậy thì chi phí nhỏ, để bật lâu được
	runtime.SetBlockProfileRate(int(time.Millisecond))
	runtime.SetMutexProfileFraction(100)
}
