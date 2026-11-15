package task

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
)

// ErrNotFound được trả về (đã wrap) khi không có task cần tìm.
var ErrNotFound = errors.New("không tìm thấy")

// Store là nơi lưu và tìm task.
type Store interface {
	// Add cấp ID mới cho t rồi lưu lại.
	Add(t *Task)
	// Get trả về task có id cần tìm, lỗi wrap ErrNotFound khi không có task đó.
	Get(id int) (*Task, error)
	// List trả về mọi task, sắp xếp theo ID.
	List() []*Task
}

// MemoryStore lưu task trong bộ nhớ bằng một map.
type MemoryStore struct {
	tasks  map[int]*Task
	nextID int
}

// Kiểm tra lúc biên dịch: *MemoryStore phải thỏa mãn Store.
var _ Store = (*MemoryStore)(nil)

// NewMemoryStore tạo một store rỗng, task đầu tiên có ID là 1.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:  make(map[int]*Task),
		nextID: 1,
	}
}

func (s *MemoryStore) Add(t *Task) {
	t.ID = s.nextID
	s.nextID++
	s.tasks[t.ID] = t
}

func (s *MemoryStore) Get(id int) (*Task, error) {
	t, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task #%d: %w", id, ErrNotFound)
	}
	return t, nil
}

func (s *MemoryStore) List() []*Task {
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	slices.SortFunc(tasks, func(a, b *Task) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return tasks
}

// Save ghi mọi task ra file path dưới dạng JSON.
func (s *MemoryStore) Save(path string) error {
	data, err := json.MarshalIndent(s.List(), "", "  ")
	if err != nil {
		return fmt.Errorf("mã hóa task: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("lưu task: %w", err)
	}
	return nil
}

// Load đọc task từ file path, thay cho các task đang có trong store.
func (s *MemoryStore) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("đọc task: %w", err)
	}
	var tasks []*Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return fmt.Errorf("đọc task từ %s: %w", path, err)
	}

	s.tasks = make(map[int]*Task, len(tasks))
	s.nextID = 1
	for _, t := range tasks {
		s.tasks[t.ID] = t
		if t.ID >= s.nextID {
			s.nextID = t.ID + 1
		}
	}
	return nil
}
