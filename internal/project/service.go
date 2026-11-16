package project

import (
	"context"
	"fmt"
)

// Tasks là phần việc với task mà Service cần. Interface khai báo ở đây, nơi dùng nó, chứ không
// import internal/task: task đã import project (luật quyền cần Project), import ngược lại là import vòng.
type Tasks interface {
	// HasTasks cho biết project projectID còn task nào không.
	HasTasks(ctx context.Context, projectID int) (bool, error)
}

// Service chứa các use case của project: tạo, xem, sửa, xóa, kèm kiểm tra quyền.
// Nó không biết gì về HTTP, userID là user đang đăng nhập do tầng gọi truyền vào.
type Service struct {
	projects Store
	tasks    Tasks
}

// NewService tạo Service lưu project trong projects và hỏi tasks trước khi xóa project.
func NewService(projects Store, tasks Tasks) *Service {
	return &Service{projects: projects, tasks: tasks}
}

// List trả về các project của user userID.
func (s *Service) List(ctx context.Context, userID int) ([]Project, error) {
	return s.projects.List(ctx, userID)
}

// Get trả về project id. User không phải chủ project nhận lỗi ErrNotFound y như khi không có
// project đó, để người ngoài không dò được project nào đang có.
func (s *Service) Get(ctx context.Context, userID, id int) (Project, error) {
	p, err := s.projects.Get(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if !CanAccess(userID, p) {
		return Project{}, fmt.Errorf("project #%d: %w", id, ErrNotFound) // có nhưng giả như không có
	}
	return p, nil
}

// Create tạo project mới, chủ project là userID.
func (s *Service) Create(ctx context.Context, userID int, name, description string) (*Project, error) {
	p := New(name, userID)
	p.Description = description
	if err := s.projects.Add(ctx, p); err != nil {
		return nil, err // khóa ngoại owner_id: user đã bị xóa thì lỗi wrap user.ErrNotFound
	}
	return p, nil
}

// Replace thay tên và mô tả của project id, giữ nguyên chủ project.
func (s *Service) Replace(ctx context.Context, userID, id int, name, description string) (*Project, error) {
	old, err := s.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	p := New(name, old.OwnerID)
	p.ID, p.Description = id, description
	if err := s.projects.Update(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// Delete xóa project id khi nó không còn task nào, còn task thì lỗi wrap ErrHasTasks.
func (s *Service) Delete(ctx context.Context, userID, id int) error {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return err
	}
	has, err := s.tasks.HasTasks(ctx, id)
	if err != nil {
		return err
	}
	if has {
		return fmt.Errorf("project #%d: %w", id, ErrHasTasks)
	}
	// task được thêm ngay sau lúc kiểm tra thì khóa ngoại của PostgreSQL vẫn chặn: store trả ErrHasTasks
	return s.projects.Delete(ctx, id)
}
