package user

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNotFound được trả về (đã wrap) khi không có user cần tìm.
var ErrNotFound = errors.New("không tìm thấy")

// ErrEmailTaken được trả về (đã wrap) khi thêm user có email đã được đăng ký.
var ErrEmailTaken = errors.New("email đã được đăng ký")

// Store là nơi lưu và tìm user, cùng quy ước với task.Store và project.Store.
// Email truyền vào phải được chuẩn hóa trước bằng NormalizeEmail.
type Store interface {
	// Add lưu u, ghi ID và CreatedAt được cấp vào u. Lỗi wrap ErrEmailTaken khi email đã có.
	Add(ctx context.Context, u *User) error
	// Get trả về user có id cần tìm, lỗi wrap ErrNotFound khi không có user đó.
	Get(ctx context.Context, id int) (User, error)
	// GetByEmail trả về user có email cần tìm, lỗi wrap ErrNotFound khi không có user đó.
	GetByEmail(ctx context.Context, email string) (User, error)
}

// MemoryStore lưu user trong bộ nhớ, dùng được từ nhiều goroutine.
type MemoryStore struct {
	mu      sync.RWMutex // bảo vệ các field bên dưới
	users   map[int]User
	byEmail map[string]int // email → ID, giữ email là duy nhất
	nextID  int
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore tạo một store rỗng, user đầu tiên có ID là 1.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{users: make(map[int]User), byEmail: make(map[string]int), nextID: 1}
}

func (s *MemoryStore) Add(ctx context.Context, u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byEmail[u.Email]; ok {
		return fmt.Errorf("thêm user: %w", ErrEmailTaken)
	}
	u.ID = s.nextID
	s.nextID++
	u.CreatedAt = time.Now()
	s.users[u.ID] = *u
	s.byEmail[u.Email] = u.ID
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id int) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return User{}, fmt.Errorf("user #%d: %w", id, ErrNotFound)
	}
	return u, nil
}

func (s *MemoryStore) GetByEmail(ctx context.Context, email string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byEmail[email]
	if !ok {
		return User{}, fmt.Errorf("user %q: %w", email, ErrNotFound)
	}
	return s.users[id], nil
}
