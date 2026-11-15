// Package user quản lý người dùng của TaskFlow.
package user

import (
	"strings"
	"time"
)

// User là một người dùng của TaskFlow.
type User struct {
	ID           int       `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"` // bcrypt hash: không bao giờ gửi cho client hay ghi ra log
	CreatedAt    time.Time `json:"created_at"`
}

// NormalizeEmail bỏ khoảng trắng hai đầu và đổi email sang chữ thường:
// TaskFlow coi An@Example.com và an@example.com là cùng một email.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
