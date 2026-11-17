package realtime

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/toidicode/taskflow/internal/task"
)

// Redis chuyển event giữa các bản API qua Redis Pub/Sub: Publish gửi event lên channel,
// Run của mọi bản API (kể cả bản vừa gửi) nhận lại và đưa vào Hub của mình.
// Pub/Sub không lưu message: bản API đang mất kết nối với Redis sẽ lỡ các event trong lúc đó.
type Redis struct {
	client  *redis.Client
	channel string
	hub     *Hub
}

// NewRedis tạo Redis dùng channel trên client, chuyển event nhận được vào hub.
func NewRedis(client *redis.Client, channel string, hub *Hub) *Redis {
	return &Redis{client: client, channel: channel, hub: hub}
}

// Publish gửi e lên channel (cài task.Publisher).
func (r *Redis) Publish(ctx context.Context, e task.Event) error {
	msg, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return r.client.Publish(ctx, r.channel, msg).Err()
}

// Run nhận event từ channel và chuyển vào hub cho tới khi ctx bị hủy. Mất kết nối thì
// go-redis tự kết nối và đăng ký lại.
func (r *Redis) Run(ctx context.Context) {
	sub := r.client.Subscribe(ctx, r.channel)
	defer sub.Close()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok { // sub đã đóng
				return
			}
			var e struct {
				ProjectID int `json:"project_id"`
			}
			if err := json.Unmarshal([]byte(m.Payload), &e); err != nil {
				slog.Warn("realtime: bỏ qua message lạ", "channel", r.channel, "err", err)
				continue
			}
			r.hub.Broadcast(e.ProjectID, []byte(m.Payload)) // gửi nguyên message, không mã hóa lại
		}
	}
}
