package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer   = "taskflow"     // iss: ai phát token
	audience = "taskflow-api" // aud: token dùng cho dịch vụ nào
)

// ErrInvalidToken được trả về (đã wrap lỗi gốc của jwt) khi token không dùng được.
var ErrInvalidToken = errors.New("token không hợp lệ")

// Tokens phát và kiểm tra access token dạng JWT, ký bằng HS256 với một secret.
type Tokens struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time // test thay bằng đồng hồ giả
}

// NewTokens tạo Tokens ký bằng secret, token hết hạn sau ttl.
func NewTokens(secret []byte, ttl time.Duration) *Tokens {
	return &Tokens{secret: secret, ttl: ttl, now: time.Now}
}

// TTL là thời gian sống của một token.
func (t *Tokens) TTL() time.Duration { return t.ttl }

// Issue phát token cho user userID.
func (t *Tokens) Issue(userID int) (string, error) {
	now := t.now()
	claims := jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   strconv.Itoa(userID), // sub là chuỗi theo chuẩn JWT
		Audience:  jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(t.ttl)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

// Verify kiểm tra chữ ký, thuật toán, hạn dùng, iss, aud của token rồi trả về user ID trong sub.
func (t *Tokens) Verify(token string) (int, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		// chỉ nhận HS256, không tin "alg" mà token tự khai
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		// token không có exp thì không bao giờ hết hạn: từ chối
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	id, err := strconv.Atoi(claims.Subject)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%w: sub %q", ErrInvalidToken, claims.Subject)
	}
	return id, nil
}
