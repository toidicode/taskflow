package task_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		if err := s.Add(context.Background(), tk); err != nil {
			t.Fatal(err)
		}
	}
	// chạy khi test (hoặc subtest) kết thúc: test hỏng thì in nội dung store
	t.Cleanup(func() {
		if t.Failed() {
			all, _ := s.List(context.Background(), task.Filter{})
			for _, tk := range all {
				t.Logf("store: #%d %q %v", tk.ID, tk.Title, tk.Status)
			}
		}
	})
	return s
}

// titles trả về title của các task theo thứ tự List trả ra.
func titles(t *testing.T, s *task.MemoryStore) []string {
	t.Helper()
	all, err := s.List(context.Background(), task.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tk := range all {
		out = append(out, tk.Title)
	}
	return out
}

func TestMemoryStore_AddGet(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, "Thiết kế database", "Viết API")

	got, err := s.Get(ctx, 2)
	if err != nil {
		t.Fatalf("Get(2) lỗi: %v", err)
	}
	if got.ID != 2 || got.Title != "Viết API" {
		t.Errorf("Get(2) = #%d %q, muốn #2 %q", got.ID, got.Title, "Viết API")
	}

	_, err = s.Get(ctx, 9)
	if !errors.Is(err, task.ErrNotFound) {
		t.Errorf("Get(9) lỗi = %v, muốn wrap ErrNotFound", err)
	}
}

func TestMemoryStore_Copies(t *testing.T) {
	ctx := context.Background()
	s := task.NewMemoryStore()
	tk, _ := task.New(1, "Viết API")
	_ = s.Add(ctx, tk)

	// sửa task đã Add và task do Get trả về đều không đổi dữ liệu trong store
	tk.Title = "sửa sau khi Add"
	got, _ := s.Get(ctx, 1)
	got.Start()
	if again, _ := s.Get(ctx, 1); again.Title != "Viết API" || again.Status != task.StatusTodo {
		t.Errorf("task trong store bị đổi: %q %v", again.Title, again.Status)
	}
}

func TestMemoryStore_UpdateDelete(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, "Thiết kế database", "Viết API")

	c, _ := s.Get(ctx, 2)
	c.Title = "Viết REST API"
	if err := s.Update(ctx, &c); err != nil {
		t.Fatalf("Update() lỗi: %v", err)
	}
	if got, _ := s.Get(ctx, 2); got.Title != "Viết REST API" {
		t.Errorf("sau Update: %q", got.Title)
	}
	if err := s.Update(ctx, &task.Task{ID: 9}); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("Update(#9) lỗi = %v, muốn wrap ErrNotFound", err)
	}

	if err := s.Delete(ctx, 1); err != nil {
		t.Fatalf("Delete(1) lỗi: %v", err)
	}
	if got := titles(t, s); !slices.Equal(got, []string{"Viết REST API"}) {
		t.Errorf("sau Delete: %v", got)
	}
	if err := s.Delete(ctx, 1); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("Delete(1) lần hai lỗi = %v, muốn wrap ErrNotFound", err)
	}
}

func TestMemoryStore_List(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, "a", "b", "c", "d")
	other, _ := task.New(2, "e")
	_ = s.Add(ctx, other)
	b, _ := s.Get(ctx, 2)
	b.SetStatus(task.StatusDone)
	_ = s.Update(ctx, &b)

	done := task.StatusDone
	tests := []struct {
		name string
		f    task.Filter
		want []int
	}{
		{"không lọc, theo thứ tự ID", task.Filter{}, []int{1, 2, 3, 4, 5}},
		{"theo project", task.Filter{ProjectID: 2}, []int{5}},
		{"theo trạng thái", task.Filter{Status: &done}, []int{2}},
		{"cả hai", task.Filter{ProjectID: 2, Status: &done}, []int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, err := s.List(ctx, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			ids := []int{}
			for _, tk := range list {
				ids = append(ids, tk.ID)
			}
			if !slices.Equal(ids, tt.want) {
				t.Errorf("List(%+v) = %v, muốn %v", tt.f, ids, tt.want)
			}
		})
	}
}

func TestMemoryStore_SaveLoad(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, "Thiết kế database", "Viết API", "Viết tài liệu")
	design, _ := s.Get(ctx, 1)
	design.Complete()
	_ = s.Update(ctx, &design)

	// thư mục tạm riêng cho test này, tự xóa khi test kết thúc
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save() lỗi: %v", err)
	}

	// từ Bài 25 file dùng key theo tag json và status dạng chữ
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"project_id": 1`, `"status": "done"`, `"completed_at": "`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("file không chứa %s:\n%s", want, data)
		}
	}

	loaded := task.NewMemoryStore()
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load() lỗi: %v", err)
	}
	if got, want := titles(t, loaded), titles(t, s); !slices.Equal(got, want) {
		t.Errorf("sau Load: %v, muốn %v", got, want)
	}
	if got, _ := loaded.Get(ctx, 1); !got.IsDone() || !got.CompletedAt.Equal(design.CompletedAt) {
		t.Errorf("task #1 sau Load: %v, %v; muốn done, %v", got.Status, got.CompletedAt, design.CompletedAt)
	}

	// task thêm sau Load phải nhận ID tiếp theo, không đè lên task cũ
	tk, _ := task.New(1, "Deploy")
	_ = loaded.Add(ctx, tk)
	if tk.ID != 4 {
		t.Errorf("ID của task mới = %d, muốn 4", tk.ID)
	}
}

func TestMemoryStore_LoadError(t *testing.T) {
	dir := t.TempDir()

	t.Run("chưa có file", func(t *testing.T) {
		err := task.NewMemoryStore().Load(filepath.Join(dir, "khong-co.json"))
		if !errors.Is(err, fs.ErrNotExist) { // CLI dựa vào lỗi này để bắt đầu với store rỗng
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
		ctx := context.Background()
		s := task.NewMemoryStore()
		first, _ := task.New(1, "Viết API")
		_ = s.Add(ctx, first)

		var wg sync.WaitGroup
		for i := range 50 {
			wg.Go(func() {
				tk, _ := task.New(1, "Viết API")
				_ = s.Add(ctx, tk)
			})
			wg.Go(func() {
				// đọc rồi sửa bản sao của cùng một task: không còn data race như ở Bài 18
				tk, _ := s.Get(ctx, 1)
				tk.Assign(i + 1)
				_ = s.Update(ctx, &tk)
			})
		}
		wg.Wait()

		if all, _ := s.List(ctx, task.Filter{}); len(all) != 51 {
			t.Errorf("số task = %d, muốn 51", len(all))
		}
	})
}

func BenchmarkMemoryStore_Get(b *testing.B) {
	ctx := context.Background()
	s := task.NewMemoryStore()
	for range 1000 {
		tk, _ := task.New(1, "Viết API")
		_ = s.Add(ctx, tk)
	}

	for b.Loop() { // chỉ phần trong vòng lặp được đo
		if _, err := s.Get(ctx, 500); err != nil {
			b.Fatal(err)
		}
	}
}
