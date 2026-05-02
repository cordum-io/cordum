package agentd

import (
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestLoadConfigFromEnvAppliesDefaultsAndExplicitValues(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(map[string]string{
		"CORDUM_GATEWAY":                 "http://127.0.0.1:8081",
		"CORDUM_API_KEY":                 "api-key-123",
		"CORDUM_TENANT_ID":               "tenant-a",
		"CORDUM_EDGE_POLICY_MODE":        "enforce",
		"CORDUM_AGENTD_SOCKET":           "http://127.0.0.1:8765/v1/edge/hooks/claude",
		"CORDUM_AGENTD_HOOK_TIMEOUT":     "3s",
		"CORDUM_EDGE_HEARTBEAT_TTL":      "40s",
		"CORDUM_EDGE_HEARTBEAT_INTERVAL": "10s",
		"CORDUM_AGENTD_FAIL_CLOSED":      "true",
		"CORDUM_AGENTD_STATE_DIR":        "D:/Cordum/.tmp/agentd-state",
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.GatewayURL != "http://127.0.0.1:8081" || cfg.APIKey != "api-key-123" || cfg.TenantID != "tenant-a" {
		t.Fatalf("gateway/api/tenant = %q/%q/%q", cfg.GatewayURL, cfg.APIKey, cfg.TenantID)
	}
	if cfg.PolicyMode != edgecore.PolicyModeEnforce || !cfg.FailClosed {
		t.Fatalf("policy/failClosed = %q/%v", cfg.PolicyMode, cfg.FailClosed)
	}
	if cfg.HookTimeout != 3*time.Second || cfg.HeartbeatTTL != 40*time.Second || cfg.HeartbeatInterval != 10*time.Second {
		t.Fatalf("durations = hook:%s ttl:%s interval:%s", cfg.HookTimeout, cfg.HeartbeatTTL, cfg.HeartbeatInterval)
	}
	if cfg.BindURL != "http://127.0.0.1:8765/v1/edge/hooks/claude" {
		t.Fatalf("bind URL = %q", cfg.BindURL)
	}
	if !strings.Contains(cfg.StateDir, "agentd-state") {
		t.Fatalf("state dir = %q", cfg.StateDir)
	}
}

func TestLoadConfigRejectsMissingGatewayCredentialsForNewSession(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(map[string]string{"CORDUM_GATEWAY": "http://127.0.0.1:8081"})
	if err == nil {
		t.Fatal("LoadConfig returned nil error without API key/tenant")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CORDUM_API_KEY") || !strings.Contains(msg, "CORDUM_TENANT_ID") {
		t.Fatalf("error = %q, want missing credential names", msg)
	}
}

func TestLoadConfigRejectsNonLocalAgentdBindURL(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(map[string]string{
		"CORDUM_GATEWAY":       "http://127.0.0.1:8081",
		"CORDUM_API_KEY":       "api-key-123",
		"CORDUM_TENANT_ID":     "tenant-a",
		"CORDUM_AGENTD_SOCKET": "http://0.0.0.0:8765/v1/edge/hooks/claude",
	})
	if err == nil {
		t.Fatal("LoadConfig returned nil error for broad bind")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "local") {
		t.Fatalf("error = %q, want local-only guidance", err.Error())
	}
}

func TestLoadConfigRejectsInvalidDurations(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(map[string]string{
		"CORDUM_GATEWAY":             "http://127.0.0.1:8081",
		"CORDUM_API_KEY":             "api-key-123",
		"CORDUM_TENANT_ID":           "tenant-a",
		"CORDUM_AGENTD_HOOK_TIMEOUT": "0s",
	})
	if err == nil {
		t.Fatal("LoadConfig returned nil error for zero timeout")
	}
	if !strings.Contains(err.Error(), "CORDUM_AGENTD_HOOK_TIMEOUT") {
		t.Fatalf("error = %q, want timeout env var name", err.Error())
	}
}

func TestLoadConfigRejectsHeartbeatIntervalGreaterThanHalfTTL(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(map[string]string{
		"CORDUM_GATEWAY":                 "http://127.0.0.1:8081",
		"CORDUM_API_KEY":                 "api-key-123",
		"CORDUM_TENANT_ID":               "tenant-a",
		"CORDUM_EDGE_HEARTBEAT_TTL":      "40s",
		"CORDUM_EDGE_HEARTBEAT_INTERVAL": "25s",
	})
	if err == nil {
		t.Fatal("LoadConfig returned nil error for heartbeat interval > TTL/2")
	}
	if !strings.Contains(err.Error(), "TTL/2") {
		t.Fatalf("error = %q, want TTL/2 guidance", err.Error())
	}
}
