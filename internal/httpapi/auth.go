package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/user"
)

// registerInput là body của POST /auth/register.
type registerInput struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// validate kiểm tra body đăng ký, cài interface validatable.
func (in *registerInput) validate(v validator) {
	email := user.NormalizeEmail(in.Email)
	addr, err := mail.ParseAddress(email)
	v.check(err == nil && addr.Address == email && len(email) <= 254, "email", "email không hợp lệ")
	name := strings.TrimSpace(in.Name)
	v.check(name != "", "name", "không được để trống")
	v.check(utf8.RuneCountInString(name) <= 100, "name", "tối đa 100 ký tự")
	v.check(utf8.RuneCountInString(in.Password) >= 8, "password", "tối thiểu 8 ký tự")
	v.check(len(in.Password) <= auth.MaxPasswordBytes, "password", // len đếm byte, chữ có dấu chiếm 2-3 byte
		fmt.Sprintf("tối đa %d byte", auth.MaxPasswordBytes))
}

// register tạo tài khoản mới, trả 201 kèm thông tin user (không có mật khẩu).
func register(users user.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in registerInput
		if !decodeValid(w, r, &in) {
			return
		}
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			writeError(w, r, err)
			return
		}
		u := &user.User{Email: user.NormalizeEmail(in.Email), Name: strings.TrimSpace(in.Name), PasswordHash: hash}
		if err := users.Add(r.Context(), u); errors.Is(err, user.ErrEmailTaken) {
			errorJSON(w, http.StatusConflict, user.ErrEmailTaken.Error())
			return
		} else if err != nil {
			writeError(w, r, err)
			return
		}
		w.Header().Set("Location", "/api/v1/me")
		writeJSON(w, http.StatusCreated, u)
	}
}

// loginInput là body của POST /auth/login.
type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// tokenResponse là body trả về khi đăng nhập thành công, đặt tên field như OAuth 2.0.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"` // số giây tới khi token hết hạn
}

// login kiểm tra email, mật khẩu rồi phát access token.
// Sai email hay sai mật khẩu đều nhận cùng một lỗi 401.
func login(users user.Store, tokens *auth.Tokens) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in loginInput
		if err := readJSON(w, r, &in); err != nil {
			bodyError(w, err)
			return
		}
		u, err := users.GetByEmail(r.Context(), user.NormalizeEmail(in.Email))
		if err != nil && !errors.Is(err, user.ErrNotFound) {
			writeError(w, r, err)
			return
		}
		// không có user thì u.PasswordHash rỗng: CheckPassword vẫn chạy bcrypt rồi trả false
		if !auth.CheckPassword(u.PasswordHash, in.Password) {
			unauthorized(w, false, "email hoặc mật khẩu không đúng")
			return
		}
		token, err := tokens.Issue(u.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, tokenResponse{
			AccessToken: token,
			TokenType:   "Bearer",
			ExpiresIn:   int(tokens.TTL().Seconds()),
		})
	}
}

// me trả về user đang đăng nhập.
func me(users user.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := users.Get(r.Context(), userIDFrom(r.Context()))
		if errors.Is(err, user.ErrNotFound) { // token còn hạn nhưng user đã bị xóa
			unauthorized(w, true, "tài khoản không còn tồn tại")
			return
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, u)
	}
}

// requireAuth chỉ cho request có access token hợp lệ đi tiếp và gắn user ID vào context.
func requireAuth(tokens *auth.Tokens) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				unauthorized(w, false, "cần đăng nhập: thiếu access token")
				return
			}
			id, err := tokens.Verify(token)
			if err != nil {
				// ghi lý do để debug, không bao giờ ghi bản thân token
				requestLogger(r).Debug("token bị từ chối", "err", err)
				unauthorized(w, true, "token không hợp lệ hoặc đã hết hạn")
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken đọc token từ header "Authorization: Bearer <token>".
// Tên scheme không phân biệt hoa thường (RFC 9110).
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// userIDFrom trả về ID của user đã đăng nhập, 0 khi request không đi qua requireAuth.
func userIDFrom(ctx context.Context) int {
	id, _ := ctx.Value(userIDKey).(int)
	return id
}

// unauthorized trả 401 kèm header WWW-Authenticate mà 401 bắt buộc phải có.
// invalidToken là true khi client có gửi token nhưng token không dùng được (RFC 6750).
func unauthorized(w http.ResponseWriter, invalidToken bool, message string) {
	challenge := `Bearer realm="taskflow"`
	if invalidToken {
		challenge += `, error="invalid_token"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	errorJSON(w, http.StatusUnauthorized, message)
}
