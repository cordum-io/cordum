package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	cfg := Load()
	if cfg.NatsURL != defaultNATSURL {
		t.Fatalf("expected default nats url")
	}
	if cfg.RedisURL != defaultRedisURL {
		t.Fatalf("expected default redis url")
	}
	if cfg.SafetyKernelAddr != defaultSafetyKernel {
		t.Fatalf("expected default safety kernel")
	}
	if cfg.ContextEngineAddr != defaultContextEngine {
		t.Fatalf("expected default context engine")
	}
	if cfg.PoolConfigPath != defaultPoolConfig {
		t.Fatalf("expected default pool config path")
	}
	if cfg.TimeoutConfigPath != defaultTimeoutConfig {
		t.Fatalf("expected default timeout config path")
	}
	if cfg.SafetyPolicyPath != defaultSafetyPolicy {
		t.Fatalf("expected default safety policy path")
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv(envNATSURL, "nats://example:4222")
	t.Setenv(envRedisURL, "redis://example:6379")
	t.Setenv(envSafetyKernelAddr, "example:5000")
	t.Setenv(envContextEngineAddr, ":1234")
	t.Setenv(envPoolConfigPath, "custom/pools.yaml")
	t.Setenv(envTimeoutConfigPath, "custom/timeouts.yaml")
	t.Setenv(envSafetyPolicyPath, "custom/safety.yaml")

	cfg := Load()
	if cfg.NatsURL != "nats://example:4222" {
		t.Fatalf("unexpected nats url")
	}
	if cfg.RedisURL != "redis://example:6379" {
		t.Fatalf("unexpected redis url")
	}
	if cfg.SafetyKernelAddr != "example:5000" {
		t.Fatalf("unexpected safety kernel")
	}
	if cfg.ContextEngineAddr != ":1234" {
		t.Fatalf("unexpected context engine")
	}
	if cfg.PoolConfigPath != "custom/pools.yaml" {
		t.Fatalf("unexpected pool config path")
	}
	if cfg.TimeoutConfigPath != "custom/timeouts.yaml" {
		t.Fatalf("unexpected timeout config path")
	}
	if cfg.SafetyPolicyPath != "custom/safety.yaml" {
		t.Fatalf("unexpected safety policy path")
	}
}
