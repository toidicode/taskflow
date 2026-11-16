// Package ratelimit giới hạn số request theo key bằng thuật toán token bucket.
// Memory giữ các bucket trong bộ nhớ của một process; Redis giữ chúng trong Redis
// để mọi bản API cùng đếm chung.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Limit cho phép tối đa N request mỗi Period: bucket chứa tối đa N token, cứ Period/N
// lại có thêm một token, mỗi request lấy một token. N = 0 là tắt giới hạn.
type Limit struct {
	N      int
	Period time.Duration
}

// Enabled cho biết giới hạn có được bật không.
func (l Limit) Enabled() bool { return l.N > 0 }

// interval là khoảng thời gian để có thêm một token.
func (l Limit) interval() time.Duration { return l.Period / time.Duration(l.N) }

// minInterval là interval nhỏ nhất ParseLimit nhận: Redis tính bằng µs, interval quá nhỏ
// sẽ thành 0 và script lỗi ở mọi request.
const minInterval = time.Millisecond

// ParseLimit đọc giới hạn dạng "10/1m" (10 request mỗi phút) hoặc "off".
func ParseLimit(s string) (Limit, error) {
	if s == "off" {
		return Limit{}, nil
	}
	ns, ps, ok := strings.Cut(s, "/")
	n, err1 := strconv.Atoi(ns)
	p, err2 := time.ParseDuration(ps)
	if !ok || err1 != nil || err2 != nil || n < 1 || p <= 0 {
		return Limit{}, fmt.Errorf("%q: cần dạng số-request/thời-gian như 10/1m, hoặc off", s)
	}
	l := Limit{N: n, Period: p}
	if l.interval() < minInterval {
		return Limit{}, fmt.Errorf("%q: tối đa 1 request mỗi %v (1000 request/giây)", s, minInterval)
	}
	return l, nil
}

// String trả về giới hạn ở dạng ParseLimit đọc được, VD "10/1m".
func (l Limit) String() string {
	if !l.Enabled() {
		return "off"
	}
	p := l.Period.String() // "1m0s" → "1m", "1h0m0s" → "1h"
	if strings.HasSuffix(p, "m0s") {
		p = strings.TrimSuffix(p, "0s")
	}
	if strings.HasSuffix(p, "h0m") {
		p = strings.TrimSuffix(p, "0m")
	}
	return strconv.Itoa(l.N) + "/" + p
}

// MarshalText và UnmarshalText cho Limit dùng được với flag.TextVar.
func (l Limit) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

func (l *Limit) UnmarshalText(b []byte) error {
	v, err := ParseLimit(string(b))
	if err != nil {
		return err
	}
	*l = v
	return nil
}

// Result là kết quả của một lần hỏi limiter.
type Result struct {
	Allowed    bool          // request được đi tiếp
	Remaining  int           // số request còn được đi ngay sau request này
	RetryAfter time.Duration // bị chặn: chờ chừng này thì có token
	// Window là thời gian tới khi bucket có thêm một token: trong khoảng này client chỉ đi được
	// tối đa Remaining request nữa. Bị chặn thì Window = RetryAfter.
	Window time.Duration
}

// Limiter lấy một token của key theo giới hạn l. err chỉ khác nil khi không hỏi được
// (VD Redis chết), lúc đó nơi gọi tự quyết cho qua hay chặn.
type Limiter interface {
	Allow(ctx context.Context, key string, l Limit) (Result, error)
}

var errDisabled = errors.New("ratelimit: giới hạn đang tắt (N = 0)")
