package task_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/toidicode/taskflow/internal/task"
)

// newStore tạo một MemoryStore chứa các task có title cho trước, ID lần lượt từ 1.
func newStore(t *testing.T, titles ...string) *task.MemoryStore {
	t.Helper() // lỗi được báo ở dòng gọi newStore, không phải ở đây

	s := task.NewMemoryStore()
	for _, title := range titles {
		tk, err := task.New(1, title)
		if err != nil {
			t.Fatalf("tạo task %q: %v", title, err)
		}
		s.Add(tk)
	}
	// chạy khi test (hoặc subtest) kết thúc: test hỏng thì in nội dung store
	t.Cleanup(func() {
		if t.Failed() {
			for tk := range s.All() {
				t.Logf("store: #%d %q %v", tk.ID, tk.Title, tk.Status)
			}
		}
	})
	return s
}

// titles trả về title của các task theo thứ tự All() trả ra.
func titles(s *task.MemoryStore) []string {
	var out []string
	for tk := range s.All() {
		out = append(out, tk.Title)
	}
	return out
}

func TestMemoryStore_AddGet(t *testing.T) {
	s := newStore(t, "Thiết kế database", "Viết API")

	got, err := s.Get(2)
	if err != nil {
		t.Fatalf("Get(2) lỗi: %v", err)
	}
	if got.ID != 2 || got.Title != "Viết API" {
		t.Errorf("Get(2) = #%d %q, muốn #2 %q", got.ID, got.Title, "Viết API")
	}

	_, err = s.Get(9)
	if !errors.Is(err, task.ErrNotFound) {
		t.Errorf("Get(9) lỗi = %v, muốn wrap ErrNotFound", err)
	}
}

func TestMemoryStore_All(t *testing.T) {
	s := newStore(t, "a", "b", "c", "d")

	t.Run("theo thứ tự ID", func(t *testing.T) {
		want := []string{"a", "b", "c", "d"}
		if got := titles(s); !slices.Equal(got, want) {
			t.Errorf("All() = %v, muốn %v", got, want)
		}
	})

	t.Run("dừng sớm", func(t *testing.T) {
		var got []int
		for tk := range s.All() {
			got = append(got, tk.ID)
			if tk.ID == 2 {
				break // iterator gọi yield sau khi đã trả về false sẽ làm test panic
			}
		}
		if !slices.Equal(got, []int{1, 2}) {
			t.Errorf("duyệt được %v, muốn [1 2]", got)
		}
	})
}

func TestMemoryStore_SaveLoad(t *testing.T) {
	s := newStore(t, "Thiết kế database", "Viết API", "Viết tài liệu")
	design, _ := s.Get(1)
	design.Complete()

	// thư mục tạm riêng cho test này, tự xóa khi test kết thúc
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save() lỗi: %v", err)
	}

	loaded := task.NewMemoryStore()
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load() lỗi: %v", err)
	}
	if got, want := titles(loaded), titles(s); !slices.Equal(got, want) {
		t.Errorf("sau Load: %v, muốn %v", got, want)
	}
	if got, _ := loaded.Get(1); !got.IsDone() || !got.CompletedAt.Equal(design.CompletedAt) {
		t.Errorf("task #1 sau Load: %v, %v; muốn done, %v", got.Status, got.CompletedAt, design.CompletedAt)
	}

	// task thêm sau Load phải nhận ID tiếp theo, không đè lên task cũ
	tk, _ := task.New(1, "Deploy")
	loaded.Add(tk)
	if tk.ID != 4 {
		t.Errorf("ID của task mới = %d, muốn 4", tk.ID)
	}
}

func TestMemoryStore_LoadError(t *testing.T) {
	dir := t.TempDir()

	t.Run("chưa có file", func(t *testing.T) {
		err := task.NewMemoryStore().Load(filepath.Join(dir, "khong-co.json"))
		if !errors.Is(err, fs.ErrNotExist) { // main dựa vào lỗi này để tạo task mẫu
			t.Errorf("Load() lỗi = %v, muốn wrap fs.ErrNotExist", err)
		}
	})

	t.Run("JSON hỏng", func(t *testing.T) {
		path := filepath.Join(dir, "hong.json")
		if err := os.WriteFile(path, []byte("[{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := task.NewMemoryStore().Load(path); err == nil {
			t.Error("Load() không lỗi, muốn có lỗi")
		}
	})
}

func TestMemoryStore_Concurrent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := task.NewMemoryStore()
		var wg sync.WaitGroup
		for range 50 {
			wg.Go(func() {
				tk, _ := task.New(1, "Viết API")
				s.Add(tk)
			})
			wg.Go(func() {
				for tk := range s.All() { // đọc trong lúc các goroutine khác ghi
					_, _ = s.Get(tk.ID)
				}
			})
		}
		wg.Wait()

		if n := len(s.List()); n != 50 {
			t.Errorf("số task = %d, muốn 50", n)
		}
	})
}

func BenchmarkMemoryStore_Get(b *testing.B) {
	s := task.NewMemoryStore()
	for range 1000 {
		tk, _ := task.New(1, "Viết API")
		s.Add(tk)
	}

	for b.Loop() { // chỉ phần trong vòng lặp được đo
		if _, err := s.Get(500); err != nil {
			b.Fatal(err)
		}
	}
}
