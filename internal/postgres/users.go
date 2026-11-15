package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/toidicode/taskflow/internal/postgres/dbgen"
	"github.com/toidicode/taskflow/internal/user"
)

// UserStore lưu user trong bảng users, cài interface user.Store.
type UserStore struct {
	q *dbgen.Queries
}

var _ user.Store = (*UserStore)(nil)

// NewUserStore tạo UserStore dùng pool db.
func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{q: dbgen.New(db)}
}

func (s *UserStore) Add(ctx context.Context, u *user.User) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	row, err := s.q.CreateUser(ctx, dbgen.CreateUserParams{
		Email:        u.Email,
		Name:         u.Name,
		PasswordHash: u.PasswordHash,
	})
	if isUniqueViolation(err) { // UNIQUE của cột email: hai request đăng ký cùng lúc cũng chỉ một cái qua
		return fmt.Errorf("thêm user: %w", user.ErrEmailTaken)
	}
	if err != nil {
		return fmt.Errorf("thêm user: %w", err)
	}
	u.ID, u.CreatedAt = int(row.ID), row.CreatedAt
	return nil
}

func (s *UserStore) Get(ctx context.Context, id int) (user.User, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	row, err := s.q.GetUser(ctx, int64(id))
	if errors.Is(err, sql.ErrNoRows) {
		return user.User{}, fmt.Errorf("user #%d: %w", id, user.ErrNotFound)
	}
	if err != nil {
		return user.User{}, fmt.Errorf("đọc user #%d: %w", id, err)
	}
	return toUser(row), nil
}

func (s *UserStore) GetByEmail(ctx context.Context, email string) (user.User, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	row, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		return user.User{}, fmt.Errorf("user %q: %w", email, user.ErrNotFound)
	}
	if err != nil {
		return user.User{}, fmt.Errorf("đọc user %q: %w", email, err)
	}
	return toUser(row), nil
}

// toUser đổi một hàng của bảng users sang user.User.
func toUser(row dbgen.User) user.User {
	return user.User{
		ID:           int(row.ID),
		Email:        row.Email,
		Name:         row.Name,
		PasswordHash: row.PasswordHash,
		CreatedAt:    row.CreatedAt,
	}
}

// isUniqueViolation cho biết err là lỗi vi phạm ràng buộc UNIQUE của PostgreSQL (mã 23505).
func isUniqueViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505"
}
