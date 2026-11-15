package task_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/task"
)

func TestComplete(t *testing.T) {
	tk, err := task.New(1, "Viết tài liệu")
	if err != nil {
		t.Fatalf("New() lỗi: %v", err) // không có task thì không test tiếp được
	}

	before := time.Now()
	tk.Complete()

	if !tk.IsDone() {
		t.Errorf("IsDone() = false, muốn true")
	}
	if tk.CompletedAt.Before(before) || tk.CompletedAt.After(time.Now()) {
		t.Errorf("CompletedAt = %v, muốn thời điểm gọi Complete()", tk.CompletedAt)
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		projectID int
		title     string
		wantTitle string // title sau khi tạo, khi không có lỗi
		wantErr   string // thông báo lỗi, rỗng nghĩa là hợp lệ
	}{
		{name: "hợp lệ", projectID: 1, title: "Thiết kế database", wantTitle: "Thiết kế database"},
		{name: "bỏ khoảng trắng", projectID: 1, title: "  Viết API  ", wantTitle: "Viết API"},
		{name: "title rỗng", projectID: 1, title: "   ", wantErr: "title: không được để trống"},
		{name: "project_id bằng 0", projectID: 0, title: "Viết API", wantErr: "project_id: phải lớn hơn 0"},
		{name: "sai cả hai", projectID: -1, title: "", wantErr: "project_id: phải lớn hơn 0\ntitle: không được để trống"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := task.New(tt.projectID, tt.title)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("New() lỗi: %v", err)
				}
				if got.Title != tt.wantTitle || got.Status != task.StatusTodo {
					t.Errorf("New() = {%q, %v}, muốn {%q, todo}", got.Title, got.Status, tt.wantTitle)
				}
				return
			}

			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("New() lỗi = %v, muốn %q", err, tt.wantErr)
			}
			var ve *task.ValidationError
			if !errors.As(err, &ve) {
				t.Errorf("New() lỗi không chứa *ValidationError")
			}
		})
	}
}

func TestIsOverdue(t *testing.T) {
	// thời điểm cố định: kết quả không phụ thuộc lúc chạy test
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		due  time.Time
		done bool
		want bool
	}{
		{"không có hạn", time.Time{}, false, false},
		{"còn hạn", now.Add(time.Hour), false, false},
		{"đúng hạn", now, false, false},
		{"quá hạn", now.Add(-time.Hour), false, true},
		{"quá hạn nhưng đã xong", now.Add(-time.Hour), true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // các case độc lập nên chạy song song được

			tk := &task.Task{DueDate: tt.due}
			if tt.done {
				tk.Complete()
			}
			if got := tk.IsOverdue(now); got != tt.want {
				t.Errorf("IsOverdue(%v) = %v, muốn %v", now, got, tt.want)
			}
		})
	}
}

func FuzzNew(f *testing.F) {
	// corpus mẫu: fuzzer biến đổi dần từ các giá trị này
	f.Add("Thiết kế database")
	f.Add("   ")
	f.Add("\tViết API\n")

	f.Fuzz(func(t *testing.T, title string) {
		tk, err := task.New(1, title)
		if err != nil {
			var ve *task.ValidationError
			if !errors.As(err, &ve) || ve.Field != "title" {
				t.Fatalf("New(1, %q) lỗi lạ: %v", title, err)
			}
			return
		}
		if tk.Title == "" || tk.Title != strings.TrimSpace(tk.Title) {
			t.Errorf("New(1, %q).Title = %q, muốn khác rỗng và không có khoảng trắng ở hai đầu", title, tk.Title)
		}
	})
}
