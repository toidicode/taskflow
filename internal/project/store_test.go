package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/toidicode/taskflow/internal/project"
)

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	s := project.NewMemoryStore()
	api := project.New("TaskFlow API", 1)
	_ = s.Add(ctx, api)
	_ = s.Add(ctx, project.New("Website giới thiệu", 1))

	if api.ID != 1 {
		t.Errorf("Add() cấp ID %d, muốn 1", api.ID)
	}
	p, err := s.Get(ctx, 2)
	if err != nil || p.Name != "Website giới thiệu" {
		t.Fatalf("Get(2) = %v, %v", p, err)
	}
	if _, err := s.Get(ctx, 9); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Get(9) err = %v, muốn ErrNotFound", err)
	}

	list, err := s.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != 1 || list[1].ID != 2 {
		t.Errorf("List() = %v, %v; muốn ID [1 2]", list, err)
	}
}

func TestMemoryStore_UpdateDelete(t *testing.T) {
	ctx := context.Background()
	s := project.NewMemoryStore()
	_ = s.Add(ctx, project.New("TaskFlow API", 1))

	updated := &project.Project{ID: 1, Name: "TaskFlow API v2", OwnerID: 2}
	if err := s.Update(ctx, updated); err != nil {
		t.Fatalf("Update() lỗi: %v", err)
	}
	updated.Name = "sửa sau Update" // store giữ bản sao, không bị đổi theo
	if p, _ := s.Get(ctx, 1); p.Name != "TaskFlow API v2" || p.OwnerID != 2 {
		t.Errorf("sau Update: %+v", p)
	}
	if err := s.Update(ctx, &project.Project{ID: 9}); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Update(#9) err = %v, muốn ErrNotFound", err)
	}

	if err := s.Delete(ctx, 1); err != nil {
		t.Fatalf("Delete(1) lỗi: %v", err)
	}
	if _, err := s.Get(ctx, 1); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Get(1) sau Delete err = %v, muốn ErrNotFound", err)
	}
	if err := s.Delete(ctx, 1); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Delete(1) lần hai err = %v, muốn ErrNotFound", err)
	}

	// ID đã xóa không được cấp lại
	p := project.New("Website", 1)
	_ = s.Add(ctx, p)
	if p.ID != 2 {
		t.Errorf("ID sau khi xóa = %d, muốn 2", p.ID)
	}
}
