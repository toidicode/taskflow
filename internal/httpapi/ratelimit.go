package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/ratelimit"
)

// Limits là các giới hạn request của API. Limit có N = 0 (giá trị "off") là tắt.
type Limits struct {
	AuthIP     ratelimit.Limit // mỗi IP, tính riêng cho /auth/register và /auth/login
	LoginEmail ratelimit.Limit // mỗi email trên /auth/login, cộng dồn từ mọi IP
	User       ratelimit.Limit // mỗi user trên các route cần đăng nhập
}

// RateLimit là cấu hình rate limiting của API. Limiter nil là tắt hẳn (test dùng newMux).
type RateLimit struct {
	Limiter  ratelimit.Limiter // Redis khi chạy nhiều bản API, không thì Memory
	Fallback ratelimit.Limiter // dùng cho route đăng nhập, đăng ký khi Limiter lỗi (Redis chết)
	Limits   Limits
	// EmailSecret là khóa HMAC để băm email thành key. Bí mật: không ghi log.
	EmailSecret []byte
}

// policy là một luật giới hạn: tên (phần đầu của key và tên trong header RateLimit),
// giới hạn, và có cho request qua khi limiter lỗi hay không.
type policy struct {
	name     string
	limit    ratelimit.Limit
	failOpen bool // true: limiter lỗi thì cho qua; false: đếm bằng Fallback trong bộ nhớ
}

// middleware giới hạn request theo key(c), VD IP hay user ID.
func (rl RateLimit) middleware(p policy, key func(*gin.Context) string) gin.HandlerFunc {
	if rl.Limiter == nil || !p.limit.Enabled() {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if rl.allow(c, p, key(c)) {
			c.Next()
		}
	}
}

// allow lấy một token cho key. Bị chặn thì ghi 429 kèm Retry-After, gọi Abort và trả về false.
func (rl RateLimit) allow(c *gin.Context, p policy, key string) bool {
	if rl.Limiter == nil || !p.limit.Enabled() {
		return true
	}
	ctx, key := c.Request.Context(), p.name+":"+key
	res, err := rl.Limiter.Allow(ctx, key, p.limit)
	if err != nil {
		if p.failOpen || rl.Fallback == nil {
			requestLogger(c).Warn("rate limit lỗi, cho request đi qua", "policy", p.name, "err", err)
			return true
		}
		requestLogger(c).Warn("rate limit lỗi, đếm trong bộ nhớ", "policy", p.name, "err", err)
		if res, err = rl.Fallback.Allow(ctx, key, p.limit); err != nil {
			return true
		}
	}

	// header theo bản nháp IETF draft-ietf-httpapi-ratelimit-headers-11
	h := c.Writer.Header() // Add: một request có thể qua nhiều luật (đăng nhập: IP rồi email)
	h.Add("RateLimit-Policy", fmt.Sprintf(`"%s";q=%d;w=%d`, p.name, p.limit.N, seconds(p.limit.Period)))
	// t: trong t giây tới client chỉ đi được tối đa r request (tới khi có token kế tiếp).
	// Bị chặn thì t = Retry-After: Retry-After không được sớm hơn lúc hết t (draft-11 §6).
	h.Add("RateLimit", fmt.Sprintf(`"%s";r=%d;t=%d`, p.name, res.Remaining, seconds(res.Window)))
	if res.Allowed {
		return true
	}
	retry := seconds(res.RetryAfter)
	h.Set("Retry-After", strconv.Itoa(retry)) // số giây (RFC 9110)
	requestLogger(c).Info("rate limit: chặn request", "policy", p.name)
	errorJSON(c, http.StatusTooManyRequests, fmt.Sprintf("quá nhiều request, hãy thử lại sau %d giây", retry))
	return false
}

// seconds làm tròn lên thành số giây: chờ 0,3 giây thì báo 1 giây, không báo 0.
func seconds(d time.Duration) int {
	return int(math.Ceil(d.Seconds()))
}

// ipKey là key theo IP client. c.ClientIP() chỉ tin X-Forwarded-For từ proxy tin cậy (Bài 39, 41).
// IPv6 tính theo cả dải /64: một máy thường được cấp nguyên dải và đổi địa chỉ trong đó rất dễ.
func ipKey(c *gin.Context) string {
	ip, err := netip.ParseAddr(c.ClientIP())
	if err != nil {
		return c.ClientIP()
	}
	ip = ip.Unmap() // ::ffff:1.2.3.4 là IPv4
	if ip.Is6() {
		return netip.PrefixFrom(ip, 64).Masked().String()
	}
	return ip.String()
}

// userKey là key theo user đã đăng nhập, dùng sau requireAuth.
func userKey(c *gin.Context) string {
	return strconv.Itoa(userIDFrom(c))
}

// emailKey băm email đã chuẩn hóa bằng HMAC-SHA256 với EmailSecret: key có độ dài cố định, và
// không có secret thì không thể thử một danh sách email để biết key nào của ai.
func (rl RateLimit) emailKey(email string) string {
	mac := hmac.New(sha256.New, rl.EmailSecret)
	mac.Write([]byte(email))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}
