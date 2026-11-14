// Package identity holds PR1-01's audience-binding contract: which credential
// is meant for which entry point. It also provides a cell generator that turns
// the contract into one subtest per credential at each entry.
//
// Runners. Each Entry has one runner in the module: a test whose name starts
// with TestAudienceMatrix and calls Run exactly once, with the Entry as a
// constant selector such as identity.HTTPAPI, never a computed value.
// PR1-01's coverage guard counts these calls.
//
// Presenters. A Presenter mints each credential with its real issuer and keys.
// It presents the credential from the entry's legitimate caller position, so
// that a refusal can only come from credential type or audience. For an entry
// with no allowed credential, the Baseline shows the legitimate caller's
// effect, and each cell presents its credential without that caller's
// authority. Presenters run under the production profile. A refused cell
// presents the credential in every carrier the entry reads:
//   - HTTP: Authorization Bearer, X-API-Key, the cordum_session cookie and, on
//     WebSocket routes, the api-key subprotocol;
//   - gRPC metadata: x-api-key, api-key and authorization;
//   - bus: BusPacket.auth_token, plus the proof signature at bus_handshake;
//   - any entry-specific header, such as X-Cordum-Agentd-Nonce or X-Agent-Id.
//
// Effect. Result.Effect comes from a before/after snapshot of what the entry's
// protected effect writes (store records, bus publishes, files), never from a
// status code. Result.Evidence names what was snapshotted.
//
// Skips. Skip takes a board key only. A skipped cell reports exactly
// "matrix skip <KEY>".
package identity

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"testing"
)

// Credential is a kind of credential a caller can present.
type Credential string

// The credentials, in matrix order. Each comment names the issuer.
const (
	Anonymous                  Credential = "anonymous"                     // no credential at all
	UserSession                Credential = "user_session"                  // session- token: password login, OIDC SSO, SAML ACS
	APIKeyStatic               Credential = "api_key_static"                // CORDUM_API_KEYS
	APIKeyManaged              Credential = "api_key_managed"               // managed ck_ key from the gateway key store
	GatewayJWT                 Credential = "gateway_jwt"                   // gateway JWT issuer, CORDUM_JWT_AUDIENCE audience
	GatewayJWTWrongAudience    Credential = "gateway_jwt_wrong_audience"    // gateway JWT issuer, another audience
	OIDCBearer                 Credential = "oidc_bearer"                   // OIDC provider, CORDUM_OIDC_AUDIENCE audience
	OIDCBearerWrongAudience    Credential = "oidc_bearer_wrong_audience"    // OIDC provider, another audience
	DelegationToken            Credential = "delegation_token"              // core/auth/delegation
	ServiceToken               Credential = "service_token"                 // servicetoken.MintService, typ cordum-service
	WorkerCredential           Credential = "worker_credential"             // core/controlplane/workercredentials
	WorkerSession              Credential = "worker_session"                // worker handshake, bound typ cordum-session
	WorkerSessionWrongAudience Credential = "worker_session_wrong_audience" // worker handshake, another audience
	SCIMBearer                 Credential = "scim_bearer"                   // SCIM bearer token
	AgentdNonce                Credential = "agentd_nonce"                  // per-session nonce from core/edge/agentd
	AgentIDHeader              Credential = "agent_id_header"               // X-Agent-Id sent with no credential
)

// Credentials lists every Credential in matrix order.
var Credentials = []Credential{
	Anonymous, UserSession, APIKeyStatic, APIKeyManaged, GatewayJWT, GatewayJWTWrongAudience,
	OIDCBearer, OIDCBearerWrongAudience, DelegationToken, ServiceToken, WorkerCredential,
	WorkerSession, WorkerSessionWrongAudience, SCIMBearer, AgentdNonce, AgentIDHeader,
}

// Entry is an entry point that authenticates its callers.
type Entry string

// The entries, in matrix order. Each comment defines the entry.
const (
	HTTPAPI                Entry = "http_api"                  // gateway /api/v1/ routes other than Edge and SCIM, core/controlplane/gateway/gateway.go
	EdgeAPI                Entry = "edge_api"                  // gateway /api/v1/edge/ routes, core/controlplane/gateway/gateway.go
	SCIMAPI                Entry = "scim_api"                  // gateway /api/v1/scim/v2 routes, core/controlplane/gateway/auth/scim.go
	MCPGateway             Entry = "mcp_gateway"               // gateway /mcp/sse, /mcp/message and /mcp/status, core/controlplane/gateway/handlers_mcp.go
	GRPCAPI                Entry = "grpc_api"                  // gateway gRPC CordumApi, core/controlplane/gateway/handlers_grpc.go
	BusGatewayHeartbeatTap Entry = "bus_gateway_heartbeat_tap" // gateway sys.heartbeat subscription, core/controlplane/gateway/handlers_stream.go
	BusGatewayDLQ          Entry = "bus_gateway_dlq"           // gateway sys.job.dlq subscription, core/controlplane/gateway/handlers_stream.go; no Credential is meant for it; Run needs a Baseline
	BusGatewayStream       Entry = "bus_gateway_stream"        // gateway sys.job.> and sys.audit.> subscriptions, core/controlplane/gateway/handlers_stream.go
	BusJobSubmit           Entry = "bus_job_submit"            // scheduler sys.job.submit, core/controlplane/scheduler/engine.go
	BusWorker              Entry = "bus_worker"                // to the scheduler from a worker about itself: sys.heartbeat, sys.job.progress, sys.handshake and its own results on sys.job.result
	BusControl             Entry = "bus_control"               // to the scheduler from the control plane besides submissions: sys.job.cancel, sys.config.changed and control-plane results on sys.job.result
	BusHandshake           Entry = "bus_handshake"             // sys.worker.handshake.challenge and .authenticate, core/controlplane/scheduler/handshake_subscriber.go
	BusAuditExport         Entry = "bus_audit_export"          // sys.audit.export, core/audit/consumer.go; no Credential is meant for it; Run needs a Baseline
	BusWorkflowResult      Entry = "bus_workflow_result"       // sys.internal.job.result.accepted, core/workflow/runner.go
	MCPStandalone          Entry = "mcp_standalone"            // cmd/cordum-mcp HTTP /sse and /message; no Credential is meant for it; Run needs a Baseline
	EdgeAgentdHook         Entry = "edge_agentd_hook"          // agentd local hook server, core/edge/agentd/local_server.go
	GRPCSafetyKernel       Entry = "grpc_safety_kernel"        // core/controlplane/safetykernel/kernel.go; no Credential is meant for it; Run needs a Baseline
	GRPCContextEngine      Entry = "grpc_context_engine"       // cmd/cordum-context-engine/main.go; no Credential is meant for it; Run needs a Baseline
)

// Entries lists every Entry in matrix order.
var Entries = []Entry{
	HTTPAPI, EdgeAPI, SCIMAPI, MCPGateway, GRPCAPI, BusGatewayHeartbeatTap, BusGatewayDLQ,
	BusGatewayStream, BusJobSubmit, BusWorker, BusControl, BusHandshake, BusAuditExport,
	BusWorkflowResult, MCPStandalone, EdgeAgentdHook, GRPCSafetyKernel, GRPCContextEngine,
}

// grants has one row per Entry naming the credentials meant for it, each with
// a one-line rationale that cites the repo. Every other credential is refused.
var grants = map[Entry]map[Credential]string{
	HTTPAPI: {
		UserSession:   "browser sessions from password login, OIDC SSO and SAML ACS authenticate gateway HTTP (core/controlplane/gateway/handlers_auth.go, core/controlplane/gateway/auth/basic.go AuthenticateHTTP)",
		APIKeyStatic:  "X-API-Key is the documented gateway HTTP credential; static keys come from CORDUM_API_KEYS (docs/system_overview.md, core/controlplane/gateway/auth/basic.go)",
		APIKeyManaged: "managed ck_ keys are validated on the same X-API-Key path (core/controlplane/gateway/auth/keystore_redis.go, core/controlplane/gateway/auth/basic.go)",
		GatewayJWT:    "bearer JWTs with the CORDUM_JWT_AUDIENCE audience authenticate gateway HTTP (core/controlplane/gateway/auth/jwt.go, core/controlplane/gateway/auth/basic.go AuthenticateHTTP)",
		OIDCBearer:    "OIDC bearers with the CORDUM_OIDC_AUDIENCE audience authenticate gateway HTTP (core/controlplane/gateway/auth/oidc.go OIDCAuthAdapter.AuthenticateHTTP)",
	},
	EdgeAPI: {
		UserSession:   "Edge routes use the existing gateway auth path, which reads session tokens (docs/edge/api.md, core/controlplane/gateway/auth/basic.go)",
		APIKeyStatic:  "Edge routes use the existing gateway auth path with X-API-Key (docs/edge/api.md, core/controlplane/gateway/auth/basic.go)",
		APIKeyManaged: "Edge routes use the existing gateway auth path with X-API-Key, managed keys included (docs/edge/api.md, core/controlplane/gateway/auth/keystore_redis.go)",
		GatewayJWT:    "Edge routes use the existing gateway auth path with a bearer Authorization header (docs/edge/api.md, core/controlplane/gateway/auth/jwt.go)",
		OIDCBearer:    "Edge routes use the existing gateway auth path with a bearer Authorization header (docs/edge/api.md, core/controlplane/gateway/auth/oidc.go)",
	},
	SCIMAPI: {
		SCIMBearer: "SCIM routes authenticate with the SCIM bearer token, CORDUM_SCIM_BEARER_TOKEN or the managed SCIM token (core/controlplane/gateway/auth/scim.go)",
	},
	MCPGateway: {
		UserSession:   "MCP endpoints use the gateway's standard auth, mcpAuth calling AuthenticateHTTP (docs/mcp-integration.md, core/controlplane/gateway/handlers_mcp.go)",
		APIKeyStatic:  "MCP endpoints use the gateway's standard auth, mcpAuth calling AuthenticateHTTP with X-API-Key (docs/mcp-integration.md, core/controlplane/gateway/auth/basic.go)",
		APIKeyManaged: "MCP endpoints use the gateway's standard auth, mcpAuth calling AuthenticateHTTP with managed keys (docs/mcp-integration.md, core/controlplane/gateway/auth/keystore_redis.go)",
		GatewayJWT:    "MCP endpoints use the gateway's standard auth, mcpAuth calling AuthenticateHTTP with a bearer JWT (docs/mcp-integration.md, core/controlplane/gateway/auth/jwt.go)",
		OIDCBearer:    "MCP endpoints use the gateway's standard auth, mcpAuth calling AuthenticateHTTP with an OIDC bearer (docs/mcp-integration.md, core/controlplane/gateway/auth/oidc.go)",
	},
	GRPCAPI: {
		APIKeyStatic:  "gRPC carries the API key in x-api-key metadata (docs/system_overview.md, core/controlplane/gateway/auth/basic.go AuthenticateGRPC)",
		APIKeyManaged: "managed ck_ keys are validated on the same gRPC metadata path (core/controlplane/gateway/auth/basic.go AuthenticateGRPC, core/controlplane/gateway/auth/keystore_redis.go)",
		GatewayJWT:    "AuthenticateGRPC validates a bearer JWT from authorization metadata (core/controlplane/gateway/auth/basic.go, core/controlplane/gateway/auth/jwt.go)",
		OIDCBearer:    "OIDCAuthAdapter.AuthenticateGRPC validates an OIDC bearer from authorization metadata (core/controlplane/gateway/auth/oidc.go)",
	},
	BusGatewayHeartbeatTap: {
		WorkerSession: "workers send sys.heartbeat under their bound session, and the production profile requires handshake enforce (core/controlplane/scheduler/engine.go, core/controlplane/scheduler/production_profile.go)",
	},
	BusGatewayDLQ: {},
	BusGatewayStream: {
		WorkerSession: "workers publish sys.job.result and sys.job.progress under their bound session (core/controlplane/scheduler/engine.go)",
		ServiceToken:  "control-plane services attach the service token to sys.job.submit, sys.job.cancel and their results (core/controlplane/gateway/handlers_jobs.go, core/controlplane/scheduler/engine_accepted_event.go)",
	},
	BusJobSubmit: {
		ServiceToken: "job submission is a reserved-identity action carrying the service token (core/controlplane/scheduler/engine_active_authority.go, core/controlplane/gateway/handlers_jobs.go)",
	},
	BusWorker: {
		WorkerSession: "heartbeats, progress, results and capability announcements carry the worker's bound session (core/controlplane/scheduler/engine.go, core/controlplane/scheduler/engine_capability_trust.go)",
	},
	BusControl: {
		ServiceToken: "cancel, config change and control-plane results are reserved-identity actions (core/controlplane/scheduler/engine_durable_event.go, core/controlplane/scheduler/engine_active_authority.go, core/controlplane/scheduler/engine_accepted_event.go)",
	},
	BusHandshake: {
		WorkerCredential: "the worker handshake is signed with the proof key registered in the worker's credential, audience cordum-scheduler (core/controlplane/scheduler/handshake_validation.go, core/controlplane/workercredentials/proof_key.go)",
	},
	BusAuditExport: {},
	BusWorkflowResult: {
		ServiceToken: "the scheduler publishes sys.internal.job.result.accepted with its service token for the workflow runner (core/controlplane/scheduler/engine_accepted_event.go, core/workflow/runner.go)",
	},
	MCPStandalone: {},
	EdgeAgentdHook: {
		AgentdNonce: "the local hook server requires the per-session X-Cordum-Agentd-Nonce (core/edge/agentd/local_server.go)",
	},
	GRPCSafetyKernel:  {},
	GRPCContextEngine: {},
}

var (
	boardKey = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9A-Z]+$`)
	citation = regexp.MustCompile(`(core|cmd|docs|tests|sdk|tools)/[A-Za-z0-9_./-]+\.(go|md|ya?ml|proto)`)
)

// Allowed reports whether credential c is meant for entry e; unknown values give false.
func Allowed(c Credential, e Entry) bool {
	return matrix{Credentials, Entries, grants}.allowed(c, e)
}

// Result is what a Presenter observed for one cell.
type Result struct {
	Accepted, Effect bool   // Effect comes from a before/after snapshot, never a status code
	Evidence         string // names what was snapshotted
}

// Presenter presents credential c at the runner's entry and reports what it observed.
type Presenter func(t *testing.T, c Credential) Result

// Option adjusts one Run call.
type Option func(*options)

type options struct {
	skips       map[Credential]string
	baseline    func(t *testing.T) Result
	baselineSet bool
	problems    []string
}

// Skip skips the cell for credential c under board key key, such as PR1-01C.
func Skip(c Credential, key string) Option {
	return func(o *options) {
		if _, dup := o.skips[c]; dup {
			o.problems = append(o.problems, fmt.Sprintf("Skip given twice for %q", c))
		}
		o.skips[c] = key
	}
}

// Baseline sets a probe that shows the entry's legitimate caller producing the
// protected effect. Run needs it for an entry with no allowed credential.
func Baseline(probe func(t *testing.T) Result) Option {
	return func(o *options) {
		if o.baselineSet {
			o.problems = append(o.problems, "Baseline given twice")
		}
		o.baselineSet, o.baseline = true, probe
	}
}

// Run checks the contract and the call, then runs one subtest per credential
// at entry e, named <entry>/<credential>, in Credentials order.
func Run(t *testing.T, e Entry, present Presenter, opts ...Option) {
	t.Helper()
	run(realTB{t}, matrix{Credentials, Entries, grants}, e, present, opts...)
}

// tb is the part of *testing.T that run uses; the self-tests record it.
type tb interface {
	Errorf(format string, args ...any)
	Logf(format string, args ...any)
	Skipf(format string, args ...any)
	Run(name string, f func(tb)) bool
	testingT() *testing.T
}

type realTB struct{ *testing.T }

func (r realTB) Run(name string, f func(tb)) bool {
	return r.T.Run(name, func(t *testing.T) { f(realTB{t}) })
}

func (r realTB) testingT() *testing.T { return r.T }

type matrix struct {
	credentials []Credential
	entries     []Entry
	grants      map[Entry]map[Credential]string
}

func (m matrix) allowed(c Credential, e Entry) bool {
	_, ok := m.grants[e][c]
	return ok
}

// problems lists every defect in the contract itself.
func (m matrix) problems() []string {
	var probs []string
	creds := map[Credential]bool{}
	for _, c := range m.credentials {
		if creds[c] {
			probs = append(probs, fmt.Sprintf("duplicate credential %q", c))
		}
		creds[c] = true
	}
	entries := map[Entry]bool{}
	for _, e := range m.entries {
		if entries[e] {
			probs = append(probs, fmt.Sprintf("duplicate entry %q", e))
			continue
		}
		entries[e] = true
		if _, ok := m.grants[e]; !ok {
			probs = append(probs, fmt.Sprintf("no table row for entry %q", e))
		}
	}
	for e, row := range m.grants {
		if !entries[e] {
			probs = append(probs, fmt.Sprintf("table row for unknown entry %q", e))
		}
		for c, why := range row {
			if !creds[c] {
				probs = append(probs, fmt.Sprintf("row %q names unknown credential %q", e, c))
			}
			if !citation.MatchString(why) {
				probs = append(probs, fmt.Sprintf("rationale for %s/%s cites no repo path", e, c))
			}
		}
	}
	return probs
}

// callProblems lists every defect in one Run call.
func callProblems(m matrix, e Entry, present Presenter, o options) []string {
	probs := slices.Clone(o.problems)
	known := slices.Contains(m.entries, e)
	if !known {
		probs = append(probs, fmt.Sprintf("unknown entry %q", e))
	}
	if present == nil {
		probs = append(probs, "nil Presenter")
	}
	if o.baselineSet && o.baseline == nil {
		probs = append(probs, "Baseline probe is nil")
	}
	control := o.baselineSet
	for c, key := range o.skips {
		if !slices.Contains(m.credentials, c) {
			probs = append(probs, fmt.Sprintf("Skip names unknown credential %q", c))
		}
		if !boardKey.MatchString(key) {
			probs = append(probs, fmt.Sprintf("skip key %q is not a board key", key))
		}
	}
	for c := range m.grants[e] {
		if _, skipped := o.skips[c]; !skipped {
			control = true
		}
	}
	if known && !control {
		probs = append(probs, "no positive control: supply Baseline or an unskipped allowed credential")
	}
	return probs
}

// run is Run's whole body. After any contract or call problem, no cell runs.
func run(t tb, m matrix, e Entry, present Presenter, opts ...Option) {
	o := options{skips: map[Credential]string{}}
	for _, opt := range opts {
		opt(&o)
	}
	probs := m.problems()
	if len(probs) == 0 {
		probs = callProblems(m, e, present, o)
	}
	sort.Strings(probs)
	for _, p := range probs {
		t.Errorf("%s", p)
	}
	if len(probs) > 0 {
		return
	}
	t.Run(string(e), func(t tb) {
		if o.baselineSet {
			t.Run("baseline", func(t tb) { checkBaseline(t, o.baseline(t.testingT())) })
		}
		for _, c := range m.credentials {
			t.Run(string(c), func(t tb) {
				if key, skipped := o.skips[c]; skipped {
					t.Skipf("matrix skip %s", key)
					return
				}
				checkCell(t, m.allowed(c, e), present(t.testingT(), c))
			})
		}
	})
}

func checkBaseline(t tb, res Result) {
	t.Logf("evidence: %s", res.Evidence)
	if res.Evidence == "" {
		t.Errorf("empty Evidence")
	}
	if !res.Accepted || !res.Effect {
		t.Errorf("baseline must be Accepted with an observed Effect")
	}
}

func checkCell(t tb, allowed bool, res Result) {
	t.Logf("evidence: %s", res.Evidence)
	if res.Evidence == "" {
		t.Errorf("empty Evidence")
	}
	if allowed && !res.Accepted {
		t.Errorf("allowed credential was refused")
	} else if allowed && !res.Effect {
		t.Errorf("allowed credential accepted without an observed effect")
	}
	if !allowed && res.Accepted {
		t.Errorf("credential not meant for this entry was accepted")
	}
	if !allowed && res.Effect {
		t.Errorf("effect observed on a refused cell")
	}
}
