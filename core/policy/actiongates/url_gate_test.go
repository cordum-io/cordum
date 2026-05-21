package actiongates

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type fakeHostResolver struct {
	mu               sync.Mutex
	resolve          map[string][]string
	orderedResponses map[string][][]string
	err              map[string]error
	orderedErrors    map[string][]error
	calls            map[string]int
	started          chan<- string
	waitBeforeReturn <-chan struct{}
}

func (r *fakeHostResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	ips, err := r.resolveFor(host)
	if r.started != nil {
		select {
		case r.started <- host:
		default:
		}
	}
	if r.waitBeforeReturn != nil {
		select {
		case <-r.waitBeforeReturn:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return ips, err
}

func (r *fakeHostResolver) resolveFor(host string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = make(map[string]int)
	}
	r.calls[host]++
	callIndex := r.calls[host] - 1
	if errs := r.orderedErrors[host]; callIndex < len(errs) && errs[callIndex] != nil {
		return nil, errs[callIndex]
	}
	if err, ok := r.err[host]; ok {
		return nil, err
	}
	if responses := r.orderedResponses[host]; callIndex < len(responses) {
		return cloneStrings(responses[callIndex]), nil
	}
	if ips, ok := r.resolve[host]; ok {
		return cloneStrings(ips), nil
	}
	return []string{"203.0.113.5"}, nil
}

func (r *fakeHostResolver) callsFor(host string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[host]
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

type urlGateCase struct {
	name         string
	url          string
	verb         config.ActionVerb
	riskTags     []string
	resolver     *fakeHostResolver
	wantDecision pb.DecisionType
	wantCode     string
	subReasonHas string
}

type ordinaryDNSCase struct {
	name         string
	host         string
	answers      []string
	resolverErr  error
	wantDecision pb.DecisionType
	wantCode     string
	subReasonHas string
}

var ordinaryDNSSSRFCases = []ordinaryDNSCase{
	{name: "internal_example_loopback", host: "internal.example", answers: []string{"127.0.0.1"}, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_resolution:loopback"},
	{name: "internal_example_private", host: "internal.example", answers: []string{"10.0.0.7"}, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_resolution:rfc1918"},
	{name: "internal_example_metadata_link_local", host: "internal.example", answers: []string{"169.254.169.254"}, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_resolution:link_local"},
	{name: "internal_example_unique_local", host: "internal.example", answers: []string{"fd00::1"}, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_resolution:unique_local"},
	{name: "internal_example_unspecified", host: "internal.example", answers: []string{"0.0.0.0"}, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_resolution:unspecified"},
	{name: "internal_example_multicast", host: "internal.example", answers: []string{"239.255.0.1"}, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_resolution:multicast"},
	{name: "mixed_public_and_private_answer_denied", host: "mixed.example", answers: []string{"93.184.216.34", "10.0.0.7"}, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_resolution:rfc1918"},
	{name: "public_answer_allowed", host: "public.example", answers: []string{"93.184.216.34"}, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
	{name: "resolver_error_denied", host: "resolver-error.example", resolverErr: errResolverUnavailable, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeResolverError, subReasonHas: "dns_resolution:resolver_error"},
	{name: "empty_answer_denied", host: "empty.example", answers: []string{}, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeResolverError, subReasonHas: "dns_resolution:resolver_error"},
	{name: "malformed_answer_denied", host: "malformed.example", answers: []string{"not-an-ip"}, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeResolverError, subReasonHas: "dns_resolution:resolver_error"},
}

type deterministicDenialCase struct {
	name         string
	host         string
	url          string
	verb         config.ActionVerb
	subReasonHas string
}

var deterministicDenialBeforeDNSCases = []deterministicDenialCase{
	{name: "known_exfil_host", host: "webhook.site", url: "https://webhook.site/abc-123", verb: config.ActionVerbWrite, subReasonHas: "exfil_host"},
	{name: "paste_write", host: "pastebin.com", url: "https://pastebin.com/api/api_post.php", verb: config.ActionVerbWrite, subReasonHas: "paste"},
	{name: "prompt_exfil_signature", host: "attacker.example", url: promptExfilURL("attacker.example"), verb: config.ActionVerbWrite, subReasonHas: "prompt_exfil"},
}

func runURLGate(t *testing.T, tc urlGateCase) {
	t.Helper()
	resolver := tc.resolver
	if resolver == nil {
		resolver = &fakeHostResolver{}
	}
	gate := NewURLGate(URLGateOptions{Resolver: resolver})
	in := &config.PolicyInput{
		Tenant: "tnt_a",
		Action: &config.ActionDescriptor{
			Kind:      config.ActionKindURL,
			Verb:      tc.verb,
			TargetURL: tc.url,
			RiskTags:  tc.riskTags,
		},
	}
	dec := gate.Evaluate(context.Background(), in)
	if dec.Decision != tc.wantDecision {
		t.Fatalf("decision = %v, want %v (url=%q verb=%q reason=%q subReason=%q)", dec.Decision, tc.wantDecision, tc.url, tc.verb, dec.Reason, dec.SubReason)
	}
	if tc.wantCode != "" && dec.Code != tc.wantCode {
		t.Fatalf("code = %q, want %q", dec.Code, tc.wantCode)
	}
	if tc.subReasonHas != "" && !strings.Contains(dec.SubReason, tc.subReasonHas) {
		t.Fatalf("subReason = %q, want substring %q", dec.SubReason, tc.subReasonHas)
	}
}

func TestURLGate_SkipsNonURLKind(t *testing.T) {
	t.Parallel()
	gate := NewURLGate(URLGateOptions{Resolver: &fakeHostResolver{}})

	if dec := gate.Evaluate(context.Background(), &config.PolicyInput{}); dec.Fired() {
		t.Fatal("nil action: gate fired")
	}
	if dec := gate.Evaluate(context.Background(), &config.PolicyInput{
		Action: &config.ActionDescriptor{Kind: config.ActionKindFile, TargetPath: "/tmp/x"},
	}); dec.Fired() {
		t.Fatal("file kind: gate fired")
	}
	if dec := gate.Evaluate(context.Background(), &config.PolicyInput{
		Action: &config.ActionDescriptor{Kind: config.ActionKindURL, TargetURL: ""},
	}); dec.Fired() {
		t.Fatal("empty url: gate fired")
	}
}

func TestURLGate_DenyCloudMetadataServices(t *testing.T) {
	t.Parallel()
	cases := []urlGateCase{
		{name: "aws_imds_v4", url: "http://169.254.169.254/latest/meta-data/iam/security-credentials/", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "metadata_service"},
		{name: "aws_imds_v6", url: "http://[fd00:ec2::254]/latest/meta-data/", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "metadata_service"},
		{name: "ecs_creds", url: "http://169.254.170.2/v2/credentials/abc", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "metadata_service"},
		{name: "gcp_metadata_host", url: "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "metadata_service"},
		{name: "azure_imds_link_local_v6", url: "http://[fe80::a9fe:a9fe]/metadata/instance", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "link_local"},
		{name: "user_at_imds_bypass", url: "http://google.com@169.254.169.254/latest/meta-data/", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "metadata_service"},
		{name: "ipv4_in_ipv6", url: "http://[::ffff:169.254.169.254]/latest/meta-data/", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "metadata_service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runURLGate(t, tc) })
	}
}

func TestURLGate_DenyKnownExfilDestinations(t *testing.T) {
	t.Parallel()
	cases := []urlGateCase{
		{name: "webhook_site", url: "https://webhook.site/abc-123", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "exfil_host"},
		{name: "ngrok_io", url: "https://abc.ngrok.io/upload", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "exfil_host"},
		{name: "ngrok_free", url: "https://abc.ngrok-free.app/upload", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "exfil_host"},
		{name: "serveo", url: "https://abc.serveo.net/", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "exfil_host"},
		{name: "pipedream", url: "https://eohjvz.m.pipedream.net/", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "exfil_host"},
		{name: "requestbin", url: "https://abc.requestbin.com/notify", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "exfil_host"},
		{name: "beeceptor", url: "https://abc.beeceptor.com/path", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "exfil_host"},
		{name: "burp_collab", url: "https://abc.burpcollaborator.net/", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "exfil_host"},
		{name: "canarytokens", url: "https://canarytokens.com/test", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "exfil_host"},
		{name: "interactsh", url: "https://abc.interactsh.com/payload", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "exfil_host"},
		{name: "pastebin_api_post", url: "https://pastebin.com/api/api_post.php", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "paste"},
		{name: "gist_post", url: "https://gist.github.com/api/v3/gists", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "paste"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runURLGate(t, tc) })
	}
}

func TestURLGate_PastebinReadAllowed(t *testing.T) {
	t.Parallel()
	// pastebin.com and gist.github.com are read-allowed (browse a known paste).
	// Only POST/PUT-style writes hit the paste rule.
	runURLGate(t, urlGateCase{
		name:         "pastebin_read",
		url:          "https://pastebin.com/abcdef",
		verb:         config.ActionVerbRead,
		wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
	})
	runURLGate(t, urlGateCase{
		name:         "gist_read",
		url:          "https://gist.github.com/user/abcd",
		verb:         config.ActionVerbRead,
		wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
	})
}

func TestURLGate_DNSRebindingToRFC1918(t *testing.T) {
	t.Parallel()
	// *.nip.io and similar resolve to attacker-controlled IPs; gate must
	// resolve at eval time and DENY when the IP is RFC1918 / link-local.
	resolver := &fakeHostResolver{
		resolve: map[string][]string{
			"169-254-169-254.nip.io":    {"169.254.169.254"},
			"10-0-0-1.sslip.io":         {"10.0.0.1"},
			"192-168-1-1.xip.io":        {"192.168.1.1"},
			"172-16-0-1.nip.io":         {"172.16.0.1"},
			"public.example.com.nip.io": {"203.0.113.42"}, // public IP — allowed
		},
	}
	cases := []urlGateCase{
		{name: "nip_imds", url: "http://169-254-169-254.nip.io/latest/", verb: config.ActionVerbRead, resolver: resolver, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_rebind"},
		{name: "sslip_rfc1918", url: "http://10-0-0-1.sslip.io/", verb: config.ActionVerbRead, resolver: resolver, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_rebind"},
		{name: "xip_rfc1918", url: "http://192-168-1-1.xip.io/", verb: config.ActionVerbRead, resolver: resolver, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_rebind"},
		{name: "nip_carrier_grade_rfc1918", url: "http://172-16-0-1.nip.io/", verb: config.ActionVerbRead, resolver: resolver, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "dns_rebind"},
		{name: "nip_resolves_public_allowed", url: "http://public.example.com.nip.io/", verb: config.ActionVerbRead, resolver: resolver, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runURLGate(t, tc) })
	}
}

func TestURLGate_DNSResolvesOrdinaryHostnamesForSSRF(t *testing.T) {
	t.Parallel()
	for _, tc := range ordinaryDNSSSRFCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runOrdinaryDNSCase(t, tc)
		})
	}
}

func runOrdinaryDNSCase(t *testing.T, tc ordinaryDNSCase) {
	t.Helper()
	resolver := &fakeHostResolver{resolve: map[string][]string{tc.host: tc.answers}}
	if tc.resolverErr != nil {
		resolver.err = map[string]error{tc.host: tc.resolverErr}
	}
	dec := evalURL(NewURLGate(URLGateOptions{Resolver: resolver}), "https://"+tc.host+"/resource")
	if dec.Decision != tc.wantDecision {
		t.Fatalf("decision = %v, want %v (code=%q subReason=%q)", dec.Decision, tc.wantDecision, dec.Code, dec.SubReason)
	}
	if tc.wantCode != "" && dec.Code != tc.wantCode {
		t.Fatalf("code = %q, want %q", dec.Code, tc.wantCode)
	}
	if tc.subReasonHas != "" && !strings.Contains(dec.SubReason, tc.subReasonHas) {
		t.Fatalf("subReason = %q, want substring %q", dec.SubReason, tc.subReasonHas)
	}
	if got := resolver.callsFor(tc.host); got != 1 {
		t.Fatalf("resolver calls for %q = %d, want 1", tc.host, got)
	}
}

func TestURLGate_LiteralIPsDoNotUseResolver(t *testing.T) {
	t.Parallel()
	resolver := &fakeHostResolver{
		err: map[string]error{
			"169.254.169.254": errResolverUnavailable,
			"10.0.0.7":        errResolverUnavailable,
			"93.184.216.34":   errResolverUnavailable,
		},
	}
	gate := NewURLGate(URLGateOptions{Resolver: resolver})

	evaluateURL(t, gate, "http://169.254.169.254/latest/meta-data/", pb.DecisionType_DECISION_TYPE_DENY, CodeAccessDenied)
	evaluateURL(t, gate, "http://10.0.0.7/private", pb.DecisionType_DECISION_TYPE_DENY, CodeAccessDenied)
	evaluateURL(t, gate, "http://93.184.216.34/public", pb.DecisionType_DECISION_TYPE_ALLOW, "")
	if got := resolver.callsFor("169.254.169.254"); got != 0 {
		t.Fatalf("metadata literal resolver calls = %d, want 0", got)
	}
	if got := resolver.callsFor("10.0.0.7"); got != 0 {
		t.Fatalf("private literal resolver calls = %d, want 0", got)
	}
	if got := resolver.callsFor("93.184.216.34"); got != 0 {
		t.Fatalf("public literal resolver calls = %d, want 0", got)
	}
}

func TestURLGate_PrivateIPClassCoversSSRFRanges(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw       string
		wantClass string
	}{
		{raw: "127.0.0.1", wantClass: "loopback"},
		{raw: "10.0.0.7", wantClass: "rfc1918"},
		{raw: "172.16.0.1", wantClass: "rfc1918"},
		{raw: "192.168.1.1", wantClass: "rfc1918"},
		{raw: "169.254.1.10", wantClass: "link_local"},
		{raw: "0.0.0.0", wantClass: "unspecified"},
		{raw: "fd00::1", wantClass: "unique_local"},
		{raw: "::ffff:10.0.0.7", wantClass: "rfc1918"},
		{raw: "239.255.0.1", wantClass: "multicast"},
		{raw: "ff05::1", wantClass: "multicast"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			got, ok := privateIPClass(net.ParseIP(tc.raw))
			if !ok || got != tc.wantClass {
				t.Fatalf("privateIPClass(%q) = %q, %v; want %q, true", tc.raw, got, ok, tc.wantClass)
			}
		})
	}
}

func TestURLGate_ResolvedMetadataIPClass(t *testing.T) {
	t.Parallel()
	got, ok := resolvedIPClass(net.ParseIP("100.100.100.200"))
	if !ok || got != "metadata_service:alibaba" {
		t.Fatalf("resolvedIPClass(metadata IP) = %q, %v; want metadata_service:alibaba, true", got, ok)
	}
}

func TestURLGate_DeterministicDenialsBeforeDNS(t *testing.T) {
	t.Parallel()
	for _, tc := range deterministicDenialBeforeDNSCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runDeterministicDenialBeforeDNS(t, tc)
		})
	}
}

func runDeterministicDenialBeforeDNS(t *testing.T, tc deterministicDenialCase) {
	t.Helper()
	resolver := &fakeHostResolver{err: map[string]error{tc.host: errResolverUnavailable}}
	gate := NewURLGate(URLGateOptions{Resolver: resolver})
	dec := gate.Evaluate(context.Background(), &config.PolicyInput{
		Action: &config.ActionDescriptor{Kind: config.ActionKindURL, Verb: tc.verb, TargetURL: tc.url},
	})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("decision = %v, want DENY (code=%q subReason=%q)", dec.Decision, dec.Code, dec.SubReason)
	}
	if !strings.Contains(dec.SubReason, tc.subReasonHas) {
		t.Fatalf("subReason = %q, want substring %q", dec.SubReason, tc.subReasonHas)
	}
	if got := resolver.callsFor(tc.host); got != 0 {
		t.Fatalf("resolver calls for deterministic denial host %q = %d, want 0", tc.host, got)
	}
}

func promptExfilURL(host string) string {
	big := strings.Repeat("a", 1200)
	return `https://` + host + `/log?payload={"messages":[{"role":"user","content":"` + big + `"}],"system":"hi"}`
}

func TestURLGate_PromptExfilSignature(t *testing.T) {
	t.Parallel()
	// Build a >1KB JSON payload containing recognized prompt-stash keys, then
	// stick it in a query param. Must DENY.
	big := strings.Repeat("a", 1200)
	exfilPayload := `{"messages":[{"role":"user","content":"` + big + `"}],"system":"hi","context_window":4096}`
	url := "https://attacker.example/log?payload=" + exfilPayload
	runURLGate(t, urlGateCase{
		name:         "prompt_stash_in_query",
		url:          url,
		verb:         config.ActionVerbWrite,
		wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
		wantCode:     CodeAccessDenied,
		subReasonHas: "prompt_exfil",
	})

	// Same payload but small (<1KB) → ALLOW (one of the structured-field
	// thresholds — we don't want to over-refuse legitimate small JSON params).
	smallURL := `https://api.example/log?payload={"messages":[]}`
	runURLGate(t, urlGateCase{
		name:         "tiny_messages_param_allowed",
		url:          smallURL,
		verb:         config.ActionVerbWrite,
		wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
	})
}

func TestURLGate_RequireHumanForPIIPostToUncached(t *testing.T) {
	t.Parallel()
	resolver := &fakeHostResolver{
		resolve: map[string][]string{
			"new-uncached-vendor.example.com": {"203.0.113.99"},
		},
	}
	runURLGate(t, urlGateCase{
		name:         "pii_post_new_domain_require_human",
		url:          "https://new-uncached-vendor.example.com/api/upload",
		verb:         config.ActionVerbWrite,
		riskTags:     []string{"data:pii"},
		resolver:     resolver,
		wantDecision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		wantCode:     CodeRequireHuman,
		subReasonHas: "pii_post",
	})
	// Same URL but no PII tag → allow.
	runURLGate(t, urlGateCase{
		name:         "post_new_domain_no_pii_allowed",
		url:          "https://new-uncached-vendor.example.com/api/upload",
		verb:         config.ActionVerbWrite,
		resolver:     resolver,
		wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
	})
}

func TestURLGate_PIIPostRequiresHumanAfterPublicDNSValidation(t *testing.T) {
	t.Parallel()
	const host = "new-validated-vendor.example.com"
	resolver := &fakeHostResolver{
		resolve: map[string][]string{host: {"93.184.216.34"}},
	}
	gate := NewURLGate(URLGateOptions{Resolver: resolver})

	dec := gate.Evaluate(context.Background(), &config.PolicyInput{
		Action: &config.ActionDescriptor{
			Kind:      config.ActionKindURL,
			Verb:      config.ActionVerbWrite,
			TargetURL: "https://" + host + "/api/upload",
			RiskTags:  []string{"data:pii"},
		},
	})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
		t.Fatalf("decision = %v, want REQUIRE_HUMAN (code=%q subReason=%q)", dec.Decision, dec.Code, dec.SubReason)
	}
	if dec.Code != CodeRequireHuman || !strings.Contains(dec.SubReason, "pii_post") {
		t.Fatalf("PII decision code/subReason = %q/%q, want %q and pii_post", dec.Code, dec.SubReason, CodeRequireHuman)
	}
	if got := resolver.callsFor(host); got != 1 {
		t.Fatalf("PII public DNS resolver calls = %d, want 1 before require-human", got)
	}
}

func TestURLGate_AllowLegitimateDestinations(t *testing.T) {
	t.Parallel()
	// ≥4 over-refusal cases from security-research + cybersecurity-education,
	// plus standard docs/training references.
	cases := []urlGateCase{
		{name: "anthropic_docs", url: "https://docs.anthropic.com/claude/docs/intro", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "anthropic_github", url: "https://github.com/anthropics/anthropic-sdk-python", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "owasp_top10", url: "https://owasp.org/Top10/A03_2021-Injection/", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "huggingface_models", url: "https://huggingface.co/datasets/test", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "schema_org_ref", url: "https://schema.org/Person", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		// Security-research + cybersecurity-education over-refusal allow cases.
		{name: "mitre_attack_research", url: "https://attack.mitre.org/techniques/T1059/", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "cve_lookup", url: "https://cve.mitre.org/cgi-bin/cvename.cgi?name=CVE-2024-1234", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "nvd_education", url: "https://nvd.nist.gov/vuln/detail/CVE-2024-1234", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "exploit_db_research", url: "https://www.exploit-db.com/exploits/12345", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "github_post_pr_no_pii", url: "https://api.github.com/repos/owner/repo/pulls", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "internal_corp", url: "https://internal.corp.local/api/v1/health", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runURLGate(t, tc) })
	}
}

func TestURLGate_FailsClosedOnMalformedHostlessAndUnsupportedSchemes(t *testing.T) {
	t.Parallel()
	cases := []urlGateCase{
		{name: "file_scheme", url: "file:///etc/passwd", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "unsupported_scheme"},
		{name: "mailto_scheme", url: "mailto:ops@example.com", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "unsupported_scheme"},
		{name: "ftp_scheme", url: "ftp://example.com/x", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "unsupported_scheme"},
		{name: "http_missing_host", url: "http:///missing-host", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "missing_host"},
		{name: "malformed_url", url: "http://[::1", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "malformed_url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runURLGate(t, tc) })
	}
}

var errResolverUnavailable = errors.New("resolver unavailable")
