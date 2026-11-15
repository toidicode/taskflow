package project

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

// ErrNotFound được trả về (đã wrap) khi không có project cần tìm.
var ErrNotFound = errors.New("không tìm thấy")

// MemoryStore lưu project trong bộ nhớ, dùng được từ nhiều goroutine.
type MemoryStore struct {
	mu       sync.RWMutex // bảo vệ projects và nextID
	projects map[int]*Project
	nextID   int
}

// NewMemoryStore tạo một store rỗng, project đầu tiên có ID là 1.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{projects: make(map[int]*Project), nextID: 1}
}

// Add cấp ID mới cho p rồi lưu lại.
func (s *MemoryStore) Add(p *Project) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.ID = s.nextID
	s.nextID++
	s.projects[p.ID] = p
}

// Update thay project có cùng ID với p bằng p, lỗi wrap ErrNotFound khi không có project đó.
func (s *MemoryStore) Update(p *Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[p.ID]; !ok {
		return fmt.Errorf("project #%d: %w", p.ID, ErrNotFound)
	}
	s.projects[p.ID] = p
	return nil
}

// Delete xóa project có id cần xóa, lỗi wrap ErrNotFound khi không có project đó.
func (s *MemoryStore) Delete(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[id]; !ok {
		return fmt.Errorf("project #%d: %w", id, ErrNotFound)
	}
	delete(s.projects, id)
	return nil
}

// Get trả về project có id cần tìm, lỗi wrap ErrNotFound khi không có project đó.
func (s *MemoryStore) Get(id int) (*Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[id]
	if !ok {
		return nil, fmt.Errorf("project #%d: %w", id, ErrNotFound)
	}
	return p, nil
}

// List trả về mọi project, sắp xếp theo ID.
func (s *MemoryStore) List() []*Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ps := make([]*Project, 0, len(s.projects))
	for _, id := range slices.Sorted(maps.Keys(s.projects)) {
		ps = append(ps, s.projects[id])
	}
	return ps
}
