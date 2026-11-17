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

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
)

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
	go func() { done <- run(ctx, lis, slog.New(slog.NewTextHandler(&logs, nil))) }()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	resp, err := notifyv1.NewNotificationServiceClient(conn).Send(t.Context(), &notifyv1.SendRequest{
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
	case <-time.After(shutdownTimeout + time.Second):
		t.Fatal("run không dừng sau khi hủy ctx")
	}
	for _, want := range []string{`msg="notifier đang chạy"`, `task_id=7`, `msg="đang tắt notifier"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log thiếu %s:\n%s", want, logs.String())
		}
	}
}
