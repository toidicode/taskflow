package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// withTx chạy fn trong một transaction trên db. fn trả về lỗi hoặc panic thì
// transaction bị rollback, không thì được commit.
func withTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil) // nil: mức cô lập mặc định (Read Committed)
	if err != nil {
		return fmt.Errorf("bắt đầu transaction: %w", err)
	}
	// chạy cả khi fn lỗi lẫn khi fn panic; sau Commit thì Rollback không làm gì (trả sql.ErrTxDone)
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// isForeignKeyViolation cho biết err là lỗi vi phạm khóa ngoại của PostgreSQL (mã 23503).
func isForeignKeyViolation(err error) bool {
	return violatedForeignKey(err) != ""
}

// violatedForeignKey trả về tên khóa ngoại bị vi phạm (VD "tasks_assignee_id_fkey") khi err là
// lỗi mã 23503, không thì trả "". Dùng khi một bảng có nhiều khóa ngoại.
func violatedForeignKey(err error) string {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "23503" {
		return ""
	}
	return pgErr.ConstraintName
}
