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
		"CORDUM_GATEWAY":                             "http://127.0.0.1:8081",
		"CORDUM_API_KEY":                             "api-key-123",
		"CORDUM_TENANT_ID":                           "tenant-a",
		"CORDUM_EDGE_POLICY_MODE":                    "enforce",
		"CORDUM_AGENTD_SOCKET":                       "http://127.0.0.1:8765/v1/edge/hooks/claude",
		"CORDUM_AGENTD_HOOK_TIMEOUT":                 "3s",
		"CORDUM_EDGE_HEARTBEAT_TTL":                  "40s",
		"CORDUM_EDGE_HEARTBEAT_INTERVAL":             "10s",
		"CORDUM_AGENTD_FAIL_CLOSED":                  "true",
		"CORDUM_AGENTD_STATE_DIR":                    "D:/Cordum/.tmp/agentd-state",
		"CORDUM_AGENTD_SAFE_ALLOW_CACHE":             "true",
		"CORDUM_AGENTD_SAFE_ALLOW_CACHE_TTL":         "2m",
		"CORDUM_AGENTD_SAFE_ALLOW_CACHE_MAX_ENTRIES": "32",
		"CORDUM_AGENTD_INLINE_APPROVAL_WAIT":         "true",
		"CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT": "4s",
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
	if !cfg.SafeAllowCache.Enabled || cfg.SafeAllowCache.TTL != 2*time.Minute || cfg.SafeAllowCache.MaxEntries != 32 {
		t.Fatalf("safe allow cache config = %#v, want enabled ttl=2m max=32", cfg.SafeAllowCache)
	}
	if !cfg.InlineApprovalWaitEnabled || cfg.InlineApprovalWaitTimeout != 4*time.Second {
		t.Fatalf("inline approval wait config = enabled:%v timeout:%s, want true/4s", cfg.InlineApprovalWaitEnabled, cfg.InlineApprovalWaitTimeout)
	}
}

func TestLoadConfigSafeAllowCacheDefaultsOffAndRejectsInvalidBounds(t *testing.T) {
	t.Parallel()

	base := map[string]string{
		"CORDUM_GATEWAY":   "http://127.0.0.1:8081",
		"CORDUM_API_KEY":   "api-key-123",
		"CORDUM_TENANT_ID": "tenant-a",
	}
	cfg, err := LoadConfig(base)
	if err != nil {
		t.Fatalf("LoadConfig defaults: %v", err)
	}
	if cfg.SafeAllowCache.Enabled {
		t.Fatalf("safe allow cache enabled by default: %#v", cfg.SafeAllowCache)
	}

	invalidTTL := cloneConfigEnv(base)
	invalidTTL["CORDUM_AGENTD_SAFE_ALLOW_CACHE"] = "true"
	invalidTTL["CORDUM_AGENTD_SAFE_ALLOW_CACHE_TTL"] = "0s"
	if _, err := LoadConfig(invalidTTL); err == nil || !strings.Contains(err.Error(), "CORDUM_AGENTD_SAFE_ALLOW_CACHE_TTL") {
		t.Fatalf("LoadConfig invalid cache TTL err = %v, want env var name", err)
	}

	invalidMax := cloneConfigEnv(base)
	invalidMax["CORDUM_AGENTD_SAFE_ALLOW_CACHE"] = "true"
	invalidMax["CORDUM_AGENTD_SAFE_ALLOW_CACHE_MAX_ENTRIES"] = "0"
	if _, err := LoadConfig(invalidMax); err == nil || !strings.Contains(err.Error(), "CORDUM_AGENTD_SAFE_ALLOW_CACHE_MAX_ENTRIES") {
		t.Fatalf("LoadConfig invalid cache max err = %v, want env var name", err)
	}
}

func TestLoadConfigInlineApprovalWaitDefaultsOffAndRejectsInvalidTimeout(t *testing.T) {
	t.Parallel()

	base := map[string]string{
		"CORDUM_GATEWAY":   "http://127.0.0.1:8081",
		"CORDUM_API_KEY":   "api-key-123",
		"CORDUM_TENANT_ID": "tenant-a",
	}
	cfg, err := LoadConfig(base)
	if err != nil {
		t.Fatalf("LoadConfig defaults: %v", err)
	}
	if cfg.InlineApprovalWaitEnabled {
		t.Fatalf("inline approval wait enabled by default")
	}
	if cfg.InlineApprovalWaitTimeout != defaultInlineApprovalWaitTimeout {
		t.Fatalf("inline approval wait default timeout = %s, want %s", cfg.InlineApprovalWaitTimeout, defaultInlineApprovalWaitTimeout)
	}

	invalid := cloneConfigEnv(base)
	invalid["CORDUM_AGENTD_INLINE_APPROVAL_WAIT"] = "true"
	invalid["CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT"] = "0s"
	if _, err := LoadConfig(invalid); err == nil || !strings.Contains(err.Error(), "CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT") {
		t.Fatalf("LoadConfig invalid inline approval wait timeout err = %v, want env var name", err)
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

func TestLoadConfigRejectsNonHTTPSocketPathUntilSocketListenerImplemented(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(map[string]string{
		"CORDUM_GATEWAY":       "http://127.0.0.1:8081",
		"CORDUM_API_KEY":       "api-key-123",
		"CORDUM_TENANT_ID":     "tenant-a",
		"CORDUM_AGENTD_SOCKET": "/tmp/cordum-agentd.sock",
	})
	if err == nil {
		t.Fatal("LoadConfig returned nil error for unsupported non-HTTP socket path")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "socket") || !strings.Contains(msg, "not supported") {
		t.Fatalf("error = %q, want unsupported socket-path guidance", err.Error())
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

func cloneConfigEnv(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
