package attachment

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

func TestCleanName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Báo cáo tháng 11.pdf", "Báo cáo tháng 11.pdf"},
		{`..\..\x.txt`, `.._.._x.txt`},
		{"a/b.png", "a_b.png"},
		{"dòng\nmới.txt", "dòng_mới.txt"},
		{"  ", "file"},
		{"..", "file"},
		{".", "file"},
		{"\xff\xfe.txt", ".txt"},
		{strings.Repeat("ệ", 100), strings.Repeat("ệ", 85)}, // "ệ" 3 byte: 85 × 3 = 255
	}
	for _, tt := range tests {
		if got := CleanName(tt.in); got != tt.want {
			t.Errorf("CleanName(%q) = %q, muốn %q", tt.in, got, tt.want)
		}
	}
}

func TestPolicy(t *testing.T) {
	// An (#1) chủ project, Bình (#2) được giao task, Chi (#3) người ngoài
	p := project.Project{ID: 1, OwnerID: 1}
	tk := task.Task{ID: 3, ProjectID: 1, AssigneeID: 2}
	byAn := Attachment{TaskID: 3, UploadedBy: 1}
	byBinh := Attachment{TaskID: 3, UploadedBy: 2}
	otherTask := Attachment{TaskID: 4, UploadedBy: 2}

	for _, tt := range []struct {
		userID int
		upload bool
		delAn  bool
		delBnh bool
	}{
		{1, true, true, true},
		{2, true, false, true},
		{3, false, false, false},
		{0, false, false, false},
	} {
		if got := CanUpload(tt.userID, p, tk); got != tt.upload {
			t.Errorf("CanUpload(user #%d) = %v", tt.userID, got)
		}
		if got := CanDelete(tt.userID, p, tk, byAn); got != tt.delAn {
			t.Errorf("CanDelete(user #%d, file của An) = %v", tt.userID, got)
		}
		if got := CanDelete(tt.userID, p, tk, byBinh); got != tt.delBnh {
			t.Errorf("CanDelete(user #%d, file của Bình) = %v", tt.userID, got)
		}
	}
	if CanDelete(2, p, tk, otherTask) {
		t.Error("CanDelete với file của task khác = true")
	}
	// Bình không còn được giao task: không xóa được cả file mình đã upload
	if CanDelete(2, p, task.Task{ID: 3, ProjectID: 1}, byBinh) {
		t.Error("CanDelete sau khi bị giao lại = true")
	}
}

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	for _, taskID := range []int{3, 4, 3} {
		a := &Attachment{TaskID: taskID, Name: "a.txt", StoredName: "X", Size: 1}
		if err := s.Add(ctx, a); err != nil || a.ID == 0 || a.CreatedAt.IsZero() {
			t.Fatalf("Add() = %v, %+v", err, a)
		}
	}
	list, _ := s.List(ctx, 3)
	if len(list) != 2 || list[0].ID != 1 || list[1].ID != 3 {
		t.Errorf("List(3) = %+v", list)
	}
	if list, _ := s.List(ctx, 9); list == nil || len(list) != 0 {
		t.Errorf("List(9) = %#v, muốn slice rỗng", list)
	}
	if err := s.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() sau Delete: %v", err)
	}
	if err := s.Delete(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() lần hai: %v", err)
	}
}
