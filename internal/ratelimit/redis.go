package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// gcra là token bucket viết theo GCRA (Generic Cell Rate Algorithm): mỗi key chỉ lưu một số,
// thời điểm bucket đầy lại (tat, tính bằng µs). Cả script chạy nguyên khối trong Redis nên hai bản
// API không thể cùng đọc rồi ghi đè lên nhau. Thời gian lấy bằng TIME của Redis: đồng hồ
// các máy chạy API lệch nhau cũng không sao.
var gcra = redis.NewScript(`
local interval = tonumber(ARGV[1]) -- thời gian để có thêm một token (µs)
local burst = tonumber(ARGV[2])    -- số token tối đa
local t = redis.call('TIME')
local now = t[1] * 1000000 + t[2]
local tat = tonumber(redis.call('GET', KEYS[1]) or now)
if tat < now then tat = now end
local new_tat = tat + interval
local allow_at = new_tat - burst * interval
if now < allow_at then
  return {0, 0, allow_at - now} -- hết token, không ghi gì
end
redis.call('SET', KEYS[1], new_tat, 'PX', math.ceil((new_tat - now) / 1000))
local x = now - allow_at -- phần dư: x / interval token còn lại
return {1, math.floor(x / interval), interval - x % interval}
`)

// Redis giữ bucket của mọi key trong Redis để nhiều bản API dùng chung một giới hạn.
type Redis struct {
	client redis.Scripter
	prefix string
}

// NewRedis tạo limiter dùng client; mọi key được thêm prefix, VD "taskflow:ratelimit:".
func NewRedis(client redis.Scripter, prefix string) *Redis {
	return &Redis{client: client, prefix: prefix}
}

// Allow chạy script gcra cho key. Lỗi kết nối, timeout được trả về cho nơi gọi.
func (r *Redis) Allow(ctx context.Context, key string, l Limit) (Result, error) {
	if !l.Enabled() {
		return Result{}, errDisabled
	}
	v, err := gcra.Run(ctx, r.client, []string{r.prefix + key}, l.interval().Microseconds(), l.N).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit redis: %w", err)
	}
	res := Result{Allowed: v[0] == 1, Remaining: int(v[1]), Window: time.Duration(v[2]) * time.Microsecond}
	if !res.Allowed {
		res.RetryAfter = res.Window
	}
	return res, nil
}
