package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestEnabled(t *testing.T) {
	tests := []struct {
		env  map[string]string
		want bool
	}{
		{nil, false},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://jaeger:4318"}, true},
		{map[string]string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://jaeger:4318/v1/traces"}, true},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": ""}, false},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://jaeger:4318", "OTEL_SDK_DISABLED": "true"}, false},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://jaeger:4318", "OTEL_TRACES_EXPORTER": "none"}, false},
	}
	for _, tt := range tests {
		lookup := func(k string) (string, bool) { v, ok := tt.env[k]; return v, ok }
		if got := Enabled(lookup); got != tt.want {
			t.Errorf("Enabled(%v) = %v, muốn %v", tt.env, got, tt.want)
		}
	}
}

// TestSetupNoop: không có OTEL_EXPORTER_OTLP_ENDPOINT thì Setup không thay tracer provider toàn cục.
func TestSetupNoop(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	before := otel.GetTracerProvider()
	shutdown, err := Setup(t.Context(), "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if otel.GetTracerProvider() != before {
		t.Error("Setup đã thay tracer provider dù không có endpoint")
	}
	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
		t.Error("tracer provider toàn cục là SDK, muốn no-op")
	}
}

func TestLogHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(LogHandler{slog.NewTextHandler(&buf, nil)}).With("app", "test")
	tp := sdktrace.NewTracerProvider() // provider riêng của test, không đụng provider toàn cục
	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	logger.InfoContext(ctx, "trong span")
	span.End()
	logger.InfoContext(context.Background(), "ngoài span")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	sc := span.SpanContext()
	want := "trace_id=" + sc.TraceID().String() + " span_id=" + sc.SpanID().String()
	if !strings.Contains(lines[0], want) || !strings.Contains(lines[0], "app=test") {
		t.Errorf("log trong span = %q, muốn có %q", lines[0], want)
	}
	if strings.Contains(lines[1], "trace_id") {
		t.Errorf("log ngoài span = %q, không được có trace_id", lines[1])
	}
}
