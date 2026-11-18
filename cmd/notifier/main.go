// Command notifier là notification service của TaskFlow: một gRPC server cài
// NotificationService (proto/notify/v1/notify.proto), ghi thông báo ra log.
//
// Cấu hình: TASKFLOW_NOTIFIER_ADDR (hoặc -addr, mặc định :9090) là địa chỉ lắng nghe,
// TASKFLOW_NOTIFIER_TOKEN (bắt buộc, ít nhất 32 byte) là token client phải gửi kèm.
// -healthcheck gọi health check của notifier đang chạy rồi thoát (cho healthcheck của Docker).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/notifyrpc"
	"github.com/toidicode/taskflow/internal/reqlog"
)

// shutdownTimeout: thời gian chờ các RPC đang chạy khi tắt.
const shutdownTimeout = 5 * time.Second

// minToken: token chung phải dài ít nhất chừng này, VD tạo bằng openssl rand -hex 32.
const minToken = 32

func main() {
	addr := os.Getenv("TASKFLOW_NOTIFIER_ADDR")
	if addr == "" {
		addr = ":9090"
	}
	flag.StringVar(&addr, "addr", addr, "địa chỉ lắng nghe gRPC (TASKFLOW_NOTIFIER_ADDR)")
	check := flag.Bool("healthcheck", false, "gọi health check của notifier đang chạy rồi thoát")
	flag.Parse()
	if *check {
		os.Exit(healthcheck(addr))
	}
	token := os.Getenv("TASKFLOW_NOTIFIER_TOKEN")
	if len(token) < minToken {
		fmt.Fprintf(os.Stderr, "TASKFLOW_NOTIFIER_TOKEN: cần ít nhất %d byte\n", minToken)
		os.Exit(2)
	}

	// reqlog.Handler: dòng log nào ghi với ctx của RPC đều có request_id
	logger := slog.New(reqlog.Handler{Handler: slog.NewTextHandler(os.Stderr, nil)})
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("không mở được cổng", "err", err)
		os.Exit(1)
	}
	if err := run(ctx, lis, logger, token); err != nil {
		slog.Error("notifier dừng vì lỗi", "err", err)
		os.Exit(1)
	}
	slog.Info("đã tắt notifier")
}

// run phục vụ gRPC trên lis tới khi ctx bị hủy rồi tắt server an toàn.
func run(ctx context.Context, lis net.Listener, logger *slog.Logger, token string) error {
	s := grpc.NewServer(notifyrpc.ServerOptions(logger, token)...)
	srv := notifyrpc.NewServer(notify.Log{Logger: logger}, logger)
	notifyv1.RegisterNotificationServiceServer(s, srv)
	hs := health.NewServer() // service "" (cả server) ở trạng thái SERVING
	healthpb.RegisterHealthServer(s, hs)
	reflection.Register(s) // cho grpcurl, buf curl hỏi server có service, method nào (cần token)

	errc := make(chan error, 1)
	go func() { errc <- s.Serve(lis) }() // Serve chạy tới khi Stop/GracefulStop
	logger.Info("notifier đang chạy", "addr", lis.Addr().String())

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	logger.Info("đang tắt notifier")
	hs.Shutdown() // health check trả NOT_SERVING
	srv.Close()   // kết thúc các stream Watch, không thì GracefulStop chờ chúng mãi
	stopped := make(chan struct{})
	go func() {
		s.GracefulStop() // ngừng nhận RPC mới, chờ RPC đang chạy xong
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(shutdownTimeout):
		s.Stop() // quá hạn: đóng mọi kết nối, RPC còn lại nhận lỗi
		<-stopped
	}
	return nil
}

// healthcheck gọi grpc.health.v1.Health/Check của notifier lắng nghe ở addr trên máy này,
// trả về 0 khi SERVING, 1 khi không.
func healthcheck(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "addr:", err)
		return 1
	}
	conn, err := grpc.NewClient(net.JoinHostPort("127.0.0.1", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "health check:", err)
		return 1
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		fmt.Fprintln(os.Stderr, "health check:", resp.GetStatus())
		return 1
	}
	return 0
}
