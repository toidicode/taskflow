// Command notifysend gọi notifier: client gRPC mẫu để thử bằng tay. Token lấy từ TASKFLOW_NOTIFIER_TOKEN.
//
//	notifysend -addr localhost:9090 -user 1 -task 3 -title "Viết bài gRPC"
//	notifysend -due 3h ...  gửi thông báo "sắp tới hạn" thay vì "đã xong"
//	notifysend -watch -user 0  in mọi thông báo notifier gửi đi (Watch) tới khi Ctrl+C
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
	"github.com/toidicode/taskflow/internal/notifyrpc"
)

func main() {
	addr := flag.String("addr", "localhost:9090", "địa chỉ notifier")
	user := flag.Int64("user", 1, "id người nhận (-watch: chỉ xem user này, 0 là mọi user)")
	taskID := flag.Int64("task", 1, "id task")
	title := flag.String("title", "Viết bài gRPC", "tiêu đề task")
	due := flag.Duration("due", 0, "> 0: gửi thông báo sắp tới hạn, hạn là bây giờ + due")
	email := flag.Bool("email", false, "gửi qua kênh email")
	watch := flag.Bool("watch", false, "xem thông báo notifier gửi đi thay vì gửi")
	flag.Parse()

	// Dial dùng NewClient: chưa kết nối, kết nối được mở ở RPC đầu tiên (và tự nối lại khi rớt).
	conn, err := notifyrpc.Dial(*addr, os.Getenv("TASKFLOW_NOTIFIER_TOKEN"))
	if err != nil {
		fail(err)
	}
	defer conn.Close()
	client := notifyv1.NewNotificationServiceClient(conn)

	if *watch {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		watchNotifications(ctx, client, *user)
		return
	}

	req := &notifyv1.SendRequest{UserIds: []int64{*user}, TaskId: *taskID, Title: *title}
	if *email {
		req.Channel = notifyv1.Channel_CHANNEL_EMAIL
	}
	if *due > 0 {
		req.Event = &notifyv1.SendRequest_Deadline{
			Deadline: &notifyv1.Deadline{DueDate: timestamppb.New(time.Now().Add(*due))}}
	} else {
		req.Event = &notifyv1.SendRequest_TaskDone{TaskDone: &notifyv1.TaskDone{DoneBy: 2}}
	}

	// deadline của RPC: interceptor của Dial đặt tối đa notifyrpc.CallTimeout, ở đây ngắn hơn
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := client.Send(ctx, req)
	if err != nil {
		fail(err)
	}
	fmt.Printf("đã gửi cho %d người\n", resp.GetDelivered())
}

// watchNotifications mở stream Watch và in từng thông báo tới khi ctx bị hủy (Ctrl+C).
func watchNotifications(ctx context.Context, client notifyv1.NotificationServiceClient, user int64) {
	stream, err := client.Watch(ctx, &notifyv1.WatchRequest{UserId: user})
	if err != nil {
		fail(err)
	}
	for {
		m, err := stream.Recv()
		switch {
		case errors.Is(err, io.EOF): // server kết thúc stream bình thường
			return
		case status.Code(err) == codes.Canceled: // chính mình hủy ctx
			return
		case err != nil:
			fail(err)
		}
		fmt.Printf("user %d: task #%d %q %s\n", m.GetUserId(), m.GetTaskId(), m.GetTitle(), kind(m))
	}
}

// kind là loại thông báo của m.
func kind(m *notifyv1.WatchResponse) string {
	if m.GetDeadline() != nil {
		return "sắp tới hạn"
	}
	return "đã xong"
}

func fail(err error) {
	if st, ok := status.FromError(err); ok { // lỗi gRPC: có code và message
		fmt.Fprintf(os.Stderr, "lỗi %s: %s\n", st.Code(), st.Message())
	} else {
		fmt.Fprintln(os.Stderr, "lỗi:", err)
	}
	os.Exit(1)
}
