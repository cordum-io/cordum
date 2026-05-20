package edge

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

func revalidateMCPUpstreamURLAtUse(ctx context.Context, raw string) ([]string, error) {
	ips, err := resolveMCPUpstreamEndpointIPs(ctx, raw)
	if err != nil {
		return nil, err
	}
	for _, rawIP := range ips {
		ip := net.ParseIP(rawIP)
		if ip == nil || mcpIPUnsafe(ip) {
			return nil, ErrUnsafeEndpoint
		}
	}
	return ips, nil
}

func resolveMCPUpstreamEndpointIPs(ctx context.Context, raw string) ([]string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("%w: endpoint", ErrUnsafeEndpoint)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, ErrUnsafeEndpoint
	}
	host := normalizeMCPUpstreamHost(u.Hostname())
	if _, denied := mcpCloudMetadataHosts[strings.ToLower(host)]; denied {
		return nil, ErrUnsafeEndpoint
	}
	return lookupMCPUpstreamHostIPs(ctx, host)
}

func lookupMCPUpstreamHostIPs(ctx context.Context, host string) ([]string, error) {
	if host == "" || strings.EqualFold(host, "localhost") {
		return nil, ErrUnsafeEndpoint
	}
	if ip := net.ParseIP(host); ip != nil {
		return []string{ip.String()}, nil
	}
	ips, err := MCPHostLookup(ctx, host)
	if err != nil {
		return nil, ErrUnsafeEndpoint
	}
	return normalizeMCPUpstreamIPs(ips), nil
}

func mcpResolvedIPsMatch(pinned, current []string) bool {
	want := normalizeMCPUpstreamIPStrings(pinned)
	got := normalizeMCPUpstreamIPStrings(current)
	if len(want) == 0 || len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

func pinMCPUpstreamIPs(ctx context.Context, raw string) []string {
	ips, err := resolveMCPUpstreamEndpointIPs(ctx, raw)
	if err != nil {
		return nil
	}
	return ips
}

func normalizeMCPUpstreamHost(host string) string {
	host = strings.TrimSpace(host)
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	return strings.TrimSuffix(host, ".")
}

func normalizeMCPUpstreamIPs(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	seen := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		s := ip.String()
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func normalizeMCPUpstreamIPStrings(ips []string) []string {
	out := make([]string, 0, len(ips))
	seen := make(map[string]struct{}, len(ips))
	for _, raw := range ips {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil {
			continue
		}
		s := ip.String()
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
