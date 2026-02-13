package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const outboundResolveTimeout = 2 * time.Second

// outboundPrivateIPNets mirror gateway SSRF protections for private/link-local ranges.
var outboundPrivateIPNets = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",      // IPv4 unspecified
		"10.0.0.0/8",     // RFC 1918
		"100.64.0.0/10",  // carrier-grade NAT
		"127.0.0.0/8",    // IPv4 loopback
		"169.254.0.0/16", // IPv4 link-local / metadata
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"::1/128",        // IPv6 loopback
		"fe80::/10",      // IPv6 link-local
		"fc00::/7",       // IPv6 unique-local
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("invalid CIDR in outboundPrivateIPNets: " + cidr)
		}
		nets = append(nets, n)
	}
	return nets
}()

var outboundPrivateHostnames = map[string]bool{
	"localhost":                true,
	"metadata.google.internal": true,
}

// outboundLookupHostIPs resolves hostnames for outbound URL validation.
var outboundLookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if addr.IP != nil {
			ips = append(ips, addr.IP)
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("no resolved IPs")
	}
	return ips, nil
}

func normalizeAllowedHosts(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		host := strings.ToLower(strings.TrimSpace(entry))
		if host == "" {
			continue
		}
		if strings.Contains(host, "://") {
			if parsed, err := url.Parse(host); err == nil {
				host = strings.ToLower(strings.TrimSpace(parsed.Hostname()))
			}
		}
		if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			host = parsedHost
		}
		host = strings.Trim(host, "[]")
		host = strings.TrimPrefix(host, ".")
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	return out
}

func validateOutboundTargetURL(ctx context.Context, target *url.URL, allowedHosts []string, allowPrivateHosts bool) error {
	if target == nil {
		return errors.New("target URL required")
	}
	switch strings.ToLower(strings.TrimSpace(target.Scheme)) {
	case "http", "https":
		// allowed
	default:
		return fmt.Errorf("unsupported URL scheme %q", target.Scheme)
	}

	host := strings.ToLower(strings.TrimSpace(target.Hostname()))
	if host == "" {
		return errors.New("target URL missing host")
	}
	if len(allowedHosts) > 0 && !allowedHost(host, allowedHosts) {
		return fmt.Errorf("target host not allowed: %s", host)
	}
	if allowPrivateHosts {
		return nil
	}
	if outboundPrivateHostnames[host] {
		return fmt.Errorf("target host resolves to private address: %s", host)
	}

	ips, err := resolveHostIPs(ctx, host)
	if err != nil {
		return fmt.Errorf("target host resolution failed: %w", err)
	}
	for _, ip := range ips {
		if isPrivateOutboundIP(ip) {
			return fmt.Errorf("target host resolves to private address: %s", host)
		}
	}
	return nil
}

func allowedHost(host string, allowlist []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, entry := range allowlist {
		entry = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(entry, ".")))
		if entry == "" {
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

func resolveHostIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resolveCtx, cancel := context.WithTimeout(ctx, outboundResolveTimeout)
	defer cancel()
	return outboundLookupHostIPs(resolveCtx, host)
}

func isPrivateOutboundIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, n := range outboundPrivateIPNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
