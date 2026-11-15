package project_test

import (
	"errors"
	"testing"

	"github.com/toidicode/taskflow/internal/project"
)

func TestMemoryStore(t *testing.T) {
	s := project.NewMemoryStore()
	s.Add(project.New(2, "Website giới thiệu", 1))
	s.Add(project.New(1, "TaskFlow API", 1))

	p, err := s.Get(1)
	if err != nil || p.Name != "TaskFlow API" {
		t.Fatalf("Get(1) = %v, %v", p, err)
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
