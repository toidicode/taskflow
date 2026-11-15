package postgres

import (
	"database/sql"
	"embed"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// migrations chứa các file migration SQL, nhúng vào binary lúc biên dịch
// nên khi deploy không phải chép thư mục migrations theo.
//
//go:embed migrations/*.sql
var migrations embed.FS

// NewMigrator tạo goose.Provider chạy các migration nhúng trong binary trên db.
// Provider giữ một advisory lock của PostgreSQL trong lúc chạy migration,
// nên nhiều instance cùng chạy thì chỉ một instance làm, các instance khác chờ.
func NewMigrator(db *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrations, "migrations") // goose tìm file ở gốc của fsys
	if err != nil {
		return nil, err
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
}
