package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// captureLog chuyển output của package log vào một buffer cho tới hết test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestChainOrder(t *testing.T) {
	var steps []string
	trace := func(name string) middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				steps = append(steps, name+">")
				next.ServeHTTP(w, r)
				steps = append(steps, "<"+name)
			})
		}
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { steps = append(steps, "handler") })

	send(chain(h, trace("a"), trace("b")), "GET", "/", "")

	if got, want := strings.Join(steps, " "), "a> b> handler <b <a"; got != want {
		t.Errorf("thứ tự = %q, muốn %q", got, want)
	}
}

func TestRequestID(t *testing.T) {
	var fromCtx string
	h := requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fromCtx = requestIDFrom(r.Context())
	}))

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

	if got := requestIDFrom(httptest.NewRequest("GET", "/", nil).Context()); got != "-" {
		t.Errorf("không có ID: %q, muốn \"-\"", got)
	}
}

func TestLogRequests(t *testing.T) {
	logs := captureLog(t)

	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantLog string
	}{
		{"WriteHeader rồi Write", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, "hello")
		}, "POST /x?a=1 201 5B "},
		{"chỉ Write", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "ok")
		}, "POST /x?a=1 200 2B "},
		{"không ghi gì", func(w http.ResponseWriter, r *http.Request) {}, "POST /x?a=1 200 0B "},
		{"lỗi", func(w http.ResponseWriter, r *http.Request) {
			errorJSON(w, http.StatusNotFound, "x")
		}, "POST /x?a=1 404 14B "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs.Reset()
			send(logRequests(tt.handler), "POST", "/x?a=1", "")
			if !strings.Contains(logs.String(), " - "+tt.wantLog) {
				t.Errorf("log = %q, muốn chứa %q", logs.String(), tt.wantLog)
			}
		})
	}
}

func TestStatusRecorderUnwrap(t *testing.T) {
	captureLog(t)
	var flushErr error
	h := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "dữ liệu đầu tiên")
		flushErr = http.NewResponseController(w).Flush() // w là *statusRecorder
	}))

	rec := send(h, "GET", "/", "")
	if flushErr != nil || !rec.Flushed {
		t.Errorf("Flush qua statusRecorder: err = %v, Flushed = %v", flushErr, rec.Flushed)
	}
}

func TestRecoverPanic(t *testing.T) {
	logs := captureLog(t)
	boom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("toang")
	})

	rec := send(chain(boom, requestID, logRequests, recoverPanic), "GET", "/boom", "")

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
	for _, want := range []string{id + " panic: toang\n", "goroutine ", id + " GET /boom 500 "} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log thiếu %q:\n%s", want, logs.String())
		}
	}
}

func TestRecoverPanicAbortHandler(t *testing.T) {
	h := recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
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
	h := cors(origin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

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
	projects, tasks := newTestStores(t)
	api := newAPI(projects, tasks, "http://localhost:5173")

	rec := send(api, "GET", "/api/v1/tasks/2", "")
	id := rec.Header().Get("X-Request-ID")
	if rec.Code != 200 || id == "" {
		t.Errorf("status = %d, X-Request-ID = %q", rec.Code, id)
	}
	if want := id + " GET /api/v1/tasks/2 200 "; !strings.Contains(logs.String(), want) {
		t.Errorf("log = %q, muốn chứa %q", logs.String(), want)
	}

	// mux trả 405 cho OPTIONS, nhưng preflight đã được cors trả lời trước khi tới mux
	req := httptest.NewRequest("OPTIONS", "/api/v1/tasks/2", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight: status = %d, muốn 204", rec.Code)
	}
}
