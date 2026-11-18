// Package telemetry cấu hình OpenTelemetry tracing cho các service của TaskFlow (API, notifier)
// bằng các biến môi trường chuẩn OTEL_*, và nối log slog với trace (LogHandler).
package telemetry

import (
	"context"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Setup bật tracing cho service khi có nơi nhận trace: OTEL_EXPORTER_OTLP_ENDPOINT hoặc
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT. Không có thì tracer provider toàn cục vẫn là bản no-op:
// mọi span đều rỗng, gần như không tốn gì, test chạy không cần mạng.
// shutdown gửi nốt các span còn trong bộ đệm, gọi khi tắt service.
func Setup(ctx context.Context, service, version string) (shutdown func(context.Context) error, err error) {
	// W3C Trace Context (header traceparent) + Baggage: cách trace đi qua HTTP, gRPC, job
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	noop := func(context.Context) error { return nil }
	if !Enabled(os.LookupEnv) {
		return noop, nil
	}

	exp, err := newExporter(ctx)
	if err != nil {
		return noop, err
	}
	if version == "" {
		version = "dev"
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(service), semconv.ServiceVersion(version)),
		resource.WithFromEnv(), // OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES đè lên giá trị trong code
		resource.WithTelemetrySDK(),
		resource.WithHost(),
	)
	if err != nil {
		return noop, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp), // gom span rồi gửi theo lô trong goroutine riêng, không chặn request
		sdktrace.WithResource(res),
		// không có WithSampler: SDK đọc OTEL_TRACES_SAMPLER, mặc định parentbased_always_on
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Enabled cho biết có xuất trace không, đọc biến môi trường bằng lookup.
func Enabled(lookup func(string) (string, bool)) bool {
	if v, _ := lookup("OTEL_SDK_DISABLED"); strings.EqualFold(v, "true") {
		return false
	}
	if v, _ := lookup("OTEL_TRACES_EXPORTER"); v == "none" {
		return false
	}
	for _, k := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"} {
		if v, _ := lookup(k); v != "" {
			return true
		}
	}
	return false
}

// newExporter tạo exporter OTLP theo OTEL_EXPORTER_OTLP_(TRACES_)PROTOCOL: grpc (cổng 4317) hoặc
// http/protobuf (cổng 4318, mặc định của đặc tả). Endpoint, header, timeout... exporter tự đọc từ OTEL_*.
func newExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	proto := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL")
	if proto == "" {
		proto = os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
	}
	if proto == "grpc" {
		return otlptracegrpc.New(ctx)
	}
	return otlptracehttp.New(ctx)
}
