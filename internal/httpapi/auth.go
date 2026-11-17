package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/auth"
	"github.com/toidicode/taskflow/internal/user"
)

// registerInput là body của POST /auth/register. Rule đơn giản viết bằng tag binding,
// phần còn lại trong method validate.
type registerInput struct {
	Email    string `json:"email"`
	Name     string `json:"name" binding:"notblank,max=100"`
	Password string `json:"password" binding:"min=8"` // min, max của string đếm ký tự (rune)
}

// validate kiểm tra những gì tag không diễn đạt được, cài interface validatable:
// email phải là một địa chỉ trần sau khi chuẩn hóa, mật khẩu tính theo byte.
func (in *registerInput) validate(v validator) {
	email := user.NormalizeEmail(in.Email)
	addr, err := mail.ParseAddress(email)
	v.check(err == nil && addr.Address == email && len(email) <= 254, "email", "email không hợp lệ")
	v.check(len(in.Password) <= auth.MaxPasswordBytes, "password", // len đếm byte, chữ có dấu chiếm 2-3 byte
		fmt.Sprintf("tối đa %d byte", auth.MaxPasswordBytes))
}

// register tạo tài khoản mới, trả 201 kèm thông tin user (không có mật khẩu).
func register(users user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in registerInput
		if !bindJSON(c, &in) {
			return
		}
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			writeError(c, err)
			return
		}
		u := &user.User{Email: user.NormalizeEmail(in.Email), Name: strings.TrimSpace(in.Name), PasswordHash: hash}
		if err := users.Add(c.Request.Context(), u); errors.Is(err, user.ErrEmailTaken) {
			errorJSON(c, http.StatusConflict, user.ErrEmailTaken.Error())
			return
		} else if err != nil {
			writeError(c, err)
			return
		}
		c.Header("Location", "/api/v1/me")
		writeJSON(c, http.StatusCreated, u)
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
func login(users user.Store, tokens *auth.Tokens, rl RateLimit) gin.HandlerFunc {
	emailPolicy := policy{"login-email", rl.Limits.LoginEmail, false}
	return func(c *gin.Context) {
		var in loginInput
		if err := readJSON(c, &in); err != nil {
			bodyError(c, err)
			return
		}
		// giới hạn theo email, cộng dồn từ mọi IP: chặn dò mật khẩu của một tài khoản từ nhiều IP
		email := user.NormalizeEmail(in.Email)
		if !rl.allow(c, emailPolicy, rl.emailKey(email)) {
			return
		}
		u, ok, err := authenticate(c.Request.Context(), users, email, in.Password)
		if err != nil {
			writeError(c, err)
			return
		}
		if !ok {
			unauthorized(c, false, "email hoặc mật khẩu không đúng")
			return
		}
		token, err := tokens.Issue(u.ID)
		if err != nil {
			writeError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, tokenResponse{
			AccessToken: token,
			TokenType:   "Bearer",
			ExpiresIn:   int(tokens.TTL().Seconds()),
		})
	}
}

// authenticate kiểm tra email (đã chuẩn hóa) và mật khẩu, dùng chung cho API và trang web.
// Sai email hay sai mật khẩu đều trả ok = false; err khác nil chỉ khi store lỗi.
func authenticate(ctx context.Context, users user.Store, email, password string) (u user.User, ok bool, err error) {
	u, err = users.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, user.ErrNotFound) {
		return user.User{}, false, err
	}
	// không có user thì u.PasswordHash rỗng: CheckPassword vẫn chạy bcrypt rồi trả false
	if !auth.CheckPassword(u.PasswordHash, password) {
		return user.User{}, false, nil
	}
	return u, true, nil
}

// me trả về user đang đăng nhập.
func me(users user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		u, err := users.Get(c.Request.Context(), userIDFrom(c))
		if errors.Is(err, user.ErrNotFound) { // token còn hạn nhưng user đã bị xóa
			unauthorized(c, true, "tài khoản không còn tồn tại")
			return
		}
		if err != nil {
			writeError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, u)
	}
}

// requireAuth chỉ cho request có access token hợp lệ đi tiếp và gắn user ID vào gin.Context.
// Token sai thì unauthorized ghi 401 và gọi c.Abort: handler của route không chạy.
func requireAuth(tokens *auth.Tokens) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c.Request)
		if !ok {
			unauthorized(c, false, "cần đăng nhập: thiếu access token")
			return
		}
		id, err := tokens.Verify(token)
		if err != nil {
			// ghi lý do để debug, không bao giờ ghi bản thân token
			requestLogger(c).Debug("token bị từ chối", "err", err)
			unauthorized(c, true, "token không hợp lệ hoặc đã hết hạn")
			return
		}
		c.Set(userIDKey, id)
		c.Next()
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
func userIDFrom(c *gin.Context) int {
	return c.GetInt(userIDKey)
}

// unauthorized trả 401 kèm header WWW-Authenticate mà 401 bắt buộc phải có.
// invalidToken là true khi client có gửi token nhưng token không dùng được (RFC 6750).
func unauthorized(c *gin.Context, invalidToken bool, message string) {
	challenge := `Bearer realm="taskflow"`
	if invalidToken {
		challenge += `, error="invalid_token"`
	}
	c.Header("WWW-Authenticate", challenge)
	errorJSON(c, http.StatusUnauthorized, message)
}
