package project_test

import (
	"errors"
	"testing"

	"github.com/toidicode/taskflow/internal/project"
)

func TestMemoryStore(t *testing.T) {
	s := project.NewMemoryStore()
	api := project.New("TaskFlow API", 1)
	s.Add(api)
	s.Add(project.New("Website giới thiệu", 1))

	if api.ID != 1 {
		t.Errorf("Add() cấp ID %d, muốn 1", api.ID)
	}
	p, err := s.Get(2)
	if err != nil || p.Name != "Website giới thiệu" {
		t.Fatalf("Get(2) = %v, %v", p, err)
	}
	if _, err := s.Get(9); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Get(9) err = %v, muốn ErrNotFound", err)
	}

	var ids []int
	for _, p := range s.List() {
		ids = append(ids, p.ID)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Errorf("List() ids = %v, muốn [1 2]", ids)
	}
}

func TestMemoryStore_UpdateDelete(t *testing.T) {
	s := project.NewMemoryStore()
	s.Add(project.New("TaskFlow API", 1))

	// Update thay cả con trỏ trong store bằng project mới
	updated := &project.Project{ID: 1, Name: "TaskFlow API v2", OwnerID: 2}
	if err := s.Update(updated); err != nil {
		t.Fatalf("Update() lỗi: %v", err)
	}
	if p, _ := s.Get(1); p.Name != "TaskFlow API v2" || p.OwnerID != 2 {
		t.Errorf("sau Update: %+v", p)
	}
	if err := s.Update(&project.Project{ID: 9}); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Update(#9) err = %v, muốn ErrNotFound", err)
	}

	if err := s.Delete(1); err != nil {
		t.Fatalf("Delete(1) lỗi: %v", err)
	}
	if _, err := s.Get(1); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Get(1) sau Delete err = %v, muốn ErrNotFound", err)
	}
	if err := s.Delete(1); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Delete(1) lần hai err = %v, muốn ErrNotFound", err)
	}

	// ID đã xóa không được cấp lại
	p := project.New("Website", 1)
	s.Add(p)
	if p.ID != 2 {
		t.Errorf("ID sau khi xóa = %d, muốn 2", p.ID)
	}
}
