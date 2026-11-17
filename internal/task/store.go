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
	"time"

	"github.com/toidicode/taskflow/internal/project"
)

// ErrNotFound được trả về (đã wrap) khi không có task cần tìm.
var ErrNotFound = errors.New("không tìm thấy")

// Filter chọn task khi liệt kê, field có zero value là không lọc theo field đó.
type Filter struct {
	ProjectID int     // 0: mọi project
	Status    *Status // nil: mọi trạng thái (StatusTodo là 0 nên phải dùng con trỏ)
	Tag       string  // "": không lọc theo tag
	AfterID   int     // chỉ lấy task có ID lớn hơn AfterID (phân trang theo cursor)
	Limit     int     // số task tối đa, 0: không giới hạn
	VisibleTo int     // chỉ lấy task mà user này được xem (CanView), 0: không lọc (chỉ dùng ngoài API)
}

// Match cho biết t có thỏa các điều kiện lọc của f không (không tính Limit).
func (f Filter) Match(t Task) bool {
	return (f.ProjectID == 0 || t.ProjectID == f.ProjectID) &&
		(f.Status == nil || t.Status == *f.Status) &&
		(f.Tag == "" || t.HasTag(f.Tag)) &&
		t.ID > f.AfterID
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
	// AddTag gắn tag name (đã chuẩn hóa) vào task taskID, task đã có tag đó thì không làm gì.
	// Lỗi wrap ErrNotFound khi không có task đó.
	AddTag(ctx context.Context, taskID int, name string) error
	// RemoveTag gỡ tag name khỏi task taskID, task không có tag đó thì không làm gì.
	// Lỗi wrap ErrNotFound khi không có task đó.
	RemoveTag(ctx context.Context, taskID int, name string) error
	// DueSoon trả về các task chưa xong có hạn trong (from, to] mà chưa được nhắc cho đúng hạn
	// đó (MarkReminded), sắp xếp theo hạn.
	DueSoon(ctx context.Context, from, to time.Time) ([]Task, error)
	// MarkReminded ghi lại đã nhắc task id cho hạn due. Không có task đó thì không làm gì.
	MarkReminded(ctx context.Context, id int, due time.Time) error
}

// MemoryStore lưu task trong bộ nhớ, dùng được từ nhiều goroutine.
// Map chứa giá trị Task: mọi method đều chép task ra/vào (kể cả slice Tags),
// bên ngoài không giữ được con trỏ tới dữ liệu của store.
type MemoryStore struct {
	mu        sync.RWMutex // bảo vệ các field bên dưới
	tasks     map[int]Task
	nextID    int
	projectOf ProjectFunc       // tìm project của task khi lọc theo Filter.VisibleTo
	reminded  map[int]time.Time // task ID → hạn đã nhắc
}

// ProjectFunc trả về project có ID projectID, false khi không có project đó.
type ProjectFunc func(projectID int) (project.Project, bool)

// SetProjectFunc cho MemoryStore cách tìm project của một task. Task không lưu chủ project,
// nên thiếu f thì List với Filter.VisibleTo chỉ trả về task được giao cho user đó.
func (s *MemoryStore) SetProjectFunc(f ProjectFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectOf = f
}

// visible cho biết user userID có được xem t không, userID 0 là không lọc. s.mu phải đang được khóa.
func (s *MemoryStore) visible(userID int, t Task) bool {
	if userID == 0 {
		return true
	}
	var p project.Project
	if s.projectOf != nil {
		p, _ = s.projectOf(t.ProjectID) // không có project thì p rỗng, chỉ còn quyền của người được giao
	}
	return CanView(userID, p, t)
}

// Kiểm tra lúc biên dịch: *MemoryStore phải thỏa mãn Store.
var _ Store = (*MemoryStore)(nil)

// NewMemoryStore tạo một store rỗng, task đầu tiên có ID là 1.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:    make(map[int]Task),
		nextID:   1,
		reminded: make(map[int]time.Time),
	}
}

func (s *MemoryStore) Add(ctx context.Context, t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = s.nextID
	s.nextID++
	s.tasks[t.ID] = clone(*t) // lưu bản sao: sửa *t sau này không đổi dữ liệu trong store
	return nil
}

// clone chép t kèm mảng của slice Tags: Task chép bằng phép gán vẫn dùng chung mảng đó.
func clone(t Task) Task {
	t.Tags = slices.Clone(t.Tags)
	return t
}

func (s *MemoryStore) Get(ctx context.Context, id int) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("task #%d: %w", id, ErrNotFound)
	}
	return clone(t), nil
}

func (s *MemoryStore) Update(ctx context.Context, t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[t.ID]; !ok {
		return fmt.Errorf("task #%d: %w", t.ID, ErrNotFound)
	}
	s.tasks[t.ID] = clone(*t)
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return fmt.Errorf("task #%d: %w", id, ErrNotFound)
	}
	delete(s.tasks, id)
	delete(s.reminded, id)
	return nil
}

func (s *MemoryStore) List(ctx context.Context, f Filter) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tasks := []Task{} // không phải nil slice: không có task thì JSON là [] thay vì null
	for t := range maps.Values(s.tasks) {
		if f.Match(t) && s.visible(f.VisibleTo, t) {
			tasks = append(tasks, clone(t))
		}
	}
	slices.SortFunc(tasks, func(a, b Task) int { return cmp.Compare(a.ID, b.ID) })
	if f.Limit > 0 && len(tasks) > f.Limit {
		tasks = tasks[:f.Limit]
	}
	return tasks, nil
}

func (s *MemoryStore) AddTag(ctx context.Context, taskID int, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[taskID]
	if !ok {
		return fmt.Errorf("task #%d: %w", taskID, ErrNotFound)
	}
	if !t.HasTag(name) {
		t.Tags = NormalizeTags(append(slices.Clone(t.Tags), name))
		s.tasks[taskID] = t
	}
	return nil
}

func (s *MemoryStore) RemoveTag(ctx context.Context, taskID int, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[taskID]
	if !ok {
		return fmt.Errorf("task #%d: %w", taskID, ErrNotFound)
	}
	t.Tags = slices.DeleteFunc(slices.Clone(t.Tags), func(tag string) bool { return tag == name })
	s.tasks[taskID] = t
	return nil
}

func (s *MemoryStore) DueSoon(ctx context.Context, from, to time.Time) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var tasks []Task
	for t := range maps.Values(s.tasks) {
		if !t.IsDone() && t.DueDate.After(from) && !t.DueDate.After(to) && !s.reminded[t.ID].Equal(t.DueDate) {
			tasks = append(tasks, clone(t))
		}
	}
	slices.SortFunc(tasks, func(a, b Task) int {
		return cmp.Or(a.DueDate.Compare(b.DueDate), cmp.Compare(a.ID, b.ID))
	})
	return tasks, nil
}

func (s *MemoryStore) MarkReminded(ctx context.Context, id int, due time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; ok {
		s.reminded[id] = due
	}
	return nil
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
