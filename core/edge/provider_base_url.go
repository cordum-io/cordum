package edge

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
)

const providerBaseURLResolveTimeout = 2 * time.Second

// ValidateProviderBaseURL validates a managed provider/LLM-proxy base URL and
// returns a canonical, safe value suitable for Claude managed settings.
func ValidateProviderBaseURL(ctx context.Context, raw string) (string, error) {
	u, err := parseProviderBaseURL(raw)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(normalizeMCPUpstreamHost(u.Hostname()))
	if err := validateProviderBaseURLParts(ctx, u, host); err != nil {
		return "", err
	}
	return canonicalProviderBaseURL(u, host), nil
}

func parseProviderBaseURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("provider_base_url required")
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" {
		return nil, errors.New("provider_base_url malformed")
	}
	return u, nil
}

func validateProviderBaseURLParts(ctx context.Context, u *url.URL, host string) error {
	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "https":
	default:
		return errors.New("provider_base_url scheme must be https")
	}
	if host == "" || u.Host == "" {
		return errors.New("provider_base_url host required")
	}
	if u.User != nil {
		return errors.New("provider_base_url credentials rejected")
	}
	if u.RawQuery != "" {
		return errors.New("provider_base_url query rejected")
	}
	if u.Fragment != "" {
		return errors.New("provider_base_url fragment rejected")
	}
	return validateProviderBaseURLHost(ctx, host)
}

func validateProviderBaseURLHost(ctx context.Context, host string) error {
	if _, denied := mcpCloudMetadataHosts[host]; denied {
		return errors.New("provider_base_url metadata host rejected")
	}
	unsafe, err := providerBaseURLHostResolvesUnsafe(ctx, host)
	if err != nil {
		return nil
	}
	if unsafe {
		return errors.New("provider_base_url unsafe host rejected")
	}
	return nil
}

func providerBaseURLHostResolvesUnsafe(ctx context.Context, host string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resolveCtx, cancel := context.WithTimeout(ctx, providerBaseURLResolveTimeout)
	defer cancel()
	return mcpHostResolvesUnsafe(resolveCtx, host)
}

func canonicalProviderBaseURL(u *url.URL, host string) string {
	clean := *u
	clean.Scheme = "https"
	clean.Host = canonicalProviderHostPort(host, clean.Port())
	clean.Path = strings.TrimRight(clean.Path, "/")
	clean.RawPath = ""
	clean.RawQuery = ""
	clean.Fragment = ""
	clean.User = nil
	return clean.String()
}

func canonicalProviderHostPort(host, port string) string {
	if port == "" {
		if ip := net.ParseIP(host); ip != nil && strings.Contains(host, ":") {
			return "[" + ip.String() + "]"
		}
		return host
	}
	return net.JoinHostPort(host, port)
}
