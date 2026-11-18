package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	notifyv1 "github.com/toidicode/taskflow/internal/gen/notify/v1"
	"github.com/toidicode/taskflow/internal/jobs"
	"github.com/toidicode/taskflow/internal/metrics"
	"github.com/toidicode/taskflow/internal/notify"
	"github.com/toidicode/taskflow/internal/notifyrpc"
	"github.com/toidicode/taskflow/internal/project"
	"github.com/toidicode/taskflow/internal/task"
	"github.com/toidicode/taskflow/internal/testdb"
	"github.com/toidicode/taskflow/internal/testredis"
)

var (
	spansOnce sync.Once
	spans     *tracetest.SpanRecorder
)

// recordSpans đặt tracer provider toàn cục ghi span vào bộ nhớ (thay cho exporter OTLP) và
// propagator W3C, một lần cho cả package: otelgin, otelgrpc giữ tracer lấy lúc được tạo.
func recordSpans() *tracetest.SpanRecorder {
	spansOnce.Do(func() {
		spans = tracetest.NewSpanRecorder()
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)))
		otel.SetTextMapPropagator(propagation.TraceContext{})
	})
	return spans
}

const testNotifierToken = "token-notifier-chi-dung-trong-test-0123"

// startNotifier chạy notifier gRPC thật (ServerOptions: tracing, log, auth) trên bufconn và trả về
// notify.Notifier gọi nó qua notifyrpc.Dial, như TaskFlow API khi có TASKFLOW_NOTIFIER_ADDR.
func startNotifier(t *testing.T) notify.Notifier {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	logger := slog.New(slog.DiscardHandler)
	s := grpc.NewServer(notifyrpc.ServerOptions(logger, testNotifierToken)...)
	srv := notifyrpc.NewServer(notify.Log{Logger: logger}, logger)
	notifyv1.RegisterNotificationServiceServer(s, srv)
	go s.Serve(lis)
	t.Cleanup(func() { srv.Close(); s.Stop() })
	conn, err := notifyrpc.Dial("passthrough:///bufnet", testNotifierToken,
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return notifyrpc.NewNotifier(conn)
}

// TestTracePropagation: một PATCH chuyển task sang done tạo một trace duy nhất đi qua
// HTTP → hàng đợi job → gRPC tới notifier, và dòng log của request có trace_id của trace đó.
func TestTracePropagation(t *testing.T) {
	if testing.Short() {
		t.Skip("đăng ký user dùng bcrypt, bỏ qua khi chạy go test -short")
	}
	rec := recordSpans()
	backends := []struct {
		name string
		open func(t *testing.T) *app
	}{
		{"memory", func(t *testing.T) *app { return memoryApp(newTestFiles(t)) }},
		{"postgres+redis", func(t *testing.T) *app { // truy vấn SQL có span, job đi qua header của task asynq
			_, prefix := testredis.New(t)
			a := dbApp(testdb.New(t), newTestFiles(t))
			q, err := jobs.NewRedis(testredis.URL(), jobs.RedisOptions{Concurrency: 1, QueuePrefix: prefix})
			if err != nil {
				t.Fatal(err)
			}
			a.queue = q
			return a
		}},
	}
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(newLogger(&logs, Config{LogFormat: "json", LogLevel: slog.LevelInfo}))
			t.Cleanup(func() { slog.SetDefault(old) })

			a := b.open(t)
			if a.queue == nil {
				a.queue = jobs.NewLocal(jobs.Options{Workers: 1})
			}
			a.metrics = metrics.New()
			a.notifier = startNotifier(t)
			if err := a.startJobs(0, "off"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { a.queue.Stop(context.Background()) })
			a.rateLimit = rateLimit(nil, e2eLimits)
			srv := httptest.NewServer(a.handler(testTokens, "", nil))
			t.Cleanup(srv.Close)

			an := signUp(t, srv, "An", "an@example.com", "mat-khau-cua-an")
			binh := signUp(t, srv, "Bình", "binh@example.com", "mat-khau-cua-binh")
			p := call[project.Project](an, "POST", "/api/v1/projects", map[string]any{"name": "Trace"}, http.StatusCreated)
			tk := call[task.Task](an, "POST", "/api/v1/tasks",
				map[string]any{"project_id": p.ID, "title": "Theo dõi trace", "assignee_id": 2}, http.StatusCreated)

			// client gửi sẵn traceparent (như một service khác, hay trình duyệt có OpenTelemetry):
			// API nối tiếp trace đó thay vì mở trace mới
			traceID, parentID := randomHex(16), randomHex(8)
			req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/api/v1/tasks/%d", srv.URL, tk.ID), strings.NewReader(`{"status":"done"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+binh.token)
			req.Header.Set("traceparent", "00-"+traceID+"-"+parentID+"-01")
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("PATCH: %d", resp.StatusCode)
			}

			// chờ các chặng cuối của trace (RPC tới notifier và job chứa nó)
			byName := map[string]sdktrace.ReadOnlySpan{}
			var sqlSpans []string
			const rpc = "notify.v1.NotificationService/Send"
			waitForSpans(t, func() bool {
				clear(byName)
				sqlSpans = sqlSpans[:0]
				for _, s := range rec.Ended() {
					if s.SpanContext().TraceID().String() != traceID {
						continue
					}
					name := s.Name()
					if name == rpc {
						name += " " + s.SpanKind().String() // client và server cùng tên
					}
					byName[name] = s
					if s.InstrumentationScope().Name == "github.com/exaring/otelpgx" {
						sqlSpans = append(sqlSpans, s.Name())
					}
				}
				// span chỉ vào rec khi kết thúc: server xong trước client và process, phải chờ cả ba
				return byName[rpc+" server"] != nil && byName[rpc+" client"] != nil &&
					byName["process notify:task_done:v1"] != nil
			})

			// mỗi chặng là con của chặng trước
			chain := []string{"PATCH /api/v1/tasks/:id", "send notify:task_done:v1", "process notify:task_done:v1",
				rpc + " client", rpc + " server"}
			parent := parentID
			for _, name := range chain {
				s := byName[name]
				if s == nil {
					t.Fatalf("thiếu span %q trong trace, có %v", name, slices.Collect(maps.Keys(byName)))
				}
				if got := s.Parent().SpanID().String(); got != parent {
					t.Errorf("span %q có cha %s, muốn %s", name, got, parent)
				}
				parent = s.SpanContext().SpanID().String()
			}
			if b.name == "postgres+redis" && !slices.Contains(sqlSpans, "UpdateTask") {
				t.Errorf("span SQL: %v, muốn có UpdateTask (tên query sqlc)", sqlSpans)
			}

			// dòng log của request có trace_id: từ log tìm được trace
			httpSpan := byName["PATCH /api/v1/tasks/:id"].SpanContext()
			found := false
			for _, e := range logEntries(t, &logs) {
				if e["msg"] == "request" && e["method"] == "PATCH" {
					found = e["trace_id"] == traceID && e["span_id"] == httpSpan.SpanID().String()
				}
			}
			if !found {
				t.Error("log của PATCH không có trace_id, span_id của span HTTP")
			}
		})
	}
}

// TestNoTraceForProbes: /health và /ready không tạo span (Docker hỏi vài giây một lần).
func TestNoTraceForProbes(t *testing.T) {
	rec := recordSpans()
	a := newTestApp(t)
	h := a.handler(testTokens, "", nil)
	traceID := randomHex(16)
	for _, path := range []string{"/health", "/ready"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("traceparent", "00-"+traceID+"-"+randomHex(8)+"-01")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	for _, s := range rec.Ended() {
		if s.SpanContext().TraceID().String() == traceID {
			t.Errorf("có span %q cho probe", s.Name())
		}
	}
}

// TestServeMetrics: /metrics và pprof nằm trên cổng riêng, không có trên cổng của API.
func TestServeMetrics(t *testing.T) {
	m := metrics.New()
	stop, err := serveMetrics("127.0.0.1:0", m.Handler(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	a := newTestApp(t)
	a.metrics = m
	srv := httptest.NewServer(a.handler(testTokens, "", nil))
	defer srv.Close()

	for _, path := range []string{"/health", "/api/v1/tasks/1", "/metrics", "/debug/pprof/"} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if (path == "/metrics" || path == "/debug/pprof/") && resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s trên cổng API: %d, muốn 404", path, resp.StatusCode)
		}
	}
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`taskflow_http_requests_total{method="GET",route="/health",status="200"} 1`,
		`taskflow_http_requests_total{method="GET",route="/api/v1/tasks/:id",status="401"} 1`,
		`taskflow_http_requests_total{method="GET",route="unmatched",status="404"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics thiếu %s", want)
		}
	}
}

func waitForSpans(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("chưa thấy span của notifier sau 10 giây")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
