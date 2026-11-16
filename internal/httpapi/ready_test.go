package httpapi

import (
	"net/http"
	"testing"

	"github.com/toidicode/taskflow/internal/testdb"
)

func TestReady(t *testing.T) {
	captureLog(t)
	backends := []struct {
		name string
		open func(t *testing.T) stores
	}{
		{"memory", func(t *testing.T) stores { return memoryStores(newTestFiles(t)) }},
		{"postgres", func(t *testing.T) stores { return dbStores(testdb.New(t), newTestFiles(t)) }},
	}
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			s := b.open(t)
			mux := newMux(s, testTokens)
			check := func(wantCode int, wantBody string) {
				t.Helper()
				rec := sendAs(mux, 0, "GET", "/ready", "")
				if rec.Code != wantCode || rec.Body.String() != wantBody {
					t.Errorf("/ready = %d %q, muốn %d %q", rec.Code, rec.Body, wantCode, wantBody)
				}
			}
			check(http.StatusOK, "ok\n")
			if s.db != nil {
				s.db.Close() // mất database: không nhận request nữa, nhưng /health vẫn ok
				check(http.StatusServiceUnavailable, "database không phản hồi\n")
				if rec := sendAs(mux, 0, "GET", "/health", ""); rec.Code != http.StatusOK {
					t.Errorf("/health = %d, muốn 200", rec.Code)
				}
			}
			s.ready.stopping.Store(true)
			check(http.StatusServiceUnavailable, "đang tắt\n")
		})
	}
}
