package otel

import (
	"testing"
)

func TestMetricsEnabled_Default(t *testing.T) {
	t.Setenv(envOTELMetricsEnabled, "")
	if MetricsEnabled() {
		t.Fatal("expected metrics disabled by default")
	}
}

func TestMetricsEnabled_True(t *testing.T) {
	t.Setenv(envOTELMetricsEnabled, "true")
	if !MetricsEnabled() {
		t.Fatal("expected metrics enabled")
	}
}

func TestMetricsEnabled_False(t *testing.T) {
	t.Setenv(envOTELMetricsEnabled, "false")
	if MetricsEnabled() {
		t.Fatal("expected metrics disabled")
	}
}

func TestMetricsEnabled_One(t *testing.T) {
	t.Setenv(envOTELMetricsEnabled, "1")
	if !MetricsEnabled() {
		t.Fatal("expected metrics enabled with '1'")
	}
}

func TestMeter_ReturnsNonNil(t *testing.T) {
	m := Meter("test")
	if m == nil {
		t.Fatal("expected non-nil meter")
	}
}

func TestShutdownMetrics_NoopWhenDisabled(t *testing.T) {
	if err := ShutdownMetrics(); err != nil {
		t.Fatalf("ShutdownMetrics: %v", err)
	}
}
