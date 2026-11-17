package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// idQueue ghi lại ID và payload của mỗi job, từ chối ID trùng như hàng đợi Redis.
type idQueue struct {
	ids  []string
	sent []notify.Deadline
	fail error // khác nil: Enqueue trả lỗi này
}

func (q *idQueue) Enqueue(ctx context.Context, typ string, payload any, opts ...jobs.Option) error {
	if q.fail != nil {
		return q.fail
	}
	id := jobs.Collect(opts).ID
	if slices.Contains(q.ids, id) {
		return fmt.Errorf("%s: %w", typ, jobs.ErrDuplicate)
	}
	q.ids = append(q.ids, id)
	var n notify.Deadline
	data, _ := json.Marshal(payload)
	json.Unmarshal(data, &n)
	q.sent = append(q.sent, n)
	return nil
}

func TestRemindDeadlines(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	projects, tasks, users := project.NewMemoryStore(), task.NewMemoryStore(), user.NewMemoryStore()
	projects.Add(ctx, project.New("TaskFlow API", 1))
	users.Add(ctx, &user.User{Email: "an@example.com"})
	users.Add(ctx, &user.User{Email: "binh@example.com"})
	for _, x := range []struct {
		title    string
		assignee int
		due      time.Time
		done     bool
	}{
		{"Hạn trong 2 giờ, giao cho Bình", 2, now.Add(2 * time.Hour), false},
		{"Hạn trong 20 giờ, chưa giao", 0, now.Add(20 * time.Hour), false},
		{"Hạn sau 30 giờ", 2, now.Add(30 * time.Hour), false},
		{"Đã xong", 2, now.Add(time.Hour), true},
		{"Đã quá hạn", 2, now.Add(-time.Hour), false},
		{"Không có hạn", 2, time.Time{}, false},
	} {
		tk := &task.Task{ProjectID: 1, Title: x.title, AssigneeID: x.assignee, DueDate: x.due}
		if x.done {
			tk.SetStatus(task.StatusDone)
		}
		if err := tasks.Add(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}
	q := &idQueue{}
	s := task.NewService(tasks, projects, users, noFiles{}, q, nil)

	n, err := s.RemindDeadlines(ctx, now)
	if err != nil || n != 2 {
		t.Fatalf("lần 1: nhắc %d task, lỗi %v; muốn 2", n, err)
	}
	if !slices.Equal(q.ids, []string{"deadline:1:1794913200", "deadline:2:1794978000"}) {
		t.Errorf("ID của job: %q", q.ids)
	}
	if got := q.sent[0]; got.TaskID != 1 || !slices.Equal(got.To, []int{2, 1}) || !got.DueDate.Equal(now.Add(2*time.Hour)) {
		t.Errorf("thông báo task #1: %+v, muốn gửi Bình và An", got)
	}
	if got := q.sent[1]; got.TaskID != 2 || !slices.Equal(got.To, []int{1}) {
		t.Errorf("thông báo task #2: %+v, muốn gửi An (chủ project)", got)
	}

	// chạy lại (lần quét sau, hay bản API thứ hai): không nhắc lại
	if n, err := s.RemindDeadlines(ctx, now.Add(time.Minute)); err != nil || n != 0 {
		t.Errorf("lần 2: nhắc %d task, lỗi %v; muốn 0", n, err)
	}

	// đổi hạn của task #1: nhắc lại cho hạn mới
	t1, _ := tasks.Get(ctx, 1)
	t1.DueDate = now.Add(5 * time.Hour)
	tasks.Update(ctx, &t1)
	if n, err := s.RemindDeadlines(ctx, now); err != nil || n != 1 {
		t.Errorf("sau khi đổi hạn: nhắc %d task, lỗi %v; muốn 1", n, err)
	}

	// job đã có trong hàng đợi (lần trước lỗi sau khi đưa vào, trước khi đánh dấu): vẫn đánh dấu
	t2, _ := tasks.Get(ctx, 2)
	tasks.MarkReminded(ctx, 2, time.Time{}) // giả lập chưa kịp đánh dấu
	if n, err := s.RemindDeadlines(ctx, now); err != nil || n != 1 {
		t.Errorf("job trùng ID: nhắc %d task, lỗi %v; muốn 1 (đánh dấu, không gửi lại)", n, err)
	}
	if len(q.sent) != 3 {
		t.Errorf("đã gửi %d thông báo, muốn 3", len(q.sent))
	}

	// hàng đợi lỗi: báo lỗi, không đánh dấu, lần sau thử lại
	t2.DueDate = now.Add(3 * time.Hour)
	tasks.Update(ctx, &t2)
	q.fail = errors.New("redis: connection refused")
	if n, err := s.RemindDeadlines(ctx, now); err == nil || n != 0 {
		t.Errorf("hàng đợi lỗi: nhắc %d task, lỗi %v; muốn 0 và có lỗi", n, err)
	}
	q.fail = nil
	if n, err := s.RemindDeadlines(ctx, now); err != nil || n != 1 {
		t.Errorf("lần sau: nhắc %d task, lỗi %v; muốn 1", n, err)
	}
}
