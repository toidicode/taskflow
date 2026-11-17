package postgres_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/postgres"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/testdb"
)

func TestTaskStoreDueSoon(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := context.Background()
	projects, tasks := postgres.NewProjectStore(db), postgres.NewTaskStore(db)
	addUsers(t, db, "An")
	p := project.New("TaskFlow API", 1)
	if err := projects.Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	for _, x := range []struct {
		title string
		due   time.Time
		done  bool
	}{
		{"Hạn sau 20 giờ", now.Add(20 * time.Hour), false},
		{"Hạn sau 2 giờ", now.Add(2 * time.Hour), false},
		{"Hạn sau 30 giờ", now.Add(30 * time.Hour), false},
		{"Đã xong", now.Add(time.Hour), true},
		{"Không có hạn", time.Time{}, false},
	} {
		tk := &task.Task{ProjectID: p.ID, Title: x.title, DueDate: x.due, Tags: []string{"deadline"}}
		if x.done {
			tk.SetStatus(task.StatusDone)
		}
		if err := tasks.Add(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}

	ids := func() []int {
		t.Helper()
		due, err := tasks.DueSoon(ctx, now, now.Add(24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		var out []int
		for _, tk := range due {
			out = append(out, tk.ID)
			if len(tk.Tags) != 1 { // đọc kèm tag như mọi cách đọc task khác
				t.Errorf("task #%d không có tag", tk.ID)
			}
		}
		return out
	}
	if got := ids(); !slices.Equal(got, []int{2, 1}) { // sắp xếp theo hạn
		t.Fatalf("DueSoon = %v, muốn [2 1]", got)
	}

	// đã nhắc task #2 cho đúng hạn đó: không còn trong danh sách; đổi hạn thì có lại
	t2, _ := tasks.Get(ctx, 2)
	if err := tasks.MarkReminded(ctx, 2, t2.DueDate); err != nil {
		t.Fatal(err)
	}
	if got := ids(); !slices.Equal(got, []int{1}) {
		t.Errorf("sau MarkReminded(2) = %v, muốn [1]", got)
	}
	t2.DueDate = now.Add(3 * time.Hour)
	if err := tasks.Update(ctx, &t2); err != nil {
		t.Fatal(err)
	}
	if got := ids(); !slices.Equal(got, []int{2, 1}) {
		t.Errorf("sau khi đổi hạn task #2 = %v, muốn [2 1]", got)
	}
}
