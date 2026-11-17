// Package notifyrpc cài NotificationService (proto/notify/v1/notify.proto) bằng một notify.Notifier.
package notifyrpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
	"github.com/toidicode/taskflow/internal/notify"
)

// Server là phía server của NotificationService.
type Server struct {
	// nhúng theo giá trị: RPC thêm vào proto sau này mà chưa cài sẽ trả codes.Unimplemented
	notifyv1.UnimplementedNotificationServiceServer
	n notify.Notifier
}

// NewServer tạo Server gửi thông báo qua n.
func NewServer(n notify.Notifier) *Server {
	return &Server{n: n}
}

// Send kiểm tra request, đổi message protobuf thành kiểu của package notify rồi gửi.
func (s *Server) Send(ctx context.Context, req *notifyv1.SendRequest) (*notifyv1.SendResponse, error) {
	if err := validate(req); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	switch req.GetChannel() {
	case notifyv1.Channel_CHANNEL_UNSPECIFIED, notifyv1.Channel_CHANNEL_LOG: // zero value: kênh mặc định
	case notifyv1.Channel_CHANNEL_EMAIL:
		return nil, status.Error(codes.Unimplemented, "chưa hỗ trợ kênh email")
	default: // số mà server chưa biết, VD client dùng file proto mới hơn
		return nil, status.Errorf(codes.InvalidArgument, "channel %d không hợp lệ", req.GetChannel())
	}

	to := make([]int, len(req.GetUserIds()))
	for i, id := range req.GetUserIds() {
		to[i] = int(id)
	}
	taskID, title := int(req.GetTaskId()), req.GetTitle()

	var err error
	switch ev := req.GetEvent().(type) { // oneof: kiểu của field event cho biết loại thông báo
	case *notifyv1.SendRequest_TaskDone:
		err = s.n.TaskDone(ctx, notify.TaskDone{TaskID: taskID, Title: title,
			DoneBy: int(ev.TaskDone.GetDoneBy()), To: to})
	case *notifyv1.SendRequest_Deadline:
		err = s.n.Deadline(ctx, notify.Deadline{TaskID: taskID, Title: title,
			DueDate: ev.Deadline.GetDueDate().AsTime(), To: to})
	}
	if err != nil {
		// lỗi của Notifier là lỗi tạm thời (xem notify.Notifier): client nên gọi lại sau
		return nil, status.Errorf(codes.Unavailable, "gửi thông báo: %v", err)
	}
	return &notifyv1.SendResponse{Delivered: int32(len(to))}, nil
}

// validate trả về mọi lỗi của req, nối bằng errors.Join.
func validate(req *notifyv1.SendRequest) error {
	var errs []error
	if len(req.GetUserIds()) == 0 {
		errs = append(errs, errors.New("user_ids: cần ít nhất một người nhận"))
	}
	for _, id := range req.GetUserIds() {
		if id <= 0 {
			errs = append(errs, errors.New("user_ids: id phải lớn hơn 0"))
			break
		}
	}
	if req.GetTaskId() <= 0 {
		errs = append(errs, errors.New("task_id: phải lớn hơn 0"))
	}
	if req.GetTitle() == "" {
		errs = append(errs, errors.New("title: không được để trống"))
	}
	switch ev := req.GetEvent().(type) {
	case nil: // client không đặt field nào của oneof
		errs = append(errs, errors.New("event: cần task_done hoặc deadline"))
	case *notifyv1.SendRequest_Deadline:
		if !ev.Deadline.GetDueDate().IsValid() { // nil hoặc ngoài khoảng của Timestamp
			errs = append(errs, errors.New("deadline.due_date: thiếu hoặc không hợp lệ"))
		}
	}
	return errors.Join(errs...)
}
