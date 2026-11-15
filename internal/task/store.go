package task

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"
)

// ErrNotFound được trả về (đã wrap) khi không có task cần tìm.
var ErrNotFound = errors.New("không tìm thấy")

// Filter chọn task khi liệt kê, field có zero value là không lọc theo field đó.
type Filter struct {
	ProjectID int     // 0: mọi project
	Status    *Status // nil: mọi trạng thái (StatusTodo là 0 nên phải dùng con trỏ)
}

// Match cho biết t có thỏa f không.
func (f Filter) Match(t Task) bool {
	return (f.ProjectID == 0 || t.ProjectID == f.ProjectID) &&
		(f.Status == nil || t.Status == *f.Status)
}

// Store là nơi lưu và tìm task. Mọi method nhận context để dừng được khi request bị hủy,
// trả về lỗi vì store thật (database) có thể hỏng bất cứ lúc nào, và trả về bản sao
// của task chứ không phải con trỏ tới dữ liệu bên trong store.
type Store interface {
	// Add lưu t và ghi ID mới được cấp vào t.ID.
	Add(ctx context.Context, t *Task) error
	// Get trả về task có id cần tìm, lỗi wrap ErrNotFound khi không có task đó.
	Get(ctx context.Context, id int) (Task, error)
	// Update ghi đè task có cùng ID với t, lỗi wrap ErrNotFound khi không có task đó.
	Update(ctx context.Context, t *Task) error
	// Delete xóa task có id cần xóa, lỗi wrap ErrNotFound khi không có task đó.
	Delete(ctx context.Context, id int) error
	// List trả về các task thỏa f, sắp xếp theo ID.
	List(ctx context.Context, f Filter) ([]Task, error)
}

// MemoryStore lưu task trong bộ nhớ, dùng được từ nhiều goroutine.
// Map chứa giá trị Task: mọi method đều chép task ra/vào, bên ngoài không giữ được
// con trỏ tới dữ liệu của store.
type MemoryStore struct {
	mu     sync.RWMutex // bảo vệ tasks và nextID
	tasks  map[int]Task
	nextID int
}

// Kiểm tra lúc biên dịch: *MemoryStore phải thỏa mãn Store.
var _ Store = (*MemoryStore)(nil)

// NewMemoryStore tạo một store rỗng, task đầu tiên có ID là 1.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:  make(map[int]Task),
		nextID: 1,
	}
}

func (s *MemoryStore) Add(ctx context.Context, t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = s.nextID
	s.nextID++
	s.tasks[t.ID] = *t // lưu bản sao: sửa *t sau này không đổi dữ liệu trong store
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id int) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("task #%d: %w", id, ErrNotFound)
	}
	return t, nil // t là bản sao của giá trị trong map
}

func (s *MemoryStore) Update(ctx context.Context, t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[t.ID]; !ok {
		return fmt.Errorf("task #%d: %w", t.ID, ErrNotFound)
	}
	s.tasks[t.ID] = *t
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return fmt.Errorf("task #%d: %w", id, ErrNotFound)
	}
	delete(s.tasks, id)
	return nil
}

func (s *MemoryStore) List(ctx context.Context, f Filter) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tasks := []Task{} // không phải nil slice: không có task thì JSON là [] thay vì null
	for t := range maps.Values(s.tasks) {
		if f.Match(t) {
			tasks = append(tasks, t)
		}
	}
	slices.SortFunc(tasks, func(a, b Task) int { return cmp.Compare(a.ID, b.ID) })
	return tasks, nil
}

// Save ghi mọi task ra file path dưới dạng JSON.
func (s *MemoryStore) Save(path string) error {
	tasks, _ := s.List(context.Background(), Filter{}) // MemoryStore.List không trả lỗi
	data, err := json.MarshalIndent(tasks, "", "  ")
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
	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return fmt.Errorf("đọc task từ %s: %w", path, err)
	}

	// dựng map mới bên ngoài khóa, chỉ khóa lúc thay vào store
	m := make(map[int]Task, len(tasks))
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
