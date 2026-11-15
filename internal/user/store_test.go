package user_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/toidicode/taskflow/internal/user"
)

func TestNormalizeEmail(t *testing.T) {
	if got := user.NormalizeEmail("  An.Nguyen@Example.COM "); got != "an.nguyen@example.com" {
		t.Errorf("NormalizeEmail = %q", got)
	}
}

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	s := user.NewMemoryStore()

	an := &user.User{Email: "an@example.com", Name: "An", PasswordHash: "$2a$12$..."}
	if err := s.Add(ctx, an); err != nil || an.ID != 1 || an.CreatedAt.IsZero() {
		t.Fatalf("Add() = %v, ID %d, CreatedAt %v", err, an.ID, an.CreatedAt)
	}
	err := s.Add(ctx, &user.User{Email: "an@example.com", Name: "An 2"})
	if !errors.Is(err, user.ErrEmailTaken) {
		t.Errorf("Add() email trùng: lỗi = %v, muốn ErrEmailTaken", err)
	}

	if u, err := s.GetByEmail(ctx, "an@example.com"); err != nil || u != *an {
		t.Errorf("GetByEmail() = %+v, %v", u, err)
	}
	if u, err := s.Get(ctx, 1); err != nil || u != *an {
		t.Errorf("Get(1) = %+v, %v", u, err)
	}
	if _, err := s.Get(ctx, 9); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("Get(9) lỗi = %v, muốn ErrNotFound", err)
	}
	if _, err := s.GetByEmail(ctx, "binh@example.com"); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("GetByEmail(binh) lỗi = %v, muốn ErrNotFound", err)
	}
}

func TestUserJSONHidesHash(t *testing.T) {
	data, err := json.Marshal(user.User{ID: 1, Email: "an@example.com", Name: "An", PasswordHash: "$2a$12$bi-mat"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "bi-mat") || strings.Contains(string(data), "password") {
		t.Errorf("JSON lộ hash: %s", data)
	}
}
