package gateway

import (
	"testing"
)

// ensureSSRFProtection makes sure the private IP check is active for these
// security tests, even if another test set the bypass.
func ensureSSRFProtection(t *testing.T) {
	t.Helper()
	old := skipPrivateIPCheck
	skipPrivateIPCheck = false
	t.Cleanup(func() { skipPrivateIPCheck = old })
}

func TestIsPrivateIP(t *testing.T) {
	ensureSSRFProtection(t)

	privateHosts := []string{
		"127.0.0.1",
		"127.0.0.2",
		"10.0.0.1",
		"10.255.255.255",
		"172.16.0.1",
		"172.31.255.255",
		"192.168.1.1",
		"192.168.0.0",
		"169.254.169.254", // AWS metadata
		"169.254.1.1",
		"::1",
		"fe80::1",
		"fc00::1",
		"fd12:3456::1",
		"localhost",
		"LOCALHOST",
		"metadata.google.internal",
	}

	for _, host := range privateHosts {
		if !isPrivateIP(host) {
			t.Errorf("isPrivateIP(%q) = false, want true", host)
		}
	}
}

func TestIsPrivateIP_PublicAddresses(t *testing.T) {
	ensureSSRFProtection(t)

	publicHosts := []string{
		"8.8.8.8",
		"142.250.80.14",
		"1.1.1.1",
		"203.0.113.1",
		"2607:f8b0:4004:800::200e", // public IPv6
		"github.com",
		"example.com",
		"",
	}

	for _, host := range publicHosts {
		if isPrivateIP(host) {
			t.Errorf("isPrivateIP(%q) = true, want false", host)
		}
	}
}

func TestIsPrivateIP_EdgeCases(t *testing.T) {
	ensureSSRFProtection(t)

	tests := []struct {
		host    string
		private bool
	}{
		{"172.15.255.255", false}, // just below 172.16.0.0/12
		{"172.16.0.0", true},
		{"172.32.0.0", false}, // just above 172.16.0.0/12
		{"11.0.0.0", false},  // just above 10.0.0.0/8
		{"9.255.255.255", false},
		{"192.167.255.255", false}, // just below 192.168.0.0/16
		{"192.169.0.0", false},     // just above 192.168.0.0/16
		{"  127.0.0.1  ", true},    // whitespace trimmed
	}

	for _, tt := range tests {
		got := isPrivateIP(tt.host)
		if got != tt.private {
			t.Errorf("isPrivateIP(%q) = %v, want %v", tt.host, got, tt.private)
		}
	}
}

func TestValidateMarketplaceURL_RejectsPrivateIPs(t *testing.T) {
	ensureSSRFProtection(t)

	// Even when host is in the allowlist, private IPs must be rejected.
	allowedHosts := map[string]struct{}{
		"127.0.0.1":       {},
		"10.0.0.1":        {},
		"192.168.1.1":     {},
		"169.254.169.254": {},
		"localhost":        {},
	}

	urls := []string{
		"https://127.0.0.1/pack.tar.gz",
		"https://10.0.0.1/pack.tar.gz",
		"https://192.168.1.1/pack.tar.gz",
		"https://169.254.169.254/latest/meta-data",
		"https://localhost/pack.tar.gz",
	}

	for _, u := range urls {
		_, err := validateMarketplaceURL(u, allowedHosts)
		if err == nil {
			t.Errorf("validateMarketplaceURL(%q) = nil error, want rejection", u)
		} else if err.Error() != "invalid pack url" {
			t.Errorf("validateMarketplaceURL(%q) error = %q, want 'invalid pack url'", u, err.Error())
		}
	}
}

func TestValidateMarketplaceURL_AcceptsPublicHosts(t *testing.T) {
	ensureSSRFProtection(t)

	allowedHosts := map[string]struct{}{
		"github.com": {},
		"8.8.8.8":    {},
	}

	urls := []string{
		"https://github.com/cordum/packs/releases/download/v1/pack.tar.gz",
		"https://8.8.8.8/pack.tar.gz",
	}

	for _, u := range urls {
		parsed, err := validateMarketplaceURL(u, allowedHosts)
		if err != nil {
			t.Errorf("validateMarketplaceURL(%q) = error %v, want success", u, err)
		}
		if parsed == nil {
			t.Errorf("validateMarketplaceURL(%q) returned nil parsed URL", u)
		}
	}
}

func TestValidateMarketplaceURL_RejectsNonAllowedHost(t *testing.T) {
	ensureSSRFProtection(t)

	allowedHosts := map[string]struct{}{
		"github.com": {},
	}

	_, err := validateMarketplaceURL("https://evil.com/pack.tar.gz", allowedHosts)
	if err == nil {
		t.Error("validateMarketplaceURL should reject hosts not in allowlist")
	}
}
