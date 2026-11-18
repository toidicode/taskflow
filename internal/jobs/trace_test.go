package jobs_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/toidicode/taskflow/internal/jobs"
)

var (
	spansOnce sync.Once
	spans     *tracetest.SpanRecorder
	testTP    *sdktrace.TracerProvider
)

// recordSpans đặt (một lần cho cả package) tracer provider toàn cục ghi span vào bộ nhớ
// và propagator W3C, như telemetry.Setup nhưng không gửi đi đâu.
func recordSpans() (*tracetest.SpanRecorder, trace.Tracer) {
	spansOnce.Do(func() {
		spans = tracetest.NewSpanRecorder()
		testTP = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
		otel.SetTracerProvider(testTP)
		otel.SetTextMapPropagator(propagation.TraceContext{})
	})
	return spans, testTP.Tracer("test")
}

// TestTraceContext: job chạy trong span con của span "send", cùng trace với nơi gọi Enqueue,
// với cả hàng đợi trong bộ nhớ và hàng đợi Redis (trace context đi qua header của task asynq).
func TestTraceContext(t *testing.T) {
	queues := map[string]func(t *testing.T) jobs.Queue{
		"local": func(t *testing.T) jobs.Queue {
			return jobs.NewLocal(jobs.Options{Workers: 1, Logger: slog.New(slog.DiscardHandler)})
		},
		"redis": func(t *testing.T) jobs.Queue { q, _, _ := newRedisQueue(t); return q },
	}
	for name, open := range queues {
		t.Run(name, func(t *testing.T) {
			rec, tracer := recordSpans()
			q := open(t)
			got := make(chan trace.SpanContext, 1)
			q.Handle(jobs.Handler{Type: "test:trace", Run: func(ctx context.Context, _ []byte) error {
				got <- trace.SpanContextFromContext(ctx)
				return nil
			}})
			if err := q.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { q.Stop(context.Background()) })

			ctx, parent := tracer.Start(t.Context(), "request")
			if err := q.Enqueue(ctx, "test:trace", nil); err != nil {
				t.Fatal(err)
			}
			parent.End()
			sc := <-got
			if sc.TraceID() != parent.SpanContext().TraceID() {
				t.Fatalf("job ở trace %s, muốn %s", sc.TraceID(), parent.SpanContext().TraceID())
			}
			byName := map[string]sdktrace.ReadOnlySpan{}
			waitFor(t, "span process kết thúc", func() bool { // span process kết thúc ngay sau khi Run trả về
				for _, s := range rec.Ended() {
					if s.SpanContext().TraceID() == sc.TraceID() {
						byName[s.Name()] = s
					}
				}
				return byName["process test:trace"] != nil
			})
			send, process := byName["send test:trace"], byName["process test:trace"]
			if send == nil {
				t.Fatalf("thiếu span send, có %v", byName)
			}
			if send.Parent().SpanID() != parent.SpanContext().SpanID() || process.Parent().SpanID() != send.SpanContext().SpanID() {
				t.Error("muốn request → send → process")
			}
			if process.SpanKind() != trace.SpanKindConsumer || process.SpanContext().SpanID() != sc.SpanID() {
				t.Error("Run phải chạy trong span process (consumer)")
			}
		})
	}
}

func TestRedisStats(t *testing.T) {
	q, _, _ := newRedisQueue(t)
	q.Handle(jobs.Handler{Type: "test:stats", Queue: jobs.QueueLow, Run: func(context.Context, []byte) error { return nil }})
	if st, err := q.Stats(); err != nil || len(st) != 0 {
		t.Fatalf("Stats() = %v, %v; muốn rỗng khi chưa có job", st, err)
	}
	for range 2 { // chưa Start: job nằm chờ trong queue
		if err := q.Enqueue(t.Context(), "test:stats", nil); err != nil {
			t.Fatal(err)
		}
	}
	st, err := q.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 1 || st[0].Queue != jobs.QueueLow || st[0].Pending != 2 {
		t.Errorf("Stats() = %+v, muốn low với 2 job pending", st)
	}
}
