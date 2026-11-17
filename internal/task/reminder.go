package task

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/notify"
)

// TypeRemindDeadlines là job định kỳ tìm task sắp tới hạn để nhắc.
const TypeRemindDeadlines = "task:remind_deadlines:v1"

// RemindBefore: nhắc task có hạn trong chừng này thời gian tới.
const RemindBefore = 24 * time.Hour

// RemindDeadlines tìm các task chưa xong, có hạn trong (now, now+RemindBefore] và chưa được nhắc
// cho đúng hạn đó, đưa một job thông báo cho mỗi task vào hàng đợi rồi đánh dấu đã nhắc.
// Trả về số task đã nhắc.
//
// Job này có thể chạy hai lần cùng lúc (hai bản API) hoặc chạy lại sau khi lỗi giữa chừng, nên
// mỗi bước đều làm lại được: job thông báo có ID cố định theo task và hạn (hàng đợi từ chối ID
// trùng), còn đánh dấu chỉ là ghi lại hạn đã nhắc. Đổi hạn thì task được nhắc lại.
func (s *Service) RemindDeadlines(ctx context.Context, now time.Time) (int, error) {
	due, err := s.tasks.DueSoon(ctx, now, now.Add(RemindBefore))
	if err != nil {
		return 0, err
	}
	sent := 0
	var errs []error
	for _, t := range due {
		n := notify.Deadline{TaskID: t.ID, Title: t.Title, DueDate: t.DueDate, To: []int{t.AssigneeID}}
		if p, err := s.projects.Get(ctx, t.ProjectID); err == nil {
			n.To = append(n.To, p.OwnerID)
		}
		n.To = slices.DeleteFunc(slices.Compact(n.To), func(id int) bool { return id == 0 })

		id := fmt.Sprintf("deadline:%d:%d", t.ID, t.DueDate.Unix())
		err := s.queue.Enqueue(ctx, notify.TypeDeadline, n, jobs.ID(id))
		if err != nil && !errors.Is(err, jobs.ErrDuplicate) { // trùng: lần chạy trước đã đưa vào rồi
			errs = append(errs, fmt.Errorf("nhắc task #%d: %w", t.ID, err))
			continue // không đánh dấu: lần chạy sau thử lại
		}
		if err := s.tasks.MarkReminded(ctx, t.ID, t.DueDate); err != nil {
			errs = append(errs, err)
			continue
		}
		sent++
	}
	return sent, errors.Join(errs...)
}
