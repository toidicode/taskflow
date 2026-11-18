// Package notifyrpc cài NotificationService (proto/notify/v1/notify.proto) bằng một notify.Notifier
// (phía server), và cài notify.Notifier bằng client gRPC gọi tới notifier (phía client, Bài 50).
package notifyrpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
	"github.com/toidicode/taskflow/internal/notify"
)

// keyTTL: server nhớ một idempotency_key chừng này. Phải dài hơn thời gian client còn gọi lại
// cùng một thông báo (job TaskFlow chạy lại tối đa 10 lần, chờ tối đa 10 phút giữa hai lần).
const keyTTL = 24 * time.Hour

// Server là phía server của NotificationService.
type Server struct {
	// nhúng theo giá trị: RPC thêm vào proto sau này mà chưa cài sẽ trả codes.Unimplemented
	notifyv1.UnimplementedNotificationServiceServer
	n      notify.Notifier
	keys   *keys
	broker *broker
}

// NewServer tạo Server gửi thông báo qua n. Thông báo bị bỏ (Watch đọc chậm) được ghi vào logger.
func NewServer(n notify.Notifier, logger *slog.Logger) *Server {
	return &Server{n: n, keys: newKeys(keyTTL), broker: newBroker(logger)}
}

// Close kết thúc mọi stream Watch đang mở. Gọi trước GracefulStop: GracefulStop chờ mọi RPC
// xong, mà Watch thì không bao giờ tự xong.
func (s *Server) Close() {
	s.broker.close()
}

// Send kiểm tra request, đổi message protobuf thành kiểu của package notify rồi gửi.
func (s *Server) Send(ctx context.Context, req *notifyv1.SendRequest) (*notifyv1.SendResponse, error) {
	return s.send(ctx, req)
}

// SendBatch nhận các request trên một stream cho tới khi client đóng stream (io.EOF), gửi từng
// cái rồi trả về một response tổng kết. Request lỗi thì dừng: các request trước đã được gửi.
func (s *Server) SendBatch(stream grpc.ClientStreamingServer[notifyv1.SendBatchRequest, notifyv1.SendBatchResponse]) error {
	var res notifyv1.SendBatchResponse
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) { // client đã gửi xong (CloseAndRecv)
			return stream.SendAndClose(&res)
		}
		if err != nil { // client hủy ctx, hết deadline, mất kết nối: err đã là lỗi gRPC
			return err
		}
		resp, err := s.send(stream.Context(), msg.GetRequest()) // logic chung với Send
		if err != nil {
			st := status.Convert(err)
			return status.Errorf(st.Code(), "request #%d: %s", res.Requests+1, st.Message())
		}
		res.Requests++
		res.Delivered += resp.GetDelivered()
	}
}

// Watch đẩy mọi thông báo gửi thành công (lọc theo user_id nếu khác 0) cho tới khi client hủy
// hoặc server tắt.
func (s *Server) Watch(req *notifyv1.WatchRequest, stream grpc.ServerStreamingServer[notifyv1.WatchResponse]) error {
	ch, unsubscribe := s.broker.subscribe(req.GetUserId())
	defer unsubscribe()
	ctx := stream.Context()
	for {
		select {
		case m := <-ch:
			if err := stream.Send(m); err != nil { // client đã đi: kết thúc stream
				return err
			}
		case <-ctx.Done(): // client hủy, hết deadline hoặc mất kết nối
			return status.FromContextError(ctx.Err()).Err()
		case <-s.broker.done:
			return status.Error(codes.Unavailable, "notifier đang tắt")
		}
	}
}

func (s *Server) send(ctx context.Context, req *notifyv1.SendRequest) (*notifyv1.SendResponse, error) {
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
	key := req.GetIdempotencyKey()
	if n, ok := s.keys.get(key); ok { // đã gửi: client gọi lại vì không nhận được response lần trước
		return &notifyv1.SendResponse{Delivered: n, Duplicate: true}, nil
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
	delivered := int32(len(to))
	// Ghi khóa sau khi gửi xong: hai lời gọi cùng khóa chạy song song vẫn có thể gửi hai lần,
	// chấp nhận được với thông báo (at-least-once như hàng đợi job).
	s.keys.add(key, delivered)
	for _, id := range req.GetUserIds() {
		s.broker.publish(ctx, watchResponse(id, req))
	}
	return &notifyv1.SendResponse{Delivered: delivered}, nil
}

// watchResponse là thông báo req gửi tới user id, để đẩy cho các stream Watch.
func watchResponse(id int64, req *notifyv1.SendRequest) *notifyv1.WatchResponse {
	m := &notifyv1.WatchResponse{UserId: id, TaskId: req.GetTaskId(), Title: req.GetTitle()}
	switch ev := req.GetEvent().(type) {
	case *notifyv1.SendRequest_TaskDone:
		m.Event = &notifyv1.WatchResponse_TaskDone{TaskDone: ev.TaskDone}
	case *notifyv1.SendRequest_Deadline:
		m.Event = &notifyv1.WatchResponse_Deadline{Deadline: ev.Deadline}
	}
	return m
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
	if len(req.GetIdempotencyKey()) > 128 {
		errs = append(errs, errors.New("idempotency_key: tối đa 128 byte"))
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

// keys nhớ các idempotency_key đã gửi trong ttl, trong bộ nhớ: notifier khởi động lại là quên.
// Chạy nhiều bản notifier thì cần nơi nhớ chung, VD Redis SET key NX EX.
type keys struct {
	mu        sync.Mutex
	ttl       time.Duration
	sent      map[string]sentKey
	lastSweep time.Time
}

type sentKey struct {
	delivered int32
	at        time.Time
}

func newKeys(ttl time.Duration) *keys {
	return &keys{ttl: ttl, sent: make(map[string]sentKey), lastSweep: time.Now()}
}

func (k *keys) get(key string) (int32, bool) {
	if key == "" {
		return 0, false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	s, ok := k.sent[key]
	if !ok || time.Since(s.at) > k.ttl {
		return 0, false
	}
	return s.delivered, true
}

func (k *keys) add(key string, delivered int32) {
	if key == "" {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	now := time.Now()
	k.sent[key] = sentKey{delivered: delivered, at: now}
	if now.Sub(k.lastSweep) > time.Minute { // mỗi phút tối đa một lần: xóa khóa hết hạn
		for key, s := range k.sent {
			if now.Sub(s.at) > k.ttl {
				delete(k.sent, key)
			}
		}
		k.lastSweep = now
	}
}

// broker chia thông báo cho các stream Watch. Mỗi stream có một channel có buffer: stream đọc
// chậm làm đầy buffer thì thông báo mới cho nó bị bỏ (ghi WARN), Send không bao giờ phải chờ.
type broker struct {
	logger *slog.Logger
	mu     sync.Mutex
	subs   map[chan *notifyv1.WatchResponse]int64 // channel → user_id cần lọc, 0 là mọi user
	done   chan struct{}                          // đóng khi server tắt
	once   sync.Once
}

func newBroker(logger *slog.Logger) *broker {
	return &broker{logger: logger, subs: make(map[chan *notifyv1.WatchResponse]int64), done: make(chan struct{})}
}

func (b *broker) subscribe(userID int64) (<-chan *notifyv1.WatchResponse, func()) {
	ch := make(chan *notifyv1.WatchResponse, 16)
	b.mu.Lock()
	b.subs[ch] = userID
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

func (b *broker) publish(ctx context.Context, m *notifyv1.WatchResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch, userID := range b.subs {
		if userID != 0 && userID != m.GetUserId() {
			continue
		}
		select {
		case ch <- m:
		default:
			b.logger.WarnContext(ctx, "watch đọc chậm, bỏ thông báo", "user_id", m.GetUserId(), "task_id", m.GetTaskId())
		}
	}
}

func (b *broker) close() {
	b.once.Do(func() { close(b.done) })
}
