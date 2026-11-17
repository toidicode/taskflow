package auth

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var testSecret = []byte("secret-chi-dung-trong-test-0123456789")

func TestPassword(t *testing.T) {
	hash, err := HashPassword("mat-khau-cua-an")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$2a$12$") {
		t.Errorf("hash = %q, muốn bcrypt cost 12", hash)
	}
	if !CheckPassword(hash, "mat-khau-cua-an") || CheckPassword(hash, "mat-khau-sai") {
		t.Error("CheckPassword sai")
	}
	// hash rỗng (không có user): vẫn chạy bcrypt với hash giả và luôn trả false
	if CheckPassword("", "taskflow-khong-phai-mat-khau-that") {
		t.Error("CheckPassword với hash rỗng phải trả false")
	}

	// 72 byte là giới hạn của bcrypt: "ệ" chiếm 3 byte nên 25 chữ "ệ" là 75 byte
	if _, err := HashPassword(strings.Repeat("ệ", 25)); !errors.Is(err, bcrypt.ErrPasswordTooLong) {
		t.Errorf("mật khẩu 75 byte: lỗi = %v, muốn ErrPasswordTooLong", err)
	}
}

// sign ký claims bằng method và key, dùng để tạo các token "giả" trong test.
func sign(t *testing.T, method jwt.SigningMethod, claims jwt.Claims, key any) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTokens(t *testing.T) {
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	tokens := NewTokens(testSecret, 15*time.Minute)
	tokens.now = func() time.Time { return now }

	token, err := tokens.Issue(7)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := tokens.Verify(token); err != nil || id != 7 {
		t.Fatalf("Verify() = %d, %v; muốn 7", id, err)
	}

	valid := jwt.RegisteredClaims{
		Issuer: issuer, Subject: "7", Audience: jwt.ClaimStrings{audience},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
	}
	without := func(f func(c *jwt.RegisteredClaims)) jwt.RegisteredClaims {
		c := valid
		f(&c)
		return c
	}
	// sửa payload mà giữ chữ ký cũ: đổi sub thành 1
	parts := strings.Split(token, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(payload), `"sub":"7"`, `"sub":"1"`, 1)))
	tampered := strings.Join(parts, ".")

	tests := []struct {
		name  string
		token string
		want  error // lỗi gốc của jwt
	}{
		{"sửa payload", tampered, jwt.ErrTokenSignatureInvalid},
		{"sai secret", sign(t, jwt.SigningMethodHS256, valid, []byte("secret-khac-secret-khac-secret-khac")), jwt.ErrTokenSignatureInvalid},
		{"alg HS512", sign(t, jwt.SigningMethodHS512, valid, testSecret), jwt.ErrTokenSignatureInvalid},
		{"alg none", sign(t, jwt.SigningMethodNone, valid, jwt.UnsafeAllowNoneSignatureType), jwt.ErrTokenSignatureInvalid},
		{"không có exp", sign(t, jwt.SigningMethodHS256, without(func(c *jwt.RegisteredClaims) { c.ExpiresAt = nil }), testSecret), jwt.ErrTokenRequiredClaimMissing},
		{"hết hạn", sign(t, jwt.SigningMethodHS256, without(func(c *jwt.RegisteredClaims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Second)) }), testSecret), jwt.ErrTokenExpired},
		{"sai iss", sign(t, jwt.SigningMethodHS256, without(func(c *jwt.RegisteredClaims) { c.Issuer = "app-khac" }), testSecret), jwt.ErrTokenInvalidIssuer},
		{"sai aud", sign(t, jwt.SigningMethodHS256, without(func(c *jwt.RegisteredClaims) { c.Audience = jwt.ClaimStrings{"app-khac"} }), testSecret), jwt.ErrTokenInvalidAudience},
		{"không phải JWT", "abc", jwt.ErrTokenMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := tokens.Verify(tt.token)
			if !errors.Is(err, ErrInvalidToken) || !errors.Is(err, tt.want) {
				t.Errorf("Verify() = %d, %v; muốn lỗi %v", id, err, tt.want)
			}
		})
	}

	// sub không phải số
	bad := sign(t, jwt.SigningMethodHS256, without(func(c *jwt.RegisteredClaims) { c.Subject = "an" }), testSecret)
	if _, err := tokens.Verify(bad); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("sub không phải số: lỗi = %v", err)
	}

	// token hết hạn đúng lúc now = exp
	now = now.Add(15 * time.Minute)
	if _, err := tokens.Verify(token); !errors.Is(err, jwt.ErrTokenExpired) {
		t.Errorf("sau 15 phút: lỗi = %v, muốn ErrTokenExpired", err)
	}
}

// TestForAudience: token của một aud không dùng được cho aud khác dù cùng secret.
func TestForAudience(t *testing.T) {
	api := NewTokens([]byte("secret-chi-dung-trong-test-0123456789"), time.Minute)
	web := api.ForAudience("taskflow-web", time.Hour)
	if web.TTL() != time.Hour || api.TTL() != time.Minute {
		t.Fatalf("TTL: web %v, api %v", web.TTL(), api.TTL())
	}
	webToken, _ := web.Issue(7)
	apiToken, _ := api.Issue(7)
	if id, err := web.Verify(webToken); err != nil || id != 7 {
		t.Errorf("web.Verify(web token) = %d, %v", id, err)
	}
	if _, err := api.Verify(webToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("api.Verify(web token): %v, muốn ErrInvalidToken", err)
	}
	if _, err := web.Verify(apiToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("web.Verify(api token): %v, muốn ErrInvalidToken", err)
	}
}
