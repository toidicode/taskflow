package task_test

import (
	"context"
	"testing"

	"github.com/toidicode/taskflow/internal/task"
)

// recordEvents ghi lại các event Service gửi.
type recordEvents struct{ got []task.Event }

func (r *recordEvents) Publish(_ context.Context, e task.Event) error {
	r.got = append(r.got, e)
	return nil
}

func TestServiceEvents(t *testing.T) {
	ctx := context.Background()
	rec := &recordEvents{}
	s, _ := newService(t, rec)
	title, doing := "Viết test event", task.StatusDoing

	nt := &task.Task{ProjectID: 1, Title: "Task mới"}
	steps := []func() error{
		func() error { return s.Create(ctx, 1, nt) },
		func() error { _, err := s.Update(ctx, 1, 1, task.Changes{Title: &title}); return err },
		func() error { _, err := s.Update(ctx, 2, 1, task.Changes{Status: &doing}); return err }, // Bình, người được giao
		func() error { _, err := s.AddTag(ctx, 1, 1, "backend"); return err },
		func() error { return s.RemoveTag(ctx, 1, 1, "backend") },
		func() error { return s.Delete(ctx, 1, 1) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("bước %d: %v", i+1, err)
		}
	}
	// thay đổi bị từ chối không sinh event
	if _, err := s.Update(ctx, 3, nt.ID, task.Changes{Title: &title}); err == nil {
		t.Fatal("user #3 sửa được task của An")
	}

	want := []struct {
		typ task.EventType
		id  int
		by  int
	}{
		{task.EventCreated, nt.ID, 1},
		{task.EventUpdated, 1, 1},
		{task.EventStatusChanged, 1, 2},
		{task.EventUpdated, 1, 1},
		{task.EventUpdated, 1, 1},
		{task.EventDeleted, 1, 1},
	}
	if len(rec.got) != len(want) {
		t.Fatalf("có %d event, muốn %d: %+v", len(rec.got), len(want), rec.got)
	}
	for i, w := range want {
		e := rec.got[i]
		if e.Type != w.typ || e.TaskID != w.id || e.By != w.by || e.ProjectID != 1 {
			t.Errorf("event %d = %+v, muốn %s task #%d bởi user #%d", i+1, e, w.typ, w.id, w.by)
		}
		if (e.Task == nil) != (w.typ == task.EventDeleted) {
			t.Errorf("event %d: Task = %v, chỉ task.deleted không có task", i+1, e.Task)
		}
	}
	if e := rec.got[2]; e.Task.Status != task.StatusDoing || e.Task.Title != title {
		t.Errorf("task.status_changed mang task %+v, muốn task sau khi sửa", e.Task)
	}
	if e := rec.got[4]; len(e.Task.Tags) != 0 {
		t.Errorf("sau khi gỡ tag: %v", e.Task.Tags)
	}
}
