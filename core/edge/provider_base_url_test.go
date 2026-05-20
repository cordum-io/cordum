package edge

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestValidateProviderBaseURLAcceptsHTTPSProxyAndCanonicalizes(t *testing.T) {
	restore := stubMCPHostLookup(map[string][]net.IP{
		"llm-proxy.cordum.example": {net.ParseIP("203.0.113.10")},
	})
	t.Cleanup(restore)

	got, err := ValidateProviderBaseURL(context.Background(), " HTTPS://LLM-PROXY.CORDUM.EXAMPLE/v1/ ")
	if err != nil {
		t.Fatalf("ValidateProviderBaseURL returned error: %v", err)
	}
	if got != "https://llm-proxy.cordum.example/v1" {
		t.Fatalf("canonical URL = %q, want https://llm-proxy.cordum.example/v1", got)
	}
}

func TestValidateProviderBaseURLRejectsUnsafeInputsAndRedacts(t *testing.T) {
	restore := stubMCPHostLookup(map[string][]net.IP{
		"private-dns.cordum.example": {net.ParseIP("10.0.0.7")},
		"safe.cordum.example":        {net.ParseIP("203.0.113.20")},
	})
	t.Cleanup(restore)

	cases := []struct {
		name      string
		raw       string
		leakWords []string
	}{
		{name: "empty", raw: ""},
		{name: "malformed", raw: "://not-a-url"},
		{name: "missing_host", raw: "https:///v1"},
		{name: "userinfo_password", raw: "https://user:pass@safe.cordum.example/v1", leakWords: []string{"user:pass", "pass@"}},
		{name: "userinfo_username", raw: "https://user@safe.cordum.example/v1", leakWords: []string{"user@"}},
		{name: "file_scheme", raw: "file:///tmp/socket"},
		{name: "ftp_scheme", raw: "ftp://safe.cordum.example"},
		{name: "unix_scheme", raw: "unix:///var/run/provider.sock"},
		{name: "plain_http", raw: "http://safe.cordum.example/v1"},
		{name: "localhost", raw: "https://localhost/v1"},
		{name: "loopback_v4", raw: "https://127.0.0.1/v1"},
		{name: "loopback_v6", raw: "https://[::1]/v1"},
		{name: "rfc1918_10", raw: "https://10.0.0.1/v1"},
		{name: "rfc1918_172", raw: "https://172.16.0.1/v1"},
		{name: "rfc1918_192", raw: "https://192.168.1.1/v1"},
		{name: "link_local", raw: "https://169.254.169.254/latest"},
		{name: "metadata_hostname", raw: "https://metadata.google.internal/computeMetadata/v1/"},
		{name: "private_dns_resolution", raw: "https://private-dns.cordum.example/v1"},
		{name: "query", raw: "https://safe.cordum.example/v1?api_key=secret-query-token", leakWords: []string{"api_key", "secret-query-token"}},
		{name: "fragment", raw: "https://safe.cordum.example/v1#secret-fragment", leakWords: []string{"secret-fragment"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateProviderBaseURL(context.Background(), tc.raw)
			if err == nil {
				t.Fatalf("ValidateProviderBaseURL(%q) = %q, want error", tc.raw, got)
			}
			for _, leak := range tc.leakWords {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("error %q leaked secret/raw URL fragment %q", err.Error(), leak)
				}
			}
		})
	}
}

func stubMCPHostLookup(records map[string][]net.IP) func() {
	prev := MCPHostLookup
	MCPHostLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if ips, ok := records[strings.ToLower(host)]; ok {
			return ips, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}
	return func() { MCPHostLookup = prev }
}
