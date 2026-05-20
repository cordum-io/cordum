package edge

import (
	"context"
	"errors"
	"net"
	"testing"
)

// TestValidateMCPUpstream_PinsResolvedIPs verifies that ValidateMCPUpstream
// records the resolved IPs onto the UpstreamServer at registration time so
// later use-time callers can detect DNS-rebinding to a fresh, unsafe IP.
func TestValidateMCPUpstream_PinsResolvedIPs(t *testing.T) {
	prev := MCPHostLookup
	t.Cleanup(func() { MCPHostLookup = prev })
	MCPHostLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "mcp.example.com" {
			return []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP("203.0.113.11")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}

	upstream := validMCPUpstream("tenant-a", "pinned-tools")
	upstream.Endpoint = "https://mcp.example.com/mcp"
	if err := ValidateMCPUpstream(context.Background(), &upstream, string(PolicyModeObserve), nil); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(upstream.ResolvedIPs) != 2 {
		t.Fatalf("ResolvedIPs = %v, want 2 pinned IPs from registration-time DNS", upstream.ResolvedIPs)
	}
	for _, raw := range upstream.ResolvedIPs {
		if net.ParseIP(raw) == nil {
			t.Fatalf("ResolvedIPs contains non-IP %q", raw)
		}
	}
}

// TestRevalidateMCPUpstreamAtUse_RejectsRebindToInternalIP locks the
// DNS-rebinding mitigation: an upstream that resolved to a public IP at
// registration but whose hostname now returns 169.254.169.254 (AWS IMDS)
// MUST be rejected at use time.
func TestRevalidateMCPUpstreamAtUse_RejectsRebindToInternalIP(t *testing.T) {
	prev := MCPHostLookup
	t.Cleanup(func() { MCPHostLookup = prev })

	// Registration-time DNS: public IP only.
	MCPHostLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "mcp.example.com" {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}

	upstream := validMCPUpstream("tenant-a", "rebind-target")
	upstream.Endpoint = "https://mcp.example.com/mcp"
	if err := ValidateMCPUpstream(context.Background(), &upstream, string(PolicyModeObserve), nil); err != nil {
		t.Fatalf("registration Validate: %v", err)
	}
	if len(upstream.ResolvedIPs) == 0 {
		t.Fatalf("expected ResolvedIPs to be populated at registration; got %v", upstream.ResolvedIPs)
	}

	// Use-time DNS rebinds to AWS IMDS.
	MCPHostLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "mcp.example.com" {
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}

	err := RevalidateMCPUpstreamAtUse(context.Background(), &upstream)
	if !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("RevalidateAtUse error = %v, want ErrUnsafeEndpoint (DNS rebound to 169.254.169.254)", err)
	}
}

// TestRevalidateMCPUpstreamAtUse_AcceptsUnchangedPublicIP ensures the
// mitigation does not regress legitimate use when DNS still returns the
// original public IP.
func TestRevalidateMCPUpstreamAtUse_AcceptsUnchangedPublicIP(t *testing.T) {
	prev := MCPHostLookup
	t.Cleanup(func() { MCPHostLookup = prev })

	MCPHostLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "mcp.example.com" {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}
	upstream := validMCPUpstream("tenant-a", "stable-host")
	upstream.Endpoint = "https://mcp.example.com/mcp"
	if err := ValidateMCPUpstream(context.Background(), &upstream, string(PolicyModeObserve), nil); err != nil {
		t.Fatalf("registration Validate: %v", err)
	}
	if err := RevalidateMCPUpstreamAtUse(context.Background(), &upstream); err != nil {
		t.Fatalf("RevalidateAtUse unchanged DNS error = %v, want nil", err)
	}
}

// TestRevalidateMCPUpstreamAtUse_RejectsDifferentPublicIP ensures the
// use-time guard compares current DNS against the registration-time pins,
// not merely against the unsafe/private IP deny-set. Public-to-public DNS
// drift is still DNS rebinding and must fail closed before dialing.
func TestRevalidateMCPUpstreamAtUse_RejectsDifferentPublicIP(t *testing.T) {
	prev := MCPHostLookup
	t.Cleanup(func() { MCPHostLookup = prev })

	MCPHostLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "mcp.example.com" {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}
	upstream := validMCPUpstream("tenant-a", "public-rebind")
	upstream.Endpoint = "https://mcp.example.com/mcp"
	if err := ValidateMCPUpstream(context.Background(), &upstream, string(PolicyModeObserve), nil); err != nil {
		t.Fatalf("registration Validate: %v", err)
	}

	MCPHostLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "mcp.example.com" {
			return []net.IP{net.ParseIP("198.51.100.20")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}
	err := RevalidateMCPUpstreamAtUse(context.Background(), &upstream)
	if !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("RevalidateAtUse error = %v, want ErrUnsafeEndpoint for public IP drift", err)
	}
}

// TestRevalidateMCPUpstreamAtUse_RejectsLiteralLoopback covers the
// IP-literal endpoint case — RevalidateAtUse must still refuse a loopback
// or RFC1918 IP literal even when DNS is not involved.
func TestRevalidateMCPUpstreamAtUse_RejectsLiteralLoopback(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "ip-literal-loop")
	upstream.Endpoint = "http://127.0.0.1:8080/mcp"
	upstream.ResolvedIPs = []string{"127.0.0.1"}
	err := RevalidateMCPUpstreamAtUse(context.Background(), &upstream)
	if !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("RevalidateAtUse loopback literal error = %v, want ErrUnsafeEndpoint", err)
	}
}
