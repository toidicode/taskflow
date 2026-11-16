package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// sweepEvery là chu kỳ dọn các bucket đã đầy lại.
const sweepEvery = time.Minute

// Memory giữ một token bucket (rate.Limiter) cho mỗi key trong bộ nhớ. Bucket đã đầy lại
// giống hệt bucket mới tạo nên được xóa: map không phình mãi theo số IP từng gửi request.
// Dùng chung được cho nhiều goroutine.
type Memory struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	lim  *rate.Limiter
	full time.Time // thời điểm bucket đầy lại nếu không có request nào nữa
}

// NewMemory tạo limiter trong bộ nhớ.
func NewMemory() *Memory {
	return &Memory{buckets: make(map[string]*bucket), lastSweep: time.Now()}
}

// Allow lấy một token trong bucket của key; hết token thì trả Allowed = false kèm thời gian chờ.
func (m *Memory) Allow(_ context.Context, key string, l Limit) (Result, error) {
	if !l.Enabled() {
		return Result{}, errDisabled
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if now.Sub(m.lastSweep) >= sweepEvery {
		m.sweep(now)
	}

	b, ok := m.buckets[key]
	if !ok { // bucket mới có sẵn N token, cứ interval lại thêm một
		b = &bucket{lim: rate.NewLimiter(rate.Every(l.interval()), l.N)}
		m.buckets[key] = b
	}
	tokens := b.lim.TokensAt(now)
	if tokens < 1 { // hết token: không lấy gì, chỉ tính bao lâu nữa có token kế tiếp
		wait := nextToken(tokens, l)
		return Result{RetryAfter: wait, Window: wait}, nil
	}
	b.lim.AllowN(now, 1)
	tokens--
	b.full = now.Add(time.Duration((float64(l.N) - tokens) * float64(l.interval())))
	return Result{Allowed: true, Remaining: int(tokens), Window: nextToken(tokens, l)}, nil
}

// nextToken là thời gian tới khi bucket đang có tokens (có thể lẻ) có thêm một token nguyên.
func nextToken(tokens float64, l Limit) time.Duration {
	return time.Duration((math.Floor(tokens) + 1 - tokens) * float64(l.interval()))
}

// sweep xóa các bucket đã đầy lại. Chỉ gọi khi đang giữ m.mu.
func (m *Memory) sweep(now time.Time) {
	for key, b := range m.buckets {
		if !now.Before(b.full) {
			delete(m.buckets, key)
		}
	}
	m.lastSweep = now
}

// Len trả về số bucket đang giữ.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.buckets)
}
