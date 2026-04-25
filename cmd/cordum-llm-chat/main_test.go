package main

import (
	"strings"
	"testing"
	"time"
)

func fakeEnv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func TestLoadConfigFromEnv_DefaultsWhenOnlyRequiredSet(t *testing.T) {
	cfg, err := loadConfigFromEnv(fakeEnv(map[string]string{
		"REDIS_URL": "redis://localhost:6379/0",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Fatalf("HTTPAddr = %q, want %q", cfg.HTTPAddr, defaultHTTPAddr)
	}
	if cfg.Provider.Kind != defaultProvider {
		t.Fatalf("Provider.Kind = %q, want %q", cfg.Provider.Kind, defaultProvider)
	}
	if cfg.Provider.BaseURL != defaultBaseURL {
		t.Fatalf("Provider.BaseURL = %q, want %q", cfg.Provider.BaseURL, defaultBaseURL)
	}
	if cfg.Provider.Model != defaultModel {
		t.Fatalf("Provider.Model = %q, want %q", cfg.Provider.Model, defaultModel)
	}
	if cfg.Provider.ToolTemperature != defaultToolTemperature {
		t.Fatalf("ToolTemperature = %v, want %v", cfg.Provider.ToolTemperature, defaultToolTemperature)
	}
	if cfg.Provider.ToolTopP != defaultToolTopP {
		t.Fatalf("ToolTopP = %v, want %v", cfg.Provider.ToolTopP, defaultToolTopP)
	}
	if cfg.Provider.SummaryTemperature != defaultSummaryTemperature {
		t.Fatalf("SummaryTemperature = %v, want %v", cfg.Provider.SummaryTemperature, defaultSummaryTemperature)
	}
	if cfg.Provider.SummaryTopP != defaultSummaryTopP {
		t.Fatalf("SummaryTopP = %v, want %v", cfg.Provider.SummaryTopP, defaultSummaryTopP)
	}
	if cfg.Budget.MaxToolCallsPerTurn != defaultMaxToolCallsPerTurn {
		t.Fatalf("MaxToolCallsPerTurn = %d, want %d", cfg.Budget.MaxToolCallsPerTurn, defaultMaxToolCallsPerTurn)
	}
	if cfg.Budget.MaxWallClockPerTurn != defaultMaxWallClockPerTurn {
		t.Fatalf("MaxWallClockPerTurn = %v, want %v", cfg.Budget.MaxWallClockPerTurn, defaultMaxWallClockPerTurn)
	}
	if cfg.Budget.MaxAssistantBytes != defaultMaxAssistantBytes {
		t.Fatalf("MaxAssistantBytes = %d, want %d", cfg.Budget.MaxAssistantBytes, defaultMaxAssistantBytes)
	}
}

func TestLoadConfigFromEnv_MissingRedisURL(t *testing.T) {
	_, err := loadConfigFromEnv(fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error for missing REDIS_URL, got nil")
	}
	if !strings.Contains(err.Error(), "REDIS_URL") {
		t.Fatalf("error = %v, want REDIS_URL in message", err)
	}
}

func TestLoadConfigFromEnv_AllOverridesRead(t *testing.T) {
	cfg, err := loadConfigFromEnv(fakeEnv(map[string]string{
		"REDIS_URL":                        "redis://custom:6379/1",
		"NATS_URL":                         "nats://nats:4222",
		"CORDUM_API_KEY":                   "sekret",
		"CORDUM_GATEWAY_URL":               "https://gateway.internal:8443",
		"CORDUM_LLM_CHAT_ADDR":             ":9090",
		"CORDUM_LLM_CHAT_TLS_CERT_FILE":    "/tls/tls.crt",
		"CORDUM_LLM_CHAT_TLS_KEY_FILE":     "/tls/tls.key",
		"LLMCHAT_PROVIDER":                 "openai",
		"LLMCHAT_BASE_URL":                 "http://vllm:8000/v1",
		"LLMCHAT_MODEL":                    "qwen3-coder",
		"LLMCHAT_API_KEY":                  "token",
		"LLMCHAT_TOOL_TEMPERATURE":         "0.35",
		"LLMCHAT_TOOL_TOP_P":               "0.88",
		"LLMCHAT_SUMMARY_TEMPERATURE":      "0.72",
		"LLMCHAT_SUMMARY_TOP_P":            "0.81",
		"LLMCHAT_MAX_TOOL_CALLS_PER_TURN":  "24",
		"LLMCHAT_MAX_WALL_CLOCK_PER_TURN":  "90s",
		"LLMCHAT_MAX_ASSISTANT_BYTES":      "65536",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q, want :9090", cfg.HTTPAddr)
	}
	if cfg.TLSCertFile != "/tls/tls.crt" || cfg.TLSKeyFile != "/tls/tls.key" {
		t.Fatalf("TLS = %q/%q, want /tls/tls.crt //tls/tls.key", cfg.TLSCertFile, cfg.TLSKeyFile)
	}
	if cfg.CordumAPIKey != "sekret" {
		t.Fatalf("CordumAPIKey = %q, want sekret", cfg.CordumAPIKey)
	}
	if cfg.GatewayURL != "https://gateway.internal:8443" {
		t.Fatalf("GatewayURL = %q, want https://gateway.internal:8443", cfg.GatewayURL)
	}
	if cfg.NATSURL != "nats://nats:4222" {
		t.Fatalf("NATSURL = %q, want nats://nats:4222", cfg.NATSURL)
	}
	if cfg.Provider.APIKey != "token" {
		t.Fatalf("APIKey = %q, want token", cfg.Provider.APIKey)
	}
	if cfg.Provider.ToolTemperature != 0.35 {
		t.Fatalf("ToolTemperature = %v, want 0.35", cfg.Provider.ToolTemperature)
	}
	if cfg.Provider.ToolTopP != 0.88 {
		t.Fatalf("ToolTopP = %v, want 0.88", cfg.Provider.ToolTopP)
	}
	if cfg.Provider.SummaryTemperature != 0.72 {
		t.Fatalf("SummaryTemperature = %v, want 0.72", cfg.Provider.SummaryTemperature)
	}
	if cfg.Provider.SummaryTopP != 0.81 {
		t.Fatalf("SummaryTopP = %v, want 0.81", cfg.Provider.SummaryTopP)
	}
	if cfg.Budget.MaxToolCallsPerTurn != 24 {
		t.Fatalf("MaxToolCallsPerTurn = %d, want 24", cfg.Budget.MaxToolCallsPerTurn)
	}
	if cfg.Budget.MaxWallClockPerTurn != 90*time.Second {
		t.Fatalf("MaxWallClockPerTurn = %v, want 90s", cfg.Budget.MaxWallClockPerTurn)
	}
	if cfg.Budget.MaxAssistantBytes != 65536 {
		t.Fatalf("MaxAssistantBytes = %d, want 65536", cfg.Budget.MaxAssistantBytes)
	}
}

func TestLoadConfigFromEnv_TLSPairMustMatch(t *testing.T) {
	cases := []map[string]string{
		{
			"REDIS_URL":                     "redis://localhost:6379/0",
			"CORDUM_LLM_CHAT_TLS_CERT_FILE": "/tls/tls.crt",
		},
		{
			"REDIS_URL":                    "redis://localhost:6379/0",
			"CORDUM_LLM_CHAT_TLS_KEY_FILE": "/tls/tls.key",
		},
	}
	for _, env := range cases {
		if _, err := loadConfigFromEnv(fakeEnv(env)); err == nil {
			t.Fatalf("expected TLS pair error for env=%v", env)
		}
	}
}

func TestLoadConfigFromEnv_RejectsMalformedNumbers(t *testing.T) {
	cases := map[string]string{
		"LLMCHAT_TOOL_TEMPERATURE":        "not-a-float",
		"LLMCHAT_TOOL_TOP_P":              "abc",
		"LLMCHAT_SUMMARY_TEMPERATURE":     "inf!",
		"LLMCHAT_SUMMARY_TOP_P":           "oops",
		"LLMCHAT_MAX_TOOL_CALLS_PER_TURN": "twelve",
		"LLMCHAT_MAX_WALL_CLOCK_PER_TURN": "sixty",
		"LLMCHAT_MAX_ASSISTANT_BYTES":     "big",
	}
	for key, bad := range cases {
		env := map[string]string{
			"REDIS_URL": "redis://localhost:6379/0",
			key:         bad,
		}
		if _, err := loadConfigFromEnv(fakeEnv(env)); err == nil {
			t.Fatalf("expected parse error for %s=%q, got nil", key, bad)
		}
	}
}

func TestEnvHelpersFallback(t *testing.T) {
	get := fakeEnv(map[string]string{
		"FILLED": "  custom  ",
	})

	if got := envOrDefault(get, "FILLED", "fallback"); got != "custom" {
		t.Fatalf("envOrDefault(FILLED) = %q, want custom", got)
	}
	if got := envOrDefault(get, "MISSING", "fallback"); got != "fallback" {
		t.Fatalf("envOrDefault(MISSING) = %q, want fallback", got)
	}

	if got, err := envFloatOrDefault(get, "MISSING", 1.5); err != nil || got != 1.5 {
		t.Fatalf("envFloatOrDefault(MISSING) = %v, %v, want 1.5, nil", got, err)
	}
	if got, err := envIntOrDefault(get, "MISSING", 7); err != nil || got != 7 {
		t.Fatalf("envIntOrDefault(MISSING) = %d, %v, want 7, nil", got, err)
	}
	if got, err := envDurationOrDefault(get, "MISSING", 3*time.Second); err != nil || got != 3*time.Second {
		t.Fatalf("envDurationOrDefault(MISSING) = %v, %v, want 3s, nil", got, err)
	}
}
