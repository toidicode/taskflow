package postgres_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/toidicode/taskflow/internal/postgres"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/testdb"
	"github.com/toidicode/taskflow/internal/user"
)

// TestTaskVisibility kiểm tra ListTasks lọc theo quyền bằng SQL, cùng kịch bản với
// TestMemoryStore_VisibleTo của store trong bộ nhớ.
func TestTaskVisibility(t *testing.T) {
	t.Parallel() // mỗi test có schema riêng
	db := testdb.New(t)
	ctx := context.Background()
	projects, tasks := postgres.NewProjectStore(db), postgres.NewTaskStore(db)
	addUsers(t, db, "An", "Binh", "Chi", "Dung")

	for _, p := range []*project.Project{project.New("Của An", 1), project.New("Của Chi", 3)} {
		if err := projects.Add(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, tk := range []*task.Task{
		{ProjectID: 1, Title: "Task #1 của project 1", Tags: []string{"go"}},
		{ProjectID: 1, Title: "Task #2 giao cho Bình", AssigneeID: 2, Tags: []string{"go"}},
		{ProjectID: 2, Title: "Task #3 của project 2"},
	} {
		if err := tasks.Add(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}

	for _, tt := range []struct {
		f    task.Filter
		want []int
	}{
		{task.Filter{}, []int{1, 2, 3}}, // VisibleTo 0: không lọc
		{task.Filter{VisibleTo: 1}, []int{1, 2}},
		{task.Filter{VisibleTo: 2}, []int{2}},
		{task.Filter{VisibleTo: 3}, []int{3}},
		{task.Filter{VisibleTo: 4}, []int{}},
		{task.Filter{VisibleTo: 1, Tag: "go", AfterID: 1, Limit: 1}, []int{2}}, // kết hợp với lọc và phân trang
		{task.Filter{VisibleTo: 3, ProjectID: 1}, []int{}},
	} {
		list, err := tasks.List(ctx, tt.f)
		if err != nil {
			t.Fatal(err)
		}
		ids := []int{}
		for _, tk := range list {
			ids = append(ids, tk.ID)
		}
		if !slices.Equal(ids, tt.want) {
			t.Errorf("List(%+v) = %v, muốn %v", tt.f, ids, tt.want)
		}
	}

	// khóa ngoại tasks.assignee_id: lỗi là user.ErrNotFound, không bị nhầm với project
	bad := &task.Task{ProjectID: 1, Title: "Giao cho user #99", AssigneeID: 99}
	if err := tasks.Add(ctx, bad); !errors.Is(err, user.ErrNotFound) || errors.Is(err, project.ErrNotFound) {
		t.Errorf("Add(assignee #99) lỗi = %v, muốn user.ErrNotFound", err)
	}
	t1, _ := tasks.Get(ctx, 1)
	t1.AssigneeID = 99
	if err := tasks.Update(ctx, &t1); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("Update(assignee #99) lỗi = %v, muốn user.ErrNotFound", err)
	}
}

// TestMigrationForeignKeys chạy migration 00005 trên dữ liệu cũ: assignee không tồn tại thì
// được đặt về NULL, owner không tồn tại thì migration lỗi và không đổi gì.
func TestMigrationForeignKeys(t *testing.T) {
	db := testdb.Empty(t)
	ctx := context.Background()
	m, err := postgres.NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	// dữ liệu như từ Bài 29-32: owner_id, assignee_id là số bất kỳ
	exec := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users (email, name, password_hash) VALUES ('an@example.com', 'An', 'x')`)
	exec(`INSERT INTO projects (name, owner_id) VALUES ('TaskFlow API', 1), ('Website', 2)`)
	exec(`INSERT INTO tasks (project_id, title, assignee_id) VALUES (1, 'Giao cho An', 1), (1, 'Giao cho user #7', 7)`)

	_, err = m.Up(ctx)
	if err == nil || !strings.Contains(err.Error(), "projects_owner_id_fkey") {
		t.Fatalf("Up() với project của user #2 không tồn tại: lỗi = %v, muốn vi phạm projects_owner_id_fkey", err)
	}
	var orphans int // UPDATE trong migration bị rollback cùng câu ALTER TABLE lỗi
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE assignee_id = 7").Scan(&orphans); err != nil || orphans != 1 {
		t.Errorf("sau migration lỗi: %d task giao cho user #7 (%v), muốn 1", orphans, err)
	}

	exec(`UPDATE projects SET owner_id = 1 WHERE id = 2`) // sửa bằng tay rồi chạy lại
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var assignees []int64
	rows, err := db.QueryContext(ctx, "SELECT coalesce(assignee_id, 0) FROM tasks ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		assignees = append(assignees, id)
	}
	if err := rows.Err(); err != nil { // vòng lặp dừng vì lỗi (mất kết nối...) chứ không phải hết dòng
		t.Fatal(err)
	}
	if !slices.Equal(assignees, []int64{1, 0}) {
		t.Errorf("assignee_id sau migration = %v, muốn [1 0] (user #7 không tồn tại → NULL)", assignees)
	}
}
