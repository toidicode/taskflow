package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"os"
	"slices"
	"sync"
)

// ErrNotFound được trả về (đã wrap) khi không có task cần tìm.
var ErrNotFound = errors.New("không tìm thấy")

// Store là nơi lưu và tìm task.
type Store interface {
	// Add cấp ID mới cho t rồi lưu lại.
	Add(t *Task)
	// Get trả về task có id cần tìm, lỗi wrap ErrNotFound khi không có task đó.
	Get(id int) (*Task, error)
	// All trả về iterator duyệt mọi task theo thứ tự ID.
	All() iter.Seq[*Task]
}

// MemoryStore lưu task trong bộ nhớ bằng một map, dùng được từ nhiều goroutine.
// Mutex chỉ bảo vệ map và nextID, không bảo vệ các field của từng Task.
type MemoryStore struct {
	mu     sync.RWMutex // bảo vệ tasks và nextID
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
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = s.nextID
	s.nextID++
	s.tasks[t.ID] = t
}

func (s *MemoryStore) Get(id int) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task #%d: %w", id, ErrNotFound)
	}
	return t, nil
}

func (s *MemoryStore) All() iter.Seq[*Task] {
	return func(yield func(*Task) bool) {
		// chụp lại danh sách task khi đang giữ khóa đọc...
		s.mu.RLock()
		ids := slices.Sorted(maps.Keys(s.tasks))
		tasks := make([]*Task, len(ids))
		for i, id := range ids {
			tasks[i] = s.tasks[id]
		}
		s.mu.RUnlock()

		// ...rồi mới yield, lúc này không giữ khóa nữa
		for _, t := range tasks {
			if !yield(t) {
				return
			}
		}
	}
}

// List trả về mọi task dưới dạng slice, sắp xếp theo ID.
func (s *MemoryStore) List() []*Task {
	return slices.Collect(s.All())
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

	// dựng map mới bên ngoài khóa, chỉ khóa lúc thay vào store
	m := make(map[int]*Task, len(tasks))
	nextID := 1
	for _, t := range tasks {
		m[t.ID] = t
		if t.ID >= nextID {
			nextID = t.ID + 1
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = m
	s.nextID = nextID
	return nil
}
