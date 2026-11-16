package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/ratelimit"
	"github.com/toidicode/taskflow/internal/user"
)

// newLimitedMux tạo engine có rate limit rl, Caddy (10.39.0.10) là proxy tin cậy.
// Không có dispatcher việc nền nên dùng được trong bong bóng synctest.
func newLimitedMux(t *testing.T, rl RateLimit) *gin.Engine {
	t.Helper()
	s := memoryStores(newTestFiles(t))
	if err := seed(context.Background(), s.projects, s.tasks); err != nil {
		t.Fatal(err)
	}
	s.users.Add(context.Background(), &user.User{Email: "an@example.com", Name: "An"})
	d := s.deps(testTokens)
	d.RateLimit = rl
	trusted, _ := ParseTrustedProxies("10.39.0.10")
	r := newEngine(trusted)
	routes(r, d)
	return r
}

// sendFrom gửi POST tới target từ địa chỉ remote, xff khác rỗng thì gửi kèm X-Forwarded-For.
func sendFrom(h http.Handler, remote, xff, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", target, strings.NewReader(body))
	req.RemoteAddr = remote
	req.Header.Set("Content-Type", "application/json")
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRateLimitLoginIP(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mem := ratelimit.NewMemory()
		mux := newLimitedMux(t, RateLimit{Limiter: mem, Fallback: mem,
			Limits: Limits{AuthIP: ratelimit.Limit{N: 3, Period: time.Minute}}})
		// body hỏng: handler trả 400 ngay, không chạy bcrypt; giới hạn theo IP chạy trước khi đọc body
		login := func(remote, xff string) *httptest.ResponseRecorder {
			return sendFrom(mux, remote, xff, "/api/v1/auth/login", "x")
		}

		for i := range 3 {
			if rec := login("203.0.113.7:4000", ""); rec.Code != http.StatusBadRequest {
				t.Fatalf("request %d: %d, muốn 400", i+1, rec.Code)
			}
		}
		rec := login("203.0.113.7:4001", "")
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "20" ||
			!strings.Contains(rec.Body.String(), "hãy thử lại sau 20 giây") {
			t.Fatalf("request 4: %d, Retry-After %q, %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body)
		}
		if got := rec.Header().Get("RateLimit-Policy"); got != `"login-ip";q=3;w=60` {
			t.Errorf("RateLimit-Policy = %q", got)
		}
		if got := rec.Header().Get("RateLimit"); got != `"login-ip";r=0;t=20` {
			t.Errorf("RateLimit = %q", got)
		}

		tests := []struct {
			name, remote, xff string
			want              int
		}{
			{"tự khai X-Forwarded-For: vẫn là IP cũ", "203.0.113.7:4002", "1.2.3.4", 429},
			{"qua Caddy, IP khác", "10.39.0.10:5000", "198.51.100.1", 400},
			{"qua Caddy, cùng IP", "10.39.0.10:5000", "203.0.113.7", 429},
		}
		for _, tt := range tests {
			if rec := login(tt.remote, tt.xff); rec.Code != tt.want {
				t.Errorf("%s: %d, muốn %d", tt.name, rec.Code, tt.want)
			}
		}
		if rec := sendFrom(mux, "203.0.113.7:4003", "", "/api/v1/auth/register", "x"); rec.Code == http.StatusTooManyRequests {
			t.Error("đăng ký có giới hạn riêng, không dùng chung với đăng nhập")
		}

		// IPv6: cả dải /64 chung một bucket
		for range 3 {
			login("[2001:db8::1]:4000", "")
		}
		if rec := login("[2001:db8::ffff]:4000", ""); rec.Code != http.StatusTooManyRequests {
			t.Errorf("IPv6 cùng /64: %d, muốn 429", rec.Code)
		}
		if rec := login("[2001:db8:0:1::1]:4000", ""); rec.Code != http.StatusBadRequest {
			t.Errorf("IPv6 khác /64: %d, muốn 400", rec.Code)
		}

		time.Sleep(20 * time.Second) // đồng hồ giả: có lại một token
		if rec := login("203.0.113.7:4004", ""); rec.Code != http.StatusBadRequest {
			t.Errorf("sau 20s: %d, muốn 400", rec.Code)
		}
	})
}

// TestRateLimitLoginEmail: dò mật khẩu một tài khoản từ nhiều IP vẫn bị chặn theo email.
func TestRateLimitLoginEmail(t *testing.T) {
	mem := ratelimit.NewMemory()
	mux := newLimitedMux(t, RateLimit{Limiter: mem, Fallback: mem,
		Limits: Limits{AuthIP: ratelimit.Limit{N: 10, Period: time.Minute}, LoginEmail: ratelimit.Limit{N: 2, Period: time.Minute}}})
	body := func(email string) string { return `{"email": "` + email + `", "password": "doan-mo-1"}` }

	for _, ip := range []string{"203.0.113.7:4000", "198.51.100.1:4000"} {
		if rec := sendFrom(mux, ip, "", "/api/v1/auth/login", body("an@example.com")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: %d, muốn 401", ip, rec.Code)
		}
	}
	rec := sendFrom(mux, "192.0.2.50:4000", "", "/api/v1/auth/login", body("AN@Example.com")) // IP thứ ba, email viết hoa
	// bcrypt chạy thật (chậm hơn nhiều khi có -race) nên thời gian chờ chỉ biết là tối đa 30 giây
	if retry, _ := strconv.Atoi(rec.Header().Get("Retry-After")); rec.Code != http.StatusTooManyRequests || retry < 1 || retry > 30 {
		t.Errorf("lần thứ ba: %d, Retry-After %q; muốn 429, 1-30", rec.Code, rec.Header().Get("Retry-After"))
	}
	if got := rec.Header().Values("RateLimit"); len(got) != 2 || !strings.HasPrefix(got[0], `"login-ip";r=`) || got[1] != `"login-email";r=0;t=`+rec.Header().Get("Retry-After") {
		t.Errorf("RateLimit = %q, muốn hai luật login-ip và login-email", got)
	}
	if rec := sendFrom(mux, "192.0.2.50:4000", "", "/api/v1/auth/login", body("binh@example.com")); rec.Code != http.StatusUnauthorized {
		t.Errorf("email khác: %d, muốn 401", rec.Code)
	}
}

// TestEmailKey: key phụ thuộc secret, nên không có secret thì không đoán được email từ key.
func TestEmailKey(t *testing.T) {
	a := RateLimit{EmailSecret: []byte("secret-a")}
	b := RateLimit{EmailSecret: []byte("secret-b")}
	if k := a.emailKey("an@example.com"); len(k) != 32 || k != a.emailKey("an@example.com") {
		t.Errorf("key = %q, muốn 32 ký tự hex, ổn định", k)
	}
	if a.emailKey("an@example.com") == b.emailKey("an@example.com") {
		t.Error("secret khác phải cho key khác")
	}
}

func TestRateLimitUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mem := ratelimit.NewMemory()
		mux := newLimitedMux(t, RateLimit{Limiter: mem, Fallback: mem,
			Limits: Limits{User: ratelimit.Limit{N: 2, Period: time.Minute}}})

		rec := send(mux, "GET", "/api/v1/projects", "")
		if rec.Code != http.StatusOK || rec.Header().Get("RateLimit") != `"user";r=1;t=30` {
			t.Fatalf("request 1: %d, RateLimit %q", rec.Code, rec.Header().Get("RateLimit"))
		}
		if rec := sendAs(mux, 0, "GET", "/api/v1/projects", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("không có token: %d, muốn 401 (và không tốn token của ai)", rec.Code)
		}
		send(mux, "GET", "/api/v1/projects", "")
		rec = send(mux, "GET", "/api/v1/projects/1", "")
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "30" {
			t.Errorf("request 3: %d, Retry-After %q; muốn 429, 30", rec.Code, rec.Header().Get("Retry-After"))
		}
		if rec := sendAs(mux, 2, "GET", "/api/v1/projects", ""); rec.Code != http.StatusOK {
			t.Errorf("user khác: %d, muốn 200", rec.Code)
		}
	})
}

// brokenLimiter giả Redis chết: lần nào cũng lỗi.
type brokenLimiter struct{}

func (brokenLimiter) Allow(context.Context, string, ratelimit.Limit) (ratelimit.Result, error) {
	return ratelimit.Result{}, errors.New("dial tcp 10.39.0.5:6379: connect: connection refused")
}

// TestRateLimitLimiterDown: Redis chết thì đăng nhập vẫn bị giới hạn (bằng bộ nhớ), route khác cho qua.
func TestRateLimitLimiterDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		one := ratelimit.Limit{N: 1, Period: time.Minute}
		mux := newLimitedMux(t, RateLimit{Limiter: brokenLimiter{}, Fallback: ratelimit.NewMemory(),
			Limits: Limits{AuthIP: one, User: one}})

		sendFrom(mux, "203.0.113.7:4000", "", "/api/v1/auth/login", "x")
		if rec := sendFrom(mux, "203.0.113.7:4000", "", "/api/v1/auth/login", "x"); rec.Code != http.StatusTooManyRequests {
			t.Errorf("đăng nhập lần 2: %d, muốn 429 nhờ Fallback", rec.Code)
		}
		for i := range 3 {
			if rec := send(mux, "GET", "/api/v1/projects", ""); rec.Code != http.StatusOK {
				t.Errorf("request %d: %d, muốn 200 (luật user cho qua khi limiter lỗi)", i+1, rec.Code)
			}
		}
	})
}
