package main

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// notifyTaskDone đưa việc báo "task đã xong" cho chủ project và người được giao (trừ người
// vừa đổi status) vào hàng đợi. Hàng đợi đầy thì chỉ ghi log: status đã lưu xong, mất một
// thông báo không đáng để trả lỗi cho client.
func notifyTaskDone(r *http.Request, s stores, t task.Task, p project.Project) {
	me := userIDFrom(r.Context())
	n := notify.TaskDone{TaskID: t.ID, Title: t.Title, DoneBy: me}
	for _, id := range []int{p.OwnerID, t.AssigneeID} {
		if id > 0 && id != me && !slices.Contains(n.To, id) {
			n.To = append(n.To, id)
		}
	}
	if len(n.To) == 0 { // tự làm, tự xong: không cần báo ai
		return
	}
	// job chỉ giữ n (bản sao dữ liệu), không giữ r hay r.Context()
	err := s.jobs.Enqueue(jobs.Job{Name: "notify-task-done", Run: func(ctx context.Context) error {
		return s.notifier.TaskDone(ctx, n)
	}})
	if err != nil {
		requestLogger(r).Warn("bỏ qua thông báo task done", "task_id", t.ID, "err", err)
	}
}

// orphanMinAge: file mồ côi phải cũ hơn chừng này mới bị xóa, lớn hơn nhiều so với
// thời gian tối đa của một lần upload (transferTimeout).
const orphanMinAge = time.Hour

// cleanupJob là job xóa file mồ côi trong thư mục upload (file không có hàng attachments nào).
func cleanupJob(s stores) jobs.Job {
	return jobs.Job{Name: "clean-orphan-files", Run: func(ctx context.Context) error {
		n, err := attachment.CleanOrphans(ctx, s.attachments, s.files, time.Now().Add(-orphanMinAge))
		slog.Info("đã dọn file mồ côi", "removed", n)
		return err
	}}
}
