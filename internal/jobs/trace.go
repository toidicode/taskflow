package jobs

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// tracerName là tên tracer của package, hiện trong thuộc tính otel.scope.name của span.
const tracerName = "github.com/toidicode/taskflow/internal/jobs"

// startSend mở span "send <typ>" (producer) cho một lần Enqueue và trả về trace context của span đó
// dạng map (traceparent, tracestate, baggage) để đi kèm job. Job không mang ctx, nên trace
// phải được ghi vào job như request ID ở Bài 50, nhưng trong header của job thay vì payload.
func startSend(ctx context.Context, typ string) (context.Context, trace.Span, map[string]string) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "send "+typ,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attribute.String("messaging.destination.name", typ)))
	headers := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, headers)
	return ctx, span, headers
}

// endSpan ghi lỗi (nếu có) vào span rồi kết thúc nó.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// run chạy h trong span "process <type>" (consumer), con của span "send" đọc lại từ headers:
// trên Jaeger, job nằm chung một trace với request đã đưa nó vào hàng đợi.
func run(ctx context.Context, h Handler, id string, headers map[string]string, payload []byte) error {
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(headers))
	ctx, span := otel.Tracer(tracerName).Start(ctx, "process "+h.Type,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.message.id", id)))
	err := h.Run(withJobID(ctx, id), payload)
	endSpan(span, err)
	return err
}
