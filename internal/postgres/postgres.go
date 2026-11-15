// Package postgres lưu project và task của TaskFlow trong PostgreSQL,
// qua database/sql và driver pgx. Câu truy vấn nằm trong queries/*.sql,
// code Go gọi chúng do sqlc sinh ra trong package dbgen (xem sqlc.yaml).
package postgres

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // đăng ký driver "pgx" cho database/sql
)

// queryTimeout giới hạn thời gian chạy của mỗi câu truy vấn.
const queryTimeout = 3 * time.Second

// Open tạo pool kết nối tới PostgreSQL theo url rồi kiểm tra kết nối được thật.
func Open(ctx context.Context, url string) (*sql.DB, error) {
	db, err := sql.Open("pgx", url) // chỉ đọc url, chưa kết nối
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(25)                  // tối đa 25 kết nối cùng lúc
	db.SetMaxIdleConns(25)                  // giữ lại tới 25 kết nối rảnh để dùng lại
	db.SetConnMaxIdleTime(5 * time.Minute)  // kết nối rảnh quá 5 phút thì đóng
	db.SetConnMaxLifetime(30 * time.Minute) // kết nối nào cũng được thay mới sau 30 phút

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil { // lúc này mới mở kết nối đầu tiên
		db.Close()
		return nil, err
	}
	return db, nil
}

// nullInt đổi quy ước "0 là không có" của Go sang NULL của SQL.
func nullInt(n int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(n), Valid: n != 0}
}

// nullTime đổi quy ước "zero value là không có" của time.Time sang NULL của SQL.
func nullTime(t time.Time) sql.NullTime {
	return sql.NullTime{Time: t, Valid: !t.IsZero()}
}
