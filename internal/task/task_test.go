package task_test

import (
	"encoding/json"
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
		{name: "title quá dài", projectID: 1, title: strings.Repeat("á", 201), wantErr: "title: tối đa 200 ký tự"},
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

func TestValidate(t *testing.T) {
	valid := task.Task{ProjectID: 1, Title: "Viết API", Description: strings.Repeat("a", task.MaxDescriptionLen)}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() task hợp lệ lỗi: %v", err)
	}

	bad := task.Task{Title: strings.Repeat("á", task.MaxTitleLen+1), Description: strings.Repeat("a", task.MaxDescriptionLen+1), AssigneeID: -1}
	err := bad.Validate()
	want := "project_id: phải lớn hơn 0\ntitle: tối đa 200 ký tự\ndescription: tối đa 2000 ký tự\nassignee_id: không được âm"
	if err == nil || err.Error() != want {
		t.Fatalf("Validate() = %v, muốn %q", err, want)
	}
	// mọi lỗi con đều là *ValidationError để API trả về theo từng field
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		if _, ok := e.(*task.ValidationError); !ok {
			t.Errorf("lỗi %v có kiểu %T, muốn *task.ValidationError", e, e)
		}
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

func TestParseStatus(t *testing.T) {
	for _, want := range []task.Status{task.StatusTodo, task.StatusDoing, task.StatusDone} {
		got, err := task.ParseStatus(want.String())
		if err != nil || got != want {
			t.Errorf("ParseStatus(%q) = %v, %v; muốn %v, nil", want.String(), got, err, want)
		}
	}
	if _, err := task.ParseStatus("xong"); err == nil {
		t.Errorf("ParseStatus(%q) không trả về lỗi", "xong")
	}
}

func TestTaskJSON(t *testing.T) {
	due := time.Date(2026, 11, 30, 17, 0, 0, 0, time.UTC)
	tk := &task.Task{ID: 3, ProjectID: 1, Title: "Viết test", Status: task.StatusDoing, DueDate: due}

	b, err := json.Marshal(tk)
	if err != nil {
		t.Fatalf("Marshal() lỗi: %v", err)
	}
	// description, assignee_id, completed_at là zero value nên bị bỏ
	want := `{"id":3,"project_id":1,"title":"Viết test","status":"doing","due_date":"2026-11-30T17:00:00Z"}`
	if string(b) != want {
		t.Errorf("Marshal() = %s\nmuốn        %s", b, want)
	}

	var got task.Task
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal() lỗi: %v", err)
	}
	if got.Status != task.StatusDoing || !got.DueDate.Equal(due) || !got.CompletedAt.IsZero() {
		t.Errorf("Unmarshal() = %+v", got)
	}

	for _, in := range []string{`{"status":"xong"}`, `{"status":2}`} {
		if err := json.Unmarshal([]byte(in), &got); err == nil {
			t.Errorf("Unmarshal(%s) không lỗi, muốn có lỗi", in)
		}
	}
	if _, err := json.Marshal(task.Status(7)); err == nil {
		t.Error("Marshal(Status(7)) không lỗi, muốn có lỗi")
	}
}

func TestSetStatus(t *testing.T) {
	tk, _ := task.New(1, "Viết API")
	tk.SetStatus(task.StatusDone)
	done := tk.CompletedAt
	if !tk.IsDone() || done.IsZero() {
		t.Fatalf("sau SetStatus(done): %v, %v", tk.Status, tk.CompletedAt)
	}
	tk.SetStatus(task.StatusDone) // đã xong thì giữ nguyên thời điểm hoàn thành
	if !tk.CompletedAt.Equal(done) {
		t.Errorf("CompletedAt đổi thành %v, muốn giữ %v", tk.CompletedAt, done)
	}
	tk.SetStatus(task.StatusDoing)
	if tk.Status != task.StatusDoing || !tk.CompletedAt.IsZero() {
		t.Errorf("sau SetStatus(doing): %v, %v; muốn doing, zero", tk.Status, tk.CompletedAt)
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
