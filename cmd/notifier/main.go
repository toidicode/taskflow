// Command notifier là notification service của TaskFlow: một gRPC server cài
// NotificationService (proto/notify/v1/notify.proto), ghi thông báo ra log.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/notifyrpc"
)

// shutdownTimeout: thời gian chờ các RPC đang chạy khi tắt.
const shutdownTimeout = 5 * time.Second

func main() {
	addr := os.Getenv("TASKFLOW_NOTIFIER_ADDR")
	if addr == "" {
		addr = ":9090"
	}
	flag.StringVar(&addr, "addr", addr, "địa chỉ lắng nghe gRPC (TASKFLOW_NOTIFIER_ADDR)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("không mở được cổng", "err", err)
		os.Exit(1)
	}
	if err := run(ctx, lis, logger); err != nil {
		slog.Error("notifier dừng vì lỗi", "err", err)
		os.Exit(1)
	}
	slog.Info("đã tắt notifier")
}

// run phục vụ gRPC trên lis tới khi ctx bị hủy rồi tắt server an toàn.
func run(ctx context.Context, lis net.Listener, logger *slog.Logger) error {
	s := grpc.NewServer()
	notifyv1.RegisterNotificationServiceServer(s, notifyrpc.NewServer(notify.Log{Logger: logger}))
	reflection.Register(s) // cho grpcurl, buf curl hỏi server có service, method nào

	errc := make(chan error, 1)
	go func() { errc <- s.Serve(lis) }() // Serve chạy tới khi Stop/GracefulStop
	logger.Info("notifier đang chạy", "addr", lis.Addr().String())

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	logger.Info("đang tắt notifier")
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
