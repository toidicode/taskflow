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
	mu       sync.RWMutex // bảo vệ projects
	projects map[int]*Project
}

// NewMemoryStore tạo một store rỗng.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{projects: make(map[int]*Project)}
}

// Add lưu p theo p.ID, project cùng ID đã có sẽ bị thay.
func (s *MemoryStore) Add(p *Project) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[p.ID] = p
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
