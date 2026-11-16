// Package notify gửi thông báo cho user. TaskFlow chưa gửi email nên Log chỉ ghi
// thông báo ra log; thêm email hay push sau này chỉ cần một kiểu cài Notifier mới.
package notify

import (
	"context"
	"log/slog"
)

// TaskDone là thông báo "task đã xong".
type TaskDone struct {
	TaskID int
	Title  string
	DoneBy int   // user đã chuyển task sang done
	To     []int // người nhận: chủ project và người được giao, trừ DoneBy
}

// Notifier gửi thông báo. Lỗi trả về được coi là lỗi tạm thời: job gửi sẽ được chạy lại.
type Notifier interface {
	TaskDone(ctx context.Context, n TaskDone) error
}

// Log là Notifier ghi mỗi thông báo thành một dòng log.
type Log struct {
	Logger *slog.Logger
}

func (l Log) TaskDone(ctx context.Context, n TaskDone) error {
	for _, to := range n.To {
		l.Logger.InfoContext(ctx, "thông báo: task đã xong",
			"to_user", to, "task_id", n.TaskID, "title", n.Title, "done_by", n.DoneBy)
	}
	return nil
}
