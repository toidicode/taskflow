package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/toidicode/taskflow/internal/auth"
)

func TestRegisterLogin(t *testing.T) {
	mux := newTestMux(t) // đã có user #1 an@example.com

	rec := sendAs(mux, 0, "POST", "/api/v1/auth/register", `{"email": " Binh@Example.com ", "name": "Bình", "password": "mat-khau-cua-binh"}`)
	body := rec.Body.String()
	if rec.Code != http.StatusCreated || !strings.Contains(body, `{"id":2,"email":"binh@example.com","name":"Bình","created_at":"`) {
		t.Fatalf("register: %d %s", rec.Code, body)
	}
	if strings.Contains(body, "password") || strings.Contains(body, "$2a$") {
		t.Errorf("register trả về mật khẩu hoặc hash: %s", body)
	}

	// email không phân biệt hoa thường
	rec = sendAs(mux, 0, "POST", "/api/v1/auth/register", `{"email": "BINH@example.com", "name": "Bình 2", "password": "mat-khau-khac"}`)
	if rec.Code != http.StatusConflict || rec.Body.String() != `{"error":"email đã được đăng ký"}`+"\n" {
		t.Errorf("register email trùng: %d %s", rec.Code, rec.Body)
	}

	// sai email và sai mật khẩu nhận cùng một response
	want := `{"error":"email hoặc mật khẩu không đúng"}` + "\n"
	for _, body := range []string{
		`{"email": "binh@example.com", "password": "mat-khau-sai"}`,
		`{"email": "khong-co@example.com", "password": "mat-khau-cua-binh"}`,
	} {
		rec = sendAs(mux, 0, "POST", "/api/v1/auth/login", body)
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != want ||
			rec.Header().Get("WWW-Authenticate") != `Bearer realm="taskflow"` {
			t.Errorf("login %s: %d %s %q", body, rec.Code, rec.Body, rec.Header().Get("WWW-Authenticate"))
		}
	}

	rec = sendAs(mux, 0, "POST", "/api/v1/auth/login", `{"email": "BINH@example.com", "password": "mat-khau-cua-binh"}`)
	var tok tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); rec.Code != 200 || err != nil {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	if tok.TokenType != "Bearer" || tok.ExpiresIn != 900 {
		t.Errorf("login: %+v", tok)
	}

	// dùng token vừa nhận để gọi /me
	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `{"id":2,"email":"binh@example.com","name":"Bình"`) {
		t.Errorf("GET /me: %d %s", rec.Code, rec.Body)
	}
}

func TestRegisterValidation(t *testing.T) {
	mux := newTestMux(t)
	tests := []struct {
		name string
		body string
		want string // body JSON của response 422
	}{
		{"thiếu hết", `{}`,
			`{"email":"email không hợp lệ","name":"không được để trống","password":"tối thiểu 8 ký tự"}`},
		{"email có tên hiển thị", `{"email": "Bình <binh@example.com>", "name": "Bình", "password": "12345678"}`,
			`{"email":"email không hợp lệ"}`},
		// "ệ" chiếm 3 byte: 25 ký tự là 75 byte, quá giới hạn 72 byte của bcrypt
		{"mật khẩu quá 72 byte", `{"email": "binh@example.com", "name": "Bình", "password": "` + strings.Repeat("ệ", 25) + `"}`,
			`{"password":"tối đa 72 byte"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := sendAs(mux, 0, "POST", "/api/v1/auth/register", tt.body)
			want := `{"error":"dữ liệu không hợp lệ","fields":` + tt.want + "}\n"
			if rec.Code != http.StatusUnprocessableEntity || rec.Body.String() != want {
				t.Errorf("%d %s, muốn %s", rec.Code, rec.Body, want)
			}
		})
	}
}

func TestRequireAuth(t *testing.T) {
	mux := newTestMux(t)
	other := auth.NewTokens([]byte("secret-cua-server-khac-0123456789"), time.Minute)
	otherToken, _ := other.Issue(1)
	expired := auth.NewTokens([]byte("secret-chi-dung-trong-test-0123456789"), -time.Minute)
	expiredToken, _ := expired.Issue(1)

	const (
		noToken = `Bearer realm="taskflow"`
		invalid = `Bearer realm="taskflow", error="invalid_token"`
	)
	tests := []struct {
		name          string
		authorization string
		wantCode      int
		wantChallenge string // header WWW-Authenticate
	}{
		{"không có header", "", 401, noToken},
		{"sai scheme", "Basic YW46bWF0LWtoYXU=", 401, noToken},
		{"Bearer rỗng", "Bearer ", 401, noToken},
		{"token rác", "Bearer abc.def.ghi", 401, invalid},
		{"secret khác", "Bearer " + otherToken, 401, invalid},
		{"hết hạn", "Bearer " + expiredToken, 401, invalid},
		{"scheme viết thường", "bearer " + mustIssue(t, 1), 200, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/tasks", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode || rec.Header().Get("WWW-Authenticate") != tt.wantChallenge {
				t.Errorf("%d %q %s", rec.Code, rec.Header().Get("WWW-Authenticate"), rec.Body)
			}
		})
	}

	// route công khai không cần token
	if rec := sendAs(mux, 0, "GET", "/health", ""); rec.Code != 200 {
		t.Errorf("GET /health không token: %d", rec.Code)
	}
	// token của user đã bị xóa (hoặc chưa từng có): /me trả 401
	if rec := sendAs(mux, 99, "GET", "/api/v1/me", ""); rec.Code != 401 {
		t.Errorf("GET /me của user #99: %d %s", rec.Code, rec.Body)
	}
}

// mustIssue phát token của testTokens cho user userID.
func mustIssue(t *testing.T, userID int) string {
	t.Helper()
	token, err := testTokens.Issue(userID)
	if err != nil {
		t.Fatal(err)
	}
	return token
}
