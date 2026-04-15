package mcp

import (
	"context"
	"errors"
	"net"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestOutboundPrivateIPNetsInitialized(t *testing.T) {
	if outboundPrivateIPNets == nil {
		t.Fatal("outboundPrivateIPNets is nil — IIFE init failed")
	}
	// 10 CIDRs: 0/8, 10/8, 100.64/10, 127/8, 169.254/16, 172.16/12, 192.168/16, ::1/128, fe80::/10, fc00::/7
	if got := len(outboundPrivateIPNets); got != 10 {
		t.Fatalf("expected 10 outbound private nets, got %d", got)
	}
}

func TestNormalizeAllowedHosts(t *testing.T) {
	t.Parallel()
	got := normalizeAllowedHosts([]string{
		" example.com ",
		".example.com",
		"https://api.example.com/path",
		"[::1]:8081",
		"127.0.0.1:8081",
		"",
	})
	want := []string{
		"example.com",
		"api.example.com",
		"::1",
		"127.0.0.1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeAllowedHosts mismatch: got=%v want=%v", got, want)
	}
}

func TestValidateOutboundTargetURL(t *testing.T) {
	t.Parallel()

	origLookup := outboundLookupHostIPs
	t.Cleanup(func() {
		outboundLookupHostIPs = origLookup
	})

	outboundLookupHostIPs = func(_ context.Context, host string) ([]net.IP, error) {
		switch host {
		case "api.example.com":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		case "internal.example.com":
			return []net.IP{net.ParseIP("10.0.0.5")}, nil
		default:
			return nil, errors.New("no such host")
		}
	}

	tests := []struct {
		name              string
		rawURL            string
		allowlist         []string
		allowPrivateHosts bool
		wantErr           bool
	}{
		{
			name:              "reject_private_ip_default",
			rawURL:            "http://127.0.0.1:8081/api/v1/status",
			allowPrivateHosts: false,
			wantErr:           true,
		},
		{
			name:              "allow_private_ip_when_enabled",
			rawURL:            "http://127.0.0.1:8081/api/v1/status",
			allowPrivateHosts: true,
			wantErr:           false,
		},
		{
			name:              "allow_public_host",
			rawURL:            "https://api.example.com/api/v1/status",
			allowPrivateHosts: false,
			wantErr:           false,
		},
		{
			name:              "reject_host_not_in_allowlist",
			rawURL:            "https://api.example.com/api/v1/status",
			allowlist:         []string{"corp.example.com"},
			allowPrivateHosts: false,
			wantErr:           true,
		},
		{
			name:              "allow_host_in_allowlist_suffix",
			rawURL:            "https://api.example.com/api/v1/status",
			allowlist:         []string{"example.com"},
			allowPrivateHosts: false,
			wantErr:           false,
		},
		{
			name:              "reject_hostname_resolving_private",
			rawURL:            "https://internal.example.com/api/v1/status",
			allowPrivateHosts: false,
			wantErr:           true,
		},
		{
			name:              "reject_unsupported_scheme",
			rawURL:            "file:///etc/passwd",
			allowPrivateHosts: false,
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("parse url %q: %v", tt.rawURL, err)
			}
			err = validateOutboundTargetURL(context.Background(), parsed, normalizeAllowedHosts(tt.allowlist), tt.allowPrivateHosts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateOutboundTargetURL error mismatch: got=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAndResolveOutboundURL_ReturnsPinnedIPs(t *testing.T) {
	origLookup := outboundLookupHostIPs
	t.Cleanup(func() { outboundLookupHostIPs = origLookup })

	outboundLookupHostIPs = func(_ context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}

	parsed, _ := url.Parse("https://api.example.com/test")
	ips, err := validateAndResolveOutboundURL(context.Background(), parsed, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.ParseIP("93.184.216.34")) {
		t.Fatalf("expected pinned IP 93.184.216.34, got %v", ips)
	}
}

func TestValidateAndResolveOutboundURL_PrivateIPBlocked(t *testing.T) {
	origLookup := outboundLookupHostIPs
	t.Cleanup(func() { outboundLookupHostIPs = origLookup })

	outboundLookupHostIPs = func(_ context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.1")}, nil
	}

	parsed, _ := url.Parse("https://evil.example.com/test")
	_, err := validateAndResolveOutboundURL(context.Background(), parsed, nil, false)
	if err == nil {
		t.Fatal("expected error for private IP 10.0.0.1")
	}
}

func TestValidateAndResolveOutboundURL_IPv6LoopbackBlocked(t *testing.T) {
	origLookup := outboundLookupHostIPs
	t.Cleanup(func() { outboundLookupHostIPs = origLookup })

	outboundLookupHostIPs = func(_ context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("::1")}, nil
	}

	parsed, _ := url.Parse("https://ipv6evil.example.com/test")
	_, err := validateAndResolveOutboundURL(context.Background(), parsed, nil, false)
	if err == nil {
		t.Fatal("expected error for IPv6 loopback ::1")
	}
}

func TestPinnedDialer_UsesPinnedIPNotDNS(t *testing.T) {
	// Simulate DNS rebinding: the resolver would return 127.0.0.1 on a
	// second call, but the pinned dialer should use the pre-validated IP.
	var callCount atomic.Int32
	origLookup := outboundLookupHostIPs
	t.Cleanup(func() { outboundLookupHostIPs = origLookup })

	outboundLookupHostIPs = func(_ context.Context, host string) ([]net.IP, error) {
		if callCount.Add(1) == 1 {
			// First call: public IP (validation passes)
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		// Second call: rebind to loopback (would bypass validation)
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}

	parsed, _ := url.Parse("https://rebind.evil.com/test")
	ips, err := validateAndResolveOutboundURL(context.Background(), parsed, nil, false)
	if err != nil {
		t.Fatalf("validation should pass with public IP: %v", err)
	}

	// Create pinned dialer with the validated IP
	dial := pinnedDialer(ips)

	// The dialer should connect to 93.184.216.34:443, NOT re-resolve DNS.
	// We can't test an actual connection, but we can verify the dialer
	// attempts the pinned IP by checking it doesn't connect to 127.0.0.1.
	conn, err := dial(context.Background(), "tcp", "rebind.evil.com:443")
	if conn != nil {
		_ = conn.Close()
	}
	// Connection will likely fail (no server at 93.184.216.34:443 in test),
	// but the key assertion is that DNS was NOT re-resolved (callCount stays 1).
	// The dialer pins the IP from validation, not from a fresh DNS lookup.
	if callCount.Load() != 1 {
		t.Fatalf("DNS was re-resolved %d times; expected only 1 (validation-time)", callCount.Load())
	}
	_ = err // Connection failure is expected in test environment
}

func TestPinnedDialer_EmptyIPs_FallsBackToDefault(t *testing.T) {
	// When allowPrivateHosts=true, no IPs are returned — dialer should
	// fall back to default resolution.
	dial := pinnedDialer(nil)
	// Just verify it doesn't panic with nil IPs
	conn, err := dial(context.Background(), "tcp", "localhost:0")
	if conn != nil {
		_ = conn.Close()
	}
	_ = err // Connection failure expected (port 0)
}
