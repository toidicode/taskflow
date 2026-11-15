package project

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

// ErrNotFound được trả về (đã wrap) khi không có project cần tìm.
var ErrNotFound = errors.New("không tìm thấy")

// Store là nơi lưu và tìm project, cùng quy ước với task.Store:
// nhận context, trả về lỗi và trả về bản sao.
type Store interface {
	// Add lưu p và ghi ID mới được cấp vào p.ID.
	Add(ctx context.Context, p *Project) error
	// Get trả về project có id cần tìm, lỗi wrap ErrNotFound khi không có project đó.
	Get(ctx context.Context, id int) (Project, error)
	// Update ghi đè project có cùng ID với p, lỗi wrap ErrNotFound khi không có project đó.
	Update(ctx context.Context, p *Project) error
	// Delete xóa project có id cần xóa, lỗi wrap ErrNotFound khi không có project đó.
	Delete(ctx context.Context, id int) error
	// List trả về mọi project, sắp xếp theo ID.
	List(ctx context.Context) ([]Project, error)
}

// MemoryStore lưu project trong bộ nhớ, dùng được từ nhiều goroutine.
type MemoryStore struct {
	mu       sync.RWMutex // bảo vệ projects và nextID
	projects map[int]Project
	nextID   int
}

// Kiểm tra lúc biên dịch: *MemoryStore phải thỏa mãn Store.
var _ Store = (*MemoryStore)(nil)

// NewMemoryStore tạo một store rỗng, project đầu tiên có ID là 1.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{projects: make(map[int]Project), nextID: 1}
}

func (s *MemoryStore) Add(ctx context.Context, p *Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.ID = s.nextID
	s.nextID++
	s.projects[p.ID] = *p
	return nil
}

func (s *MemoryStore) Update(ctx context.Context, p *Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[p.ID]; !ok {
		return fmt.Errorf("project #%d: %w", p.ID, ErrNotFound)
	}
	s.projects[p.ID] = *p
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[id]; !ok {
		return fmt.Errorf("project #%d: %w", id, ErrNotFound)
	}
	delete(s.projects, id)
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id int) (Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[id]
	if !ok {
		return Project{}, fmt.Errorf("project #%d: %w", id, ErrNotFound)
	}
	return p, nil
}

func (s *MemoryStore) List(ctx context.Context) ([]Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ps := []Project{} // JSON là [] thay vì null
	for p := range maps.Values(s.projects) {
		ps = append(ps, p)
	}
	slices.SortFunc(ps, func(a, b Project) int { return cmp.Compare(a.ID, b.ID) })
	return ps, nil
}
