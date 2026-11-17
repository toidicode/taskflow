package notifyrpc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
	"github.com/toidicode/taskflow/internal/notify"
)

// newClient chạy Server gửi qua n trên bufconn (kết nối trong bộ nhớ, không mở cổng TCP)
// và trả về client đã nối tới nó.
func newClient(t *testing.T, n notify.Notifier) notifyv1.NotificationServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	notifyv1.RegisterNotificationServiceServer(s, NewServer(n))
	go s.Serve(lis)
	t.Cleanup(s.Stop)

	// "passthrough:///": đưa nguyên địa chỉ cho dialer, không phân giải DNS như mặc định của NewClient
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return notifyv1.NewNotificationServiceClient(conn)
}

// logNotifier trả về notify.Log ghi vào buffer, không có thời gian.
func logNotifier() (notify.Log, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	return notify.Log{Logger: slog.New(h)}, &buf
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
		{"task done", &notifyv1.SendRequest{UserIds: []int64{1, 5}, TaskId: 3, Title: "Viết test",
			Event: &notifyv1.SendRequest_TaskDone{TaskDone: &notifyv1.TaskDone{DoneBy: 2}}},
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

// failNotifier luôn lỗi, như một nhà cung cấp email đang sập.
type failNotifier struct{ notify.Log }

func (failNotifier) TaskDone(context.Context, notify.TaskDone) error {
	return errors.New("smtp: timeout")
}

func TestSendNotifierError(t *testing.T) {
	client := newClient(t, failNotifier{})
	_, err := client.Send(t.Context(), &notifyv1.SendRequest{UserIds: []int64{1}, TaskId: 1, Title: "x",
		Event: &notifyv1.SendRequest_TaskDone{TaskDone: &notifyv1.TaskDone{}}})
	if st := status.Convert(err); st.Code() != codes.Unavailable || st.Message() != "gửi thông báo: smtp: timeout" {
		t.Errorf("lỗi = %v, muốn Unavailable", err)
	}
}
