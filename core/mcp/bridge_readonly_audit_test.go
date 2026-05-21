package mcp

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestHTTPBridge_GetJSON_RejectsPrivateHostResolution locks the HIGH #6 fix:
// the read-only bridge's getJSON path MUST route GETs through the same
// validateAndResolveOutboundURL + pinnedDialer guard the mutating
// doRequest uses. Pre-fix, every read-only call (ListJobs, ListRuns,
// QueryAudit, all status/discovery surfaces) skipped IP-pin protection,
// so DNS rebinding from baseURL straight to 169.254.169.254 (cloud IMDS)
// or any RFC1918 internal host went through unchecked.
func TestHTTPBridge_GetJSON_RejectsPrivateHostResolution(t *testing.T) {
	// Point a DNS hook at a private IP so validation rejects the GET
	// without ever issuing a connection. Without the SSRF guard, the
	// bridge would proceed to dial the gateway via baseURL.
	origLookup := outboundLookupHostIPs
	t.Cleanup(func() { outboundLookupHostIPs = origLookup })
	outboundLookupHostIPs = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}

	bridge := NewHTTPServiceBridge(HTTPServiceBridgeConfig{
		BaseURL:           "https://attacker.example.com",
		TenantID:          "default",
		AllowPrivateHosts: false, // production default
	}.WithAuthToken("test-key"))

	_, err := bridge.ListJobs(context.Background(), ListInput{})
	if err == nil {
		t.Fatal("ListJobs returned nil error for host resolving to 169.254.169.254 — SSRF protection missing on read-only bridge")
	}
}

// TestHTTPBridge_GetJSON_PinnedDialerOverridesDNS confirms that once
// validation passes, the dialer uses the pre-resolved IP rather than
// re-resolving DNS at connect time (TOCTOU/DNS-rebinding protection).
// Mirrors TestPinnedDialer_UsesPinnedIPNotDNS but exercises the full
// getJSON path on the readonly bridge.
func TestHTTPBridge_GetJSON_PinnedDialerOverridesDNS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	// Resolve the test server's bound host (127.0.0.1) BEFORE building
	// the bridge so we can assert the pin sticks.
	u, _ := url.Parse(srv.URL)

	bridge := NewHTTPServiceBridge(HTTPServiceBridgeConfig{
		BaseURL:           srv.URL,
		TenantID:          "default",
		AllowPrivateHosts: true, // httptest binds to loopback
	}.WithAuthToken("test-key"))

	if _, err := bridge.ListJobs(context.Background(), ListInput{PageSize: 5}); err != nil {
		t.Fatalf("ListJobs through bound httptest baseURL %s failed: %v", u, err)
	}
}
