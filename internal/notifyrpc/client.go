package notifyrpc

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/reqlog"
)

// CallTimeout là thời gian tối đa của một RPC unary, tính cả các lần gọi lại.
const CallTimeout = 5 * time.Second

// serviceConfig: gRPC tự gọi lại Send khi nhận Unavailable (server sập, đang khởi động lại...),
// tối đa 4 lần, chờ 0.2s, 0.4s, 0.8s (có jitter). Chỉ Send: nó idempotent nhờ idempotency_key.
const serviceConfig = `{
  "methodConfig": [{
    "name": [{"service": "notify.v1.NotificationService", "method": "Send"}],
    "retryPolicy": {
      "maxAttempts": 4,
      "initialBackoff": "0.2s",
      "maxBackoff": "2s",
      "backoffMultiplier": 2,
      "retryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`

// Dial tạo kết nối tới notifier ở target, mọi RPC mang token và request ID của ctx.
// opts thêm vào sau option mặc định (test dùng để nối qua bufconn).
func Dial(target, token string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	opts = append([]grpc.DialOption{
		// không TLS: notifier chỉ nghe trong mạng nội bộ của Docker (xem Bài 50 về mTLS)
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
		// ping mỗi 30s khi có RPC đang chạy, 10s không có trả lời thì coi kết nối đã chết
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
		grpc.WithChainUnaryInterceptor(outgoingUnary(token), timeoutUnary(CallTimeout)),
		grpc.WithChainStreamInterceptor(outgoingStream(token)),
	}, opts...)
	return grpc.NewClient(target, opts...)
}

// outgoing gắn token và request ID (nếu ctx có) vào metadata gửi đi.
func outgoing(ctx context.Context, token string) context.Context {
	kv := []string{mdAuthorization, "Bearer " + token}
	if id := reqlog.ID(ctx); id != "-" {
		kv = append(kv, mdRequestID, id)
	}
	return metadata.AppendToOutgoingContext(ctx, kv...)
}

func outgoingUnary(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(outgoing(ctx, token), method, req, reply, cc, opts...)
	}
}

func outgoingStream(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
		streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(outgoing(ctx, token), desc, cc, method, opts...)
	}
}

// timeoutUnary đặt deadline d cho mỗi RPC unary; ctx đã có deadline sớm hơn thì giữ deadline đó.
// Không dùng cho stream: Watch chạy lâu tùy ý.
func timeoutUnary(d time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx, cancel := context.WithTimeout(ctx, d) // ctx đã có deadline sớm hơn thì giữ deadline đó
		defer cancel()
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// Notifier cài notify.Notifier bằng NotificationService.Send: TaskFlow API dùng nó thay cho
// notify.Log khi có TASKFLOW_NOTIFIER_ADDR.
type Notifier struct {
	client notifyv1.NotificationServiceClient
}

// NewNotifier tạo Notifier gọi notifier qua conn (tạo bằng Dial).
func NewNotifier(conn grpc.ClientConnInterface) *Notifier {
	return &Notifier{client: notifyv1.NewNotificationServiceClient(conn)}
}

func (n *Notifier) TaskDone(ctx context.Context, d notify.TaskDone) error {
	return n.send(ctx, &notifyv1.SendRequest{UserIds: ids(d.To), TaskId: int64(d.TaskID), Title: d.Title,
		Event: &notifyv1.SendRequest_TaskDone{TaskDone: &notifyv1.TaskDone{DoneBy: int64(d.DoneBy)}}})
}

func (n *Notifier) Deadline(ctx context.Context, d notify.Deadline) error {
	return n.send(ctx, &notifyv1.SendRequest{UserIds: ids(d.To), TaskId: int64(d.TaskID), Title: d.Title,
		Event: &notifyv1.SendRequest_Deadline{Deadline: &notifyv1.Deadline{DueDate: timestamppb.New(d.DueDate)}}})
}

// send gọi Send với ID của job làm idempotency_key: job chạy lại (at-least-once) hay gRPC gọi lại
// thì notifier cũng chỉ gửi một lần. TaskDone, Deadline đổi notify.TaskDone/Deadline sang SendRequest rồi gọi send.
func (n *Notifier) send(ctx context.Context, req *notifyv1.SendRequest) error {
	if id := jobs.JobID(ctx); id != "" {
		req.IdempotencyKey = "job:" + id
	}
	_, err := n.client.Send(ctx, req)
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.InvalidArgument, codes.Unimplemented:
		// request sai: chạy lại job bao nhiêu lần cũng vậy
		return fmt.Errorf("notifier: %w: %w", err, jobs.SkipRetry)
	default:
		// Unavailable, DeadlineExceeded, Unauthenticated (sai cấu hình, sửa xong thì chạy lại được)...
		return fmt.Errorf("notifier: %w", err)
	}
}

func ids(to []int) []int64 {
	out := make([]int64, len(to))
	for i, id := range to {
		out[i] = int64(id)
	}
	return out
}
