package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
)

// ErrNotFound được trả về (đã wrap) khi không có task cần tìm.
var ErrNotFound = errors.New("không tìm thấy")

// TaskStore là nơi lưu và tìm task.
type TaskStore interface {
	// Add cấp ID mới cho t rồi lưu lại.
	Add(t *Task)
	// Get trả về task có id cần tìm, lỗi wrap ErrNotFound khi không có task đó.
	Get(id int) (*Task, error)
	// List trả về mọi task, sắp xếp theo ID.
	List() []*Task
}

// MemoryTaskStore lưu task trong bộ nhớ bằng một map.
type MemoryTaskStore struct {
	tasks  map[int]*Task
	nextID int
}

// Kiểm tra lúc biên dịch: *MemoryTaskStore phải thỏa mãn TaskStore.
var _ TaskStore = (*MemoryTaskStore)(nil)

// NewMemoryTaskStore tạo một store rỗng, task đầu tiên có ID là 1.
func NewMemoryTaskStore() *MemoryTaskStore {
	return &MemoryTaskStore{
		tasks:  make(map[int]*Task),
		nextID: 1,
	}
}

func (s *MemoryTaskStore) Add(t *Task) {
	t.ID = s.nextID
	s.nextID++
	s.tasks[t.ID] = t
}

func (s *MemoryTaskStore) Get(id int) (*Task, error) {
	t, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task #%d: %w", id, ErrNotFound)
	}
	return t, nil
}

func (s *MemoryTaskStore) List() []*Task {
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	slices.SortFunc(tasks, func(a, b *Task) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return tasks
}
