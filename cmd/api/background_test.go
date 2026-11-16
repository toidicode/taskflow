package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
)

// recordNotifier ghi lại các thông báo thay vì gửi, dùng được từ nhiều goroutine.
type recordNotifier struct {
	mu   sync.Mutex
	sent []notify.TaskDone
}

func (r *recordNotifier) TaskDone(ctx context.Context, n notify.TaskDone) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, n)
	return nil
}

func TestTaskDoneNotification(t *testing.T) {
	ctx := context.Background()
	s := newTestStores(t)
	addUsers(t, s, "Binh")
	t3, _ := s.tasks.Get(ctx, 3)
	t3.AssigneeID = 2 // task #3 (project của An) giao cho Bình
	if err := s.tasks.Update(ctx, &t3); err != nil {
		t.Fatal(err)
	}
	mux := newMux(s, testTokens)

	steps := []struct {
		user int
		body string
	}{
		{2, `{"status": "doing"}`}, // chưa xong: không báo
		{2, `{"status": "done"}`},  // Bình làm xong: báo cho An
		{1, `{"status": "done"}`},  // đã done từ trước: không báo lại
		{1, `{"title": "Viết test cho API (đã review)"}`},
	}
	for _, st := range steps {
		if rec := sendAs(mux, st.user, "PATCH", "/api/v1/tasks/3", st.body); rec.Code != http.StatusOK {
			t.Fatalf("PATCH %s: %d %s", st.body, rec.Code, rec.Body)
		}
	}
	// task #1 đã done trong dữ liệu mẫu; task #4 không giao cho ai, An tự làm xong: không báo ai
	sendAs(mux, 1, "PATCH", "/api/v1/tasks/4", `{"status": "done"}`)

	if err := s.jobs.Stop(ctx); err != nil { // chờ worker chạy hết các job đã nhận
		t.Fatal(err)
	}
	sent := s.notifier.(*recordNotifier).sent
	if len(sent) != 1 {
		t.Fatalf("gửi %d thông báo, muốn 1: %+v", len(sent), sent)
	}
	if n := sent[0]; n.TaskID != 3 || n.DoneBy != 2 || !slices.Equal(n.To, []int{1}) {
		t.Errorf("thông báo = %+v", n)
	}
}

func TestTaskDoneWhenStopped(t *testing.T) {
	s := newTestStores(t)
	s.jobs.Stop(context.Background()) // hàng đợi không nhận thêm job nào
	addUsers(t, s, "Binh")
	rec := sendAs(newMux(s, testTokens), 1, "PATCH", "/api/v1/tasks/3", `{"assignee_id": 2, "status": "done"}`)
	if rec.Code != http.StatusOK { // không gửi được thông báo vẫn không làm hỏng request
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body)
	}
	if err := s.jobs.Enqueue(jobs.Job{}); !errors.Is(err, jobs.ErrStopped) {
		t.Errorf("Enqueue = %v", err)
	}
}
