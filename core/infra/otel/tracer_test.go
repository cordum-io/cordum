package otel

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace/noop"
)

func TestInitTracer_Disabled(t *testing.T) {
	t.Setenv(envOTELEnabled, "")

	tp, err := InitTracer("test-service")
	if err != nil {
		t.Fatalf("InitTracer: %v", err)
	}

	// Should return noop provider.
	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")
	if span.SpanContext().IsValid() {
		t.Fatal("expected noop span with invalid context")
	}
	span.End()
}

func TestInitTracer_DisabledExplicit(t *testing.T) {
	t.Setenv(envOTELEnabled, "false")

	tp, err := InitTracer("test-service")
	if err != nil {
		t.Fatalf("InitTracer: %v", err)
	}

	// Noop provider type check.
	if _, ok := tp.(noop.TracerProvider); !ok {
		t.Fatalf("expected noop.TracerProvider, got %T", tp)
	}
}

func TestEnabled_EnvValues(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"false", false},
		{"0", false},
		{"true", true},
		{"1", true},
		{"TRUE", true},
		{"invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if tt.value == "" {
				t.Setenv(envOTELEnabled, "")
			} else {
				t.Setenv(envOTELEnabled, tt.value)
			}
			if got := Enabled(); got != tt.want {
				t.Fatalf("Enabled() = %v, want %v for OTEL_ENABLED=%q", got, tt.want, tt.value)
			}
		})
	}
}

func TestTracer_ReturnsNoopWhenDisabled(t *testing.T) {
	t.Setenv(envOTELEnabled, "")

	tracer := Tracer("test-component")
	_, span := tracer.Start(context.Background(), "test-op")
	defer span.End()

	if span.SpanContext().IsValid() {
		t.Fatal("expected noop span when OTEL is disabled")
	}
}

func TestShutdown_NoopSafe(t *testing.T) {
	// Shutdown should be safe to call even when nothing is initialized.
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
