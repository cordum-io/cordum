package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// upstreamErrorRedactPatterns defines URL/token regex rules applied to
// upstream error messages before they reach a Redis-persisted event.
// Order matters: URL redaction runs first so token-extraction patterns
// don't re-leak the URL substring.
var upstreamErrorRedactPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	// Full URLs (http/https with optional userinfo + query string).
	{regexp.MustCompile(`https?://[^\s]+`), "[REDACTED:url]"},
	// Bearer tokens.
	{regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-+/=]+`), "[REDACTED:bearer]"},
	// API key tokens by common prefix (Anthropic sk-, GitHub ghp_, AWS AKIA).
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{4,}\b`), "[REDACTED:api_key]"},
	{regexp.MustCompile(`\bgh[opusr]_[A-Za-z0-9]{8,}\b`), "[REDACTED:github_token]"},
	// GitHub fine-grained PAT (github_pat_…) carries underscores in
	// the body, so the classic gh[opusr]_ rule above does not cover it.
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{8,}\b`), "[REDACTED:github_token]"},
	// GitHub Enterprise (ghe_…) — same shape, different prefix letter.
	{regexp.MustCompile(`\bghe_[A-Za-z0-9_]{8,}\b`), "[REDACTED:github_token]"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{12,}\b`), "[REDACTED:aws_key]"},
}

// redactionCompletenessPatterns is the post-redaction sentinel set: if
// any of these still match a Redactor's output, the redactor either has
// a bug or was misconfigured by policy. We refuse to emit the event so
// no raw credential lands in a Redis-persisted audit row. This is the
// defense-in-depth backstop required by EDGE-102 DoD #3.
//
// Keep this list in lockstep with DefaultRedactionRules' regex set —
// every high-severity heuristic in the redactor needs an equivalent
// guardrail check here so a partial drop in either layer still trips
// the closed-fail path.
var redactionCompletenessPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{12,}`),
	regexp.MustCompile(`sk_live_[a-zA-Z0-9]{24,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`gh[opusr]_[A-Za-z0-9]{16,}`),
	// Fine-grained PAT body carries underscores; classic class above
	// would let github_pat_ tokens through the completeness backstop.
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{16,}`),
	// Enterprise (ghe_) — same shape, distinct prefix letter.
	regexp.MustCompile(`ghe_[A-Za-z0-9_]{16,}`),
	regexp.MustCompile(`eyJ[a-zA-Z0-9_\-]+\.[a-zA-Z0-9_\-]+\.[a-zA-Z0-9_\-]+`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

// highSeverityFindingMarkers is the set of [REDACTED:...] family tags
// whose presence — even in a small payload — promotes the event to
// artifact storage. Investigators get the full redacted context for
// any credential-touching call without inflating the inline event.
var highSeverityFindingMarkers = []string{
	"[REDACTED:api_key]",
	"[REDACTED:token]",
	"[REDACTED:secret]",
	"[REDACTED:password]",
	"[REDACTED:private_key]",
	"[REDACTED:pem_private_key]",
	"[REDACTED:jwt]",
	"[REDACTED:aws_access_key]",
	"[REDACTED:aws_key]",
	"[REDACTED:github_token]",
	"[REDACTED:stripe_secret]",
	"[REDACTED:bearer]",
	"[REDACTED:authorization]",
}

// targetPathArgKeys lists the JSON property names the descriptor builder
// consults to populate ActionDescriptor.TargetPath from tools/call args.
// Order matters — the first match wins. Keeps the canonical action hash
// stable when callers use any of the common naming conventions.
var targetPathArgKeys = []string{"path", "file_path", "target_path", "filepath"}

// MaxToolCallArgsBytes is the hard limit on serialized tool-call args
// before BuildActionDescriptorFromToolCall returns an `args_too_large`
// error. Set to 1 MiB: any payload over that should never reach the
// gate pipeline as a structured descriptor — it belongs in artifact
// storage, not in a hot policy-evaluate path that runs on every call.
const MaxToolCallArgsBytes = 1 * 1024 * 1024

// inlineRedactedSummaryBytes is the inline budget retained on the
// AgentActionEvent.InputRedacted map when the redacted payload exceeds
// edge.MaxInputRedactedBytes. We keep a 4 KiB summary visible inline so
// triagers see the shape of the request without paging through the
// artifact store, then rely on the artifact pointer for full forensics.
const inlineRedactedSummaryBytes = 4 * 1024

// approvalClaimArgKey is the JSON property name on tool-call arguments
// that callers may use to declare a human-approval claim. The string
// is read verbatim into ActionDescriptor.ApprovalClaim.ClaimText — it
// is treated as UNTRUSTED ("approved by CFO" never grants); the
// provenance gate verifies the claim against backend records.
const approvalClaimArgKey = "approval_claim"

// CallMetadata carries the request-scoped identity + session linkage
// the MCP tool-call policy path needs. The gateway's HTTP middleware
// stashes this in context before dispatching tools/call into core/mcp.
//
// SessionID and ExecutionID let edge-event consumers correlate a
// tool-call's pre/post/failed events with the AgentExecution they
// belong to; without them, the audit row is unattributed and the
// EvaluateToolCall path fails closed.
//
// This type is the mcp-package mirror of the gateway's
// gateway.MCPCallMetadata. Both exist because core/policy/actiongates
// already imports core/mcp (for mcp.AgentIdentity); a core/mcp →
// gateway import would close the cycle. The gateway middleware writes
// both contexts so either accessor works at the call site.
type CallMetadata struct {
	Tenant      string
	Principal   string
	AgentID     string
	SessionID   string
	ExecutionID string
}

type callMetadataKey struct{}

// WithCallMetadata returns a context carrying the supplied metadata.
// Callers MUST use this helper instead of context.WithValue with a
// raw string key so the lookup contract stays type-safe.
func WithCallMetadata(ctx context.Context, meta CallMetadata) context.Context {
	return context.WithValue(ctx, callMetadataKey{}, meta)
}

// CallMetadataFromContext returns the metadata the gateway stashed.
// The second return distinguishes "not stashed" from "stashed with
// zero fields": callers MUST fail closed when ok=false.
func CallMetadataFromContext(ctx context.Context) (CallMetadata, bool) {
	if ctx == nil {
		return CallMetadata{}, false
	}
	m, ok := ctx.Value(callMetadataKey{}).(CallMetadata)
	return m, ok
}

// PolicyDecision is the mcp-package mirror of
// actiongates.ActionGateDecision. Defined here to break the
// import cycle (core/policy/actiongates already imports core/mcp).
// The gateway adapter converts between the two types so this layer
// stays free of an actiongates dependency.
//
// Constraints carries the `_constraints` map that gates populate when
// returning ALLOW_WITH_CONSTRAINTS. The shape mirrors the agentd wire
// format (core/edge/agentd/evaluate_client.go EvaluateResponse.Constraints)
// so audit-event consumers and downstream tools see a single canonical
// constraint payload regardless of which surface produced it.
type PolicyDecision struct {
	Decision    pb.DecisionType
	GateID      string
	Code        string
	Reason      string
	SubReason   string
	Extra       map[string]string
	Constraints map[string]any
}

// PolicyDispatcher dispatches an MCP tool-call against the production
// action-gate pipeline. The gateway provides the implementation that
// wraps actiongates.Pipeline.Run; tests inject a fake.
//
// The second return distinguishes "no gate fired" (fired=false) from
// "a gate fired with the zero decision". Callers treat fired=false as
// implicit ALLOW.
type PolicyDispatcher interface {
	Dispatch(ctx context.Context, in *config.PolicyInput) (PolicyDecision, bool)
}

// EventEmitter is the narrow contract this layer needs from the edge
// event recorder. The gateway provides an implementation backed by
// edge.RedisStore; tests use an in-memory recorder.
type EventEmitter interface {
	Emit(ctx context.Context, event *edge.AgentActionEvent) error
}

// ArtifactStore writes oversized redacted payloads to evidence
// storage and returns a pointer the calling event embeds. Production
// wires this to the gateway's edge.ArtifactStater adapter; tests
// supply an in-memory fake.
type ArtifactStore interface {
	Put(ctx context.Context, req ArtifactPutRequest) (*edge.ArtifactPointer, error)
}

// ArtifactPutRequest carries the metadata an artifact-store backend
// needs to scope the payload by tenant + session + execution. Type
// distinguishes request vs response evidence; the event records the
// resulting pointer with the same type so dashboard exports can
// pivot on it.
type ArtifactPutRequest struct {
	Type        edge.ArtifactType
	Payload     []byte
	TenantID    string
	SessionID   string
	ExecutionID string
	EventID     string
	ContentType string
}

// UpstreamToolCaller invokes the underlying tool the bridge wraps.
// Production wires this to the per-tool handler registered in
// ToolRegistry; tests supply a recording fake.
type UpstreamToolCaller interface {
	Invoke(ctx context.Context, params ToolCallParams) (*ToolCallResult, error)
}

// ApprovalHandoff routes a REQUIRE_HUMAN gate decision into the
// gateway's existing MCPApprovalStore lifecycle. The gateway-side
// adapter (gatewayApprovalGate.ConsumeActionGateDecision) returns
// the approval reference the caller surfaces to the client.
type ApprovalHandoff interface {
	ConsumeActionGateDecision(ctx context.Context, dec PolicyDecision, ctxData ToolCallApprovalContext) (string, error)
}

// ToolCallApprovalContext carries the non-descriptor metadata the
// approval-store adapter needs to find or create the pending
// EdgeApproval. The ActionHash is the canonical hash the gate
// already computed; reusing it (instead of recomputing) keeps the
// gate and approval lifecycle bound to the same key.
//
// EDGE-103 reopen #1: Args carries the canonical (already-redacted)
// tool-call arguments so the mint side can derive the same InputHash
// that the consume side (ProcessApprovalClaim → BuildMCPApprovalBinding)
// derives. Without this field the mint had no way to compute the
// args hash and was storing ActionHash in its place — surfacing as
// ApprovalConflictKindArgsMismatch on every legitimate retry.
type ToolCallApprovalContext struct {
	Tenant     string
	AgentID    string
	Server     string
	Tool       string
	ActionHash string
	Args       json.RawMessage
}

// ToolCallDeps bundles the production wiring EvaluateToolCall and
// InvokeToolWithPolicy consume. Every field is interface-typed so
// tests inject fakes without touching the real Redis/audit stores.
//
// Clock defaults to time.Now.UTC when zero. EventIDFactory defaults
// to a uuid-shaped random helper when zero. Other deps are required:
// callers receive a clear "deps.X is nil" error rather than a panic.
type ToolCallDeps struct {
	Pipeline        PolicyDispatcher
	EventEmitter    EventEmitter
	Redactor        ArgumentRedactor
	ArtifactStore   ArtifactStore
	Upstream        UpstreamToolCaller
	ApprovalHandoff ApprovalHandoff
	Clock           func() time.Time
	EventIDFactory  func() string
	// DedupeState scopes retry dedupe to a single caller (HTTP server,
	// test fixture) instead of a global map. Production wires this to
	// either a NewInProcessDedupeStore (single-instance gateway) or a
	// NewRedisDedupeStore (multi-instance HA behind a load balancer).
	// Tests inject a fresh in-process store per fixture so -count=3
	// re-runs see a clean state. A nil store disables retry dedupe.
	DedupeState DedupeStore

	// TaintStore persists prompt-injection session taints (keyed by tenant+
	// session) detected when scanning tool-call RESULTS, so a later destructive
	// call in the same session can be DENIED content-awarely. Nil disables the
	// session-taint feature (like a nil DedupeState). Production wires this via
	// SelectTaintStore off the shared Redis client; tests inject a fresh
	// in-process store per fixture.
	TaintStore TaintStore

	// ResultScanner scans a tool-call result's content for prompt injection.
	// Injected as a func dep (rather than importing the safetykernel scanner) so
	// core/mcp keeps no dependency on core/controlplane/safetykernel — the gateway
	// adapts safetykernel.ScanForPromptInjection at wiring time. Nil disables
	// result scanning. Paired with TaintStore: BOTH must be non-nil for the read
	// path to persist a taint.
	ResultScanner func(content []byte) []ResultFinding
}

// EvaluateToolCallResult bundles the artifacts a caller might want
// after pre-dispatch evaluation: the gate decision, the emitted pre
// event (so the caller can attach more metadata before post emission),
// and the artifact pointer when the request was oversized.
type EvaluateToolCallResult struct {
	Decision    PolicyDecision
	PreEvent    *edge.AgentActionEvent
	ArtifactPtr *edge.ArtifactPointer
}

// errMissingMCPMetadata is returned when the calling middleware did
// not stash CallMetadata in context. The sentinel-string is part of
// the contract — tests grep for it.
var errMissingMCPMetadata = errors.New("missing_mcp_metadata: CallMetadata not in context")

// BuildActionDescriptorFromToolCall maps an MCP tools/call into the
// structured descriptor the action-gate pipeline consumes.
//
// The build is server-side: Kind is forced to ActionKindMCPCall,
// Verb stays zero (the mutation gate classifies via its destructive-
// verb set), and RiskTags stays empty (gates derive their own). The
// optional approval_claim arg-field is copied verbatim into
// ApprovalClaim.ClaimText so the provenance gate can refuse it later.
// A path-like arg (path/file_path/target_path/filepath) is extracted
// and normalized into desc.TargetPath so canonical hashing is stable
// across Windows-style backslash and POSIX-style forward-slash spellings.
//
// Payloads >MaxToolCallArgsBytes return an `args_too_large` error so
// callers handle the size violation explicitly instead of seeing a
// silently-stripped descriptor. The cap is byte-length: multibyte UTF-8
// content does not get a quiet pass.
func BuildActionDescriptorFromToolCall(meta CallMetadata, params ToolCallParams, server string) (*config.ActionDescriptor, error) {
	if len(params.Arguments) > MaxToolCallArgsBytes {
		return nil, fmt.Errorf("args_too_large: %d > %d", len(params.Arguments), MaxToolCallArgsBytes)
	}
	desc := &config.ActionDescriptor{
		Kind:   config.ActionKindMCPCall,
		Server: server,
		Tool:   params.Name,
	}
	if len(params.Arguments) > 0 {
		parsedArgs, claimText := parseArgsForDescriptor(params.Arguments)
		if parsedArgs != nil {
			desc.Args = parsedArgs
			if p := extractTargetPathFromArgs(parsedArgs); p != "" {
				desc.TargetPath = normalizeTargetPath(p)
			}
		}
		if claimText != "" {
			desc.ApprovalClaim = &config.ActionApprovalClaim{ClaimText: claimText}
		}
	}
	return desc, nil
}

// extractTargetPathFromArgs returns the first string-valued path-like
// arg, scanning targetPathArgKeys in declaration order. Non-string
// values are ignored so a numeric or object collision in args["path"]
// doesn't poison TargetPath.
func extractTargetPathFromArgs(args map[string]any) string {
	for _, key := range targetPathArgKeys {
		if v, ok := args[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// normalizeTargetPath converts a filesystem path into a canonical form
// suitable for hashing and approval-lifecycle keying. Backslash-style
// separators (Windows callers, hand-rolled JSON) collapse to forward
// slash so the same logical file produces a single canonical hash
// regardless of platform.
func normalizeTargetPath(p string) string {
	if p == "" {
		return ""
	}
	return strings.ReplaceAll(p, `\`, `/`)
}

// ActionTupleHash returns the stable SHA-256 over the (tenant, server,
// tool, target_path) tuple that identifies an MCP tool call for
// approval-lifecycle binding. Inputs MUST be the normalized forms.
//
// Renamed from CanonicalActionHash (EDGE-103 reopen #2 DoD #3) so the
// SINGLE definition of `func CanonicalActionHash` in the repo is the
// one at `core/policy/actiongates/mutation_gate.go:218`. The two
// helpers serve different scopes: mutation_gate's hashes the full
// `*config.ActionDescriptor` (kind/verb/server/tool/target/args/filters/
// wildcards/risk_tags) for the actiongates evaluation path; this one
// hashes the per-(tenant, server, tool, target_path) tuple as the
// approval-lifecycle action-key.
func ActionTupleHash(tenant, server, tool, targetPath string) string {
	canonical := fmt.Sprintf("%s|%s|%s|%s", tenant, server, tool, normalizeTargetPath(targetPath))
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// verifyRedactionCompleteness is the defense-in-depth backstop the
// EvaluateToolCall path runs against the redactor's output. If a known
// sensitive shape survived the redactor (rules misconfig, partial
// match, or hostile stub), the function returns an error so the caller
// fails closed and no event is emitted. The contract is that no raw
// credential ever lands in a Redis-persisted audit row, even if the
// argument_redactor rule set was incomplete.
func verifyRedactionCompleteness(payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	for _, pat := range redactionCompletenessPatterns {
		if pat.Match(payload) {
			return fmt.Errorf("redaction_failed: sensitive pattern %q survived redaction", pat.String())
		}
	}
	return nil
}

// hasHighSeverityFinding reports whether the redacted payload contains
// any high-severity [REDACTED:...] marker. Used to promote a small
// event into artifact storage so investigators get the full redacted
// context for any credential-touching call. The artifact payload is
// already the redactor's output — no raw credential is persisted.
func hasHighSeverityFinding(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	for _, marker := range highSeverityFindingMarkers {
		if bytes.Contains(payload, []byte(marker)) {
			return true
		}
	}
	return false
}

// parseArgsForDescriptor unmarshals tool args into a map[string]any
// and extracts the optional approval_claim string. Returns (nil, "")
// on parse failure or non-object roots so the gate sees a benign
// empty Args set instead of malformed input.
func parseArgsForDescriptor(args json.RawMessage) (map[string]any, string) {
	var parsed any
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, ""
	}
	asMap, ok := parsed.(map[string]any)
	if !ok {
		return nil, ""
	}
	var claim string
	if v, ok := asMap[approvalClaimArgKey].(string); ok {
		claim = v
	}
	return asMap, claim
}

// EvaluateToolCall runs the pre-dispatch policy gate against a
// tool-call request. On gate ALLOW (or implicit allow via fired=false)
// the function emits an mcp.tool.pre event and returns the decision;
// the caller (the bridge wrapper) is responsible for invoking upstream
// and emitting the matching post/failed event.
//
// Errors fail closed: missing metadata, redactor outage, oversized
// descriptor — all propagate as errors and emit NO event (avoiding
// unattributed audit rows). The artifact-store outage path is the
// exception: it emits a failed event with reason=service_unavailable
// so the audit trail records the policy-side failure.
func EvaluateToolCall(ctx context.Context, deps ToolCallDeps, params ToolCallParams, server string) (EvaluateToolCallResult, error) {
	meta, ok := CallMetadataFromContext(ctx)
	// Refuse the call when ANY identity field is blank — the audit row
	// is keyed on (Tenant, SessionID, ExecutionID, AgentID), so a single
	// missing component produces unattributed events that are useless for
	// incident forensics and silently break tenant-scoped audit filters.
	// Fail-closed is preferred over best-effort emission with synthetic
	// placeholders: an upstream bridge that forgot to call
	// ContextWithCallMetadata is a bug, not a soft warning.
	if !ok || meta.Tenant == "" || meta.SessionID == "" || meta.ExecutionID == "" || meta.AgentID == "" {
		return EvaluateToolCallResult{}, errMissingMCPMetadata
	}
	// Reject oversized args before running the redactor so a 10 MB
	// payload from a hostile caller cannot waste CPU on regex/JSON
	// passes whose only outcome is args_too_large. The same cap is
	// re-enforced inside BuildActionDescriptorFromToolCall so any
	// future caller that bypasses EvaluateToolCall still fails closed.
	if len(params.Arguments) > MaxToolCallArgsBytes {
		return EvaluateToolCallResult{}, fmt.Errorf("args_too_large: %d > %d", len(params.Arguments), MaxToolCallArgsBytes)
	}
	if deps.Redactor == nil {
		deps.Redactor = DefaultRedactor()
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	if deps.EventIDFactory == nil {
		deps.EventIDFactory = defaultEventIDFactory
	}

	redactedArgs := deps.Redactor.Redact(params.Arguments)
	if err := verifyRedactionCompleteness(redactedArgs); err != nil {
		return EvaluateToolCallResult{}, err
	}
	descriptorParams := ToolCallParams{Name: params.Name, Arguments: redactedArgs}
	descriptor, err := BuildActionDescriptorFromToolCall(meta, descriptorParams, server)
	if err != nil {
		return EvaluateToolCallResult{}, err
	}

	// Pre-dispatch content session-taint stamp (DoD#2): if a PRIOR tool-call
	// RESULT in this (tenant, session) carried a prompt injection, stamp the
	// trigger risk tag + citation onto the descriptor so the MCPGate can DENY a
	// destructive call content-awarely BEFORE any upstream Invoke. The taint is
	// thus visible to the gate at decision time, never after the side effect.
	//
	// LOOKUP-ERROR POSTURE: record lookup failures on the descriptor so MCPGate
	// can fail-closed for DESTRUCTIVE tools only when
	// CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE is enabled. The flag defaults OFF,
	// preserving today's fail-open behavior; the read-side persist path remains
	// fail-open by design. Decision captured in
	// Monday-demo/artifacts/taint-fail-open-decision.md (task-098b8d02).
	if deps.TaintStore != nil {
		if t, ok, terr := deps.TaintStore.GetTaint(ctx, meta.Tenant, meta.SessionID); terr != nil {
			descriptor.TaintLookupFailed = true
			slog.WarnContext(ctx, "mcp.session_taint.lookup_failed",
				"tenant", meta.Tenant, "session_id", meta.SessionID, "error", terr.Error())
		} else if ok && t != nil {
			descriptor.RiskTags = append(descriptor.RiskTags, config.RiskTagSessionPromptInjection)
			descriptor.SessionTaint = &config.ActionSessionTaint{
				Pattern:    t.Pattern,
				Snippet:    t.Snippet,
				SourceTool: t.Tool,
				Severity:   t.Severity,
				DetectedAt: t.DetectedAt,
			}
		}
	}

	event := &edge.AgentActionEvent{
		EventID:     deps.EventIDFactory(),
		SessionID:   meta.SessionID,
		ExecutionID: meta.ExecutionID,
		TenantID:    meta.Tenant,
		PrincipalID: meta.Principal,
		Timestamp:   deps.Clock(),
		Layer:       edge.LayerMCP,
		Kind:        edge.EventKindMCPToolPre,
		ToolName:    params.Name,
		ActionName:  params.Name,
	}

	inlineRedacted, artifactPtr, err := materializeRedactedPayload(ctx, deps, redactedArgs, event, edge.ArtifactTypeMCPRequest)
	if err != nil {
		return EvaluateToolCallResult{}, err
	}
	event.InputRedacted = inlineRedacted
	if artifactPtr != nil {
		event.ArtifactPointers = append(event.ArtifactPointers, *artifactPtr)
	}

	var decision PolicyDecision
	if deps.Pipeline != nil {
		decision, _ = deps.Pipeline.Dispatch(ctx, &config.PolicyInput{
			Tenant: meta.Tenant,
			Action: descriptor,
			Meta:   config.PolicyMeta{ActorID: meta.Principal, AgentID: meta.AgentID},
		})
	}
	event.Decision = mapPolicyDecisionToEdge(decision)
	// Constraints carry through from the gate's AWC verdict so audit
	// consumers and downstream tools see the constraint map that
	// authorized the call. Empty/nil for ALLOW / DENY / REQUIRE_HUMAN /
	// THROTTLE — only AWC populates this surface today.
	if len(decision.Constraints) > 0 {
		event.Constraints = decision.Constraints
	}
	// DENY / THROTTLE flip the kind to failed because the call will not
	// reach upstream. REQUIRE_HUMAN keeps kind=pre because the call is
	// awaiting an approval decision; the matching post or failed will
	// be emitted later when the approval lifecycle resolves. ALLOW /
	// ALLOW_WITH_CONSTRAINTS keep kind=pre — upstream forwarding emits
	// the matching post in the InvokeToolWithPolicy wrapper.
	if !decision.Allowed() && !decision.requiresHuman() && decision.Decision != pb.DecisionType_DECISION_TYPE_UNSPECIFIED {
		event.Kind = edge.EventKindMCPToolFailed
		event.Status = edge.ActionStatusBlocked
		event.DecisionReason = decision.Reason
	} else {
		event.Status = edge.ActionStatusOK
		if decision.Reason != "" {
			event.DecisionReason = decision.Reason
		}
	}

	if deps.EventEmitter == nil {
		return EvaluateToolCallResult{}, errors.New("deps.EventEmitter is required")
	}
	if err := deps.EventEmitter.Emit(ctx, event); err != nil {
		return EvaluateToolCallResult{}, fmt.Errorf("emit pre event: %w", err)
	}
	logToolCallDecision(ctx, event, descriptor, decision)

	return EvaluateToolCallResult{
		Decision:    decision,
		PreEvent:    event,
		ArtifactPtr: artifactPtr,
	}, nil
}

// logToolCallDecision emits one structured line per gate decision into
// the package's slog handler. Fields are operator-facing identifiers and
// the decision outcome — no argument values, redaction summaries, or
// approval claim text. The redacted-payload inline map already lives on
// the event for the audit trail; the log line is for live observability
// (greppable decision stream, alert wiring on deny spikes).
func logToolCallDecision(ctx context.Context, event *edge.AgentActionEvent, desc *config.ActionDescriptor, dec PolicyDecision) {
	if event == nil || desc == nil {
		return
	}
	level := slog.LevelInfo
	switch dec.Decision {
	case pb.DecisionType_DECISION_TYPE_DENY,
		pb.DecisionType_DECISION_TYPE_THROTTLE:
		level = slog.LevelWarn
	case pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:
		level = slog.LevelInfo
	}
	slog.Default().LogAttrs(ctx, level, "mcp.tool.policy_decision",
		slog.String("tenant", event.TenantID),
		slog.String("principal", event.PrincipalID),
		slog.String("session_id", event.SessionID),
		slog.String("execution_id", event.ExecutionID),
		slog.String("event_id", event.EventID),
		slog.String("server", desc.Server),
		slog.String("tool", desc.Tool),
		slog.String("verb", string(desc.Verb)),
		slog.String("decision", string(event.Decision)),
		slog.String("gate_id", dec.GateID),
		slog.String("code", dec.Code),
		// constraint_count surfaces AWC volume to operator-facing log
		// streams (greppable for AWC bursts / dashboards) without
		// leaking the structured constraint VALUES, which may carry
		// sensitive policy detail (allowed hosts, redaction levels)
		// per CLAUDE.md security rails and feedback_no_ai_slop. The
		// full constraint map lives on the audit-bound event +
		// artifact pointer, not the live log stream.
		slog.Int("constraint_count", len(dec.Constraints)),
	)
}

// materializeRedactedPayload returns the inline-safe redacted map and
// an optional artifact pointer the caller embeds on the event. The
// artifact path triggers when EITHER the inline serialization exceeds
// MaxInputRedactedBytes (capacity bound) OR the redacted payload
// contains a high-severity finding marker (forensics bound: every
// credential-touching call gets preserved evidence even when small).
//
// Failures of the artifact store propagate as errors so the caller can
// fail closed; we never inline-truncate without preserving the full
// payload elsewhere.
func materializeRedactedPayload(ctx context.Context, deps ToolCallDeps, payload []byte, event *edge.AgentActionEvent, artType edge.ArtifactType) (map[string]any, *edge.ArtifactPointer, error) {
	if len(payload) == 0 {
		return nil, nil, nil
	}
	var inlineMap map[string]any
	if err := json.Unmarshal(payload, &inlineMap); err != nil {
		inlineMap = map[string]any{"_redacted": "[REDACTED:unparseable_args]"}
	}
	inlineBytes, _ := json.Marshal(inlineMap)
	oversized := len(inlineBytes) > edge.MaxInputRedactedBytes
	highSeverity := hasHighSeverityFinding(payload)
	if !oversized && !highSeverity {
		return inlineMap, nil, nil
	}
	if deps.ArtifactStore == nil {
		return nil, nil, errors.New("redacted payload requires artifact storage but no artifact store wired")
	}
	ptr, err := deps.ArtifactStore.Put(ctx, ArtifactPutRequest{
		Type:        artType,
		Payload:     payload,
		TenantID:    event.TenantID,
		SessionID:   event.SessionID,
		ExecutionID: event.ExecutionID,
		EventID:     event.EventID,
		ContentType: "application/json",
	})
	if err != nil {
		return nil, nil, fmt.Errorf("artifact store put: %w", err)
	}
	// Small payloads with high-severity findings keep their inline map
	// (no value in truncating a 200-byte event); large payloads collapse
	// to a 4 KiB summary so the inline event stays within the cap.
	if !oversized {
		inlineMap["_artifact_pointer"] = ptr.URI
		inlineMap["_artifact_sha256"] = ptr.SHA256
		inlineMap["_high_severity_finding"] = true
		return inlineMap, ptr, nil
	}
	summary := map[string]any{
		"_redacted_summary":      truncateForSummary(payload, inlineRedactedSummaryBytes),
		"_artifact_pointer":      ptr.URI,
		"_artifact_sha256":       ptr.SHA256,
		"_inline_bytes_cap":      inlineRedactedSummaryBytes,
		"_full_size":             len(payload),
		"_high_severity_finding": highSeverity,
	}
	return summary, ptr, nil
}

// truncateForSummary returns the first n bytes of payload (or all of
// payload if shorter). The summary is a marker for triagers: full
// fidelity is in the artifact store.
func truncateForSummary(payload []byte, n int) string {
	if len(payload) <= n {
		return string(payload)
	}
	return string(payload[:n]) + "...[truncated]"
}

// mapPolicyDecisionToEdge translates a gate decision into the edge
// event's EdgeDecision enum. Unfired/UNSPECIFIED maps to ALLOW so the
// downstream pre event records what actually happened (the call was
// allowed to proceed).
func mapPolicyDecisionToEdge(dec PolicyDecision) edge.EdgeDecision {
	switch dec.Decision {
	case pb.DecisionType_DECISION_TYPE_DENY:
		return edge.DecisionDeny
	case pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:
		return edge.DecisionRequireApproval
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		return edge.DecisionThrottle
	case pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		return edge.DecisionConstrain
	default:
		return edge.DecisionAllow
	}
}

// Allowed reports whether the decision passed the gate. AWC counts
// as allowed (the constraints apply at the upstream call, not at
// pipeline exit). Unfired/UNSPECIFIED also passes.
func (d PolicyDecision) Allowed() bool {
	switch d.Decision {
	case pb.DecisionType_DECISION_TYPE_UNSPECIFIED,
		pb.DecisionType_DECISION_TYPE_ALLOW,
		pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		return true
	}
	return false
}

// requiresHuman reports whether the decision requires a handoff to
// the approval-store adapter.
func (d PolicyDecision) requiresHuman() bool {
	return d.Decision == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
}

// defaultEventIDFactory returns a uuid-shaped 32-hex-char ID seeded
// from crypto/rand. EventID is for tracing / event payload identity;
// retry dedupe identity is semantic, derived in dedupeBegin from
// (tenant, server, tool, action_hash, session, execution, principal).
// Two retries with the same EventID would still dedupe; two retries
// with different EventIDs but identical semantic inputs ALSO dedupe.
func defaultEventIDFactory() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand should never fail in practice; if it does, fall
		// back to a timestamp-shaped ID so dedupe stays consistent
		// within a process even if collision probability rises.
		ns := time.Now().UTC().UnixNano()
		sum := sha256.Sum256([]byte(fmt.Sprintf("evt-%d", ns)))
		return hex.EncodeToString(sum[:16])
	}
	return hex.EncodeToString(buf[:])
}

// InvokeToolWithPolicy wraps a tool dispatch with full pre/post/failed
// policy semantics. The flow:
//  1. EvaluateToolCall — emits pre event with decision.
//  2. On DENY: return early with IsError result; failed event already
//     emitted (EvaluateToolCall flipped kind to mcp.tool.failed).
//  3. On REQUIRE_HUMAN: hand off to ApprovalHandoff; return
//     approval-pending result; NO upstream call yet.
//  4. On ALLOW/ALLOW_WITH_CONSTRAINTS: forward to deps.Upstream; emit
//     post event on success, failed event on upstream error. Both
//     paths redact the result before emission to preserve the
//     no-raw-secrets contract.
//
// Idempotency: when the caller supplies a stable EventID via
// deps.EventIDFactory, the bridge dedupes via per-EventID sync.Once
// state so retrying the same request produces exactly one pre+post
// (or pre+failed) pair in the emitter.
func InvokeToolWithPolicy(ctx context.Context, deps ToolCallDeps, params ToolCallParams, server string) (*ToolCallResult, error) {
	// Apply zero-value defaults consistently across InvokeToolWithPolicy
	// and EvaluateToolCall so downstream helpers (newPostEvent and the
	// redactor pass inside EvaluateToolCall) never see a nil Clock or
	// Redactor.
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	if deps.Redactor == nil {
		deps.Redactor = DefaultRedactor()
	}
	if deps.EventIDFactory == nil {
		deps.EventIDFactory = defaultEventIDFactory
	}
	// Pin the EventID for the entire InvokeToolWithPolicy lifecycle so
	// the pre event and any post/failed event share a single tracing
	// identifier. deps is passed by value here so overriding the factory
	// does not leak to other callers. The EventID is no longer part of
	// the retry-dedupe identity (dedupeBegin derives that from semantic
	// inputs); the pin is purely for correlated tracing.
	stableEventID := deps.EventIDFactory()
	deps.EventIDFactory = func() string { return stableEventID }
	// Compute the semantic dedupe key from CallMetadata + params BEFORE
	// EvaluateToolCall so two retries with the same caller identity +
	// tool + canonical action share a single slot regardless of the
	// EventIDs the factory emits per call. Empty key means metadata was
	// not stashed; dedupeBegin then short-circuits and EvaluateToolCall
	// surfaces the real missing_mcp_metadata error below.
	dedupeID := semanticDedupeKeyForCall(ctx, params, server)
	winner, hit := dedupeBegin(ctx, deps, dedupeID)
	if hit != nil {
		return hit.result, hit.err
	}
	// dedupeFinish must run on every return path below when we won the
	// slot. Use a named return + deferred publisher so the singleflight
	// waiters never deadlock on a panic or early-return path. The
	// skipDedupeCache flag tells the defer to publish the outcome to
	// in-process waiters AND delete the cross-process slot so a later
	// retry re-evaluates: required for DENY decisions so a policy-bundle
	// update flips behavior immediately instead of returning the cached
	// DENY for the full MCPDedupeTTL.
	var (
		finalResult     *ToolCallResult
		finalErr        error
		skipDedupeCache bool
	)
	if winner != nil {
		defer func() {
			if rec := recover(); rec != nil {
				finalErr = fmt.Errorf("panic: %v", rec)
				if skipDedupeCache {
					dedupeFinishNoCache(deps, dedupeID, winner, finalResult, finalErr)
				} else {
					dedupeFinish(deps, dedupeID, winner, finalResult, finalErr)
				}
				panic(rec)
			}
			if skipDedupeCache {
				dedupeFinishNoCache(deps, dedupeID, winner, finalResult, finalErr)
				return
			}
			dedupeFinish(deps, dedupeID, winner, finalResult, finalErr)
		}()
	}
	evalResult, err := EvaluateToolCall(ctx, deps, params, server)
	if err != nil {
		finalErr = err
		return nil, err
	}
	dec := evalResult.Decision
	switch {
	case dec.requiresHuman():
		// Require deps.ApprovalHandoff: without it we cannot mint an
		// approval reference, so returning "approval pending: " with an
		// empty ref leaves the caller no way to resume — the only safe
		// outcome is a misconfiguration error so deployment notices.
		// AgentID is sourced from CallMetadata, not from PreEvent.PrincipalID:
		// PrincipalID identifies WHO called (user / service principal),
		// while the approval-hold resume contract is keyed on the agent
		// identity that the tool-call is being attributed to.
		if deps.ApprovalHandoff == nil {
			finalErr = errors.New("deps.ApprovalHandoff is required for REQUIRE_HUMAN decisions")
			return nil, finalErr
		}
		callMeta, _ := CallMetadataFromContext(ctx)
		// Sub-E #15: route the mint-side ActionHash through the SAME
		// BuildMCPApprovalBinding helper the consume side
		// (ProcessApprovalClaim) uses. The gateway adapter
		// (mintEdgeApprovalForActionGate) ALSO calls this helper, so all
		// three approval-lifecycle hash sites resolve to a single
		// definition. Drift here previously surfaced as
		// ApprovalConflictKindArgsMismatch on retries with `_approval_ref`.
		actionHash, _ := BuildMCPApprovalBinding(callMeta.Tenant, server, params, "")
		ref, herr := deps.ApprovalHandoff.ConsumeActionGateDecision(ctx, dec, ToolCallApprovalContext{
			Tenant:     evalResult.PreEvent.TenantID,
			AgentID:    callMeta.AgentID,
			Server:     server,
			Tool:       params.Name,
			ActionHash: actionHash,
			// EDGE-103 reopen #1: plumb raw args through so the gate's
			// mint side can compute the same InputHash that
			// ProcessApprovalClaim's BuildMCPApprovalBinding produces.
			Args: params.Arguments,
		})
		if herr != nil {
			finalErr = fmt.Errorf("approval handoff: %w", herr)
			return nil, finalErr
		}
		approvalPending := &ToolCallResult{
			Content: []ContentItem{{
				Type: "text",
				Text: fmt.Sprintf("approval pending: %s", ref),
			}},
			IsError: false,
		}
		finalResult = approvalPending
		return approvalPending, nil
	case !dec.Allowed():
		// EvaluateToolCall already emitted mcp.tool.failed for the deny.
		// Do NOT cache the denial: a subsequent policy-bundle update that
		// flips the tuple to ALLOW must take effect on the next call, not
		// after MCPDedupeTTL expires (HIGH audit finding 2026-05-20).
		// skipDedupeCache routes the defer through dedupeFinishNoCache so
		// in-process waiters still observe the DENY outcome (no semantic
		// drift between winner and waiter) but the cross-process slot is
		// removed instead of being re-stored as a completed record.
		denyResult := &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: dec.Reason}},
			IsError: true,
		}
		if h := InvocationHandleFromContext(ctx); h != nil {
			h.MarkPolicyDenied(dec.Reason, dec.SubReason, dec.Extra)
		}
		finalResult = denyResult
		skipDedupeCache = true
		return denyResult, nil
	}
	if deps.Upstream == nil {
		finalErr = errors.New("deps.Upstream is required for ALLOW decisions")
		return nil, finalErr
	}
	upstreamResult, upstreamErr := deps.Upstream.Invoke(ctx, params)
	if upstreamErr != nil {
		failed := newPostEvent(evalResult.PreEvent, deps.Clock, edge.EventKindMCPToolFailed, dec)
		failed.ErrorMessage = sanitizeUpstreamError(upstreamErr)
		if emitErr := deps.EventEmitter.Emit(ctx, failed); emitErr != nil {
			finalErr = fmt.Errorf("emit failed event: %w", emitErr)
			return nil, finalErr
		}
		finalErr = upstreamErr
		return nil, upstreamErr
	}
	post := newPostEvent(evalResult.PreEvent, deps.Clock, edge.EventKindMCPToolPost, dec)
	if err := deps.EventEmitter.Emit(ctx, post); err != nil {
		finalErr = fmt.Errorf("emit post event: %w", err)
		return nil, finalErr
	}
	// Best-effort content session-taint (DoD#1): scan the read RESULT for a
	// prompt injection and, on a hit, persist a taint keyed by (tenant, session)
	// so a LATER destructive call in this session is denied content-awarely
	// (EvaluateToolCall's pre-dispatch stamp + the MCPGate taint deny). Pure side
	// effect for FUTURE calls: the read already executed upstream, so any failure
	// inside is logged and never alters this read's result or decision. A dedupe
	// cache-hit read returns via dedupeBegin without re-invoking upstream and so
	// won't re-scan — fine: the call that won the slot already persisted the taint.
	scanResultForSessionTaint(ctx, deps, params.Name, upstreamResult, stableEventID)
	finalResult = upstreamResult
	return upstreamResult, nil
}

// maxResultScanBytes bounds a single scanned window. Each Content[].Text and each
// StructuredContent string leaf is scanned in windows of this size; a field larger
// than this is split into overlapping windows (see scanResultUnits) so an injection
// at any offset still reaches the scanner. 256 KiB comfortably holds any single
// board cell / note.
const maxResultScanBytes = 256 * 1024

// scanWindowOverlapBytes is carried from the tail of one window into the next when a
// single field is split across windows, so an injection phrase straddling a window
// boundary is scanned intact. Must exceed the longest directive the scanner matches
// (the shipped injection patterns are well under 1 KiB).
const scanWindowOverlapBytes = 1024

// maxResultScanBudgetBytes bounds the TOTAL bytes scanned across all windows for one
// tool-call result (DoS cap: a jumbo board read cannot pin the scanner). Generous
// enough to cover a realistic full-page board read end to end; coverage stops past it
// only for a pathological multi-MiB result.
const maxResultScanBudgetBytes = 1024 * 1024

// maxResultScanWindows bounds the NUMBER of windows scanned for one result (DoS cap on
// count, e.g. a result with millions of tiny content items / structured leaves).
const maxResultScanWindows = 8192

// scanResultForSessionTaint is the best-effort read-side half of the content
// session-taint mechanism (DoD#1). On the ALLOW path it scans the tool-call
// RESULT for a prompt injection and, on a hit, persists a taint keyed by
// (tenant, session) so a LATER destructive call in the same session is denied
// content-awarely (see EvaluateToolCall's pre-dispatch stamp + the MCPGate taint
// deny). It is a pure side effect for FUTURE calls: the read already executed
// upstream, so every failure path here logs and returns WITHOUT touching the
// caller's result or decision. No-op unless BOTH ResultScanner and TaintStore
// are wired (feature disabled, like a nil DedupeState).
func scanResultForSessionTaint(ctx context.Context, deps ToolCallDeps, tool string, result *ToolCallResult, eventID string) {
	if deps.ResultScanner == nil || deps.TaintStore == nil || result == nil || result.IsError {
		return
	}
	meta, ok := CallMetadataFromContext(ctx)
	if !ok || meta.Tenant == "" || meta.SessionID == "" {
		return
	}
	// Scan the result as a bounded sequence of windows (scanResultUnits) so an
	// injection in ANY field/position — not only one within a single global byte
	// cap — is detected. Cite the first finding; the shipped scanner emits uniformly
	// high-severity hits, so the first is representative. (A future multi-severity
	// scanner could select the highest here without changing the persisted shape.)
	f, found := scanResultUnits(result, deps.ResultScanner)
	if !found {
		return
	}
	if err := deps.TaintStore.Taint(ctx, meta.Tenant, meta.SessionID, SessionTaint{
		Tool:          tool,
		Pattern:       f.Pattern,
		Snippet:       f.Snippet,
		Severity:      f.Severity,
		Confidence:    f.Confidence,
		DetectedAt:    deps.Clock(),
		SourceEventID: eventID,
	}); err != nil {
		slog.WarnContext(ctx, "mcp.session_taint.persist_failed",
			"tenant", meta.Tenant, "session_id", meta.SessionID, "tool", tool, "error", err.Error())
	}
}

// scanResultUnits feeds a tool-call result to the scanner as a BOUNDED sequence of
// windows so an injection in any field/position is detected — not only one that
// happens to fall within a single global byte cap. (The prior assembleResultScanContent
// concatenated all content and truncated at one 256 KiB cap, so an injected column
// after earlier large content, in StructuredContent once Content filled the cap, or
// split across the cap boundary, was never scanned.) Each Content[].Text and each
// StructuredContent string leaf is a unit; a unit larger than maxResultScanBytes is
// split into overlapping windows (scanWindowOverlapBytes) so a phrase split across a
// boundary is still scanned whole. Scanning stops at the first finding or when the
// total-bytes (maxResultScanBudgetBytes) / window-count (maxResultScanWindows) budget
// is exhausted — DoS-safe. Returns the first finding and true on a hit.
func scanResultUnits(result *ToolCallResult, scan func([]byte) []ResultFinding) (ResultFinding, bool) {
	if result == nil || scan == nil {
		return ResultFinding{}, false
	}
	var (
		scannedBytes int
		windows      int
		hit          ResultFinding
		found        bool
	)
	// scanUnit scans one string as one or more overlapping windows. It returns true to
	// STOP all further scanning — either a finding was recorded (found==true) or the
	// DoS budget is exhausted (found stays false); the caller distinguishes via found.
	scanUnit := func(s string) bool {
		if s == "" {
			return false
		}
		// Window over the string directly and copy only each window to []byte, so a
		// single pathologically large field is never materialized whole — peak alloc
		// is one window (maxResultScanBytes) and total scan work is the budget below.
		for off := 0; off < len(s); {
			if windows >= maxResultScanWindows || scannedBytes >= maxResultScanBudgetBytes {
				return true // DoS budget exhausted
			}
			end := off + maxResultScanBytes
			if end > len(s) {
				end = len(s)
			}
			window := []byte(s[off:end])
			windows++
			scannedBytes += len(window)
			if findings := scan(window); len(findings) > 0 {
				hit, found = findings[0], true
				return true
			}
			if end == len(s) {
				break
			}
			// Advance with overlap so a phrase straddling the boundary is caught.
			off = end - scanWindowOverlapBytes
		}
		return false
	}

	for i := range result.Content {
		if scanUnit(result.Content[i].Text) {
			return hit, found
		}
	}
	// StructuredContent: scan each string leaf as its own unit so an injection in a
	// structured field is covered even when Content already consumed budget. Non-string
	// leaves (numbers/bools) cannot carry a directive; the walk is depth-guarded.
	walkStructuredStrings(result.StructuredContent, 0, scanUnit)
	return hit, found
}

// maxStructuredWalkDepth caps StructuredContent recursion as a defense against a
// maliciously deep result (realistic board payloads are only a few levels deep).
const maxStructuredWalkDepth = 64

// walkStructuredStrings visits each string leaf of a JSON-decoded value
// (map[string]any / []any / string), invoking visit until it returns true (stop).
// Returns true once visit has signalled stop, so callers short-circuit.
func walkStructuredStrings(v any, depth int, visit func(string) bool) bool {
	if depth > maxStructuredWalkDepth {
		return false
	}
	switch t := v.(type) {
	case string:
		return visit(t)
	case map[string]any:
		for _, val := range t {
			if walkStructuredStrings(val, depth+1, visit) {
				return true
			}
		}
	case []any:
		for _, val := range t {
			if walkStructuredStrings(val, depth+1, visit) {
				return true
			}
		}
	}
	return false
}

// newPostEvent clones the identity/session fields from the pre event
// onto the post/failed event. EventID is reused so retry dedupe via
// EventID-keyed state stays consistent across the pre/post pair. The
// PolicyDecision sources both the EdgeDecision shape (via
// mapPolicyDecisionToEdge so an AWC verdict records `constrain` not
// `allow`) and the Constraints map (bundled scope from task-3d5c4f37
// so AWC constraint metadata survives into the post-event audit row).
// Empty/nil Constraints map leaves event.Constraints nil so the
// JSON wire payload stays identical to legacy ALLOW events.
//
// Status defaults to `failed` for mcp.tool.failed kind, `ok` for any
// other kind. The edge.Store validation rejects empty Status, so any
// post/failed event MUST carry a known-good value before AppendEvent.
func newPostEvent(pre *edge.AgentActionEvent, clock func() time.Time, kind edge.EventKind, dec PolicyDecision) *edge.AgentActionEvent {
	status := edge.ActionStatusOK
	if kind == edge.EventKindMCPToolFailed {
		status = edge.ActionStatusFailed
	}
	evt := &edge.AgentActionEvent{
		EventID:     pre.EventID,
		SessionID:   pre.SessionID,
		ExecutionID: pre.ExecutionID,
		TenantID:    pre.TenantID,
		PrincipalID: pre.PrincipalID,
		Timestamp:   clock(),
		Layer:       edge.LayerMCP,
		Kind:        kind,
		ToolName:    pre.ToolName,
		ActionName:  pre.ActionName,
		Decision:    mapPolicyDecisionToEdge(dec),
		Status:      status,
	}
	if len(dec.Constraints) > 0 {
		evt.Constraints = dec.Constraints
	}
	return evt
}

// sanitizeUpstreamError redacts URL hosts, query strings, and known
// credential token shapes from an upstream error message before
// emitting it on a failed event. Raw transport errors routinely
// include leak vectors (full URLs with `?token=...` query strings,
// `Bearer` headers, `sk-*`/`ghp_*`/AWS keys); the contract is that
// no such substring lands in a Redis event.
func sanitizeUpstreamError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, re := range upstreamErrorRedactPatterns {
		msg = re.pattern.ReplaceAllString(msg, re.replacement)
	}
	if msg == "" {
		return "[REDACTED:upstream_error]"
	}
	return msg
}

// Retry dedupe: when InvokeToolWithPolicy is called twice with the
// same EventID (e.g. an HTTP client retries on transient failure),
// the second call MUST NOT emit a second pre/post pair. The dedupe
// state lives on ToolCallDeps so each caller scope (test fixture,
// HTTP server instance) gets isolated tracking — no package-globals
// that would bleed across -count=N runs.
// dedupeEntry is the singleflight cell stored in deps.DedupeState. The
// first caller to LoadOrStore wins the slot, runs the upstream once,
// then closes done. Concurrent callers with the same stable EventID see
// the same entry, block on done, and read the result the winner stored.
// On error, the winner deletes the entry so the next retry can fire
// fresh — only successful (non-error) outcomes are cached.
type dedupeEntry struct {
	done   chan struct{}
	result *ToolCallResult
	err    error
}

// semanticDedupeSeparator is the unit-separator byte (0x1F) used to
// join fields in the canonical form fed to SHA-256. Using a
// non-printable control byte instead of `|` prevents pipe-bearing
// tenant IDs or tool names from colliding with neighboring fields
// (tenant=`foo|bar`+tool=`baz` no longer hashes the same as
// tenant=`foo`+tool=`bar|baz`).
const semanticDedupeSeparator = "\x1f"

// computeSemanticDedupeKey returns the hex SHA-256 over the canonical
// (tenant, server, tool, action_hash, session, execution, principal)
// tuple. Two calls with identical semantic inputs produce the same
// key across process retries even when the EventIDFactory emits a
// different ID per call. Empty fields are allowed — the gate-level
// validation (errMissingMCPMetadata) catches missing identity before
// this function ever runs in the InvokeToolWithPolicy path.
func computeSemanticDedupeKey(tenant, server, tool, actionHash, session, execution, principal string) string {
	var b strings.Builder
	b.Grow(len(tenant) + len(server) + len(tool) + len(actionHash) + len(session) + len(execution) + len(principal) + 6)
	b.WriteString(tenant)
	b.WriteString(semanticDedupeSeparator)
	b.WriteString(server)
	b.WriteString(semanticDedupeSeparator)
	b.WriteString(tool)
	b.WriteString(semanticDedupeSeparator)
	b.WriteString(actionHash)
	b.WriteString(semanticDedupeSeparator)
	b.WriteString(session)
	b.WriteString(semanticDedupeSeparator)
	b.WriteString(execution)
	b.WriteString(semanticDedupeSeparator)
	b.WriteString(principal)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// semanticDedupeKeyForCall derives the dedupe key from the CallMetadata
// stashed in ctx plus the tool-call's full canonical-arguments hash.
// Returns the empty string when metadata is missing or any identity
// field is blank; dedupeBegin treats an empty key as "dedupe disabled"
// so the real fail-closed path (errMissingMCPMetadata in EvaluateToolCall)
// stays the single source of identity validation.
//
// The action hash combines the (tenant, server, tool, targetPath) tuple
// AND a SHA-256 over the canonical-JSON of params.Arguments. Without
// the args-hash component, tools whose args carry no path-like key
// (cordum_audit_query?q=, cordum_query_policy?bundle=, generic SQL
// `query=`, any cordum_* tool with non-path args) collapsed every
// distinct arg set into ONE dedupe slot for MCPDedupeTTL — call #2
// with attacker-chosen args would receive call #1's cached result
// without re-evaluating policy, calling upstream, or emitting a
// pre-event audit row (CRITICAL audit finding 2026-05-20).
func semanticDedupeKeyForCall(ctx context.Context, params ToolCallParams, server string) string {
	meta, ok := CallMetadataFromContext(ctx)
	if !ok || meta.Tenant == "" || meta.SessionID == "" || meta.ExecutionID == "" {
		return ""
	}
	var targetPath string
	if parsed, _ := parseArgsForDescriptor(params.Arguments); parsed != nil {
		targetPath = extractTargetPathFromArgs(parsed)
	}
	argsHash := canonicalArgumentsHash(params.Arguments)
	actionHash := actionTupleHashWithArgs(meta.Tenant, server, params.Name, targetPath, argsHash)
	return computeSemanticDedupeKey(meta.Tenant, server, params.Name, actionHash, meta.SessionID, meta.ExecutionID, meta.Principal)
}

// canonicalArgumentsHash returns the hex SHA-256 over the canonical-JSON
// form of params.Arguments. Re-marshalling through json.Unmarshal +
// json.Marshal normalises whitespace and key order so two retries with
// equivalent JSON produce the same hash, while distinct argument values
// produce distinct hashes regardless of which keys they live under.
// Empty / unmarshalable args fall back to the raw byte hash so the
// caller cannot opt out of args-hashing by sending malformed JSON.
func canonicalArgumentsHash(raw json.RawMessage) string {
	if len(raw) == 0 {
		sum := sha256.Sum256([]byte("{}"))
		return hex.EncodeToString(sum[:])
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		if canonical, err := json.Marshal(decoded); err == nil {
			sum := sha256.Sum256(canonical)
			return hex.EncodeToString(sum[:])
		}
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// actionTupleHashWithArgs binds the (tenant, server, tool, target_path,
// args_hash) tuple into a single hex SHA-256. Mirrors ActionTupleHash
// but folds the canonical-args hash into the canonical form so retries
// with the same args dedupe and retries with different args do not.
func actionTupleHashWithArgs(tenant, server, tool, targetPath, argsHash string) string {
	canonical := fmt.Sprintf("%s|%s|%s|%s|%s", tenant, server, tool, normalizeTargetPath(targetPath), argsHash)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// dedupeOutcome bundles the cached result a loser observes when the
// dedupe entry was already completed by another caller. Mirrors the
// (result, err) pair dedupeFinish published for the winner.
type dedupeOutcome struct {
	result *ToolCallResult
	err    error
}

// dedupeWinner carries per-backend state dedupeFinish needs to publish
// the completed outcome. The caller treats this as opaque — the only
// thing that matters at the call site is "did we win?" (winner != nil)
// and "do we have a cached hit?" (handled by the outcome return).
//
// Exactly one of inProcessEntry / redisBacked is non-zero per backend
// type. A winner from the in-process backend holds the *dedupeEntry
// whose done channel must be closed when finishing; a Redis-backed
// winner publishes a completed wire record (or deletes on error).
type dedupeWinner struct {
	inProcessEntry *dedupeEntry
	redisBacked    bool
}

// dedupeBegin reserves the singleflight slot for the supplied semantic
// dedupe key. Two returns:
//
//   - (winner, nil)   caller is the winner; runs the body, then MUST
//     call dedupeFinish so waiters unblock and the cache fills (or
//     clears, on error).
//   - (nil, outcome)  caller observed an already-completed entry;
//     short-circuits with outcome.result / outcome.err.
//   - (nil, nil)      dedupe is disabled (no state or empty key).
//
// In-process: a loser blocks on the winner's done channel until the
// winner closes it. Ctx cancellation surfaces as ctx.Err() so a
// SIGTERM-bound caller doesn't get stuck waiting for a peer.
//
// Redis-backed: a loser observing a pending wire record polls every
// redisDedupePollInterval (default 50ms) until one of: the record
// transitions to completed (short-circuit), the key is deleted by the
// winner's error path (become the new winner), the TTL elapses
// (become the new winner — deadlock-breaker), or ctx is cancelled.
func dedupeBegin(ctx context.Context, deps ToolCallDeps, key string) (*dedupeWinner, *dedupeOutcome) {
	if deps.DedupeState == nil || key == "" {
		return nil, nil
	}
	_, isRedis := deps.DedupeState.(*RedisDedupeStore)
	fresh := newPendingDedupeValue(isRedis)
	existing, loaded := deps.DedupeState.LoadOrStore(key, fresh)
	if !loaded {
		return makeWinner(fresh, isRedis), nil
	}
	switch e := existing.(type) {
	case *dedupeEntry:
		select {
		case <-e.done:
			return nil, &dedupeOutcome{result: e.result, err: e.err}
		case <-ctx.Done():
			return nil, &dedupeOutcome{err: ctx.Err()}
		}
	case *redisDedupeRecord:
		if e.State == redisDedupeStateCompleted {
			return completedRedisDedupeOutcome(deps.DedupeState, key, e)
		}
		return waitForRedisDedupe(ctx, deps.DedupeState, key)
	default:
		// Defensive: a malformed wire value cannot be safely waited on.
		// Overwrite under our key and proceed as the winner; the new
		// caller's dedupeFinish will publish the canonical record.
		deps.DedupeState.Store(key, fresh)
		return makeWinner(fresh, isRedis), nil
	}
}

// newPendingDedupeValue returns the value the winner-selection LoadOrStore
// passes to the backing store. The two backends accept distinct wire
// types so the loser-side type switch in dedupeBegin can dispatch on
// the returned value without a second type-probe round trip.
func newPendingDedupeValue(isRedis bool) any {
	if isRedis {
		return &redisDedupeRecord{State: redisDedupeStatePending}
	}
	return &dedupeEntry{done: make(chan struct{})}
}

// makeWinner promotes the pending value the caller submitted into the
// opaque dedupeWinner handle dedupeFinish consumes.
func makeWinner(fresh any, isRedis bool) *dedupeWinner {
	if isRedis {
		return &dedupeWinner{redisBacked: true}
	}
	entry, _ := fresh.(*dedupeEntry)
	return &dedupeWinner{inProcessEntry: entry}
}

// decodeRedisCompletedOutcome reconstructs the safe duplicate
// *ToolCallResult a Redis loser short-circuits with from the bounded
// metadata the winner published. A malformed metadata record returns nil
// so the caller can promote itself to winner and re-run upstream rather
// than treating an unreadable Redis row as a nil success.
func decodeRedisCompletedOutcome(rec *redisDedupeRecord) *dedupeOutcome {
	if rec == nil {
		return nil
	}
	out := &dedupeOutcome{}
	if rec.ErrorClass != "" {
		out.err = errors.New(rec.ErrorClass)
	}
	if rec.Result == nil {
		if out.err != nil {
			return out
		}
		return nil
	}
	if strings.TrimSpace(rec.Result.ResultSHA256) == "" {
		return nil
	}
	if len(rec.Result.ResultSHA256) != sha256.Size*2 {
		return nil
	}
	out.result = toolCallResultFromRedisDedupeMetadata(*rec.Result)
	return out
}

func redisDedupeResultMetadataFor(result *ToolCallResult) (*redisDedupeResultMetadata, bool) {
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, false
	}
	sum := sha256.Sum256(payload)
	meta := &redisDedupeResultMetadata{
		ResultSHA256: hex.EncodeToString(sum[:]),
	}
	if result != nil {
		meta.IsError = result.IsError
		meta.ContentCount = len(result.Content)
		meta.HasStructuredContent = result.StructuredContent != nil
	}
	return meta, true
}

func toolCallResultFromRedisDedupeMetadata(meta redisDedupeResultMetadata) *ToolCallResult {
	contentCount := meta.ContentCount
	if contentCount < 0 {
		contentCount = 0
	}
	hash := strings.TrimSpace(meta.ResultSHA256)
	text := fmt.Sprintf(
		"duplicate MCP tool result suppressed from Redis dedupe; result_sha256=%s content_count=%d has_structured_content=%t",
		hash,
		contentCount,
		meta.HasStructuredContent,
	)
	return &ToolCallResult{
		Content: []ContentItem{{
			Type: "text",
			Text: text,
		}},
		IsError: meta.IsError,
	}
}

func completedRedisDedupeOutcome(store DedupeStore, key string, rec *redisDedupeRecord) (*dedupeWinner, *dedupeOutcome) {
	if out := decodeRedisCompletedOutcome(rec); out != nil {
		return nil, out
	}
	fresh := newPendingDedupeValue(true)
	store.Store(key, fresh)
	return makeWinner(fresh, true), nil
}

func redisDedupeRecordResultIsTooLarge(rec *redisDedupeRecord) bool {
	if rec == nil || rec.Result == nil {
		return false
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return true
	}
	return len(payload) > maxRedisDedupeRecordBytes
}

func validateRedisDedupeRecordForStore(rec *redisDedupeRecord) bool {
	if rec == nil {
		return false
	}
	if rec.State != redisDedupeStateCompleted {
		return true
	}
	if rec.Result == nil && rec.ErrorClass == "" {
		return false
	}
	if rec.Result != nil && strings.TrimSpace(rec.Result.ResultSHA256) == "" {
		return false
	}
	return !redisDedupeRecordResultIsTooLarge(rec)
}

func redisDedupeRecordForResult(result *ToolCallResult) (*redisDedupeRecord, bool) {
	meta, ok := redisDedupeResultMetadataFor(result)
	if !ok {
		return nil, false
	}
	rec := &redisDedupeRecord{
		State:  redisDedupeStateCompleted,
		Result: meta,
	}
	if !validateRedisDedupeRecordForStore(rec) {
		return nil, false
	}
	return rec, true
}

// redisDedupeLoserDeadlineBudget keeps Redis losers aligned with the
// actual cross-process pending marker. A positive PTTL caps the local
// deadlock-breaker to the key's remaining Redis TTL; missing/expired
// keys use a near-immediate budget so the final steal path runs without
// waiting for another poll tick. Non-Redis stores, PTTL errors, and
// no-TTL keys retain the historical MCPDedupeTTL fallback.
func redisDedupeLoserDeadlineBudget(ctx context.Context, store DedupeStore, key string) time.Duration {
	redisStore, ok := store.(*RedisDedupeStore)
	if !ok || redisStore == nil || redisStore.client == nil {
		return MCPDedupeTTL
	}
	pttlCtx, cancel := context.WithTimeout(ctx, redisDedupeCommandTimeout)
	defer cancel()
	remaining, err := redisStore.client.PTTL(pttlCtx, MCPDedupeKeyPrefix+key).Result()
	if err != nil {
		return MCPDedupeTTL
	}
	switch {
	case remaining > 0:
		return remaining
	case remaining == time.Duration(-1):
		return MCPDedupeTTL
	default:
		return time.Nanosecond
	}
}

// waitForRedisDedupe is the Redis-backed loser polling loop. It polls
// every redisDedupePollInterval, respects ctx.Done(), and stops when
// the entry becomes completed/deleted/expired.
//
// Termination outcomes:
//   - loser observes completed wire record  → short-circuit with cached
//   - loser observes a deleted key (winner errored)  → become new winner
//   - loser observes an expired key (Redis TTL)  → become new winner
//   - ctx cancelled  → outcome carrying ctx.Err()
//
// Redis-backed waits are locally bounded by the key's remaining Redis
// PTTL sampled on loser entry plus the caller context. Non-Redis,
// PTTL-error, and no-TTL cases keep MCPDedupeTTL as the fallback
// deadlock breaker so failures never become unbounded waits.
func waitForRedisDedupe(ctx context.Context, store DedupeStore, key string) (*dedupeWinner, *dedupeOutcome) {
	ticker := time.NewTicker(redisDedupePollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(redisDedupeLoserDeadlineBudget(ctx, store, key))
	defer func() {
		if !deadline.Stop() {
			select {
			case <-deadline.C:
			default:
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil, &dedupeOutcome{err: ctx.Err()}
		case <-deadline.C:
			// Deadline-breaker: TTL elapsed without resolution. Race-
			// safe final check before stealing the slot — a winner
			// that published a completed record in the last polling
			// interval should still short-circuit the loser. Only
			// force-take when the slot is still pending (the genuine
			// crashed-winner case).
			fresh := newPendingDedupeValue(true)
			existing, loaded := store.LoadOrStore(key, fresh)
			if !loaded {
				return makeWinner(existing, true), nil
			}
			if rec, ok := existing.(*redisDedupeRecord); ok && rec.State == redisDedupeStateCompleted {
				return completedRedisDedupeOutcome(store, key, rec)
			}
			store.Store(key, fresh)
			return makeWinner(fresh, true), nil
		case <-ticker.C:
			again, stillLoaded := store.LoadOrStore(key, newPendingDedupeValue(true))
			if !stillLoaded {
				// Key was deleted or expired — our pending write won;
				// proceed as the new winner.
				return makeWinner(again, true), nil
			}
			rec, ok := again.(*redisDedupeRecord)
			if !ok {
				continue
			}
			if rec.State == redisDedupeStateCompleted {
				return completedRedisDedupeOutcome(store, key, rec)
			}
			// Still pending — keep polling.
		}
	}
}

// dedupeFinish publishes the outcome for the singleflight winner. On
// success (err == nil) the entry stays in the store so subsequent
// retries short-circuit. On error the entry is removed so the next
// attempt fires upstream fresh — transient failures must not become
// sticky.
//
// In-process: closes the winner's done channel after stashing result/
// err so waiters unblock with the right outcome.
//
// Redis-backed: writes a completed metadata-only wire record
// (re-applying MCPDedupeTTL) on success, deletes the key on error.
// Unserializable metadata is dropped from the cache (delete-on-error
// fallback) so Redis never stores raw tool-result bodies.
func dedupeFinish(deps ToolCallDeps, key string, winner *dedupeWinner, result *ToolCallResult, err error) {
	if winner == nil {
		return
	}
	if winner.inProcessEntry != nil {
		entry := winner.inProcessEntry
		if entry.done == nil {
			return
		}
		entry.result = result
		entry.err = err
		if err != nil && deps.DedupeState != nil && key != "" {
			// Delete before close prevents fresh arrivals from finding
			// a completed error entry. Existing waiters already hold
			// entry and still observe result/err via close's happens-before.
			deps.DedupeState.Delete(key)
		}
		close(entry.done)
		return
	}
	if winner.redisBacked {
		if deps.DedupeState == nil || key == "" {
			return
		}
		if err != nil {
			deps.DedupeState.Delete(key)
			return
		}
		rec, ok := redisDedupeRecordForResult(result)
		if !ok {
			// Cannot persist a usable completed record cross-process;
			// delete so subsequent retries fire fresh upstream rather
			// than block on a pending record that will never resolve.
			deps.DedupeState.Delete(key)
			return
		}
		deps.DedupeState.Store(key, rec)
	}
}

// dedupeFinishNoCache publishes the outcome to in-process waiters but
// deletes the cross-process slot — no completed record is written. Used
// for DENY decisions so a later policy-bundle flip is observed on the
// next call instead of being shadowed by a stale cached denial for the
// full MCPDedupeTTL. In-process semantics match dedupeFinish: the done
// channel still closes with (result, err) so concurrent waiters observe
// the DENY outcome, but the cross-process Redis slot is cleared.
func dedupeFinishNoCache(deps ToolCallDeps, key string, winner *dedupeWinner, result *ToolCallResult, err error) {
	if winner == nil {
		return
	}
	if deps.DedupeState != nil && key != "" {
		deps.DedupeState.Delete(key)
	}
	if winner.inProcessEntry != nil {
		entry := winner.inProcessEntry
		if entry.done == nil {
			return
		}
		entry.result = result
		entry.err = err
		close(entry.done)
	}
}
