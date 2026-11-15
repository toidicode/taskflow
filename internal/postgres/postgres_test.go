package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/postgres"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
)

// openTestDB kết nối tới database trong TASKFLOW_TEST_DATABASE_URL, xóa sạch schema public
// rồi chạy lại mọi migration. Không đặt biến này thì test được bỏ qua, go test ./... vẫn chạy
// được trên máy không có PostgreSQL. Test xóa sạch dữ liệu nên đừng trỏ biến này vào database thật.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openEmptyDB(t)
	m, err := postgres.NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Up(context.Background()); err != nil {
		t.Fatalf("chạy migration: %v", err)
	}
	return db
}

// openEmptyDB kết nối tới database test và xóa mọi bảng, kể cả goose_db_version.
func openEmptyDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TASKFLOW_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("chưa đặt TASKFLOW_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, url)
	if err != nil {
		t.Fatalf("kết nối database test: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatalf("xóa schema: %v", err)
	}
	return db
}

func TestTaskStore(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	projects, tasks := postgres.NewProjectStore(db), postgres.NewTaskStore(db)

	p := project.New("TaskFlow API", 1)
	if err := projects.Add(ctx, p); err != nil {
		t.Fatal(err)
	}

	// task không có người nhận và không có hạn: hai cột phải là NULL, không phải 0
	plain, _ := task.New(p.ID, "Thiết kế database")
	if err := tasks.Add(ctx, plain); err != nil || plain.ID != 1 {
		t.Fatalf("Add() = %v, ID %d, muốn ID 1", err, plain.ID)
	}
	var assigneeNull, dueNull bool
	err := db.QueryRowContext(ctx, "SELECT assignee_id IS NULL, due_date IS NULL FROM tasks WHERE id = $1",
		plain.ID).Scan(&assigneeNull, &dueNull)
	if err != nil || !assigneeNull || !dueNull {
		t.Errorf("assignee_id IS NULL = %v, due_date IS NULL = %v (%v), muốn true, true", assigneeNull, dueNull, err)
	}

	// task đủ field: đọc lại phải ra đúng giá trị đã ghi
	due := time.Date(2026, 11, 30, 17, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	full := &task.Task{ProjectID: p.ID, Title: "Viết API", Description: "CRUD task", AssigneeID: 2, DueDate: due}
	full.Complete()
	if err := tasks.Add(ctx, full); err != nil {
		t.Fatal(err)
	}
	got, err := tasks.Get(ctx, full.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Viết API" || got.Description != "CRUD task" || got.Status != task.StatusDone ||
		got.AssigneeID != 2 || !got.DueDate.Equal(due) {
		t.Errorf("Get() = %+v", got)
	}
	// PostgreSQL lưu tới micro giây, Go có nano giây
	if want := full.CompletedAt.Truncate(time.Microsecond); !got.CompletedAt.Equal(want) {
		t.Errorf("CompletedAt = %v, muốn %v", got.CompletedAt, want)
	}

	// sửa: bỏ hạn và bỏ người nhận thì cột trở về NULL, đọc lại là zero value
	got.DueDate, got.AssigneeID = time.Time{}, 0
	got.SetStatus(task.StatusDoing)
	if err := tasks.Update(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if again, _ := tasks.Get(ctx, got.ID); !again.DueDate.IsZero() || again.AssigneeID != 0 ||
		!again.CompletedAt.IsZero() || again.Status != task.StatusDoing {
		t.Errorf("sau Update: %+v", again)
	}

	// lọc theo project và trạng thái
	doing := task.StatusDoing
	for _, tt := range []struct {
		f    task.Filter
		want int
	}{{task.Filter{}, 2}, {task.Filter{ProjectID: p.ID}, 2}, {task.Filter{ProjectID: 9}, 0}, {task.Filter{Status: &doing}, 1}} {
		// list rỗng phải là [] (JSON "[]") chứ không phải nil (JSON "null")
		if list, err := tasks.List(ctx, tt.f); err != nil || len(list) != tt.want || list == nil {
			t.Errorf("List(%+v) = %d task (nil: %v), %v; muốn %d", tt.f, len(list), list == nil, err, tt.want)
		}
	}

	// không tìm thấy: phải là task.ErrNotFound để handler trả 404
	if _, err := tasks.Get(ctx, 99); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("Get(99) lỗi = %v, muốn ErrNotFound", err)
	}
	if err := tasks.Update(ctx, &task.Task{ID: 99, ProjectID: p.ID, Title: "x"}); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("Update(#99) lỗi = %v, muốn ErrNotFound", err)
	}

	// project không tồn tại (VD bị xóa ngay sau lúc handler kiểm tra): khóa ngoại chặn,
	// lỗi là project.ErrNotFound để handler trả 422 như khi gửi project_id sai
	orphan := &task.Task{ProjectID: 99, Title: "Mồ côi"}
	if err := tasks.Add(ctx, orphan); !errors.Is(err, project.ErrNotFound) || orphan.ID != 0 {
		t.Errorf("Add(project #99) lỗi = %v, ID %d; muốn project.ErrNotFound, ID 0", err, orphan.ID)
	}
	moved := got
	moved.ProjectID = 99
	if err := tasks.Update(ctx, &moved); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Update(project #99) lỗi = %v, muốn project.ErrNotFound", err)
	}
	if again, _ := tasks.Get(ctx, got.ID); again.ProjectID != p.ID {
		t.Errorf("sau Update lỗi: ProjectID = %d, muốn %d", again.ProjectID, p.ID)
	}
	if err := tasks.Delete(ctx, plain.ID); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Delete(ctx, plain.ID); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("Delete lần hai lỗi = %v, muốn ErrNotFound", err)
	}

	// khóa ngoại: project còn task thì không xóa được, lỗi là ErrHasTasks để handler trả 409
	if err := projects.Delete(ctx, p.ID); !errors.Is(err, project.ErrHasTasks) {
		t.Errorf("xóa project còn task: lỗi = %v, muốn ErrHasTasks", err)
	}
}

// count đếm số hàng của bảng table.
func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTaskTags(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	projects, tasks := postgres.NewProjectStore(db), postgres.NewTaskStore(db)
	p := project.New("TaskFlow API", 1)
	if err := projects.Add(ctx, p); err != nil {
		t.Fatal(err)
	}

	// Add ghi task và tag trong một transaction
	a := &task.Task{ProjectID: p.ID, Title: "Viết migration", Tags: []string{"database", "go"}}
	b := &task.Task{ProjectID: p.ID, Title: "Viết handler", Tags: []string{"go"}}
	c := &task.Task{ProjectID: p.ID, Title: "Viết tài liệu"}
	for _, tk := range []*task.Task{a, b, c} {
		if err := tasks.Add(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := tasks.Get(ctx, a.ID); !slices.Equal(got.Tags, []string{"database", "go"}) {
		t.Errorf("Get(a).Tags = %v", got.Tags)
	}
	if n := count(t, db, "tags"); n != 2 { // "go" dùng chung, không tạo hai lần
		t.Errorf("bảng tags có %d hàng, muốn 2", n)
	}

	// một câu trong transaction lỗi (PostgreSQL không nhận ký tự NUL) thì task cũng không được ghi
	bad := &task.Task{ProjectID: p.ID, Title: "Tag hỏng", Tags: []string{"a\x00b"}}
	if err := tasks.Add(ctx, bad); err == nil || bad.ID != 0 {
		t.Errorf("Add(tag hỏng) = %v, ID %d; muốn lỗi và ID 0", err, bad.ID)
	}
	if n := count(t, db, "tasks"); n != 3 {
		t.Errorf("sau Add lỗi bảng tasks có %d hàng, muốn 3 (đã rollback)", n)
	}

	// List đọc tag của cả trang, lọc được theo tag
	list, err := tasks.List(ctx, task.Filter{})
	if err != nil || len(list) != 3 || !slices.Equal(list[1].Tags, []string{"go"}) || list[2].Tags != nil {
		t.Errorf("List() = %+v, %v", list, err)
	}
	if list, _ := tasks.List(ctx, task.Filter{Tag: "go"}); len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Errorf("List(tag go) = %+v", list)
	}

	// phân trang: sau task a, tối đa 1 task
	if list, _ := tasks.List(ctx, task.Filter{AfterID: a.ID, Limit: 1}); len(list) != 1 || list[0].ID != b.ID {
		t.Errorf("List(sau a, 1 task) = %+v", list)
	}

	// Update thay toàn bộ tag
	got, _ := tasks.Get(ctx, a.ID)
	got.Tags = []string{"backend", "sql"}
	if err := tasks.Update(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if again, _ := tasks.Get(ctx, a.ID); !slices.Equal(again.Tags, []string{"backend", "sql"}) {
		t.Errorf("sau Update: %v", again.Tags)
	}

	// AddTag, RemoveTag
	if err := tasks.AddTag(ctx, c.ID, "docs"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.AddTag(ctx, c.ID, "docs"); err != nil { // gắn lại: không lỗi, không trùng
		t.Fatal(err)
	}
	if got, _ := tasks.Get(ctx, c.ID); !slices.Equal(got.Tags, []string{"docs"}) {
		t.Errorf("sau AddTag: %v", got.Tags)
	}
	tagsBefore := count(t, db, "tags")
	if err := tasks.AddTag(ctx, 99, "mới"); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("AddTag(#99) lỗi = %v, muốn ErrNotFound", err)
	}
	if n := count(t, db, "tags"); n != tagsBefore { // tag "mới" bị rollback cùng transaction
		t.Errorf("bảng tags có %d hàng, muốn %d", n, tagsBefore)
	}
	if err := tasks.RemoveTag(ctx, c.ID, "docs"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.RemoveTag(ctx, c.ID, "docs"); err != nil {
		t.Errorf("RemoveTag lần hai lỗi: %v", err)
	}
	if err := tasks.RemoveTag(ctx, 99, "docs"); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("RemoveTag(#99) lỗi = %v, muốn ErrNotFound", err)
	}

	// xóa task thì liên kết trong task_tags bị xóa theo (ON DELETE CASCADE)
	if err := tasks.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "task_tags"); n != 1 { // còn tag go của task b
		t.Errorf("task_tags còn %d hàng, muốn 1", n)
	}
}

func TestProjectStore(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := postgres.NewProjectStore(db)

	if list, err := s.List(ctx); err != nil || list == nil {
		t.Errorf("List() khi chưa có project = %#v, %v; muốn slice rỗng khác nil", list, err)
	}
	for _, name := range []string{"TaskFlow API", "Website giới thiệu"} {
		if err := s.Add(ctx, project.New(name, 1)); err != nil {
			t.Fatal(err)
		}
	}
	p := project.Project{ID: 2, Name: "Website", Description: "Landing page", OwnerID: 3}
	if err := s.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "TaskFlow API" || list[1] != p {
		t.Errorf("List() = %+v, %v", list, err)
	}

	if err := s.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, 1); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Get(1) sau Delete lỗi = %v, muốn ErrNotFound", err)
	}
	if err := s.Update(ctx, &project.Project{ID: 9, Name: "x", OwnerID: 1}); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Update(#9) lỗi = %v, muốn ErrNotFound", err)
	}
}
