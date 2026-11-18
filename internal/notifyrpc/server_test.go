package notifyrpc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/reqlog"
)

// testToken chỉ dùng trong test.
const testToken = "token-chi-dung-trong-test-0123456789"

var discard = slog.New(slog.DiscardHandler)

// startServer chạy notifier (Server, interceptor, health) trên bufconn: kết nối trong bộ nhớ,
// không mở cổng TCP. logger nhận log của interceptor.
func startServer(t *testing.T, n notify.Notifier, logger *slog.Logger) (*Server, *bufconn.Listener) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer(ServerOptions(logger, testToken)...)
	srv := NewServer(n, logger)
	notifyv1.RegisterNotificationServiceServer(s, srv)
	healthpb.RegisterHealthServer(s, health.NewServer())
	go s.Serve(lis)
	t.Cleanup(func() {
		srv.Close() // kết thúc Watch trước, như cmd/notifier
		s.Stop()
	})
	return srv, lis
}

// dial nối tới lis bằng Dial (interceptor, retry như TaskFlow API) với token.
func dial(t *testing.T, lis *bufconn.Listener, token string) *grpc.ClientConn {
	t.Helper()
	// "passthrough:///": đưa nguyên địa chỉ cho dialer, không phân giải DNS như mặc định của NewClient
	conn, err := Dial("passthrough:///bufnet", token,
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// newClient chạy server gửi qua n và trả về client có token đúng.
func newClient(t *testing.T, n notify.Notifier) notifyv1.NotificationServiceClient {
	t.Helper()
	_, lis := startServer(t, n, discard)
	return notifyv1.NewNotificationServiceClient(dial(t, lis, testToken))
}

// logNotifier trả về notify.Log ghi vào buffer, không có thời gian.
func logNotifier() (notify.Log, *bytes.Buffer) {
	var buf bytes.Buffer
	return notify.Log{Logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{ReplaceAttr: noTime}))}, &buf
}

func noTime(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

func taskDone(users ...int64) *notifyv1.SendRequest {
	return &notifyv1.SendRequest{UserIds: users, TaskId: 3, Title: "Viết test",
		Event: &notifyv1.SendRequest_TaskDone{TaskDone: &notifyv1.TaskDone{DoneBy: 2}}}
}

func TestSend(t *testing.T) {
	n, logs := logNotifier()
	client := newClient(t, n)
	due := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		req  *notifyv1.SendRequest
		log  string
	}{
		{"task done", taskDone(1, 5),
			`level=INFO msg="thông báo: task đã xong" to_user=1 task_id=3 title="Viết test" done_by=2
level=INFO msg="thông báo: task đã xong" to_user=5 task_id=3 title="Viết test" done_by=2
`},
		{"deadline", &notifyv1.SendRequest{UserIds: []int64{2}, TaskId: 4, Title: "Cài buf",
			Channel: notifyv1.Channel_CHANNEL_LOG,
			Event:   &notifyv1.SendRequest_Deadline{Deadline: &notifyv1.Deadline{DueDate: timestamppb.New(due)}}},
			`level=INFO msg="thông báo: task sắp tới hạn" to_user=2 task_id=4 title="Cài buf" due_date=2026-11-18T09:00:00.000Z
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs.Reset()
			resp, err := client.Send(t.Context(), tt.req)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := resp.GetDelivered(), int32(len(tt.req.GetUserIds())); got != want {
				t.Errorf("delivered = %d, muốn %d", got, want)
			}
			if got := logs.String(); got != tt.log {
				t.Errorf("log =\n%s\nmuốn\n%s", got, tt.log)
			}
		})
	}
}

func TestSendErrors(t *testing.T) {
	n, logs := logNotifier()
	client := newClient(t, n)
	done := &notifyv1.SendRequest_TaskDone{TaskDone: &notifyv1.TaskDone{DoneBy: 2}}
	tests := []struct {
		name string
		req  *notifyv1.SendRequest
		code codes.Code
		msg  string
	}{
		{"rỗng", &notifyv1.SendRequest{}, codes.InvalidArgument, "user_ids: cần ít nhất một người nhận\n" +
			"task_id: phải lớn hơn 0\ntitle: không được để trống\nevent: cần task_done hoặc deadline"},
		{"user id 0", &notifyv1.SendRequest{UserIds: []int64{1, 0}, TaskId: 1, Title: "x", Event: done},
			codes.InvalidArgument, "user_ids: id phải lớn hơn 0"},
		{"deadline thiếu due_date", &notifyv1.SendRequest{UserIds: []int64{1}, TaskId: 1, Title: "x",
			Event: &notifyv1.SendRequest_Deadline{Deadline: &notifyv1.Deadline{}}},
			codes.InvalidArgument, "deadline.due_date: thiếu hoặc không hợp lệ"},
		{"email", &notifyv1.SendRequest{UserIds: []int64{1}, TaskId: 1, Title: "x", Event: done,
			Channel: notifyv1.Channel_CHANNEL_EMAIL}, codes.Unimplemented, "chưa hỗ trợ kênh email"},
		{"channel lạ", &notifyv1.SendRequest{UserIds: []int64{1}, TaskId: 1, Title: "x", Event: done,
			Channel: notifyv1.Channel(9)}, codes.InvalidArgument, "channel 9 không hợp lệ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Send(t.Context(), tt.req)
			st, ok := status.FromError(err)
			if !ok || st.Code() != tt.code || st.Message() != tt.msg {
				t.Errorf("lỗi = %v, muốn %s %q", err, tt.code, tt.msg)
			}
		})
	}
	if logs.Len() != 0 {
		t.Errorf("request lỗi không được gửi thông báo:\n%s", logs)
	}
}

// failNotifier luôn lỗi, như một nhà cung cấp email đang sập, và đếm số lần được gọi.
type failNotifier struct {
	notify.Log
	calls atomic.Int32
}

func (f *failNotifier) TaskDone(context.Context, notify.TaskDone) error {
	f.calls.Add(1)
	return errors.New("smtp: timeout")
}

// TestSendRetry: notifier trả Unavailable, client tự gọi lại theo retryPolicy (maxAttempts 4).
func TestSendRetry(t *testing.T) {
	f := &failNotifier{}
	client := newClient(t, f)
	_, err := client.Send(t.Context(), taskDone(1))
	// message có thêm tiền tố "max retries exhausted: failed after 4 attempts: " của gRPC
	if st := status.Convert(err); st.Code() != codes.Unavailable || !strings.HasSuffix(st.Message(), "gửi thông báo: smtp: timeout") {
		t.Errorf("lỗi = %v, muốn Unavailable", err)
	}
	if got := f.calls.Load(); got != 4 {
		t.Errorf("notifier được gọi %d lần, muốn 4 (1 lần đầu + 3 lần gọi lại)", got)
	}
}

func TestAuth(t *testing.T) {
	n, logs := logNotifier()
	_, lis := startServer(t, n, discard)
	for _, token := range []string{"", "sai-token"} {
		client := notifyv1.NewNotificationServiceClient(dial(t, lis, token))
		_, err := client.Send(t.Context(), taskDone(1))
		if st := status.Convert(err); st.Code() != codes.Unauthenticated || st.Message() != "thiếu hoặc sai token" {
			t.Errorf("token %q: lỗi = %v, muốn Unauthenticated", token, err)
		}
		stream, err := client.Watch(t.Context(), &notifyv1.WatchRequest{})
		if err == nil {
			_, err = stream.Recv() // lỗi của stream đến ở lần Recv đầu tiên
		}
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("token %q: Watch lỗi = %v, muốn Unauthenticated", token, err)
		}
	}
	if logs.Len() != 0 {
		t.Errorf("request không có token không được gửi thông báo:\n%s", logs)
	}

	// health check không cần token
	resp, err := healthpb.NewHealthClient(dial(t, lis, "")).Check(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("health = %v, %v; muốn SERVING", resp, err)
	}
}

// panicNotifier panic như một bug trong code gửi thông báo.
type panicNotifier struct{ notify.Log }

func (panicNotifier) TaskDone(context.Context, notify.TaskDone) error {
	var m map[string]int
	m["x"] = 1 // panic: assignment to entry in nil map
	return nil
}

func TestRecover(t *testing.T) {
	var logs bytes.Buffer
	_, lis := startServer(t, panicNotifier{}, slog.New(slog.NewTextHandler(&logs, nil)))
	client := notifyv1.NewNotificationServiceClient(dial(t, lis, testToken))
	for range 2 { // server vẫn sống sau panic đầu tiên
		_, err := client.Send(t.Context(), taskDone(1))
		if st := status.Convert(err); st.Code() != codes.Internal || st.Message() != "lỗi nội bộ" {
			t.Fatalf("lỗi = %v, muốn Internal", err)
		}
	}
	for _, want := range []string{`msg="panic trong RPC"`, `panic="assignment to entry in nil map"`, `level=ERROR msg=rpc`, "code=Internal"} {
		if !bytes.Contains(logs.Bytes(), []byte(want)) {
			t.Errorf("log thiếu %s:\n%s", want, logs.String())
		}
	}
}

// ctxNotifier ghi lại ctx mà handler nhận được; block: chờ tới khi ctx bị hủy.
type ctxNotifier struct {
	notify.Log
	block bool
	got   chan context.Context
}

func (c *ctxNotifier) TaskDone(ctx context.Context, _ notify.TaskDone) error {
	if c.block {
		<-ctx.Done()
	}
	c.got <- ctx
	return ctx.Err()
}

func TestDeadline(t *testing.T) {
	t.Run("hết giờ", func(t *testing.T) {
		n := &ctxNotifier{block: true, got: make(chan context.Context, 1)}
		client := newClient(t, n)
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		_, err := client.Send(ctx, taskDone(1))
		if status.Code(err) != codes.DeadlineExceeded {
			t.Errorf("lỗi = %v, muốn DeadlineExceeded", err)
		}
		// deadline của client đi theo RPC sang server: ctx của handler cũng có deadline đó và bị hủy
		// (Canceled khi lệnh hủy của client tới trước, DeadlineExceeded khi đồng hồ của server tới trước)
		// (deadline ở server tính từ lúc nhận nên có thể muộn hơn của client vài ms: không so với time.Now)
		sctx := <-n.got
		if _, ok := sctx.Deadline(); !ok || sctx.Err() == nil {
			t.Errorf("ctx ở server: có deadline %v, err %v; muốn có deadline và đã bị hủy", ok, sctx.Err())
		}
	})
	t.Run("timeout mặc định", func(t *testing.T) {
		n := &ctxNotifier{got: make(chan context.Context, 1)}
		client := newClient(t, n)
		if _, err := client.Send(context.Background(), taskDone(1)); err != nil { // ctx không có deadline
			t.Fatal(err)
		}
		d, ok := (<-n.got).Deadline()
		if left := time.Until(d); !ok || left > CallTimeout || left < CallTimeout-time.Second {
			t.Errorf("deadline ở server còn %v (ok=%v), muốn gần %v", left, ok, CallTimeout)
		}
	})
}

func TestIdempotencyKey(t *testing.T) {
	n, logs := logNotifier()
	client := newClient(t, n)
	req := taskDone(1, 5)
	req.IdempotencyKey = "job:abc"
	for i, wantDup := range []bool{false, true} {
		resp, err := client.Send(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.GetDuplicate() != wantDup || resp.GetDelivered() != 2 {
			t.Errorf("lần %d: %v, muốn duplicate=%v delivered=2", i+1, resp, wantDup)
		}
	}
	if got := bytes.Count(logs.Bytes(), []byte("\n")); got != 2 {
		t.Errorf("gửi %d thông báo, muốn 2 (một lần cho hai người):\n%s", got, logs)
	}
}

// waitWatchers chờ tới khi server có n stream Watch đã đăng ký.
func waitWatchers(t *testing.T, srv *Server, n int) {
	t.Helper()
	for range 200 {
		srv.broker.mu.Lock()
		got := len(srv.broker.subs)
		srv.broker.mu.Unlock()
		if got == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("server chưa có %d stream Watch", n)
}

func TestWatch(t *testing.T) {
	n, _ := logNotifier()
	srv, lis := startServer(t, n, discard)
	client := notifyv1.NewNotificationServiceClient(dial(t, lis, testToken))

	ctx, cancel := context.WithCancel(t.Context())
	user5, err := client.Watch(ctx, &notifyv1.WatchRequest{UserId: 5})
	if err != nil {
		t.Fatal(err)
	}
	all, err := client.Watch(t.Context(), &notifyv1.WatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	waitWatchers(t, srv, 2)

	if _, err := client.Send(t.Context(), taskDone(1, 5)); err != nil {
		t.Fatal(err)
	}
	m, err := user5.Recv()
	if err != nil || m.GetUserId() != 5 || m.GetTaskId() != 3 || m.GetTaskDone().GetDoneBy() != 2 {
		t.Errorf("Watch user 5: %v, %v", m, err)
	}
	for _, want := range []int64{1, 5} {
		if m, err := all.Recv(); err != nil || m.GetUserId() != want {
			t.Errorf("Watch mọi user: %v, %v; muốn user %d", m, err, want)
		}
	}

	cancel() // client hủy: Recv trả Canceled, server gỡ stream
	if _, err := user5.Recv(); status.Code(err) != codes.Canceled {
		t.Errorf("sau cancel: %v, muốn Canceled", err)
	}
	waitWatchers(t, srv, 1)

	srv.Close() // server tắt: stream còn lại nhận Unavailable
	if _, err := all.Recv(); status.Code(err) != codes.Unavailable {
		t.Errorf("sau Close: %v, muốn Unavailable", err)
	}
}

func TestSendBatch(t *testing.T) {
	n, logs := logNotifier()
	client := newClient(t, n)

	send := func(reqs ...*notifyv1.SendRequest) (*notifyv1.SendBatchResponse, error) {
		stream, err := client.SendBatch(t.Context())
		if err != nil {
			return nil, err
		}
		for _, r := range reqs {
			if err := stream.Send(&notifyv1.SendBatchRequest{Request: r}); err != nil {
				break // server đã trả lỗi: lỗi thật nằm ở CloseAndRecv
			}
		}
		return stream.CloseAndRecv() // đóng chiều gửi (server nhận io.EOF) rồi chờ response
	}

	resp, err := send(taskDone(1), taskDone(2, 3))
	if err != nil || resp.GetRequests() != 2 || resp.GetDelivered() != 3 {
		t.Errorf("batch = %v, %v; muốn 2 request, 3 người nhận", resp, err)
	}

	logs.Reset()
	_, err = send(taskDone(1), &notifyv1.SendRequest{}, taskDone(2))
	if st := status.Convert(err); st.Code() != codes.InvalidArgument || !bytes.HasPrefix([]byte(st.Message()), []byte("request #2: ")) {
		t.Errorf("lỗi = %v, muốn InvalidArgument request #2", err)
	}
	if got := bytes.Count(logs.Bytes(), []byte("\n")); got != 1 {
		t.Errorf("gửi %d thông báo, muốn 1 (request trước request lỗi):\n%s", got, logs)
	}
}

// TestRequestID: request ID đi từ ctx của client (reqlog) qua metadata tới ctx của handler và log.
func TestRequestID(t *testing.T) {
	var logs syncBuffer
	logger := slog.New(reqlog.Handler{Handler: slog.NewTextHandler(&logs, nil)})
	n := &ctxNotifier{got: make(chan context.Context, 2)}
	_, lis := startServer(t, n, logger)
	client := notifyv1.NewNotificationServiceClient(dial(t, lis, testToken))

	if _, err := client.Send(reqlog.WithID(t.Context(), "req-50"), taskDone(1)); err != nil {
		t.Fatal(err)
	}
	if got := reqlog.ID(<-n.got); got != "req-50" {
		t.Errorf("request ID ở server = %q, muốn req-50", got)
	}
	if !bytes.Contains([]byte(logs.String()), []byte("request_id=req-50")) {
		t.Errorf("log thiếu request_id=req-50:\n%s", logs.String())
	}

	if _, err := client.Send(t.Context(), taskDone(1)); err != nil { // không có ID: server tự sinh
		t.Fatal(err)
	}
	if got := reqlog.ID(<-n.got); got == "-" || got == "" {
		t.Errorf("server không sinh request ID: %q", got)
	}
}

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
