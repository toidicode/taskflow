// Package realtime đẩy event của task tới các client đang kết nối: Hub giữ các kết nối theo project
// trong một bản API, Redis chuyển event giữa các bản API, Tickets phát vé để mở kết nối WebSocket.
// Package không biết gì về WebSocket: tầng HTTP (internal/httpapi) đọc message từ Subscription rồi gửi đi.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/toidicode/taskflow/internal/task"
)

var (
	// ErrTooManyConnections: user đã có đủ Options.MaxPerUser kết nối.
	ErrTooManyConnections = errors.New("realtime: quá nhiều kết nối của một user")
	// ErrClosed: hub đã đóng (server đang tắt).
	ErrClosed = errors.New("realtime: server đang tắt")
	// ErrSlowConsumer: client đọc chậm tới mức hàng chờ của nó đầy.
	ErrSlowConsumer = errors.New("realtime: client đọc quá chậm")
)

// Options cấu hình Hub, field bằng 0 dùng giá trị mặc định.
type Options struct {
	Buffer     int // số message chờ gửi tối đa của mỗi kết nối, mặc định 16
	MaxPerUser int // số kết nối tối đa của mỗi user, mặc định 5
}

// Hub giữ các kết nối (Subscription) đang theo dõi từng project và chuyển message tới chúng.
// Một *Hub dùng chung cho mọi goroutine.
type Hub struct {
	opts    Options
	mu      sync.Mutex
	subs    map[int]map[*Subscription]struct{} // project ID → các kết nối
	perUser map[int]int                        // user ID → số kết nối
	closed  bool
	active  sync.WaitGroup // Subscription chưa Close: Wait chờ chúng
}

// NewHub tạo Hub rỗng.
func NewHub(opts Options) *Hub {
	if opts.Buffer <= 0 {
		opts.Buffer = 16
	}
	if opts.MaxPerUser <= 0 {
		opts.MaxPerUser = 5
	}
	return &Hub{opts: opts, subs: map[int]map[*Subscription]struct{}{}, perUser: map[int]int{}}
}

// Subscription là một kết nối đang nhận message của một project.
type Subscription struct {
	ProjectID, UserID int

	hub   *Hub
	send  chan []byte   // hàng chờ của kết nối, không bao giờ bị đóng
	done  chan struct{} // đóng khi hub ngừng gửi cho kết nối này
	err   error         // lý do, ghi trước khi đóng done
	once  sync.Once     // cho close (done)
	freed sync.Once     // cho Close (active)
}

// Messages là kênh message JSON chờ gửi tới client.
func (s *Subscription) Messages() <-chan []byte { return s.send }

// Done được đóng khi hub ngừng gửi cho kết nối này; Err cho biết lý do.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Err trả về lý do hub ngừng gửi (ErrSlowConsumer, ErrClosed), nil nếu Done chưa đóng hoặc do Close.
func (s *Subscription) Err() error {
	select {
	case <-s.done:
		return s.err
	default:
		return nil
	}
}

// Close hủy đăng ký. Handler gọi khi kết nối kết thúc vì bất kỳ lý do gì; gọi nhiều lần không sao.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	s.hub.remove(s, nil)
	s.hub.mu.Unlock()
	s.freed.Do(s.hub.active.Done)
}

// Subscribe đăng ký nhận message của project projectID cho user userID. Quyền xem project
// phải được kiểm tra trước khi gọi (hub không biết luật quyền).
func (h *Hub) Subscribe(projectID, userID int) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrClosed
	}
	if h.perUser[userID] >= h.opts.MaxPerUser {
		return nil, ErrTooManyConnections
	}
	s := &Subscription{ProjectID: projectID, UserID: userID, hub: h,
		send: make(chan []byte, h.opts.Buffer), done: make(chan struct{})}
	if h.subs[projectID] == nil {
		h.subs[projectID] = map[*Subscription]struct{}{}
	}
	h.subs[projectID][s] = struct{}{}
	h.perUser[userID]++
	h.active.Add(1)
	return s, nil
}

// Publish mã hóa e thành JSON rồi gửi cho các kết nối của project e.ProjectID (cài task.Publisher).
func (h *Hub) Publish(_ context.Context, e task.Event) error {
	msg, err := json.Marshal(e)
	if err != nil {
		return err
	}
	h.Broadcast(e.ProjectID, msg)
	return nil
}

// Broadcast đưa msg vào hàng chờ của mọi kết nối của project projectID. Không bao giờ chặn:
// kết nối có hàng chờ đầy bị ngắt (ErrSlowConsumer), để một client chậm không làm chậm
// người gửi event và các client khác.
func (h *Hub) Broadcast(projectID int, msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs[projectID] {
		select {
		case s.send <- msg:
		default:
			h.remove(s, ErrSlowConsumer)
		}
	}
}

// Close ngừng nhận kết nối mới và báo mọi kết nối đang mở kết thúc (ErrClosed).
// Dùng với http.Server.RegisterOnShutdown: Shutdown không tự đóng kết nối đã bị hijack.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for _, subs := range h.subs {
		for s := range subs {
			h.remove(s, ErrClosed)
		}
	}
}

// Wait chờ mọi Subscription được Close (handler đã gửi close frame và trả về), hoặc ctx hết hạn.
func (h *Hub) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		h.active.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Len trả về số kết nối đang mở.
func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, subs := range h.subs {
		n += len(subs)
	}
	return n
}

// remove gỡ s khỏi hub và đóng s.done với lý do err. Phải giữ h.mu.
func (h *Hub) remove(s *Subscription, err error) {
	subs := h.subs[s.ProjectID]
	if _, ok := subs[s]; !ok {
		return // đã gỡ rồi
	}
	delete(subs, s)
	if len(subs) == 0 {
		delete(h.subs, s.ProjectID)
	}
	if h.perUser[s.UserID]--; h.perUser[s.UserID] == 0 {
		delete(h.perUser, s.UserID)
	}
	s.once.Do(func() {
		s.err = err
		close(s.done)
	})
}
