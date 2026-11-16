package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/toidicode/taskflow/internal/reqlog"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode) // không in route và cảnh báo của chế độ debug ra output của go test
	m.Run()
}

// captureLog thay logger mặc định bằng logger JSON ghi vào buffer cho tới hết test.
// Test dùng captureLog không được chạy song song vì logger mặc định là biến dùng chung.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// logEntries đọc từng dòng log JSON trong buf thành map.
func logEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	dec := json.NewDecoder(buf)
	for dec.More() {
		var e map[string]any
		if err := dec.Decode(&e); err != nil {
			t.Fatalf("log không phải JSON: %v", err)
		}
		entries = append(entries, e)
	}
	return entries
}

// withMiddleware tạo engine trả lời mọi method, mọi path bằng h, đi qua các middleware mws.
func withMiddleware(h gin.HandlerFunc, mws ...gin.HandlerFunc) *gin.Engine {
	r := newEngine(nil)
	r.Use(mws...)
	r.Any("/*path", h)
	return r
}

func TestMiddlewareOrder(t *testing.T) {
	var steps []string
	trace := func(name string) gin.HandlerFunc {
		return func(c *gin.Context) {
			steps = append(steps, name+">")
			c.Next() // chạy phần còn lại của chuỗi rồi mới quay lại đây
			steps = append(steps, "<"+name)
		}
	}
	h := func(c *gin.Context) { steps = append(steps, "handler") }

	send(withMiddleware(h, trace("a"), trace("b")), "GET", "/", "")

	if got, want := strings.Join(steps, " "), "a> b> handler <b <a"; got != want {
		t.Errorf("thứ tự = %q, muốn %q", got, want)
	}
}

func TestRequestID(t *testing.T) {
	var fromCtx string
	h := withMiddleware(func(c *gin.Context) {
		fromCtx = reqlog.ID(c.Request.Context()) // service đọc được cùng ID
	}, requestID)

	tests := []struct {
		name   string
		header string
		keep   bool // giữ nguyên ID client gửi lên
	}{
		{"không gửi", "", false},
		{"hợp lệ", "abc-123_XYZ", true},
		{"ký tự lạ", "abc 123", false},
		{"quá dài", strings.Repeat("a", 65), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.header != "" {
				req.Header.Set("X-Request-ID", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get("X-Request-ID")
			if got != fromCtx {
				t.Errorf("header = %q, context = %q, muốn bằng nhau", got, fromCtx)
			}
			if tt.keep && got != tt.header {
				t.Errorf("ID = %q, muốn giữ %q", got, tt.header)
			}
			if !tt.keep && (got == tt.header || len(got) != 26) {
				t.Errorf("ID = %q, muốn ID mới 26 ký tự", got)
			}
		})
	}
}

func TestLogRequests(t *testing.T) {
	logs := captureLog(t)

	tests := []struct {
		name      string
		handler   gin.HandlerFunc
		wantLevel string
		status    float64 // số trong JSON giải mã vào any thành float64
		size      float64
	}{
		{"WriteHeader rồi Write", func(c *gin.Context) {
			c.Writer.WriteHeader(http.StatusCreated)
			fmt.Fprint(c.Writer, "hello")
		}, "INFO", 201, 5},
		{"chỉ Write", func(c *gin.Context) {
			fmt.Fprint(c.Writer, "ok")
		}, "INFO", 200, 2},
		{"không ghi gì", func(c *gin.Context) {}, "INFO", 200, 0},
		{"lỗi 404", func(c *gin.Context) {
			errorJSON(c, http.StatusNotFound, "x")
		}, "WARN", 404, 14},
		{"lỗi 500", func(c *gin.Context) {
			c.Status(http.StatusInternalServerError)
		}, "ERROR", 500, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs.Reset()
			send(withMiddleware(tt.handler, logRequests), "POST", "/x?a=1", "")

			entries := logEntries(t, logs)
			if len(entries) != 1 {
				t.Fatalf("có %d dòng log, muốn 1", len(entries))
			}
			e := entries[0]
			want := map[string]any{"level": tt.wantLevel, "msg": "request", "request_id": "-",
				"method": "POST", "path": "/x?a=1", "status": tt.status, "size": tt.size,
				"client_ip": "192.0.2.1"} // RemoteAddr mặc định của httptest.NewRequest
			for k, v := range want {
				if e[k] != v {
					t.Errorf("%s = %v, muốn %v", k, e[k], v)
				}
			}
			if _, ok := e["duration"].(float64); !ok {
				t.Errorf("duration = %v, muốn một số (nano giây)", e["duration"])
			}
		})
	}
}

// TestResponseControllerUnwrap: http.ResponseController tìm được ResponseWriter thật qua c.Writer
// của Gin, nên extendDeadlines (Bài 35) và Flush vẫn dùng được trong handler Gin.
func TestResponseControllerUnwrap(t *testing.T) {
	captureLog(t)
	var flushErr error
	h := withMiddleware(func(c *gin.Context) {
		fmt.Fprint(c.Writer, "dữ liệu đầu tiên")
		flushErr = http.NewResponseController(c.Writer).Flush()
	}, logRequests)

	rec := send(h, "GET", "/", "")
	if flushErr != nil || !rec.Flushed {
		t.Errorf("Flush qua c.Writer: err = %v, Flushed = %v", flushErr, rec.Flushed)
	}
}

func TestRecoverPanic(t *testing.T) {
	logs := captureLog(t)
	boom := func(c *gin.Context) {
		panic("toang")
	}

	rec := send(withMiddleware(boom, requestID, logRequests, recoverPanic), "GET", "/boom", "")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, muốn 500", rec.Code)
	}
	if want := `{"error":"lỗi máy chủ"}` + "\n"; rec.Body.String() != want {
		t.Errorf("body = %q, muốn %q", rec.Body.String(), want)
	}
	if got := rec.Header().Get("Connection"); got != "close" {
		t.Errorf("Connection = %q, muốn close", got)
	}
	id := rec.Header().Get("X-Request-ID")
	entries := logEntries(t, logs)
	if len(entries) != 2 {
		t.Fatalf("có %d dòng log, muốn 2 (panic và request)", len(entries))
	}
	p, req := entries[0], entries[1]
	if p["msg"] != "panic" || p["err"] != "toang" || p["request_id"] != id || p["level"] != "ERROR" {
		t.Errorf("log panic = %v", p)
	}
	if stack, _ := p["stack"].(string); !strings.Contains(stack, "goroutine ") {
		t.Errorf("log panic thiếu stack trace: %q", stack)
	}
	if req["msg"] != "request" || req["status"] != float64(500) || req["request_id"] != id {
		t.Errorf("log request = %v", req)
	}
}

func TestRecoverPanicAbortHandler(t *testing.T) {
	h := withMiddleware(func(c *gin.Context) {
		panic(http.ErrAbortHandler)
	}, recoverPanic)
	defer func() {
		if err := recover(); err != http.ErrAbortHandler {
			t.Errorf("recover() = %v, muốn http.ErrAbortHandler được panic tiếp", err)
		}
	}()
	send(h, "GET", "/", "")
}

func TestCORS(t *testing.T) {
	const origin = "http://localhost:5173"
	called := false
	h := withMiddleware(func(c *gin.Context) {
		called = true
	}, cors(origin))

	tests := []struct {
		name       string
		method     string
		origin     string
		preflight  bool // gửi kèm Access-Control-Request-Method
		wantCode   int
		wantCalled bool
		wantAllow  string // Access-Control-Allow-Origin
	}{
		{"preflight đúng origin", "OPTIONS", origin, true, 204, false, origin},
		{"preflight origin khác", "OPTIONS", "http://evil.example", true, 200, true, ""},
		{"GET đúng origin", "GET", origin, false, 200, true, origin},
		{"GET không có Origin", "GET", "", false, 200, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(tt.method, "/api/v1/tasks", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.preflight {
				req.Header.Set("Access-Control-Request-Method", "PATCH")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode || called != tt.wantCalled {
				t.Errorf("status = %d, gọi handler = %v; muốn %d, %v", rec.Code, called, tt.wantCode, tt.wantCalled)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.wantAllow {
				t.Errorf("Allow-Origin = %q, muốn %q", got, tt.wantAllow)
			}
			if got := rec.Header().Get("Vary"); got != "Origin" {
				t.Errorf("Vary = %q, muốn Origin", got)
			}
		})
	}
}

func TestNewAPI(t *testing.T) {
	logs := captureLog(t)
	api := newAPI(newTestStores(t), testTokens, "http://localhost:5173", nil)

	rec := send(api, "GET", "/api/v1/tasks/2", "")
	id := rec.Header().Get("X-Request-ID")
	if rec.Code != 200 || id == "" {
		t.Errorf("status = %d, X-Request-ID = %q", rec.Code, id)
	}
	entries := logEntries(t, logs)
	if len(entries) != 1 || entries[0]["request_id"] != id || entries[0]["path"] != "/api/v1/tasks/2" {
		t.Errorf("log = %v, muốn một dòng có request_id %q", entries, id)
	}

	// không có route OPTIONS, nhưng preflight đã được cors trả lời (middleware dùng chung
	// của Gin chạy cả với request không khớp route nào)
	req := httptest.NewRequest("OPTIONS", "/api/v1/tasks/2", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight: status = %d, muốn 204", rec.Code)
	}
	// trình duyệt chỉ gửi header Authorization khi preflight cho phép
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Errorf("Allow-Headers = %q, thiếu Authorization", got)
	}

	// route không có và sai method cũng đi qua middleware: có X-Request-ID và được ghi log
	for _, tt := range []struct {
		method, target string
		want           int
	}{{"GET", "/api/v1/khong-co", 404}, {"PUT", "/api/v1/tasks", 405}} {
		logs.Reset()
		rec := send(api, tt.method, tt.target, "")
		e := logEntries(t, logs)
		if rec.Code != tt.want || rec.Header().Get("X-Request-ID") == "" || len(e) != 1 || e[0]["status"] != float64(tt.want) {
			t.Errorf("%s %s: %d, X-Request-ID %q, log %v", tt.method, tt.target, rec.Code, rec.Header().Get("X-Request-ID"), e)
		}
	}
}
