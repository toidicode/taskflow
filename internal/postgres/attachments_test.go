package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/toidicode/taskflow/internal/attachment"
	"github.com/toidicode/taskflow/internal/postgres"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/testdb"
)

func TestAttachmentStore(t *testing.T) {
	t.Parallel() // mỗi test có schema riêng
	db := testdb.New(t)
	ctx := context.Background()
	projects, tasks := postgres.NewProjectStore(db), postgres.NewTaskStore(db)
	attachments := postgres.NewAttachmentStore(db)
	addUsers(t, db, "An")

	p := project.New("TaskFlow API", 1)
	if err := projects.Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	tk, _ := task.New(p.ID, "Viết API upload")
	if err := tasks.Add(ctx, tk); err != nil {
		t.Fatal(err)
	}

	a := &attachment.Attachment{TaskID: tk.ID, Name: "Báo cáo.pdf", StoredName: "ABC", ContentType: "application/pdf",
		Size: 1234, UploadedBy: 1}
	if err := attachments.Add(ctx, a); err != nil || a.ID != 1 || a.CreatedAt.IsZero() {
		t.Fatalf("Add() = %v, %+v", err, a)
	}
	got, err := attachments.Get(ctx, a.ID)
	if err != nil || got.Name != a.Name || got.StoredName != "ABC" || got.Size != 1234 || got.UploadedBy != 1 {
		t.Errorf("Get() = %+v, %v", got, err)
	}
	if list, err := attachments.List(ctx, tk.ID); err != nil || len(list) != 1 {
		t.Errorf("List() = %+v, %v", list, err)
	}

	// task không tồn tại: khóa ngoại chặn, store trả task.ErrNotFound
	bad := &attachment.Attachment{TaskID: 99, Name: "x", StoredName: "DEF", ContentType: "text/plain", Size: 1}
	if err := attachments.Add(ctx, bad); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("Add() vào task #99: %v, muốn task.ErrNotFound", err)
	}

	known, err := attachments.KnownFiles(ctx, []string{"ABC", "KHONG-CO"})
	if err != nil || len(known) != 1 || !known["ABC"] {
		t.Errorf("KnownFiles() = %v, %v, muốn chỉ ABC", known, err)
	}

	if err := attachments.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := attachments.Get(ctx, a.ID); !errors.Is(err, attachment.ErrNotFound) {
		t.Errorf("Get() sau Delete: %v", err)
	}

	// ON DELETE CASCADE: xóa task thì hàng attachment của nó cũng mất
	b := &attachment.Attachment{TaskID: tk.ID, Name: "b.png", StoredName: "GHI", ContentType: "image/png", Size: 10, UploadedBy: 1}
	if err := attachments.Add(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Delete(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := attachments.Get(ctx, b.ID); !errors.Is(err, attachment.ErrNotFound) {
		t.Errorf("attachment còn sau khi xóa task: %v", err)
	}
}
