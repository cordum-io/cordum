package actiongates

import (
	"context"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"golang.org/x/sync/singleflight"
)

// URLGate blocks outbound URL access to cloud metadata services, known
// exfiltration destinations, non-literal hostnames that resolve to private,
// metadata, or otherwise non-routable IPs, and URLs whose query payload
// carries a recognized prompt-stash signature.
//
// Resolution uses an injectable HostResolver (default: net.DefaultResolver)
// so DNS/SSRF tests are deterministic. A bounded LRU cache memoizes
// successful resolution results briefly to keep per-request latency
// predictable without allowing attacker-chosen host cardinality to grow
// memory without limit. Resolver uncertainty fails closed; transport callers
// that later open sockets should still pin or revalidate the selected address
// at connect time to close DNS-rebinding TOCTOU gaps outside this policy gate.
type URLGate struct {
	resolver      HostResolver
	domainSeen    func(host string) bool
	resCache      *urlGateResolverCache
	resCacheTTL   time.Duration
	resolverGroup singleflight.Group
}

// MaxResolverCacheEntries bounds attacker-influenced resolver cache
// cardinality while leaving room for normal production host churn.
const MaxResolverCacheEntries = 2048

// ResolverCacheTTL is the default lifetime for successful DNS answers;
// stale entries are re-resolved so security decisions do not trust old IPs.
const ResolverCacheTTL = 5 * time.Minute

// URLGateOptions configures the URL gate. Resolver is required for DNS
// rebinding tests; DomainSeen is optional and feeds the REQUIRE_HUMAN
// path for PII POSTs to never-before-seen hosts (returns true for hosts
// already cached as approved). ResolverCacheMax bounds the resolver
// cache; zero or negative leaves the default in place.
type URLGateOptions struct {
	Resolver         HostResolver
	DomainSeen       func(host string) bool
	ResolverTTL      time.Duration
	ResolverCacheMax int
}

// NewURLGate constructs a URLGate. Resolver defaults to a net.LookupHost
// wrapper when unset.
func NewURLGate(opts URLGateOptions) *URLGate {
	resolver := opts.Resolver
	if resolver == nil {
		resolver = netResolver{}
	}
	ttl := opts.ResolverTTL
	if ttl <= 0 {
		ttl = ResolverCacheTTL
	}
	maxEntries := opts.ResolverCacheMax
	if maxEntries <= 0 {
		maxEntries = MaxResolverCacheEntries
	}
	return &URLGate{
		resolver:    resolver,
		domainSeen:  opts.DomainSeen,
		resCache:    newURLGateResolverCache(maxEntries),
		resCacheTTL: ttl,
	}
}

func (g *URLGate) ID() string { return GateIDURL }

type netResolver struct{}

func (netResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// metadataHosts: hostnames that always indicate a cloud metadata service.
var metadataHosts = map[string]string{
	"metadata.google.internal": "gcp",
	"metadata.azure.com":       "azure",
	"169.254.169.254":          "aws",
	"169.254.170.2":            "aws_ecs",
	"100.100.100.200":          "alibaba",
	"fd00:ec2::254":            "aws_v6",
	"fd00:ec2:0:0:0:0:0:254":   "aws_v6",
	"fe80::a9fe:a9fe":          "azure_link_local_v6",
	"fe80:0:0:0:0:0:a9fe:a9fe": "azure_link_local_v6",
}

// exfilHostPatterns: substring + suffix match against the hostname. Suffixes
// MUST start with a dot or match the full host to avoid over-refusal
// (`example.ngrok.io.evil.com` should still be denied, while `ngrok.io.app`
// would only false-positive on exact `ngrok.io` ownership, which we accept).
var exfilHostPatterns = []struct {
	pattern   string
	subReason string
}{
	{"webhook.site", "exfil_host:webhook_site"},
	{".ngrok.io", "exfil_host:ngrok_io"},
	{".ngrok-free.app", "exfil_host:ngrok_free"},
	{".serveo.net", "exfil_host:serveo"},
	{".pipedream.net", "exfil_host:pipedream"},
	{".requestbin.com", "exfil_host:requestbin"},
	{".beeceptor.com", "exfil_host:beeceptor"},
	{".burpcollaborator.net", "exfil_host:burp"},
	{"burpcollaborator.net", "exfil_host:burp"},
	{"canarytokens.com", "exfil_host:canarytokens"},
	{".canarytokens.com", "exfil_host:canarytokens"},
	{".interactsh.com", "exfil_host:interactsh"},
}

// rebindDomains: well-known wildcard-IP DNS services. A hit means we MUST
// resolve and reject if the IP is private/link-local.
var rebindDomains = []string{".nip.io", ".sslip.io", ".xip.io"}

// pastePatterns: (host suffix, path prefix). Match when verb is a write
// (POST/PUT-style), since browsing a known paste is a read of public content.
var pastePatterns = []struct {
	hostSuffix string
	pathPrefix string
	subReason  string
}{
	{"pastebin.com", "/api/", "paste:pastebin_api"},
	{"gist.github.com", "/api/", "paste:gist_api"},
}

// promptStashKeys are JSON object keys whose presence inside a large query
// value strongly indicates prompt/context exfil.
var promptStashKeys = []string{
	`"messages"`, `"system"`, `"prompt"`, `"context_window"`, `"tools"`, `"conversation"`,
}

// promptExfilQueryLenThreshold is the per-query-value length (in bytes) above
// which we look for prompt stash keys. Small payloads pass through unchanged.
const promptExfilQueryLenThreshold = 1024

func (g *URLGate) Evaluate(ctx context.Context, in *config.PolicyInput) ActionGateDecision {
	if in == nil || in.Action == nil {
		return ActionGateDecision{}
	}
	act := in.Action
	if act.Kind != config.ActionKindURL {
		return ActionGateDecision{}
	}
	raw := strings.TrimSpace(act.TargetURL)
	if raw == "" {
		return ActionGateDecision{}
	}

	u, err := url.Parse(raw)
	if err != nil {
		return g.deny(CodeAccessDenied, "malformed URL denied", "malformed_url", raw, "", false)
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return g.deny(CodeAccessDenied, "unsupported URL scheme denied", "unsupported_scheme", raw, strings.ToLower(u.Hostname()), u.User != nil)
	}
	if u.Host == "" || u.Hostname() == "" {
		return g.deny(CodeAccessDenied, "URL host required", "missing_host", raw, "", u.User != nil)
	}

	host := strings.ToLower(u.Hostname())
	port := u.Port()
	_ = port // reserved for future per-port rules

	// Strip IPv4-in-IPv6 prefix (e.g. ::ffff:169.254.169.254) when present so
	// the IP-class checks see the underlying v4 literal.
	host = unmapIPv4InIPv6(host)

	// userinfo-based bypass (http://google.com@169.254.169.254/): u.Host is
	// the authority post-userinfo, so this is already captured. We still flag
	// userinfo presence in audit so SIEM can review.
	hasUserinfo := u.User != nil

	// 1) Direct metadata-host hit by name or by literal IP.
	if sub, ok := metadataHosts[host]; ok {
		return g.deny(CodeAccessDenied, "metadata service access denied", "metadata_service:"+sub, raw, host, hasUserinfo)
	}

	// 2) Literal link-local / RFC1918 / loopback / multicast / unique-local.
	literalHost := false
	if ip := net.ParseIP(host); ip != nil {
		literalHost = true
		if class, ok := privateIPClass(ip); ok {
			subReason := "link_local"
			if class != "link_local" {
				subReason = class
			}
			return g.deny(CodeAccessDenied, "metadata service access denied", "metadata_service:"+subReason, raw, host, hasUserinfo)
		}
	}

	// 3) Known exfil hosts.
	if reason, ok := matchExfilHost(host); ok {
		return g.deny(CodeAccessDenied, "exfiltration destination denied", reason, raw, host, hasUserinfo)
	}

	// 4) Paste destinations on write verbs.
	if isWriteVerb(act.Verb) {
		if reason, ok := matchPaste(host, u.Path); ok {
			return g.deny(CodeAccessDenied, "paste destination denied", reason, raw, host, hasUserinfo)
		}
	}

	// 5) Prompt-exfil signature scan on query payload.
	if hasPromptExfilSignature(u) {
		return g.deny(CodeAccessDenied, "prompt context exfiltration denied", "prompt_exfil", raw, host, hasUserinfo)
	}

	// 6) Resolve non-literal hostnames and reject any private, metadata, or
	// otherwise non-routable answer. Resolver errors fail closed: when the
	// gate cannot prove the target is public, it must not let governed URL
	// actions proceed.
	if !literalHost {
		subReasonPrefix := "dns_resolution"
		if isRebindWildcardHost(host) {
			subReasonPrefix = "dns_rebind"
		}
		if dec, denied := g.denyUnsafeResolvedHost(ctx, host, subReasonPrefix, raw, hasUserinfo); denied {
			return dec
		}
	}

	// 7) PII POST to never-before-seen host -> REQUIRE_HUMAN.
	if isWriteVerb(act.Verb) && containsRiskTag(act.RiskTags, "data:pii") {
		seen := false
		if g.domainSeen != nil {
			seen = g.domainSeen(host)
		}
		if !seen {
			return ActionGateDecision{
				Decision:  pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
				GateID:    GateIDURL,
				Code:      CodeRequireHuman,
				Reason:    "human approval required for PII upload to new destination",
				SubReason: "pii_post:new_host",
				Extra: map[string]string{
					"gate":       GateIDURL,
					"sub_reason": "pii_post:new_host",
					"host":       host,
				},
			}
		}
	}

	return ActionGateDecision{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, GateID: GateIDURL}
}

func (g *URLGate) denyUnsafeResolvedHost(ctx context.Context, host, subReasonPrefix, raw string, hasUserinfo bool) (ActionGateDecision, bool) {
	ips, _, err := g.resolveOrFail(ctx, host)
	if err != nil {
		return g.deny(CodeResolverError, "unable to validate destination host", subReasonPrefix+":resolver_error", raw, host, hasUserinfo), true
	}
	for _, ip := range ips {
		if class, ok := resolvedIPClass(ip); ok {
			reason := "private destination host denied"
			if subReasonPrefix == "dns_rebind" {
				reason = "dns rebinding denied"
			}
			return g.deny(CodeAccessDenied, reason, subReasonPrefix+":"+class, raw, host, hasUserinfo), true
		}
	}
	return ActionGateDecision{}, false
}

func (g *URLGate) deny(code, reason, subReason, raw, host string, hasUserinfo bool) ActionGateDecision {
	extra := map[string]string{
		"gate":       GateIDURL,
		"sub_reason": subReason,
		"host":       host,
		"raw_len":    itoa(len(raw)),
	}
	if hasUserinfo {
		extra["userinfo_present"] = "true"
	}
	return ActionGateDecision{
		Decision:  pb.DecisionType_DECISION_TYPE_DENY,
		GateID:    GateIDURL,
		Code:      code,
		Reason:    reason,
		SubReason: subReason,
		Extra:     extra,
	}
}

func (g *URLGate) resolveOrFail(ctx context.Context, host string) ([]net.IP, bool, error) {
	key := normalizeResolverCacheKey(host)
	if ips, ok := g.resCache.get(key, time.Now()); ok {
		return ips, true, nil
	}

	value, err, _ := g.resolverGroup.Do(key, func() (any, error) {
		if ips, ok := g.resCache.get(key, time.Now()); ok {
			return resolverLookupResult{ips: ips, cached: true}, nil
		}
		ips, err := g.resolver.LookupHost(ctx, key)
		if err != nil {
			return resolverLookupResult{}, err
		}
		parsed, err := parseResolvedIPs(ips)
		if err != nil {
			return resolverLookupResult{}, err
		}
		evictions := g.resCache.put(key, parsed, time.Now().Add(g.resCacheTTL))
		recordURLGateResolverCacheEvictions(evictions)
		return resolverLookupResult{ips: parsed}, nil
	})
	if err != nil {
		return nil, false, err
	}
	res, ok := value.(resolverLookupResult)
	if !ok {
		return nil, false, errUnexpectedResolverResult
	}
	return res.ips, res.cached, nil
}

func parseResolvedIPs(values []string) ([]net.IP, error) {
	if len(values) == 0 {
		return nil, errResolverNoAddresses
	}
	ips := make([]net.IP, 0, len(values))
	for _, value := range values {
		ip := net.ParseIP(strings.TrimSpace(value))
		if ip == nil {
			return nil, errResolverInvalidAddress
		}
		ips = append(ips, cloneResolverIP(ip))
	}
	if len(ips) == 0 {
		return nil, errResolverNoAddresses
	}
	return ips, nil
}

// unmapIPv4InIPv6 normalizes ::ffff:1.2.3.4 (IPv4-mapped IPv6) and the brackets
// around v6 hostnames into a literal v4 string when applicable. `host` is
// already lowercased and bracketed v6 had its brackets stripped by url.Hostname.
func unmapIPv4InIPv6(host string) string {
	if !strings.Contains(host, ":") {
		return host
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return host
}

func resolvedIPClass(ip net.IP) (string, bool) {
	if class, ok := privateIPClass(ip); ok {
		return class, true
	}
	if ip == nil {
		return "", false
	}
	if sub, ok := metadataHosts[ip.String()]; ok {
		return "metadata_service:" + sub, true
	}
	return "", false
}

func privateIPClass(ip net.IP) (string, bool) {
	if ip == nil {
		return "", false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() {
		return "loopback", true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return "link_local", true
	}
	if ip.IsUnspecified() {
		return "unspecified", true
	}
	// Unique local (fc00::/7).
	if v6 := ip.To16(); v6 != nil && ip.To4() == nil {
		if v6[0]&0xfe == 0xfc {
			return "unique_local", true
		}
	}
	if ip.IsPrivate() {
		return "rfc1918", true
	}
	if ip.IsMulticast() {
		return "multicast", true
	}
	return "", false
}

func matchExfilHost(host string) (string, bool) {
	for _, p := range exfilHostPatterns {
		if p.pattern == host {
			return p.subReason, true
		}
		if strings.HasPrefix(p.pattern, ".") && strings.HasSuffix(host, p.pattern) {
			return p.subReason, true
		}
	}
	return "", false
}

func isRebindWildcardHost(host string) bool {
	for _, d := range rebindDomains {
		if strings.HasSuffix(host, d) {
			return true
		}
	}
	return false
}

func matchPaste(host, path string) (string, bool) {
	for _, p := range pastePatterns {
		if host == p.hostSuffix && strings.HasPrefix(path, p.pathPrefix) {
			return p.subReason, true
		}
	}
	return "", false
}

func hasPromptExfilSignature(u *url.URL) bool {
	if u.RawQuery == "" {
		return false
	}
	q := u.Query()
	for _, vals := range q {
		for _, v := range vals {
			if len(v) < promptExfilQueryLenThreshold {
				continue
			}
			if !looksLikeJSONWithStashKey(v) {
				continue
			}
			return true
		}
	}
	return false
}

func looksLikeJSONWithStashKey(s string) bool {
	// Cheap heuristic: starts/contains a JSON object and has at least one stash key.
	if !strings.ContainsAny(s, "{[") {
		return false
	}
	for _, k := range promptStashKeys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func containsRiskTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}
