// Package testdb tạo database PostgreSQL cho integration test. Mỗi test có một schema
// riêng nên các test (kể cả test của nhiều package chạy cùng lúc) không đụng dữ liệu
// của nhau và được phép gọi t.Parallel().
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/toidicode/taskflow/internal/postgres"
)

// EnvURL là biến môi trường chứa URL của database dành riêng cho test.
// Test tạo và xóa schema trong database này, đừng trỏ nó vào database thật.
const EnvURL = "TASKFLOW_TEST_DATABASE_URL"

// New trả về kết nối tới một schema mới đã chạy mọi migration.
// Không đặt TASKFLOW_TEST_DATABASE_URL thì test được bỏ qua.
func New(t testing.TB) *sql.DB {
	t.Helper()
	db := Empty(t)
	// không dùng postgres.NewMigrator: advisory lock của nó là của cả database,
	// các test chạy song song sẽ phải chờ nhau, mà mỗi test đã có schema riêng
	p, err := goose.NewProvider(goose.DialectPostgres, db, postgres.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(context.Background()); err != nil {
		t.Fatalf("chạy migration: %v", err)
	}
	return db
}

// Empty trả về kết nối tới một schema mới chưa có bảng nào, dùng để test chính migration.
// Schema bị xóa khi test kết thúc.
func Empty(t testing.TB) *sql.DB {
	t.Helper()
	base := os.Getenv(EnvURL)
	if base == "" {
		t.Skip("chưa đặt " + EnvURL)
	}
	ctx := context.Background()
	admin, err := postgres.Open(ctx, base)
	if err != nil {
		t.Fatalf("kết nối database test: %v", err)
	}
	schema := "test_" + strings.ToLower(rand.Text()[:10]) // VD test_k7qzm2xa4b
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("tạo schema: %v", err)
	}
	t.Cleanup(func() { // chạy sau cùng: t.Cleanup chạy theo thứ tự ngược lúc đăng ký
		defer admin.Close()
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("xóa schema %s: %v", schema, err)
		}
	})

	// mọi kết nối của pool mới đều có search_path là schema này: câu SQL không ghi tên schema
	// (CREATE TABLE tasks, SELECT ... FROM tasks) đều chạy trong đó
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatalf("kết nối schema %s: %v", schema, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
