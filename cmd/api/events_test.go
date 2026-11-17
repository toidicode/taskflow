package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// ticketBody là body của POST /api/v1/realtime/tickets.
type ticketBody struct {
	Ticket string `json:"ticket"`
}

// newTicket lấy vé WebSocket bằng access token của c.
func newTicket(c *client) string {
	c.t.Helper()
	return call[ticketBody](c, "POST", "/api/v1/realtime/tickets", nil, http.StatusCreated).Ticket
}

// dialEvents lấy vé rồi mở WebSocket nhận event của project projectID qua client của server
// (đi qua contract), như trình duyệt: new WebSocket(url + "?ticket=" + vé). Trả về kết nối (nil nếu
// không mở được) và status code của handshake (101 khi thành công, 0 khi không có response).
func dialEvents(c *client, projectID int, header http.Header) (*websocket.Conn, int) {
	c.t.Helper()
	u := fmt.Sprintf("%s/api/v1/projects/%d/events?ticket=%s", c.srv.URL, projectID, newTicket(c))
	conn, resp, err := websocket.Dial(c.t.Context(), u, &websocket.DialOptions{HTTPClient: c.srv.Client(), HTTPHeader: header})
	if resp == nil {
		c.t.Logf("dial %s: %v", u, err)
		return nil, 0
	}
	return conn, resp.StatusCode
}

// checkEvent đọc một message từ conn, kiểm tra nó khớp schema TaskEvent trong openapi.yaml và có
// type là typ, rồi trả về message đã giải mã.
func checkEvent(t *testing.T, conn *websocket.Conn, typ string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("đọc event: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("event không phải JSON: %s", raw)
	}
	doc, _ := loadSpec(t)
	if err := doc.Components.Schemas["TaskEvent"].Value.VisitJSON(msg); err != nil {
		t.Errorf("event %s không khớp TaskEvent: %v", raw, err)
	}
	if msg["type"] != typ {
		t.Errorf("event %s, muốn type %s", raw, typ)
	}
	return msg
}

// track đếm các request đang chạy trong h. httptest.Server.Close không chờ kết nối đã bị hijack
// (WebSocket): test chờ bằng Wait trước khi đọc log mà handler còn đang ghi.
func track(h http.Handler) (http.Handler, *sync.WaitGroup) {
	var wg sync.WaitGroup
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		h.ServeHTTP(w, r)
	}), &wg
}
