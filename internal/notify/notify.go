// Package notify gửi thông báo cho user. TaskFlow chưa gửi email nên Log chỉ ghi
// thông báo ra log; thêm email hay push sau này chỉ cần một kiểu cài Notifier mới.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/toidicode/taskflow/internal/jobs"
)

// Tên loại job gửi thông báo. Payload là JSON của TaskDone, Deadline: chỉ được thêm field
// (bản cũ bỏ qua field lạ); đổi kiểu không tương thích thì tạo loại mới ":v2" và giữ handler
// ":v1" tới khi hàng đợi không còn job cũ.
const (
	TypeTaskDone = "notify:task_done:v1"
	TypeDeadline = "notify:deadline:v1"
)

// TaskDone là thông báo "task đã xong".
type TaskDone struct {
	TaskID int    `json:"task_id"`
	Title  string `json:"title"`
	DoneBy int    `json:"done_by"` // user đã chuyển task sang done
	To     []int  `json:"to"`      // người nhận: chủ project và người được giao, trừ DoneBy
}

// Deadline là thông báo "task sắp tới hạn".
type Deadline struct {
	TaskID  int       `json:"task_id"`
	Title   string    `json:"title"`
	DueDate time.Time `json:"due_date"`
	To      []int     `json:"to"` // người được giao và chủ project
}

// Notifier gửi thông báo. Lỗi trả về được coi là lỗi tạm thời: job gửi sẽ được chạy lại.
// Job có thể chạy nhiều lần (at-least-once), bản gửi email thật nên dùng ID của job
// làm khóa chống gửi trùng nếu nhà cung cấp hỗ trợ.
type Notifier interface {
	TaskDone(ctx context.Context, n TaskDone) error
	Deadline(ctx context.Context, n Deadline) error
}

// Handlers trả về các loại job gửi thông báo qua n, để đăng ký với hàng đợi.
func Handlers(n Notifier) []jobs.Handler {
	return []jobs.Handler{
		{Type: TypeTaskDone, Queue: jobs.QueueDefault, MaxRetry: 10, Timeout: 30 * time.Second,
			Run: decode(n.TaskDone)},
		// Retention 25 giờ: trong thời gian đó, đưa lại job nhắc cùng task, cùng hạn (cùng ID) bị từ chối
		{Type: TypeDeadline, Queue: jobs.QueueDefault, MaxRetry: 10, Timeout: 30 * time.Second,
			Retention: 25 * time.Hour, Run: decode(n.Deadline)},
	}
}

// decode tạo hàm chạy job: giải mã payload thành T rồi gọi send.
func decode[T any](send func(context.Context, T) error) func(context.Context, []byte) error {
	return func(ctx context.Context, payload []byte) error {
		var v T
		if err := json.Unmarshal(payload, &v); err != nil {
			// payload hỏng thì chạy lại bao nhiêu lần cũng vậy: vào archived luôn
			return fmt.Errorf("payload %s: %w: %w", payload, err, jobs.SkipRetry)
		}
		return send(ctx, v)
	}
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

func (l Log) Deadline(ctx context.Context, n Deadline) error {
	for _, to := range n.To {
		l.Logger.InfoContext(ctx, "thông báo: task sắp tới hạn",
			"to_user", to, "task_id", n.TaskID, "title", n.Title, "due_date", n.DueDate)
	}
	return nil
}
