package notifyrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
)

// TestNotifier chạy Notifier trong job của hàng đợi như TaskFlow API: ID của job thành
// idempotency_key, job chạy lại thì notifier không gửi lần hai.
func TestNotifier(t *testing.T) {
	n, logs := logNotifier()
	srv, lis := startServer(t, n, discard)
	client := NewNotifier(dial(t, lis, testToken))

	q := jobs.NewLocal(jobs.Options{Workers: 1, Logger: discard})
	errs := make(chan error, 2)
	q.Handle(jobs.Handler{Type: notify.TypeTaskDone, Run: func(ctx context.Context, _ []byte) error {
		err := client.TaskDone(ctx, notify.TaskDone{TaskID: 3, Title: "Viết test", DoneBy: 2, To: []int{1}})
		errs <- err
		return err
	}})
	if err := q.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Stop(context.Background()) })
	for range 2 { // cùng ID: như một job được chạy lại
		if err := q.Enqueue(t.Context(), notify.TypeTaskDone, nil, jobs.ID("42")); err != nil {
			t.Fatal(err)
		}
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := srv.keys.get("job:42"); !ok {
		t.Error("server không nhận idempotency_key job:42")
	}
	if got := logs.String(); got != `level=INFO msg="thông báo: task đã xong" to_user=1 task_id=3 title="Viết test" done_by=2`+"\n" {
		t.Errorf("log =\n%s\nmuốn đúng một thông báo", got)
	}
}

func TestNotifierErrors(t *testing.T) {
	n, _ := logNotifier()
	_, lis := startServer(t, n, discard)

	// request sai: không chạy lại job
	err := NewNotifier(dial(t, lis, testToken)).TaskDone(t.Context(), notify.TaskDone{TaskID: 3, Title: "x"})
	if !errors.Is(err, jobs.SkipRetry) {
		t.Errorf("không có người nhận: %v, muốn SkipRetry", err)
	}
	// sai token, notifier quá chậm: lỗi thường, job được chạy lại sau
	err = NewNotifier(dial(t, lis, "sai-token")).TaskDone(t.Context(), notify.TaskDone{TaskID: 3, Title: "x", To: []int{1}})
	if err == nil || errors.Is(err, jobs.SkipRetry) {
		t.Errorf("sai token: %v, muốn lỗi chạy lại được", err)
	}
	slow := &ctxNotifier{block: true, got: make(chan context.Context, 1)}
	_, lis = startServer(t, slow, discard)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err = NewNotifier(dial(t, lis, testToken)).TaskDone(ctx, notify.TaskDone{TaskID: 3, Title: "x", To: []int{1}})
	if err == nil || errors.Is(err, jobs.SkipRetry) {
		t.Errorf("hết giờ: %v, muốn lỗi chạy lại được", err)
	}
}
