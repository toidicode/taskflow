package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// Readiness trả lời /ready: server có nên nhận request mới không.
type Readiness struct {
	db       *sql.DB     // nil khi dùng store trong bộ nhớ
	stopping atomic.Bool // true từ lúc bắt đầu tắt server
}

func (rd *Readiness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if rd.stopping.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "đang tắt")
		return
	}
	if rd.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := rd.db.PingContext(ctx); err != nil {
			requestLogger(r).Warn("ready: database không phản hồi", "err", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "database không phản hồi")
			return
		}
	}
	fmt.Fprintln(w, "ok")
}

// NewReadiness tạo Readiness hỏi db (nil khi dùng store trong bộ nhớ).
func NewReadiness(db *sql.DB) *Readiness {
	return &Readiness{db: db}
}

// SetStopping cho /ready trả 503 từ giờ: server đang tắt.
func (rd *Readiness) SetStopping() {
	rd.stopping.Store(true)
}
