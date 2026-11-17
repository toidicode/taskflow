package realtime

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// TicketTTL là thời gian sống của một vé: đủ để trình duyệt mở WebSocket ngay sau khi nhận vé.
const TicketTTL = 30 * time.Second

// ErrInvalidTicket: vé không có, đã dùng hoặc đã hết hạn.
var ErrInvalidTicket = errors.New("vé không hợp lệ, đã dùng hoặc đã hết hạn")

// Tickets phát và đổi vé kết nối WebSocket. Trình duyệt không gửi được header Authorization khi mở
// WebSocket, nên client lấy vé bằng một request có access token rồi gửi vé trên URL (?ticket=).
// Vé là chuỗi ngẫu nhiên, dùng một lần, sống TicketTTL: lỡ bị ghi vào log nào đó thì cũng đã vô dụng.
type Tickets interface {
	// Issue phát vé mới cho user userID.
	Issue(ctx context.Context, userID int) (string, error)
	// Redeem đổi vé lấy user ID và hủy vé. Vé sai, đã dùng hay hết hạn trả ErrInvalidTicket.
	Redeem(ctx context.Context, ticket string) (int, error)
}

// MemoryTickets giữ vé trong bộ nhớ: chỉ đúng khi chạy một bản API.
type MemoryTickets struct {
	mu      sync.Mutex
	tickets map[string]memoryTicket
}

type memoryTicket struct {
	userID  int
	expires time.Time
}

// NewMemoryTickets tạo MemoryTickets rỗng.
func NewMemoryTickets() *MemoryTickets {
	return &MemoryTickets{tickets: map[string]memoryTicket{}}
}

func (m *MemoryTickets) Issue(_ context.Context, userID int) (string, error) {
	now := time.Now()
	t := rand.Text() // 26 ký tự base32, 128 bit ngẫu nhiên
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.tickets { // dọn vé hết hạn mà không ai dùng, map không phình mãi
		if now.After(v.expires) {
			delete(m.tickets, k)
		}
	}
	m.tickets[t] = memoryTicket{userID: userID, expires: now.Add(TicketTTL)}
	return t, nil
}

func (m *MemoryTickets) Redeem(_ context.Context, ticket string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.tickets[ticket]
	delete(m.tickets, ticket) // dùng một lần
	if !ok || time.Now().After(v.expires) {
		return 0, ErrInvalidTicket
	}
	return v.userID, nil
}

// RedisTickets giữ vé trong Redis, để lấy vé ở bản API này và kết nối tới bản API khác.
type RedisTickets struct {
	client *redis.Client
	prefix string
}

// NewRedisTickets tạo RedisTickets lưu vé dưới các key prefix+vé.
func NewRedisTickets(client *redis.Client, prefix string) *RedisTickets {
	return &RedisTickets{client: client, prefix: prefix}
}

func (r *RedisTickets) Issue(ctx context.Context, userID int) (string, error) {
	t := rand.Text()
	if err := r.client.Set(ctx, r.prefix+t, userID, TicketTTL).Err(); err != nil {
		return "", err
	}
	return t, nil
}

func (r *RedisTickets) Redeem(ctx context.Context, ticket string) (int, error) {
	v, err := r.client.GetDel(ctx, r.prefix+ticket).Result() // đọc và xóa trong một lệnh: hai request cùng vé chỉ một cái thắng
	if errors.Is(err, redis.Nil) {
		return 0, ErrInvalidTicket
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}
