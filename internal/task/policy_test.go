package task_test

import (
	"context"
	"slices"
	"testing"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

func TestPolicy(t *testing.T) {
	p := project.Project{ID: 1, Name: "TaskFlow API", OwnerID: 1} // chủ project là user #1
	assigned := task.Task{ID: 3, ProjectID: 1, Title: "Viết test", AssigneeID: 2}
	unassigned := task.Task{ID: 4, ProjectID: 1, Title: "Viết tài liệu"} // AssigneeID 0

	tests := []struct {
		name                     string
		userID                   int
		p                        project.Project
		t                        task.Task
		view, changeStatus, edit bool
	}{
		{"chủ project", 1, p, assigned, true, true, true},
		{"người được giao", 2, p, assigned, true, true, false},
		{"người ngoài", 3, p, assigned, false, false, false},
		{"chủ project, task chưa giao", 1, p, unassigned, true, true, true},
		// userID 0 trùng AssigneeID 0 của task chưa giao: vẫn không được gì
		{"chưa đăng nhập, task chưa giao", 0, p, unassigned, false, false, false},
		// project truyền vào không phải project của task: không tin OwnerID của nó
		{"project khác", 1, p, task.Task{ID: 9, ProjectID: 2}, false, false, false},
		{"không đọc được project", 1, project.Project{}, assigned, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := task.CanView(tt.userID, tt.p, tt.t); got != tt.view {
				t.Errorf("CanView = %v, muốn %v", got, tt.view)
			}
			if got := task.CanChangeStatus(tt.userID, tt.p, tt.t); got != tt.changeStatus {
				t.Errorf("CanChangeStatus = %v, muốn %v", got, tt.changeStatus)
			}
			if got := task.CanEdit(tt.userID, tt.p, tt.t); got != tt.edit {
				t.Errorf("CanEdit = %v, muốn %v", got, tt.edit)
			}
		})
	}
}

func TestMemoryStore_VisibleTo(t *testing.T) {
	ctx := context.Background()
	projects := map[int]project.Project{1: {ID: 1, OwnerID: 1}, 2: {ID: 2, OwnerID: 3}}
	s := task.NewMemoryStore()
	s.SetProjectFunc(func(id int) (project.Project, bool) {
		p, ok := projects[id]
		return p, ok
	})
	for _, tk := range []*task.Task{
		{ProjectID: 1, Title: "Task #1 của project 1"},
		{ProjectID: 1, Title: "Task #2 giao cho user 2", AssigneeID: 2},
		{ProjectID: 2, Title: "Task #3 của project 2"},
	} {
		_ = s.Add(ctx, tk)
	}

	for _, tt := range []struct {
		userID int
		want   []int
	}{{0, []int{1, 2, 3}}, {1, []int{1, 2}}, {2, []int{2}}, {3, []int{3}}, {4, []int{}}} {
		list, _ := s.List(ctx, task.Filter{VisibleTo: tt.userID})
		ids := []int{}
		for _, tk := range list {
			ids = append(ids, tk.ID)
		}
		if !slices.Equal(ids, tt.want) {
			t.Errorf("List(VisibleTo: %d) = %v, muốn %v", tt.userID, ids, tt.want)
		}
	}
}
