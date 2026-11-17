// Command notifysend gọi NotificationService.Send của notifier: client gRPC mẫu để thử bằng tay.
//
//	notifysend -addr localhost:9090 -user 1 -task 3 -title "Viết bài gRPC"
//	notifysend -due 3h ...  gửi thông báo "sắp tới hạn" thay vì "đã xong"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
)

func main() {
	addr := flag.String("addr", "localhost:9090", "địa chỉ notifier")
	user := flag.Int64("user", 1, "id người nhận")
	taskID := flag.Int64("task", 1, "id task")
	title := flag.String("title", "Viết bài gRPC", "tiêu đề task")
	due := flag.Duration("due", 0, "> 0: gửi thông báo sắp tới hạn, hạn là bây giờ + due")
	email := flag.Bool("email", false, "gửi qua kênh email")
	flag.Parse()

	// NewClient chưa kết nối: kết nối được mở ở RPC đầu tiên (và tự nối lại khi rớt).
	// insecure: không TLS, chỉ dùng trong máy dev hay mạng nội bộ đã được bảo vệ.
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "tạo client:", err)
		os.Exit(1)
	}
	defer conn.Close()
	client := notifyv1.NewNotificationServiceClient(conn)

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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second) // deadline của RPC
	defer cancel()
	resp, err := client.Send(ctx, req)
	if err != nil {
		if st, ok := status.FromError(err); ok { // lỗi gRPC: có code và message
			fmt.Fprintf(os.Stderr, "lỗi %s: %s\n", st.Code(), st.Message())
		} else {
			fmt.Fprintln(os.Stderr, "lỗi:", err)
		}
		os.Exit(1)
	}
	fmt.Printf("đã gửi cho %d người\n", resp.GetDelivered())
}
