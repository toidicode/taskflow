package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/realtime"
)

// Realtime là những gì route WebSocket cần.
type Realtime struct {
	Hub          *realtime.Hub
	Tickets      realtime.Tickets
	PingInterval time.Duration // mặc định 30s
	PingTimeout  time.Duration // chờ pong tối đa, mặc định 10s

	origins []string // origin của frontend được mở WebSocket, New lấy từ corsOrigin
}

// writeTimeout là thời gian tối đa để gửi một message; quá thì coi như client đã chết.
const writeTimeout = 10 * time.Second

// ticketResponse là body của POST /api/v1/realtime/tickets.
type ticketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"` // giây
}

// issueTicket phát vé mở WebSocket cho user đang đăng nhập (route này cần access token như mọi route khác).
func issueTicket(tickets realtime.Tickets) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, err := tickets.Issue(c.Request.Context(), userIDFrom(c))
		if err != nil {
			writeError(c, err)
			return
		}
		writeJSON(c, http.StatusCreated, ticketResponse{Ticket: t, ExpiresIn: int(realtime.TicketTTL / time.Second)})
	}
}

// requireTicket thay requireAuth trên route WebSocket: đổi ?ticket= lấy user ID. Vé chỉ dùng được
// một lần, kể cả khi request sau đó bị từ chối.
func requireTicket(tickets realtime.Tickets) gin.HandlerFunc {
	return func(c *gin.Context) {
		ticket := c.Query("ticket")
		if ticket == "" {
			unauthorized(c, false, "cần vé kết nối: lấy bằng POST /api/v1/realtime/tickets")
			return
		}
		id, err := tickets.Redeem(c.Request.Context(), ticket)
		switch {
		case errors.Is(err, realtime.ErrInvalidTicket):
			unauthorized(c, true, err.Error())
			return
		case err != nil:
			writeError(c, err)
			return
		}
		c.Set(userIDKey, id)
		c.Next()
	}
}

// projectEvents mở WebSocket gửi event của các task trong project :id. Chỉ người xem được project
// (chủ project, Bài 34) mới kết nối được; người khác nhận 404 như mọi route khác.
func projectEvents(projects *project.Service, rt Realtime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		me := userIDFrom(c)
		if _, err := projects.Get(c.Request.Context(), me, id); err != nil {
			writeError(c, err) // 404: không có project hoặc không được xem
			return
		}
		if !c.IsWebsocket() {
			c.Header("Upgrade", "websocket")
			errorJSON(c, http.StatusUpgradeRequired, "cần mở bằng WebSocket")
			return
		}

		sub, err := rt.Hub.Subscribe(id, me) // đăng ký trước khi upgrade: còn trả lời được bằng HTTP
		switch {
		case errors.Is(err, realtime.ErrTooManyConnections):
			c.Header("Retry-After", "30")
			errorJSON(c, http.StatusTooManyRequests, "quá nhiều kết nối realtime, hãy đóng bớt tab")
			return
		case err != nil:
			errorJSON(c, http.StatusServiceUnavailable, "server đang tắt")
			return
		}
		defer sub.Close()

		// Accept kiểm tra Origin: chỉ cùng host hoặc frontend trong origins. Sai thì nó tự trả 403.
		conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{OriginPatterns: rt.origins})
		if err != nil {
			requestLogger(c).Warn("không mở được WebSocket", "err", err)
			return
		}
		defer conn.CloseNow()

		// sau Accept không dùng context của request nữa (kết nối đã bị hijack), chỉ giữ giá trị (request ID)
		ctx := context.WithoutCancel(c.Request.Context())
		rt.stream(ctx, conn, sub)
	}
}

// stream gửi message của sub tới conn và ping định kỳ cho tới khi một bên kết thúc.
func (rt Realtime) stream(ctx context.Context, conn *websocket.Conn, sub *realtime.Subscription) {
	// Client không gửi gì lên: CloseRead đọc trong goroutine riêng để xử lý pong và close frame,
	// client gửi message thì đóng kết nối. ctx bị hủy khi kết nối đóng.
	ctx = conn.CloseRead(ctx)
	ping := time.NewTicker(orDefault(rt.PingInterval, 30*time.Second))
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done(): // client đóng hoặc mất kết nối
			return
		case <-sub.Done():
			if errors.Is(sub.Err(), realtime.ErrSlowConsumer) {
				conn.Close(websocket.StatusPolicyViolation, "client đọc quá chậm")
			} else {
				conn.Close(websocket.StatusGoingAway, "server đang tắt")
			}
			return
		case msg := <-sub.Messages():
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C: // không nhận được pong kịp: kết nối đã chết (mạng rớt, máy ngủ) dù TCP chưa biết
			pctx, cancel := context.WithTimeout(ctx, orDefault(rt.PingTimeout, 10*time.Second))
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// orDefault trả về d, hoặc def khi d bằng 0.
func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
