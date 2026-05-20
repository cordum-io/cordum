package edge

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

const secretRefPrefix = "secret://"

var (
	mcpUpstreamNameRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
	mcpUpstreamTenantRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
)

// MCPHostLookup resolves a hostname to IPs. Override in tests to inject
// controlled DNS responses (e.g. to simulate DNS rebinding). Defaults to
// the system resolver.
var MCPHostLookup = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// MCPUpstreamValidationOptions carries trusted operator-only exceptions.
// Callers must never populate these from request query/body parameters.
type MCPUpstreamValidationOptions struct {
	AllowPlainHTTP bool
}

func Validate(ctx context.Context, upstream *UpstreamServer, policyMode string, allowlist []string) error {
	return ValidateMCPUpstream(ctx, upstream, policyMode, allowlist)
}

func ValidateMCPUpstream(ctx context.Context, upstream *UpstreamServer, policyMode string, allowlist []string) error {
	return ValidateMCPUpstreamWithOptions(ctx, upstream, policyMode, allowlist, MCPUpstreamValidationOptions{})
}

func ValidateMCPUpstreamWithOptions(ctx context.Context, upstream *UpstreamServer, policyMode string, allowlist []string, opts MCPUpstreamValidationOptions) error {
	if upstream == nil {
		return ErrInvalidUpstream
	}
	name := strings.TrimSpace(upstream.Name)
	if !mcpUpstreamNameRE.MatchString(name) {
		return fmt.Errorf("%w: name", ErrInvalidUpstream)
	}
	tenantID := strings.TrimSpace(upstream.TenantID)
	if tenantID == "" || (tenantID != "*" && !mcpUpstreamTenantRE.MatchString(tenantID)) {
		return fmt.Errorf("%w: tenant_id", ErrInvalidUpstream)
	}
	if err := validateMCPUpstreamSecret(upstream.AuthSecretRef); err != nil {
		return err
	}
	if isMCPEnterpriseStrict(policyMode) && !mcpUpstreamAllowed(name, allowlist) {
		return ErrUpstreamNotAllowlisted
	}
	if err := validateMCPUpstreamRisk(upstream.Risk); err != nil {
		return err
	}
	switch strings.TrimSpace(upstream.Transport) {
	case "http", "sse":
		if err := validateMCPUpstreamURL(ctx, upstream.Endpoint, policyMode, opts); err != nil {
			return err
		}
		upstream.ResolvedIPs = pinMCPUpstreamIPs(ctx, upstream.Endpoint)
		return nil
	case "stdio":
		return validateMCPUpstreamCommand(upstream.Command)
	default:
		return ErrInvalidTransport
	}
}

// RevalidateMCPUpstreamAtUse re-resolves the upstream's hostname (or
// re-parses its IP literal) and rejects the call if any current
// resolution falls into the SSRF deny-set (loopback, RFC1918,
// link-local incl. 169.254.169.254, IPv6 ULA, multicast, unspecified)
// or if a cloud-metadata hostname is in play. Callers MUST invoke this
// immediately before dialing an upstream so that a hostname which
// resolved to a public IP at registration but has since been rebound
// to internal infrastructure is refused at connect time.
func RevalidateMCPUpstreamAtUse(ctx context.Context, upstream *UpstreamServer) error {
	if upstream == nil {
		return ErrInvalidUpstream
	}
	switch strings.TrimSpace(upstream.Transport) {
	case "http", "sse":
		current, err := revalidateMCPUpstreamURLAtUse(ctx, upstream.Endpoint)
		if err != nil {
			return err
		}
		if !mcpResolvedIPsMatch(upstream.ResolvedIPs, current) {
			return ErrUnsafeEndpoint
		}
		return nil
	case "stdio", "":
		return nil
	default:
		return ErrInvalidTransport
	}
}

func validateMCPUpstreamSecret(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if !strings.HasPrefix(ref, secretRefPrefix) {
		return ErrSecretMustUseRef
	}
	return nil
}

func validateMCPUpstreamRisk(risk string) error {
	switch strings.TrimSpace(risk) {
	case "", "low", "medium", "high", "critical":
		return nil
	default:
		return fmt.Errorf("%w: risk", ErrInvalidUpstream)
	}
}

// mcpCloudMetadataHosts is a deny-list of well-known cloud-provider
// instance-metadata hostnames. Match is case-insensitive and trailing-dot
// FQDN form is normalized away before lookup. These hosts have no
// legitimate MCP upstream use case; allowing them would enable SSRF onto
// the cloud provider's metadata endpoint.
var mcpCloudMetadataHosts = map[string]struct{}{
	"metadata.google.internal":   {},
	"metadata.azure.com":         {},
	"instance-data.ec2.internal": {},
}

func validateMCPUpstreamURL(ctx context.Context, raw, policyMode string, opts MCPUpstreamValidationOptions) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%w: endpoint", ErrUnsafeEndpoint)
	}
	strict := isMCPEnterpriseStrict(policyMode)
	host := normalizeMCPUpstreamHost(u.Hostname())
	// Cloud-metadata hostname check (pre-DNS): rejects in ALL modes.
	if _, denied := mcpCloudMetadataHosts[strings.ToLower(host)]; denied {
		return ErrUnsafeEndpoint
	}
	// Internal-host SSRF rejection (RFC1918, link-local incl 169.254.169.254,
	// IPv6 ULA + link-local, multicast, unspecified, loopback) runs in
	// EVERY mode — these are SSRF vectors with no legitimate MCP use case.
	unsafe, err := mcpHostResolvesUnsafe(ctx, host)
	if err != nil {
		// Fail-closed in strict mode; in observe/enforce, refuse only on
		// confirmed unsafe (DNS failure on a free-form hostname is not
		// itself an SSRF signal — caller-supplied DNS errors shouldn't
		// block legitimate public hosts at observe-time).
		if strict {
			return ErrUnsafeEndpoint
		}
	} else if unsafe {
		return ErrUnsafeEndpoint
	}
	if u.Scheme == "http" && !opts.AllowPlainHTTP {
		return ErrUnsafeEndpoint
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return ErrUnsafeEndpoint
	}
	return nil
}

func mcpHostResolvesUnsafe(ctx context.Context, host string) (bool, error) {
	if host == "" {
		return true, nil
	}
	if strings.EqualFold(host, "localhost") {
		return true, nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return mcpIPUnsafe(ip), nil
	}
	ips, err := MCPHostLookup(ctx, host)
	if err != nil {
		return false, err
	}
	for _, ip := range ips {
		if mcpIPUnsafe(ip) {
			return true, nil
		}
	}
	return false, nil
}

// mcpIPUnsafe rejects all internal/private/metadata-reachable address
// classes that have no legitimate MCP upstream use case. Includes RFC1918
// private (10/8, 172.16/12, 192.168/16), loopback (127/8, ::1), link-local
// v4 (169.254/16, covers all cloud IMDS endpoints), link-local v6 (fe80::/10),
// IPv6 ULA (fc00::/7), multicast (224.0.0.0/4, ff00::/8), and unspecified
// (0.0.0.0, ::). Also handles IPv4-mapped IPv6 by unwrapping to v4.
func mcpIPUnsafe(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Unwrap IPv4-mapped IPv6 (e.g. ::ffff:169.254.169.254) so the check
	// applies to the v4 class.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// IPv6 unique-local addresses (fc00::/7 = fc00::/8 + fd00::/8).
	// stdlib IsPrivate() covers this since Go 1.17, but be defensive in case
	// of future stdlib changes.
	if len(ip) == net.IPv6len && (ip[0]&0xfe) == 0xfc {
		return true
	}
	return false
}

func validateMCPUpstreamCommand(command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("%w: command", ErrInvalidUpstream)
	}
	for _, part := range command {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("%w: command", ErrInvalidUpstream)
		}
		if mcpCommandHasShellMetachar(part) {
			return ErrShellMetacharsRejected
		}
	}
	return nil
}

func mcpCommandHasShellMetachar(part string) bool {
	for _, token := range []string{";", "&&", "||", "|", ">", "<", "`", "$("} {
		if strings.Contains(part, token) {
			return true
		}
	}
	return false
}

func isMCPEnterpriseStrict(policyMode string) bool {
	return strings.EqualFold(strings.TrimSpace(policyMode), string(PolicyModeEnterpriseStrict))
}

func mcpUpstreamAllowed(name string, allowlist []string) bool {
	for _, candidate := range allowlist {
		if strings.TrimSpace(candidate) == name {
			return true
		}
	}
	return false
}
