package task_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/user"
)

// Service được test thẳng bằng store trong bộ nhớ, không cần HTTP: đó là lợi ích chính của tầng service.

// noFiles cài task.Files cho task không có file đính kèm.
type noFiles struct{}

func (noFiles) StoredNames(context.Context, int) ([]string, error) { return nil, nil }
func (noFiles) Remove(context.Context, ...string)                  {}

// runQueue cài task.Queue: chạy job ngay khi nhận, đủ cho test.
type runQueue struct{}

func (runQueue) Enqueue(j jobs.Job) error { return j.Run(context.Background()) }

// sentNotifier ghi lại các thông báo.
type sentNotifier struct{ sent []notify.TaskDone }

func (n *sentNotifier) TaskDone(_ context.Context, d notify.TaskDone) error {
	n.sent = append(n.sent, d)
	return nil
}

// newService tạo Service với project #1 của user #1 (An), user #2 (Bình) và task #1 giao cho Bình.
// events nhận event của Service, nil là không gửi.
func newService(t *testing.T, events task.Publisher) (*task.Service, *sentNotifier) {
	t.Helper()
	ctx := context.Background()
	projects, tasks, users := project.NewMemoryStore(), task.NewMemoryStore(), user.NewMemoryStore()
	for _, err := range []error{
		projects.Add(ctx, project.New("TaskFlow API", 1)),
		users.Add(ctx, &user.User{Email: "an@example.com"}),
		users.Add(ctx, &user.User{Email: "binh@example.com"}),
		tasks.Add(ctx, &task.Task{ProjectID: 1, Title: "Viết test", AssigneeID: 2}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	n := &sentNotifier{}
	return task.NewService(tasks, projects, users, noFiles{}, runQueue{}, n, events), n
}

func TestServiceUpdate(t *testing.T) {
	ctx := context.Background()
	s, n := newService(t, nil)
	title, done := "Viết test service", task.StatusDone

	// người ngoài: như không có task; người được giao: chỉ đổi status
	if _, err := s.Update(ctx, 3, 1, task.Changes{Status: &done}); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("user #3: lỗi = %v, muốn ErrNotFound", err)
	}
	if _, err := s.Update(ctx, 2, 1, task.Changes{Title: &title}); !errors.As(err, new(*task.ForbiddenError)) {
		t.Errorf("Bình sửa title: lỗi = %v, muốn ForbiddenError", err)
	}
	got, err := s.Update(ctx, 2, 1, task.Changes{Status: &done})
	if err != nil || !got.IsDone() {
		t.Fatalf("Bình chuyển sang done: %+v, %v", got, err)
	}
	if len(n.sent) != 1 || n.sent[0].DoneBy != 2 || !slices.Equal(n.sent[0].To, []int{1}) {
		t.Errorf("thông báo = %+v, muốn một thông báo cho An", n.sent)
	}
}

func TestServiceCreateInvalid(t *testing.T) {
	s, _ := newService(t, nil)
	err := s.Create(context.Background(), 2, &task.Task{ProjectID: 1, Title: " ", AssigneeID: 9})

	// mọi lỗi dữ liệu được báo cùng lúc: project của người khác, title trống, user không có
	joined, ok := err.(interface{ Unwrap() []error }) // lỗi gộp bằng errors.Join
	if !ok {
		t.Fatalf("lỗi = %v, muốn nhiều lỗi gộp lại", err)
	}
	fields := map[string]bool{}
	for _, e := range joined.Unwrap() {
		if ve, ok := errors.AsType[*task.ValidationError](e); ok {
			fields[ve.Field] = true
		}
	}
	if len(fields) != 3 || !fields["project_id"] || !fields["title"] || !fields["assignee_id"] {
		t.Errorf("lỗi = %v, muốn lỗi ở project_id, title, assignee_id", err)
	}
}
