package identity

// The inventory guard finds every place in core/ and cmd/ product code that
// opens a listener, subscribes to the bus, registers a gateway route or
// issues a credential, and checks each against the inventory below.

import (
	"fmt"
	"strings"
	"testing"
)

// site is one place in product code. Its key leaves out the line, which shifts.
type site struct {
	path, fn, kind, subject, at string
	auto                        Entry // a gateway route, mapped by its path prefix
}

func (s site) key() string { return s.path + "|" + s.fn + "|" + s.kind + "|" + s.subject }

// use is what the inventory says about one site key: how many sites it
// covers, and the Entries it serves, the Credentials it issues, or why it is
// outside the matrix. Exactly one of the three is set.
type use struct {
	n       int
	entries []Entry
	creds   []Credential
	why     string
}

func serves(n int, e ...Entry) use      { return use{n: n, entries: e} }
func issues(n int, c ...Credential) use { return use{n: n, creds: c} }
func outside(n int, why string) use     { return use{n: n, why: why} }

func (u use) valid(kind string) bool {
	switch {
	case u.why != "":
		return len(u.entries) == 0 && len(u.creds) == 0
	case kind == "issuer" || kind == "auth_source" || kind == "token_type":
		return len(u.creds) > 0 && len(u.entries) == 0
	}
	return len(u.entries) > 0 && len(u.creds) == 0
}

// inventoryProblems reports unmapped sites, count mismatches, stale keys and
// invalid inventory lines.
func inventoryProblems(sites []site, inv map[string]use) []string {
	var probs []string
	found := map[string][]string{}
	for _, s := range sites {
		if s.auto == "" {
			found[s.key()] = append(found[s.key()], s.at)
		}
	}
	for k, at := range found {
		if u, ok := inv[k]; !ok {
			probs = append(probs, fmt.Sprintf("unmapped site %s at %s", k, strings.Join(at, ", ")))
		} else if u.n != len(at) {
			probs = append(probs, fmt.Sprintf("site %s is found %d time(s) at %s, the inventory says %d", k, len(at), strings.Join(at, ", "), u.n))
		}
	}
	for k, u := range inv {
		if len(found[k]) == 0 {
			probs = append(probs, fmt.Sprintf("stale inventory key %s: no such site", k))
		}
		if parts := strings.Split(k, "|"); len(parts) != 4 || !u.valid(parts[2]) {
			probs = append(probs, fmt.Sprintf("inventory key %s must name its Entries, its Credentials (for a source) or a reason, and only one", k))
		}
	}
	return probs
}

// referenceProblems checks that some site serves every Entry and some source
// issues every Credential that is not a pseudo-credential.
func referenceProblems(sites []site, inv map[string]use) []string {
	served, issued := map[Entry]bool{}, map[Credential]bool{}
	for _, s := range sites {
		served[s.auto] = true
		for _, e := range inv[s.key()].entries {
			served[e] = true
		}
		for _, c := range inv[s.key()].creds {
			issued[c] = true
		}
	}
	var probs []string
	for _, e := range Entries {
		if !served[e] {
			probs = append(probs, fmt.Sprintf("no inventoried site serves entry %q", e))
		}
	}
	for _, c := range Credentials {
		if _, pseudo := pseudoCredentials[c]; !pseudo && !issued[c] {
			probs = append(probs, fmt.Sprintf("no inventoried source issues credential %q", c))
		}
	}
	return probs
}

func TestGuardInventory(t *testing.T) {
	sites := productSites(t, moduleRoot(t))
	probs := append(inventoryProblems(sites, inventory), referenceProblems(sites, inventory)...)
	report(t, "Map each site in the inventory in tests/identity/inventory_test.go: the Entry it serves "+
		"(a new entry point goes in identity.Entries, with a runner), the Credential it issues "+
		"(a new credential goes in identity.Credentials), or a neutral reason it is outside the matrix. "+
		"A line's count is the number of sites with its key.", probs)
}

// pseudoCredentials have no issuer of their own: each presents no
// credential, or a real one in the wrong place.
var pseudoCredentials = map[Credential]string{
	Anonymous:                  "no credential at all",
	GatewayJWTWrongAudience:    "a gateway JWT minted for another audience",
	OIDCBearerWrongAudience:    "an OIDC bearer minted for another audience",
	WorkerSessionWrongAudience: "a worker session minted for another audience",
	AgentIDHeader:              "an X-Agent-Id header sent with no credential",
}

const (
	probeServer = "metrics and health server: /metrics and the health probes, on their own address"
	busWrapper  = "the NATS bus wrapper's own subscribe methods; each caller names its real subject"
	keyMaterial = "generates signing key material, not a credential a caller presents"
)

var gatewayHTTP = []Entry{HTTPAPI, EdgeAPI, SCIMAPI, MCPGateway}

// inventory maps each site key to what the site is. Gateway routes under
// /api/ and /mcp/ map by path prefix and need no line here.
var inventory = map[string]use{
	"cmd/cordum-context-engine/main.go|main|listen|http.Server{} metricsSrv":                                              outside(1, probeServer),
	"cmd/cordum-context-engine/main.go|main|listen|metricsSrv.ListenAndServe":                                             outside(1, probeServer),
	"cmd/cordum-context-engine/main.go|main|listen|net.Listen":                                                            serves(1, GRPCContextEngine),
	"cmd/cordum-context-engine/main.go|newGRPCServer|listen|grpc.NewServer":                                               serves(1, GRPCContextEngine),
	"cmd/cordum-mcp/main.go|main|listen|http.Server{} httpSrv":                                                            serves(1, MCPStandalone),
	"cmd/cordum-mcp/main.go|main|listen|httpSrv.ListenAndServe":                                                           serves(1, MCPStandalone),
	"cmd/cordum-scheduler/main.go|main|listen|http.Server{} metricsSrv":                                                   outside(1, probeServer),
	"cmd/cordum-scheduler/main.go|main|listen|metricsSrv.ListenAndServe":                                                  outside(1, probeServer),
	"cmd/cordumctl-governance-helper/governance.go|runGovernanceTail|subscribe|func":                                      serves(1, BusAuditExport),
	"cmd/cordumctl-governance-helper/governance.go|(natsGovernanceSubscriber).Subscribe|subscribe|governanceAuditSubject": serves(1, BusAuditExport),

	"core/audit/consumer.go|NewNATSAuditConsumer|subscribe|capsdk.SubjectAuditExport":                serves(2, BusAuditExport),
	"core/auth/delegation/keys.go|GenerateSigningKey|issuer|GenerateSigningKey":                      outside(1, keyMaterial),
	"core/auth/delegation/token.go|(*TokenService).IssueDelegationToken|issuer|IssueDelegationToken": issues(1, DelegationToken),
	"core/auth/servicetoken/servicetoken.go|package|token_type|TypService":                           issues(1, ServiceToken),
	"core/auth/servicetoken/servicetoken.go|package|token_type|TypSession":                           issues(1, WorkerSession),

	"core/controlplane/gateway/gateway.go|RunWithAuth|listen|grpc.NewServer":                      serves(1, GRPCAPI),
	"core/controlplane/gateway/gateway.go|RunWithAuth|listen|net.Listen":                          serves(1, GRPCAPI),
	"core/controlplane/gateway/gateway.go|startHTTPServer|listen|http.Server{} metricsSrv":        outside(1, probeServer),
	"core/controlplane/gateway/gateway.go|startHTTPServer|listen|metricsSrv.ListenAndServe":       outside(1, probeServer),
	"core/controlplane/gateway/gateway.go|startHTTPServer|route|/metrics":                         outside(1, probeServer),
	"core/controlplane/gateway/gateway.go|startHTTPServer|listen|http.Server{} srv":               serves(1, gatewayHTTP...),
	"core/controlplane/gateway/gateway.go|startHTTPServer|listen|srv.ListenAndServe":              serves(1, gatewayHTTP...),
	"core/controlplane/gateway/gateway.go|startHTTPServer|listen|srv.ListenAndServeTLS":           serves(1, gatewayHTTP...),
	"core/controlplane/gateway/gateway.go|(*server).registerRoutes|route|GET /health":             outside(1, "the gateway health probe"),
	"core/controlplane/gateway/gateway.go|(*server).registerRoute|route|pattern":                  outside(1, "registerRoute hands its pattern to the mux; the guard reads each pattern at its registerRoute call"),
	"core/controlplane/gateway/auth/keystore_redis.go|GenerateRawKey|issuer|GenerateRawKey":       issues(1, APIKeyManaged),
	"core/controlplane/gateway/auth/oidc.go|(*OIDCProvider).issuerCacheKey|issuer|issuerCacheKey": outside(1, "builds the Redis key for an OIDC issuer's JWKS cache"),
	"core/controlplane/gateway/auth/saml.go|newSAMLStateToken|issuer|newSAMLStateToken":           outside(1, "the SAML login's state value, which the identity provider returns to the ACS callback"),
	"core/controlplane/gateway/auth/saml.go|newSessionToken|issuer|newSessionToken":               issues(1, UserSession),
	"core/controlplane/gateway/auth/scim.go|newSCIMBearerToken|issuer|newSCIMBearerToken":         issues(1, SCIMBearer),
	"core/controlplane/gateway/auth/types.go|package|auth_source|AuthSourceAPIKey":                issues(1, APIKeyStatic, APIKeyManaged),
	"core/controlplane/gateway/auth/types.go|package|auth_source|AuthSourceJWT":                   issues(1, GatewayJWT),
	"core/controlplane/gateway/auth/types.go|package|auth_source|AuthSourceOIDC":                  issues(1, OIDCBearer),
	"core/controlplane/gateway/auth/types.go|package|auth_source|AuthSourceSession":               issues(1, UserSession),

	"core/controlplane/gateway/handlers_packs.go|(*server).issuePackWorkerCredential|issuer|issuePackWorkerCredential": issues(1, WorkerCredential),
	"core/controlplane/gateway/handlers_stream.go|(*server).startBusTaps|subscribe|capsdk.SubjectHeartbeat":            serves(1, BusGatewayHeartbeatTap),
	"core/controlplane/gateway/handlers_stream.go|(*server).startBusTaps|subscribe|capsdk.SubjectDLQ":                  serves(1, BusGatewayDLQ),
	"core/controlplane/gateway/handlers_stream.go|(*server).startBusTaps|subscribe|subject":                            serves(1, BusGatewayStream),

	"core/controlplane/safetykernel/kernel.go|RunWithEntitlements|listen|net.Listen":              serves(1, GRPCSafetyKernel),
	"core/controlplane/safetykernel/kernel.go|RunWithEntitlements|listen|http.Server{} adminSrv":  outside(1, probeServer),
	"core/controlplane/safetykernel/kernel.go|RunWithEntitlements|listen|adminSrv.ListenAndServe": outside(1, probeServer),
	"core/controlplane/safetykernel/kernel.go|newGRPCServer|listen|grpc.NewServer":                serves(1, GRPCSafetyKernel),

	"core/controlplane/scheduler/engine.go|(*Engine).Start|subscribe|capsdk.SubjectHeartbeat":     serves(1, BusWorker),
	"core/controlplane/scheduler/engine.go|(*Engine).Start|subscribe|capsdk.SubjectSubmit":        serves(2, BusJobSubmit),
	"core/controlplane/scheduler/engine.go|(*Engine).Start|subscribe|capsdk.SubjectResult":        serves(2, BusWorker, BusControl),
	"core/controlplane/scheduler/engine.go|(*Engine).Start|subscribe|capsdk.SubjectCancel":        serves(2, BusControl),
	"core/controlplane/scheduler/engine.go|(*Engine).Start|subscribe|capsdk.SubjectProgress":      serves(2, BusWorker),
	"core/controlplane/scheduler/engine.go|(*Engine).Start|subscribe|capsdk.SubjectHandshake":     serves(1, BusWorker),
	"core/controlplane/scheduler/engine.go|(*Engine).Start|subscribe|capsdk.SubjectConfigChanged": serves(1, BusControl),

	"core/controlplane/scheduler/handshake_proof.go|(*HandshakeService).mintHandshakeSession|issuer|mintHandshakeSession":          issues(1, WorkerSession),
	"core/controlplane/scheduler/handshake_subscriber.go|(*HandshakeSubscriber).registerResponders|subscribe|registration.subject": serves(1, BusHandshake),
	"core/controlplane/scheduler/session_token.go|(*SessionTokenIssuer).MintServiceToken|issuer|MintServiceToken":                  issues(1, ServiceToken),
	"core/controlplane/scheduler/token_middleware.go|(*SessionTokenMiddleware).MintServiceToken|issuer|MintServiceToken":           issues(1, ServiceToken),
	"core/controlplane/workercredentials/service.go|GenerateToken|issuer|GenerateToken":                                            issues(1, WorkerCredential),

	"core/edge/agentd/app.go|newHTTPServer|listen|net.Listen":                                  serves(1, EdgeAgentdHook),
	"core/edge/agentd/app.go|newHTTPServer|listen|http.Server{} srv":                           serves(1, EdgeAgentdHook),
	"core/edge/agentd/local_server.go|generateNonce|issuer|generateNonce":                      issues(1, AgentdNonce),
	"core/edge/claude/launcher_helpers.go|reserveLoopbackHookListener|listen|net.Listen":       serves(1, EdgeAgentdHook),
	"core/edge/claude/launcher_helpers.go|reserveLoopbackHookURLLegacy|listen|net.Listen":      outside(1, "picks a free loopback port for the legacy hook URL and closes the listener before it returns"),
	"core/edge/claude/launcher_metadata.go|GenerateLauncherNonce|issuer|GenerateLauncherNonce": issues(1, AgentdNonce),
	"core/edge/shadow/network/ingest_syslog.go|(*SyslogIngestor).Stream|listen|net.ListenUDP":  outside(1, "the edge shadow network observer's UDP syslog input, at an operator-configured address"),

	"core/infra/bus/nats.go|(*NatsBus).SubscribeWithContext|subscribe|subject":           outside(4, busWrapper),
	"core/infra/bus/nats.go|(*NatsBus).subscribe|subscribe|subject":                      outside(4, busWrapper),
	"core/infra/bus/raw_request.go|(*NatsBus).QueueRespond|subscribe|subject":            outside(1, busWrapper),
	"core/mcp/outbound/keys.go|GeneratePrivateKey|issuer|GeneratePrivateKey":             outside(1, keyMaterial),
	"core/workflow/runner.go|RunWithEntitlements|subscribe|capsdk.SubjectAcceptedResult": serves(1, BusWorkflowResult),
	"core/workflow/runner.go|startHealthServer|listen|http.Server{} srv":                 outside(1, probeServer),
	"core/workflow/runner.go|startHealthServer|listen|srv.ListenAndServe":                outside(1, probeServer),
}
