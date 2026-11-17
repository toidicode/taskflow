package task

import (
	"context"

	"github.com/toidicode/taskflow/internal/reqlog"
)

// EventType là loại thay đổi của task, client dựa vào nó để cập nhật giao diện.
type EventType string

const (
	EventCreated       EventType = "task.created"
	EventUpdated       EventType = "task.updated"
	EventStatusChanged EventType = "task.status_changed" // một lần sửa có đổi status (có thể đổi cả field khác)
	EventDeleted       EventType = "task.deleted"
)

// Event mô tả một thay đổi của task đã được lưu xong. Task là nil khi task bị xóa.
type Event struct {
	Type      EventType `json:"type"`
	ProjectID int       `json:"project_id"`
	TaskID    int       `json:"task_id"`
	By        int       `json:"by"` // user đã làm thay đổi
	Task      *Task     `json:"task,omitempty"`
}

// Publisher nhận event sau mỗi thay đổi, VD realtime.Hub đẩy tới các kết nối WebSocket.
// Service chỉ biết interface này, không biết gì về WebSocket hay Redis.
type Publisher interface {
	Publish(ctx context.Context, e Event) error
}

// publish gửi e cho s.events (nil là không gửi). Lỗi chỉ ghi log: thay đổi đã lưu xong,
// client bỏ lỡ một event vẫn đọc lại được bằng API. context.WithoutCancel: client của request
// ngắt kết nối ngay sau khi lưu thì event vẫn phải đi.
func (s *Service) publish(ctx context.Context, typ EventType, by int, t Task) {
	if s.events == nil {
		return
	}
	e := Event{Type: typ, ProjectID: t.ProjectID, TaskID: t.ID, By: by}
	if typ != EventDeleted {
		e.Task = &t
	}
	if err := s.events.Publish(context.WithoutCancel(ctx), e); err != nil {
		reqlog.Logger(ctx).Warn("không gửi được event", "type", typ, "task_id", t.ID, "err", err)
	}
}
