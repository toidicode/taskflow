package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// TestRemindDeadlinesJob chạy job nhắc deadline qua hàng đợi như job định kỳ: job quét đưa job
// thông báo vào cùng hàng đợi, Notifier nhận một thông báo cho mỗi task sắp tới hạn.
func TestRemindDeadlinesJob(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	a.users.Add(ctx, &user.User{Email: "an@example.com"})
	a.users.Add(ctx, &user.User{Email: "binh@example.com"})
	a.projects.Add(ctx, project.New("TaskFlow API", 1))
	due := time.Now().Add(3 * time.Hour)
	a.tasks.Add(ctx, &task.Task{ProjectID: 1, Title: "Viết bài hàng đợi job", AssigneeID: 2, DueDate: due})
	a.tasks.Add(ctx, &task.Task{ProjectID: 1, Title: "Còn lâu", DueDate: time.Now().Add(72 * time.Hour)})

	for range 2 { // chạy hai lần (hai bản API cùng quét): vẫn chỉ một thông báo
		if err := a.queue.Enqueue(ctx, task.TypeRemindDeadlines, nil); err != nil {
			t.Fatal(err)
		}
	}
	// một worker chạy lần lượt: quét, quét, rồi job thông báo do lần quét đầu đưa vào.
	// Không gọi Stop ngay được: hàng đợi đã dừng thì job quét không đưa được job thông báo vào.
	rn := a.notifier.(*recordNotifier)
	for i := 0; i < 200 && len(rn.list()) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if err := a.queue.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	got := rn.list()
	if len(got) != 1 || got[0].TaskID != 1 || !slices.Equal(got[0].To, []int{2, 1}) || !got[0].DueDate.Equal(due) {
		t.Errorf("thông báo: %+v, muốn một thông báo task #1 cho Bình và An", got)
	}
}
