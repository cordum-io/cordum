package claude

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge"
)

func TestGenerateManagedSettingsTemplateValidatesProviderBaseURL(t *testing.T) {
	restore := stubProviderURLLookup(map[string][]net.IP{
		"llm-proxy.cordum.example": {net.ParseIP("203.0.113.10")},
	})
	t.Cleanup(restore)

	opts := validManagedSettingsOptionsForProviderURL()
	opts.LLMProxyBaseURL = " https://LLM-PROXY.CORDUM.EXAMPLE/v1/ "

	bundle, err := GenerateManagedSettingsTemplate(opts)
	if err != nil {
		t.Fatalf("GenerateManagedSettingsTemplate returned error: %v", err)
	}
	settings := decodeJSONMap(t, bundle.ManagedSettingsJSON)
	env := jsonObject(t, settings["env"])
	if got := env["ANTHROPIC_BASE_URL"]; got != "https://llm-proxy.cordum.example/v1" {
		t.Fatalf("ANTHROPIC_BASE_URL = %v, want canonical https://llm-proxy.cordum.example/v1", got)
	}
}

func TestGenerateManagedSettingsTemplateRejectsUnsafeProviderBaseURLs(t *testing.T) {
	restore := stubProviderURLLookup(map[string][]net.IP{
		"safe.cordum.example":        {net.ParseIP("203.0.113.20")},
		"private-dns.cordum.example": {net.ParseIP("10.0.0.7")},
	})
	t.Cleanup(restore)

	cases := []struct {
		name      string
		raw       string
		leakWords []string
	}{
		{name: "malformed", raw: "://not-a-url"},
		{name: "credential_bearing", raw: "https://user:pass@safe.cordum.example", leakWords: []string{"user:pass", "pass@"}},
		{name: "file_scheme", raw: "file:///tmp/provider.sock"},
		{name: "ftp_scheme", raw: "ftp://safe.cordum.example"},
		{name: "unix_scheme", raw: "unix:///var/run/provider.sock"},
		{name: "plain_http", raw: "http://safe.cordum.example/v1"},
		{name: "localhost", raw: "https://localhost/v1"},
		{name: "loopback_v4", raw: "https://127.0.0.1/v1"},
		{name: "loopback_v6", raw: "https://[::1]/v1"},
		{name: "rfc1918", raw: "https://192.168.1.8/v1"},
		{name: "link_local_metadata_ip", raw: "https://169.254.169.254/latest"},
		{name: "metadata_hostname", raw: "https://metadata.google.internal/computeMetadata/v1/"},
		{name: "private_dns_resolution", raw: "https://private-dns.cordum.example/v1"},
		{name: "query", raw: "https://safe.cordum.example/v1?api_key=secret-query-token", leakWords: []string{"api_key", "secret-query-token"}},
		{name: "fragment", raw: "https://safe.cordum.example/v1#secret-fragment", leakWords: []string{"secret-fragment"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := validManagedSettingsOptionsForProviderURL()
			opts.LLMProxyBaseURL = tc.raw
			bundle, err := GenerateManagedSettingsTemplate(opts)
			if err == nil {
				t.Fatalf("GenerateManagedSettingsTemplate(%q) returned nil error and emitted %s", tc.raw, bundle.ManagedSettingsJSON)
			}
			if !strings.Contains(err.Error(), "llm_proxy_base_url") {
				t.Fatalf("error = %v, want llm_proxy_base_url field context", err)
			}
			for _, leak := range tc.leakWords {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("error %q leaked secret/raw URL fragment %q", err.Error(), leak)
				}
			}
			assertProviderBaseURLNotRendered(t, bundle.ManagedSettingsJSON, tc.raw)
		})
	}
}

func validManagedSettingsOptionsForProviderURL() ManagedSettingsOptions {
	return ManagedSettingsOptions{
		HookCommand:                "/opt/cordum/bin/cordum-hook",
		HookTimeout:                DefaultHookTimeout,
		AgentdURL:                  "http://127.0.0.1:8765/v1/edge/hooks/claude",
		MCPGatewayURL:              "https://mcp.cordum.example/mcp",
		LLMProxyBaseURL:            "https://safe.cordum.example/v1",
		APIKeyHelperCommand:        "/opt/cordum/bin/cordum-agentd claude api-key-helper",
		ForceRemoteSettingsRefresh: true,
		Platform:                   "linux",
	}
}

func stubProviderURLLookup(records map[string][]net.IP) func() {
	prev := edge.MCPHostLookup
	edge.MCPHostLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if ips, ok := records[strings.ToLower(host)]; ok {
			return ips, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}
	return func() { edge.MCPHostLookup = prev }
}

func assertProviderBaseURLNotRendered(t *testing.T, rawJSON []byte, raw string) {
	t.Helper()
	if len(rawJSON) == 0 {
		return
	}
	var settings map[string]any
	if err := json.Unmarshal(rawJSON, &settings); err == nil {
		env := jsonObject(t, settings["env"])
		if got, ok := env["ANTHROPIC_BASE_URL"]; ok && got == raw {
			t.Fatalf("unsafe ANTHROPIC_BASE_URL rendered unchanged: %q", raw)
		}
	}
}
