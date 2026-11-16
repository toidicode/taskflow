package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/toidicode/taskflow/internal/project"
)

// fakeTasks cài project.Tasks: project nào có trong map là còn task.
type fakeTasks map[int]bool

func (f fakeTasks) HasTasks(_ context.Context, projectID int) (bool, error) {
	return f[projectID], nil
}

func TestServiceDelete(t *testing.T) {
	ctx := context.Background()
	s := project.NewService(project.NewMemoryStore(), fakeTasks{1: true})
	for _, name := range []string{"TaskFlow API", "Website"} { // project #1, #2 của user #1
		if _, err := s.Create(ctx, 1, name, ""); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Delete(ctx, 2, 2); !errors.Is(err, project.ErrNotFound) { // không phải chủ: như không có
		t.Errorf("user #2 xóa: lỗi = %v, muốn ErrNotFound", err)
	}
	if err := s.Delete(ctx, 1, 1); !errors.Is(err, project.ErrHasTasks) {
		t.Errorf("xóa project còn task: lỗi = %v, muốn ErrHasTasks", err)
	}
	if err := s.Delete(ctx, 1, 2); err != nil {
		t.Errorf("xóa project #2: %v", err)
	}
	if _, err := s.Get(ctx, 1, 2); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("sau khi xóa: lỗi = %v, muốn ErrNotFound", err)
	}
}
