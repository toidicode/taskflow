package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
	"github.com/toidicode/taskflow/internal/notifyrpc"
	"github.com/toidicode/taskflow/internal/reqlog"
)

const testToken = "token-chi-dung-trong-test-0123456789"

// syncBuffer: server ghi log trong goroutine khác với test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRun chạy notifier trên cổng TCP thật, gọi Send rồi tắt bằng ctx.
func TestRun(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var logs syncBuffer
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	logger := slog.New(reqlog.Handler{Handler: slog.NewTextHandler(&logs, nil)})
	go func() { done <- run(ctx, lis, logger, testToken) }()

	if code := healthcheck(lis.Addr().String()); code != 0 {
		t.Errorf("healthcheck = %d, muốn 0", code)
	}
	conn, err := notifyrpc.Dial(lis.Addr().String(), testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := notifyv1.NewNotificationServiceClient(conn)
	// stream Watch đang mở không được làm notifier treo khi tắt
	watch, err := client.Watch(t.Context(), &notifyv1.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Send(reqlog.WithID(t.Context(), "req-7"), &notifyv1.SendRequest{
		UserIds: []int64{1}, TaskId: 7, Title: "Chạy notifier",
		Event: &notifyv1.SendRequest_TaskDone{TaskDone: &notifyv1.TaskDone{DoneBy: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetDelivered() != 1 {
		t.Errorf("delivered = %d, muốn 1", resp.GetDelivered())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run trả lỗi: %v", err)
		}
	case <-time.After(shutdownTimeout / 2): // không phải chờ tới lúc Stop cưỡng bức
		t.Fatal("run không dừng sau khi hủy ctx")
	}
	for {
		if _, err := watch.Recv(); err != nil {
			if status.Code(err) != codes.Unavailable {
				t.Errorf("Watch khi notifier tắt: %v, muốn Unavailable", err)
			}
			break
		}
	}
	for _, want := range []string{`msg="notifier đang chạy"`, `task_id=7 title="Chạy notifier" done_by=2 request_id=req-7`,
		`method=/notify.v1.NotificationService/Send code=OK`, `msg="đang tắt notifier"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log thiếu %s:\n%s", want, logs.String())
		}
	}
}
