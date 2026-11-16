package postgres_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/toidicode/taskflow/internal/postgres"
	"github.com/toidicode/taskflow/internal/testdb"
)

// tables là các bảng mà migration tạo ra.
var tables = []string{"projects", "tasks", "users", "tags", "task_tags", "attachments"}

// tableExists cho biết bảng name có trong database không.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(context.Background(), "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists)
	if err != nil {
		t.Fatal(err)
	}
	return exists
}

// TestMigrations chạy hết Up, lùi về 0 rồi Up lại: phần Down của mọi migration phải chạy được.
func TestMigrations(t *testing.T) {
	db := testdb.Empty(t)
	ctx := context.Background()
	m, err := postgres.NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	want := len(m.ListSources())

	for round := 1; round <= 2; round++ {
		results, err := m.Up(ctx)
		if err != nil || len(results) != want {
			t.Fatalf("lần %d: Up() chạy %d migration, lỗi %v; muốn %d", round, len(results), err, want)
		}
		for _, table := range tables {
			if !tableExists(t, db, table) {
				t.Errorf("lần %d: sau Up không có bảng %s", round, table)
			}
		}
		// chạy Up lần nữa thì không còn gì để làm
		if results, err := m.Up(ctx); err != nil || len(results) != 0 {
			t.Errorf("lần %d: Up() lần hai chạy %d migration, lỗi %v", round, len(results), err)
		}

		results, err = m.DownTo(ctx, 0)
		if err != nil || len(results) != want {
			t.Fatalf("lần %d: DownTo(0) chạy %d migration, lỗi %v; muốn %d", round, len(results), err, want)
		}
		for _, table := range tables {
			if tableExists(t, db, table) {
				t.Errorf("lần %d: sau DownTo(0) vẫn còn bảng %s", round, table)
			}
		}
	}
}

// TestMigrateConcurrent mô phỏng hai instance cùng chạy -migrate trên database rỗng:
// mỗi migration chỉ được chạy đúng một lần và không instance nào lỗi.
func TestMigrateConcurrent(t *testing.T) {
	db := testdb.Empty(t)
	ctx := context.Background()

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int // số migration hai instance đã chạy
		want  int
	)
	for range 2 {
		m, err := postgres.NewMigrator(db) // mỗi instance có Provider riêng
		if err != nil {
			t.Fatal(err)
		}
		want = len(m.ListSources())
		wg.Go(func() {
			results, err := m.Up(ctx)
			if err != nil {
				t.Errorf("Up() lỗi: %v", err)
			}
			mu.Lock()
			total += len(results)
			mu.Unlock()
		})
	}
	wg.Wait()

	var applied int
	err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id > 0").Scan(&applied)
	if err != nil {
		t.Fatal(err)
	}
	if total != want || applied != want {
		t.Errorf("hai instance chạy tổng cộng %d migration, goose_db_version có %d; muốn %d", total, applied, want)
	}
}
