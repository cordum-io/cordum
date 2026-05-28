package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/edge"
)

// EDGE-071: package-level recorder for redaction fail-closed events.
// Defaults to a no-op so unit tests do not need to inject one. Production
// init code (gateway/agentd/hook startup) replaces this with the
// Prometheus recorder via SetRedactionRecorder so the
// redaction_failed_total counter actually fires under the real registry.
var (
	redactionRecorder   edge.Recorder = edge.NewNoopRecorder()
	redactionRecorderMu sync.RWMutex
)

// SetRedactionRecorder replaces the package-level recorder used by Edge
// claude redaction call sites. Pass nil to revert to a no-op (used by
// tests that want to drop emissions without exposing them via the
// production registry). Safe for concurrent use.
func SetRedactionRecorder(r edge.Recorder) {
	if r == nil {
		r = edge.NewNoopRecorder()
	}
	redactionRecorderMu.Lock()
	defer redactionRecorderMu.Unlock()
	redactionRecorder = r
}

func emitRedactionFailed(site, reason string) {
	redactionRecorderMu.RLock()
	r := redactionRecorder
	redactionRecorderMu.RUnlock()
	r.RecordRedactionFailed(site, reason)
}

// MappedHookAction is the transport-neutral result of mapping one Claude Code
// hook payload into the Edge action shape used by Gateway evaluate, AgentdRequest
// forwarding, and AgentActionEvent persistence. Callers turn this into the
// concrete request shape they need without having to re-parse the raw HookInput.
//
// Capability/RiskTags/Labels come from edge.ClassifyEvent; client-supplied values
// in the original HookInput are NOT trusted (the classifier strips them). The
// raw stdin bytes live only in RawPayload, which is never persisted by Cordum
// and is forwarded only to the local cordum-agentd loopback HTTP endpoint.
type MappedHookAction struct {
	Layer       edge.Layer
	Kind        edge.EventKind
	SessionID   string
	ExecutionID string
	TenantID    string
	PrincipalID string
	ToolName    string
	ToolUseID   string
	Capability  string
	RiskTags    []string
	Labels      edge.Labels
	// InputRedacted is the EDGE-004-redacted view of the hook payload's
	// action-relevant fields (tool_input for PreToolUse, prompt for
	// UserPromptSubmit, tool_response/error for PostToolUse, etc.). Raw
	// secret values are masked; truncation findings live in Labels under
	// the `redaction.*` keys.
	InputRedacted map[string]any
	// InputHash is sha256 over the canonical JSON of InputRedacted, formatted
	// as "sha256:<hex>". Stable across replays of the same redacted input.
	InputHash string
	// ActionHash binds the mapped action shape — kind + tool + capability +
	// input_hash + tool_use_id — so EDGE-012 approval retry can replay
	// exactly once for the same action.
	ActionHash string
	// ReasonCode is set on degraded mappings ("missing_tool_name",
	// "missing_tool_input", "unsupported_tool_input_shape",
	// "unknown_hook_version") so the Gateway can record a degraded event
	// instead of a normal action.
	ReasonCode string
	// RawPayload mirrors HookInput.RawPayload — verbatim hook stdin bytes,
	// in-memory only, never persisted. Forwarded to local agentd only.
	RawPayload []byte
	// AgentProduct/AgentVersion identify the Claude binary that fired the
	// hook. Used for Edge labels/audit but NOT for policy decisions.
	AgentProduct string
	AgentVersion string
}

// MappingContext carries the env-derived metadata that turns a raw hook payload
// into a tenant/session/execution-bound action. It is supplied by the runner
// (typically from CORDUM_EDGE_SESSION_ID / CORDUM_EDGE_EXECUTION_ID env vars
// that cordum-agentd sets before invoking the hook). The mapper does not read
// os.Environ() directly so tests can pass a stable context.
type MappingContext struct {
	TenantID     string
	PrincipalID  string
	SessionID    string
	ExecutionID  string
	AgentProduct string
	AgentVersion string

	// Now is used by tests to freeze the timestamp embedded in the
	// AgentActionEvent the mapper builds before classification. Defaults
	// to time.Now if nil.
	Now func() time.Time

	// HashMode controls the redaction hash output. Defaults to
	// edge.RedactionHashRedacted when zero so the deterministic input_hash
	// over the redacted shape is always available.
	HashMode edge.RedactionHashMode
}

// Reason codes the mapper sets on MappedHookAction.ReasonCode when the hook
// payload is parseable but missing required action fields, so the Gateway
// can record a degraded event instead of allowing through a normal action.
const (
	reasonMissingToolName           = "missing_tool_name"
	reasonMissingToolInput          = "missing_tool_input"
	reasonUnsupportedToolInputShape = "unsupported_tool_input_shape"
	reasonUnknownHookEvent          = "unknown_hook_event"
)

// MapHookInput converts a parsed HookInput plus the env-derived MappingContext
// into a MappedHookAction. It deterministically classifies the action via
// edge.ClassifyEvent, redacts the hook payload via edge.RedactValue, and
// produces a stable input_hash + action_hash. Callers must NOT pass raw
// secret values in MappingContext — fields are written to logs/labels.
//
// The runner-supplied MappingContext.SessionID/ExecutionID/TenantID always
// win over any same-named field in the hook payload — cordum-agentd is the
// source of truth for session/execution binding, not whatever Claude reports
// in the hook stdin.
func MapHookInput(input HookInput, ctx MappingContext) (MappedHookAction, error) {
	ctx = sanitizeMappingContext(ctx)
	now := time.Now().UTC()
	if ctx.Now != nil {
		now = ctx.Now()
	}

	kind, kindOK := mapHookEventToKind(input.HookEventName)
	if !kindOK {
		// Runner already filters supportedHookEvent; if a future hook
		// shape slips through, surface a degraded event instead of
		// classifying as if it were a known kind.
		return MappedHookAction{
			Layer:        edge.LayerHook,
			Kind:         edge.EventKind("hook." + safeHookEventLabel(input.HookEventName)),
			SessionID:    ctx.SessionID,
			ExecutionID:  ctx.ExecutionID,
			TenantID:     ctx.TenantID,
			PrincipalID:  ctx.PrincipalID,
			ToolName:     input.ToolName,
			ToolUseID:    input.ToolUseID,
			Capability:   "edge.unknown",
			RiskTags:     []string{"review_required", "unknown"},
			Labels:       baseHookLabels(input, ctx, kind),
			ReasonCode:   reasonUnknownHookEvent,
			RawPayload:   cloneBytes(input.RawPayload),
			AgentProduct: ctx.AgentProduct,
			AgentVersion: ctx.AgentVersion,
		}, nil
	}

	// Detect malformed-but-parseable cases before redaction so the mapper
	// short-circuits with a descriptive ReasonCode instead of forcing the
	// classifier to guess.
	reasonCode := degradedReason(input, kind)

	redactedInput, err := redactHookActionInput(input, kind)
	if err != nil {
		return MappedHookAction{}, err
	}

	inputHash, err := canonicalSHA256(redactedInput)
	if err != nil {
		return MappedHookAction{}, err
	}

	// If the hook payload is degraded (missing required action fields),
	// short-circuit before ClassifyEvent. ClassifyEvent rejects events with
	// missing ToolName/Kind, and even when it accepts a partial event the
	// resulting classification could land on `safe` (empty Bash command
	// path is currently `unknown + review_required`, which is fine, but
	// we'd lose the specific ReasonCode for downstream audit). Failing
	// closed with the original ReasonCode keeps the audit trail honest.
	if reasonCode != "" {
		return MappedHookAction{
			Layer:         edge.LayerHook,
			Kind:          kind,
			SessionID:     ctx.SessionID,
			ExecutionID:   ctx.ExecutionID,
			TenantID:      ctx.TenantID,
			PrincipalID:   ctx.PrincipalID,
			ToolName:      input.ToolName,
			ToolUseID:     input.ToolUseID,
			Capability:    "edge.unknown",
			RiskTags:      []string{"review_required", "unknown"},
			Labels:        degradedHookLabels(input, ctx, kind),
			InputRedacted: redactedInput,
			InputHash:     inputHash,
			ReasonCode:    reasonCode,
			RawPayload:    cloneBytes(input.RawPayload),
			AgentProduct:  ctx.AgentProduct,
			AgentVersion:  ctx.AgentVersion,
		}, nil
	}

	actionEvent := edge.AgentActionEvent{
		EventID:       deriveEventID(input, ctx, now),
		SessionID:     ctx.SessionID,
		ExecutionID:   ctx.ExecutionID,
		TenantID:      ctx.TenantID,
		PrincipalID:   ctx.PrincipalID,
		Timestamp:     now,
		Layer:         edge.LayerHook,
		Kind:          kind,
		AgentProduct:  ctx.AgentProduct,
		ToolName:      input.ToolName,
		InputRedacted: redactedInput,
		Decision:      edge.DecisionRecorded,
		Status:        edge.ActionStatusOK,
		Labels:        baseHookLabels(input, ctx, kind),
	}

	classification, err := edge.ClassifyEvent(actionEvent)
	if err != nil {
		// ClassifyEvent rejects only on missing required fields; the
		// mapper guarantees Layer/Kind/ToolName upstream via degradedReason.
		// If it fires anyway (future schema drift), surface a degraded
		// action with reasonUnsupportedToolInputShape so the Gateway can
		// record an audit event without trusting client classification.
		return MappedHookAction{
			Layer:         edge.LayerHook,
			Kind:          kind,
			SessionID:     ctx.SessionID,
			ExecutionID:   ctx.ExecutionID,
			TenantID:      ctx.TenantID,
			PrincipalID:   ctx.PrincipalID,
			ToolName:      input.ToolName,
			ToolUseID:     input.ToolUseID,
			Capability:    "edge.unknown",
			RiskTags:      []string{"review_required", "unknown"},
			Labels:        degradedHookLabels(input, ctx, kind),
			InputRedacted: redactedInput,
			InputHash:     inputHash,
			ReasonCode:    reasonUnsupportedToolInputShape,
			RawPayload:    cloneBytes(input.RawPayload),
			AgentProduct:  ctx.AgentProduct,
			AgentVersion:  ctx.AgentVersion,
		}, nil
	}

	capability := classification.Capability
	riskTags := append([]string(nil), classification.RiskTags...)
	labels := mergeLabels(actionEvent.Labels, classification.Labels)

	actionHash, err := canonicalSHA256(actionHashInputs{
		Kind:       string(kind),
		ToolName:   input.ToolName,
		ToolUseID:  input.ToolUseID,
		Capability: capability,
		InputHash:  inputHash,
	})
	if err != nil {
		return MappedHookAction{}, err
	}

	return MappedHookAction{
		Layer:         edge.LayerHook,
		Kind:          kind,
		SessionID:     ctx.SessionID,
		ExecutionID:   ctx.ExecutionID,
		TenantID:      ctx.TenantID,
		PrincipalID:   ctx.PrincipalID,
		ToolName:      input.ToolName,
		ToolUseID:     input.ToolUseID,
		Capability:    capability,
		RiskTags:      riskTags,
		Labels:        labels,
		InputRedacted: redactedInput,
		InputHash:     inputHash,
		ActionHash:    actionHash,
		ReasonCode:    reasonCode,
		RawPayload:    cloneBytes(input.RawPayload),
		AgentProduct:  ctx.AgentProduct,
		AgentVersion:  ctx.AgentVersion,
	}, nil
}

// mapHookEventToKind translates the Claude hook event name to the canonical
// EventKind. The switch must be exhaustive over the EventKindHook* constants
// declared in core/edge/event.go — EDGE-049 closed the UserPromptSubmit gap;
// EDGE-066 closes the same gap for PolicyDecision + PermissionRequest, two
// tool-less metadata kinds the classifier already accepts via
// hookKindRequiresTool.
func mapHookEventToKind(eventName string) (edge.EventKind, bool) {
	switch eventName {
	case "PreToolUse":
		return edge.EventKindHookPreToolUse, true
	case "PostToolUse":
		return edge.EventKindHookPostToolUse, true
	case "PostToolUseFailure":
		return edge.EventKindHookPostToolUseFailure, true
	case "UserPromptSubmit":
		return edge.EventKindHookUserPromptSubmit, true
	case "ConfigChange":
		return edge.EventKindHookConfigChange, true
	case "FileChanged":
		return edge.EventKindHookFileChanged, true
	case "PolicyDecision":
		return edge.EventKindHookPolicyDecision, true
	case "PermissionRequest":
		return edge.EventKindHookPermissionRequest, true
	default:
		return "", false
	}
}

// degradedReason returns a ReasonCode if the hook payload is missing
// required action fields. Used to short-circuit safe-classification for
// version-drift / malformed shapes without rejecting them outright.
func degradedReason(input HookInput, kind edge.EventKind) string {
	switch kind {
	case edge.EventKindHookPreToolUse:
		if strings.TrimSpace(input.ToolName) == "" {
			return reasonMissingToolName
		}
		if len(input.ToolInput) == 0 {
			return reasonMissingToolInput
		}
	case edge.EventKindHookPostToolUse, edge.EventKindHookPostToolUseFailure:
		if strings.TrimSpace(input.ToolName) == "" {
			return reasonMissingToolName
		}
	}
	return ""
}

// redactHookActionInput pulls the action-relevant fields from the hook
// payload and runs them through the EDGE-004 redactor. The result is the
// authoritative `input_redacted` shape passed to ClassifyEvent and embedded
// in MappedHookAction.InputRedacted. Hook-level fields like prompt and
// tool_response are merged in for events that don't carry tool_input.
func redactHookActionInput(input HookInput, kind edge.EventKind) (map[string]any, error) {
	source := map[string]any{}
	switch kind {
	case edge.EventKindHookPreToolUse:
		copyToolInputRedacted(source, input.ToolInput)
	case edge.EventKindHookPostToolUse, edge.EventKindHookPostToolUseFailure:
		copyToolInputRedacted(source, input.ToolInput)
		if len(input.ToolResponse) > 0 {
			source["tool_response_redacted"] = input.ToolResponse
		}
		if input.DurationMS > 0 {
			source["duration_ms"] = input.DurationMS
		}
		if input.Error != "" {
			source["error_redacted"] = input.Error
		}
	case edge.EventKindHookUserPromptSubmit:
		if input.Prompt != "" {
			// Suffix `_redacted` signals to the dashboard sanitizer that this
			// content has already passed through edge.RedactValue and is safe
			// to render. Bare `prompt` would be stripped by the dashboard's
			// defense-in-depth (see dashboard/src/api/transform.ts isUnsafeEdgeKey).
			source["prompt_redacted"] = input.Prompt
		}
	case edge.EventKindHookConfigChange:
		if input.Source != "" {
			source["source"] = input.Source
		}
	case edge.EventKindHookFileChanged:
		if input.FilePath != "" {
			source["file_path"] = input.FilePath
		}
		if input.FileEvent != "" {
			source["event"] = input.FileEvent
		}
	}

	if len(source) == 0 {
		return map[string]any{}, nil
	}

	result, err := edge.RedactValue(source, edge.RedactionOptions{
		HashMode: edge.RedactionHashNone,
	})
	if err != nil {
		return nil, err
	}
	redacted, ok := result.Value.(map[string]any)
	if !ok {
		// Defensive: if RedactValue returns a non-map (e.g. truncation
		// placeholder), keep the redaction signal but represent it as a
		// map so downstream JSON encoding is stable.
		return map[string]any{"_redacted": result.Value}, nil
	}
	return sanitizeHookBoundaryMap(redacted), nil
}

// copyToolInputRedacted flattens Claude's tool_input map into the action source
// map under `_redacted`-suffixed keys so the dashboard sanitizer accepts them.
// Bare keys like `command`, `file_path`, `tool_input` are stripped by
// `dashboard/src/api/transform.ts isUnsafeEdgeKey` as defense-in-depth; only
// keys ending in `_redacted` (or containing `redacted`) are trusted because the
// suffix is the wire signal that the value already passed through
// edge.RedactValue (EDGE-004 secret stripper). Known Claude tool_input field
// names are renamed individually so classifier.go can still find them via
// inputStringAny multi-alias lookups; unknown fields fall through into a
// `tool_input_redacted` bucket so we never silently drop content from
// version-drifted Claude payloads.
func copyToolInputRedacted(dest, src map[string]any) {
	if len(src) == 0 {
		return
	}
	var extras map[string]any
	for key, value := range src {
		renamed, known := claudeToolInputFieldRedactedName(key)
		if known {
			dest[renamed] = value
			continue
		}
		if extras == nil {
			extras = map[string]any{}
		}
		extras[key] = value
	}
	if len(extras) > 0 {
		dest["tool_input_redacted"] = extras
	}
}

// claudeToolInputFieldRedactedName maps every Claude built-in tool_input field
// name to its `_redacted`-suffixed counterpart. The known set covers the
// Read/Edit/Write/MultiEdit/Bash/Glob/Grep/Move tools listed in
// classifier.go's classifyHookEvent dispatch plus the alias keys
// classifier.go's classifyFileMove already accepts (source/destination/dest/
// old_path/new_path/from/to). Any field NOT in this set is bucketed by the
// caller into `tool_input_redacted` so unknown content reaches evidence intact.
func claudeToolInputFieldRedactedName(key string) (string, bool) {
	switch key {
	case "file_path":
		return "file_path_redacted", true
	case "path":
		return "path_redacted", true
	case "command":
		return "command_redacted", true
	case "content":
		return "content_redacted", true
	case "old_string":
		return "old_string_redacted", true
	case "new_string":
		return "new_string_redacted", true
	case "source":
		return "source_redacted", true
	case "destination":
		return "destination_redacted", true
	case "dest":
		return "dest_redacted", true
	case "old_path":
		return "old_path_redacted", true
	case "new_path":
		return "new_path_redacted", true
	case "from":
		return "from_redacted", true
	case "to":
		return "to_redacted", true
	case "pattern":
		return "pattern_redacted", true
	case "url":
		return "url_redacted", true
	}
	return "", false
}

// baseHookLabels builds the labels every Claude hook event carries before
// classifier-specific labels are merged in. Only trusted, bounded values
// are added — never raw command/prompt content, never untyped extras from
// the hook payload.
func baseHookLabels(input HookInput, ctx MappingContext, kind edge.EventKind) edge.Labels {
	labels := edge.Labels{
		"edge.layer": string(edge.LayerHook),
	}
	if kind != "" {
		labels["edge.kind"] = string(kind)
		labels["hook.event"] = string(kind)
	}
	if strings.TrimSpace(input.ToolName) != "" {
		labels["hook.tool_name"] = input.ToolName
	}
	if strings.TrimSpace(ctx.AgentProduct) != "" {
		labels["agent.product"] = ctx.AgentProduct
	}
	if strings.TrimSpace(ctx.AgentVersion) != "" {
		labels["agent.version"] = ctx.AgentVersion
	}
	return labels
}

// degradedHookLabels mirrors baseHookLabels but pins command.class/family
// to "unknown" so a degraded action cannot accidentally carry a `safe`
// label even if the classifier had been called. Used on every short-circuit
// path that does not call ClassifyEvent.
func degradedHookLabels(input HookInput, ctx MappingContext, kind edge.EventKind) edge.Labels {
	labels := baseHookLabels(input, ctx, kind)
	labels["command.class"] = "unknown"
	labels["command.family"] = "unknown"
	return labels
}

// mergeLabels merges classifier labels into base labels. Classifier values
// win on collision because they are deterministic server-side.
func mergeLabels(base, override edge.Labels) edge.Labels {
	out := edge.Labels{}
	maps.Copy(out, base)
	maps.Copy(out, override)
	return out
}

// deriveEventID synthesizes a stable EventID from the hook payload + context.
// The mapper-provided EventID is for ClassifyEvent's required-field check;
// the gateway's evaluate handler assigns the authoritative event_id when it
// persists the AgentActionEvent.
func deriveEventID(input HookInput, ctx MappingContext, now time.Time) string {
	parts := []string{
		ctx.TenantID,
		ctx.SessionID,
		ctx.ExecutionID,
		input.HookEventName,
		input.ToolUseID,
		now.UTC().Format(time.RFC3339Nano),
	}
	hash, _ := canonicalSHA256(parts)
	return "evt_synthetic_" + strings.TrimPrefix(hash, "sha256:")
}

// canonicalSHA256 produces a stable "sha256:<hex>" hash over the canonical
// JSON encoding of value. Mirrors stableSHA256 in core/edge/redaction.go but
// stays in this package so the mapper does not import unexported edge code.
func canonicalSHA256(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// actionHashInputs is the stable struct hashed into MappedHookAction.ActionHash.
// EDGE-012 approval retry uses this hash to bind an approval to one specific
// action shape; tool_use_id is included so retries of the same kind+tool+input
// for a different tool_use don't reuse a prior approval.
type actionHashInputs struct {
	Kind       string `json:"kind"`
	ToolName   string `json:"tool_name"`
	ToolUseID  string `json:"tool_use_id"`
	Capability string `json:"capability"`
	InputHash  string `json:"input_hash"`
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// mappingContextFromEnv builds a MappingContext from cordum-agentd-set env
// vars. The runner calls this when constructing the AgentdRequest so the
// mapped fields use trusted env metadata, not whatever Claude reported in
// the hook payload.
func mappingContextFromEnv(env map[string]string) MappingContext {
	return MappingContext{
		TenantID:     envOrEmpty(env, "CORDUM_TENANT_ID"),
		PrincipalID:  envOrEmpty(env, "CORDUM_EDGE_PRINCIPAL_ID"),
		SessionID:    envOrEmpty(env, "CORDUM_EDGE_SESSION_ID"),
		ExecutionID:  envOrEmpty(env, "CORDUM_EDGE_EXECUTION_ID"),
		AgentProduct: envOrDefault(env, "CORDUM_AGENT_PRODUCT", "claude-code"),
		AgentVersion: envOrEmpty(env, "CORDUM_AGENT_VERSION"),
	}
}

func envOrEmpty(env map[string]string, key string) string {
	if env != nil {
		return strings.TrimSpace(env[key])
	}
	return strings.TrimSpace(getenv(key))
}

func envOrDefault(env map[string]string, key, fallback string) string {
	v := envOrEmpty(env, key)
	if v != "" {
		return v
	}
	return fallback
}

func sanitizeMappingContext(ctx MappingContext) MappingContext {
	ctx.TenantID = redactHookBoundaryString(ctx.TenantID)
	ctx.PrincipalID = redactHookBoundaryString(ctx.PrincipalID)
	ctx.SessionID = redactHookBoundaryString(ctx.SessionID)
	ctx.ExecutionID = redactHookBoundaryString(ctx.ExecutionID)
	ctx.AgentProduct = redactHookBoundaryString(ctx.AgentProduct)
	ctx.AgentVersion = redactHookBoundaryString(ctx.AgentVersion)
	return ctx
}

// claudeRedactValue is the package-level alias for edge.RedactValue, exposed
// only so tests can inject errors to verify the EDGE-071 fail-closed
// contract on the redaction-error path. Production code MUST NOT rebind
// this variable — it exists for testability of an otherwise-unreachable
// error branch in edge.RedactValue (the only error source today is
// applyHashOptions, which sha256 cannot trigger in practice). The
// fail-closed branch protects against a future regression where the
// redactor grows a reachable error path.
var claudeRedactValue = edge.RedactValue

func redactHookBoundaryString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	// EDGE-071: bound input size BEFORE the redactor sees it as a
	// memory-safety net. The redactor itself caps via MaxInputRedactedBytes
	// (64 KiB), but call sites enforce the higher MaxRedactionInputBytes
	// ceiling first so an attacker-supplied value cannot force an
	// arbitrary allocation. Truncated inputs are STILL scanned (per
	// task rail "truncate + scan, never skip-scan-on-oversize") so the
	// redaction patterns at least cover the leading 1 MiB; the call site
	// then fail-closes to "<redacted>" because the unscanned tail might
	// have contained secrets we cannot prove absent.
	truncated := false
	if len(value) > edge.MaxRedactionInputBytes {
		value = value[:edge.MaxRedactionInputBytes]
		truncated = true
		emitRedactionFailed("claude.redact_hook_boundary_string", "input_too_large")
	}
	result, err := claudeRedactValue(value, edge.RedactionOptions{HashMode: edge.RedactionHashNone})
	if err != nil {
		// EDGE-071: NEVER let the raw value through on a redaction error.
		// The dangerous pattern this guards against is "redact failed,
		// fallback to raw" — which leaks the very secret the redactor
		// was supposed to mask. Returning the safe placeholder forfeits
		// some forensic detail to preserve the data-loss-prevention
		// invariant.
		emitRedactionFailed("claude.redact_hook_boundary_string", "redactor_error")
		return "<redacted>"
	}
	if truncated {
		// Scan happened (above), result is discarded because the
		// unscanned tail forfeits any guarantee that the head alone is
		// safe to surface. The metric emission above is the operational
		// signal.
		return "<redacted>"
	}
	candidate := value
	if redacted, ok := result.Value.(string); ok {
		candidate = redacted
	} else if result.Value != nil {
		candidate = fmt.Sprint(result.Value)
	}
	diagnostic := redactDiagnostic(candidate)
	// EDGE-046: do not add a broad substring check on the word "secret" here.
	// The four guards below cover real leak detection:
	//   - result.Redacted: edge.RedactValue's precise regex patterns fired
	//     (bearer/sk-/AKIA/env-secret-assignment/private-key/etc.).
	//   - result.Truncated: value exceeded the size cap.
	//   - diagnostic != candidate: redactDiagnostic transformed the value.
	//   - "[REDACTED]" substring: agentd's secretLikePattern marker.
	// A bare strings.Contains(..., "secret") confuses CONTEXT (the user is
	// talking about secrets) with CONTENT (an actual secret value) and wipes
	// every prompt mentioning /etc/secrets, --secret-name=foo, or "secrets
	// management" wholesale. That regression is filed as EDGE-046.
	if result.Redacted || result.Truncated || diagnostic != candidate || strings.Contains(diagnostic, "[REDACTED]") {
		return "<redacted>"
	}
	return diagnostic
}

func redactHookBoundaryStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, redactHookBoundaryString(value))
	}
	return out
}

func redactHookBoundaryMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	result, err := edge.RedactValue(values, edge.RedactionOptions{HashMode: edge.RedactionHashNone})
	if err != nil {
		return map[string]any{"_redacted": "<redacted>"}
	}
	sanitized := sanitizeHookBoundaryValue(result.Value)
	if redacted, ok := sanitized.(map[string]any); ok {
		return redacted
	}
	return map[string]any{"_redacted": sanitized}
}

func sanitizeHookBoundaryMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = sanitizeHookBoundaryValue(value)
	}
	return out
}

func sanitizeHookBoundaryValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return redactHookBoundaryString(v)
	case map[string]any:
		return sanitizeHookBoundaryMap(v)
	case map[string]string:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[key] = redactHookBoundaryString(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, child := range v {
			out = append(out, sanitizeHookBoundaryValue(child))
		}
		return out
	case []string:
		out := make([]any, 0, len(v))
		for _, child := range v {
			out = append(out, redactHookBoundaryString(child))
		}
		return out
	default:
		return v
	}
}

func sanitizeEdgeDecisionResponse(resp EdgeDecisionResponse) EdgeDecisionResponse {
	resp.Reason = redactHookBoundaryString(resp.Reason)
	resp.AdditionalContext = redactHookBoundaryString(resp.AdditionalContext)
	resp.UpdatedInput = redactHookBoundaryMap(resp.UpdatedInput)
	return resp
}

// safeHookEventLabel produces a bounded lowercase label fragment for an
// unrecognized hook event. The classifier rejects high-cardinality client
// strings; we follow the same pattern.
func safeHookEventLabel(eventName string) string {
	cleaned := strings.ToLower(strings.TrimSpace(eventName))
	if cleaned == "" {
		return "unknown"
	}
	if len(cleaned) > 32 {
		cleaned = cleaned[:32]
	}
	out := strings.Builder{}
	for _, r := range cleaned {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			out.WriteRune(r)
			continue
		}
		out.WriteRune('_')
	}
	return out.String()
}

// errors imported for sentinel construction above; keep the import alive
// for future sentinel additions without touching imports each time.
var _ = errors.New

// EdgeDecisionResponse is the subset of the EDGE-009 evaluate response shape
// the Claude hook output mapper consumes. Decisions are uppercase per PRD
// §11.2 (ALLOW / DENY / REQUIRE_APPROVAL / THROTTLE / CONSTRAIN); the mapper
// translates them to Claude's lowercase permissionDecision values.
//
// This struct is decoupled from the gateway-package edgeEvaluateResponse so
// EDGE-016 does not reach into a different package's unexported types. The
// runner is responsible for converting whatever Gateway/Agentd response shape
// it has in hand into an EdgeDecisionResponse before calling the output
// mapper.
type EdgeDecisionResponse struct {
	// Decision is the uppercase Edge decision ("ALLOW", "DENY",
	// "REQUIRE_APPROVAL", "THROTTLE", "CONSTRAIN"). Unknown values map to
	// an empty Claude hook output (no claim) so the runner's fail-closed
	// branch handles strict modes.
	Decision string
	// Reason is a terminal-safe, redacted message. The mapper appends
	// approval/retry instructions for REQUIRE_APPROVAL.
	Reason string
	// ApprovalRef carries the EDGE-011 approval token used by
	// REQUIRE_APPROVAL retry. The mapper appends it to the Claude
	// permissionDecisionReason so the operator sees the retry guidance.
	ApprovalRef string
	// AdditionalContext is forwarded to UserPromptSubmit/PostToolUse hooks
	// for non-blocking guidance.
	AdditionalContext string
	// UpdatedInput is applied only on CONSTRAIN with a safe, schema-valid
	// shape; unsafe constrains fall back to deny+reason in step-11.
	UpdatedInput map[string]any
	// WaitAfterSeconds optionally tells the operator how long to wait
	// before retrying after REQUIRE_APPROVAL/THROTTLE.
	WaitAfterSeconds int
}

// errUnknownEdgeDecision is returned by MapEdgeDecisionToHookOutput when
// EdgeDecisionResponse.Decision is not one of the documented PRD §11.2 values
// (ALLOW / DENY / REQUIRE_APPROVAL / THROTTLE / CONSTRAIN, case-insensitive).
// Callers should treat it as "no claim" and fall back to fail-closed deny in
// strict mode or no-op otherwise.
var errUnknownEdgeDecision = errors.New("claude output mapper: unknown edge decision")

// edge decision constants (uppercase, PRD §11.2).
const (
	edgeDecisionAllow           = "ALLOW"
	edgeDecisionDeny            = "DENY"
	edgeDecisionRequireApproval = "REQUIRE_APPROVAL"
	edgeDecisionThrottle        = "THROTTLE"
	edgeDecisionConstrain       = "CONSTRAIN"
)

// MapEdgeDecisionToHookOutput translates an EDGE-009 evaluate response into
// the Claude Code hook output JSON shape. Per PRD/ADR-010, REQUIRE_APPROVAL
// is mapped to an immediate deny with approval_ref + retry guidance — never
// to interactive defer/wait — because Claude does not honor permissionDecision:
// "defer" in interactive sessions.
//
// The function is total over decisions: unknown values produce
// ClaudeHookOutput{} and a non-nil error, leaving the runner free to choose
// fail-closed deny or no-op based on strict mode. Lowercase decisions are
// accepted defensively for legacy AgentdDecision passthrough.
func MapEdgeDecisionToHookOutput(eventName string, resp EdgeDecisionResponse) (ClaudeHookOutput, error) {
	resp = sanitizeEdgeDecisionResponse(resp)
	if !isSupportedHookEventName(eventName) {
		return ClaudeHookOutput{}, nil
	}
	decision := normalizeEdgeDecision(resp.Decision)
	if decision == "" {
		return ClaudeHookOutput{}, errUnknownEdgeDecision
	}
	switch eventName {
	case "PreToolUse":
		return preToolUseHookOutput(decision, resp), nil
	case "UserPromptSubmit":
		return userPromptSubmitHookOutput(decision, resp), nil
	case "PostToolUse", "PostToolUseFailure":
		return postToolUseHookOutput(eventName, decision, resp), nil
	case "ConfigChange":
		return configChangeHookOutput(decision, resp), nil
	case "FileChanged":
		// Non-blocking by design — the hook is an audit hook, not an
		// enforcement hook. Always no-op output.
		return ClaudeHookOutput{}, nil
	default:
		return ClaudeHookOutput{}, nil
	}
}

// normalizeEdgeDecision returns the canonical uppercase decision or "" if the
// value is not one of the documented decisions. Accepts lowercase for legacy
// AgentdDecision passthrough.
func normalizeEdgeDecision(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case edgeDecisionAllow:
		return edgeDecisionAllow
	case edgeDecisionDeny:
		return edgeDecisionDeny
	case edgeDecisionRequireApproval, "REQUIRE-APPROVAL":
		return edgeDecisionRequireApproval
	case edgeDecisionThrottle:
		return edgeDecisionThrottle
	case edgeDecisionConstrain:
		return edgeDecisionConstrain
	}
	return ""
}

func isSupportedHookEventName(eventName string) bool {
	switch eventName {
	case "PreToolUse", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "ConfigChange", "FileChanged":
		return true
	}
	return false
}

func preToolUseHookOutput(decision string, resp EdgeDecisionResponse) ClaudeHookOutput {
	hsop := &HookSpecificOutput{HookEventName: "PreToolUse"}
	switch decision {
	case edgeDecisionAllow:
		hsop.PermissionDecision = "allow"
		hsop.PermissionDecisionReason = resp.Reason
		hsop.AdditionalContext = resp.AdditionalContext
	case edgeDecisionDeny, edgeDecisionThrottle:
		hsop.PermissionDecision = "deny"
		hsop.PermissionDecisionReason = resp.Reason
		hsop.AdditionalContext = resp.AdditionalContext
	case edgeDecisionRequireApproval:
		hsop.PermissionDecision = "deny"
		reason := resp.Reason
		if reason == "" {
			reason = "approval required"
		}
		if resp.ApprovalRef != "" {
			reason = fmt.Sprintf("%s; approval_ref=%s; approve then retry the tool call", reason, resp.ApprovalRef)
		}
		hsop.PermissionDecisionReason = reason
		hsop.AdditionalContext = resp.AdditionalContext
	case edgeDecisionConstrain:
		// CONSTRAIN without an updated_input shape cannot be applied
		// safely — we'd be inventing a command. Fall back to deny so
		// the operator sees the policy reason and can retry with a
		// different input.
		if len(resp.UpdatedInput) == 0 {
			hsop.PermissionDecision = "deny"
			reason := resp.Reason
			if reason == "" {
				reason = "constrain decision missing updated_input; falling back to deny"
			}
			hsop.PermissionDecisionReason = reason
			break
		}
		hsop.PermissionDecision = "allow"
		hsop.PermissionDecisionReason = resp.Reason
		hsop.UpdatedInput = resp.UpdatedInput
		hsop.AdditionalContext = resp.AdditionalContext
	}
	out := ClaudeHookOutput{HookSpecificOutput: hsop}
	// Mirror the top-level `decision: "block"` that every other hook event
	// mapper in this file emits for deny outcomes (userPromptSubmitHookOutput
	// line ~953, postToolUseHookOutput, configChangeHookOutput). Without
	// the outer field, Claude Code v2.1.x treats the deny as a soft ask
	// and proceeds with the tool call in non-interactive (`claude -p`)
	// mode. PreToolUse was the only path that omitted the mirror.
	if hsop.PermissionDecision == "deny" {
		out.Decision = "block"
		out.Reason = hsop.PermissionDecisionReason
	}
	return out
}

func userPromptSubmitHookOutput(decision string, resp EdgeDecisionResponse) ClaudeHookOutput {
	out := ClaudeHookOutput{HookSpecificOutput: &HookSpecificOutput{HookEventName: "UserPromptSubmit"}}
	if resp.AdditionalContext != "" {
		out.HookSpecificOutput.AdditionalContext = resp.AdditionalContext
	}
	switch decision {
	case edgeDecisionDeny, edgeDecisionRequireApproval, edgeDecisionThrottle:
		out.Decision = "block"
		out.Reason = resp.Reason
	case edgeDecisionAllow, edgeDecisionConstrain:
		// CONSTRAIN on UserPromptSubmit cannot rewrite the prompt safely
		// (Claude doesn't expose a prompt-edit primitive on this hook),
		// so it acts like ALLOW with optional additionalContext.
		if resp.AdditionalContext == "" {
			return ClaudeHookOutput{}
		}
	}
	return out
}

func postToolUseHookOutput(eventName, decision string, resp EdgeDecisionResponse) ClaudeHookOutput {
	out := ClaudeHookOutput{HookSpecificOutput: &HookSpecificOutput{HookEventName: eventName}}
	if resp.AdditionalContext != "" {
		out.HookSpecificOutput.AdditionalContext = resp.AdditionalContext
	}
	switch decision {
	case edgeDecisionDeny, edgeDecisionRequireApproval, edgeDecisionThrottle:
		// PostToolUse fires AFTER the tool ran, so "block" is feedback
		// for the model — NOT prevention. The mapper does not synthesize
		// a permissionDecision on PostToolUse output (that field is
		// PreToolUse-only in the Claude hook contract).
		out.Decision = "block"
		out.Reason = resp.Reason
	case edgeDecisionAllow, edgeDecisionConstrain:
		if resp.AdditionalContext == "" {
			return ClaudeHookOutput{}
		}
	}
	return out
}

func configChangeHookOutput(decision string, resp EdgeDecisionResponse) ClaudeHookOutput {
	out := ClaudeHookOutput{HookSpecificOutput: &HookSpecificOutput{HookEventName: "ConfigChange"}}
	switch decision {
	case edgeDecisionDeny, edgeDecisionRequireApproval, edgeDecisionThrottle:
		out.Decision = "block"
		if resp.Reason != "" {
			out.Reason = resp.Reason
		} else {
			out.Reason = "configuration change blocked by Cordum policy"
		}
	case edgeDecisionAllow, edgeDecisionConstrain:
		return ClaudeHookOutput{}
	}
	return out
}
