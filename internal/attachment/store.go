package attachment

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// ErrNotFound được trả về (đã wrap) khi không có file đính kèm cần tìm.
var ErrNotFound = errors.New("không tìm thấy")

// Store lưu thông tin file đính kèm, cùng quy ước với task.Store.
type Store interface {
	// Add lưu a, ghi ID và CreatedAt được cấp vào a. Không có task a.TaskID
	// (VD vừa bị xóa) thì lỗi wrap task.ErrNotFound.
	Add(ctx context.Context, a *Attachment) error
	// Get trả về file có id cần tìm, lỗi wrap ErrNotFound khi không có.
	Get(ctx context.Context, id int) (Attachment, error)
	// List trả về các file của task taskID, sắp xếp theo ID.
	List(ctx context.Context, taskID int) ([]Attachment, error)
	// Delete xóa thông tin file id (không xóa nội dung trong storage), lỗi wrap ErrNotFound khi không có.
	Delete(ctx context.Context, id int) error
}

// MemoryStore lưu thông tin file trong bộ nhớ, dùng được từ nhiều goroutine.
// Nó không có khóa ngoại như PostgreSQL: xóa task thì hàng của task đó vẫn còn,
// nhưng không ai đọc được vì API luôn kiểm tra task trước.
type MemoryStore struct {
	mu          sync.RWMutex // bảo vệ các field bên dưới
	attachments map[int]Attachment
	nextID      int
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore tạo một store rỗng, file đầu tiên có ID là 1.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{attachments: make(map[int]Attachment), nextID: 1}
}

func (s *MemoryStore) Add(ctx context.Context, a *Attachment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.ID = s.nextID
	s.nextID++
	a.CreatedAt = time.Now()
	s.attachments[a.ID] = *a
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id int) (Attachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.attachments[id]
	if !ok {
		return Attachment{}, fmt.Errorf("attachment #%d: %w", id, ErrNotFound)
	}
	return a, nil
}

func (s *MemoryStore) List(ctx context.Context, taskID int) ([]Attachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := []Attachment{} // không có file nào thì vẫn là [] chứ không phải nil
	for _, a := range s.attachments {
		if a.TaskID == taskID {
			list = append(list, a)
		}
	}
	slices.SortFunc(list, func(a, b Attachment) int { return cmp.Compare(a.ID, b.ID) })
	return list, nil
}

func (s *MemoryStore) Delete(ctx context.Context, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.attachments[id]; !ok {
		return fmt.Errorf("attachment #%d: %w", id, ErrNotFound)
	}
	delete(s.attachments, id)
	return nil
}
