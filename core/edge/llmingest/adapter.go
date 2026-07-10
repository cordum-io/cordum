package llmingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

// Errors returned by the adapter. Gateway handlers route on these via errors.Is.
var (
	// ErrInvalidBatch is returned when the batch envelope (source identity,
	// non-empty events, batch size, nonce) is malformed.
	ErrInvalidBatch = errors.New("llm ingest: invalid batch")
	// ErrInvalidEnvelope is returned when a single envelope is missing a
	// required field, names an unknown kind, carries a forbidden raw-field
	// key, fails redaction/classification, or fails
	// edge.AgentActionEvent.Validate after mapping.
	ErrInvalidEnvelope = errors.New("llm ingest: invalid envelope")
	// ErrLLMBatchTooLarge is returned when the batch exceeds MaxLLMBatchEvents
	// or a single envelope's raw JSON encoding exceeds MaxLLMRawEnvelopeBytes.
	ErrLLMBatchTooLarge = errors.New("llm ingest: too large")
)

// Caps. LLM prompts/responses carry far more content than runtime telemetry,
// so the byte budgets are larger than runtimeingest's — but still bounded so a
// chatty proxy cannot eat the entitlement budget or force a large allocation.
const (
	// MaxLLMBatchEvents bounds the number of events one Adapter.Map call (or
	// one HTTP POST body) may carry. Chat turns batch in small numbers.
	MaxLLMBatchEvents = 64
	// MaxLLMBatchBodyBytes bounds the HTTP body size before JSON decode.
	MaxLLMBatchBodyBytes int64 = 4 << 20 // 4 MiB
	// MaxLLMRawEnvelopeBytes bounds the raw JSON encoding of one envelope
	// (pre-redaction). Content above this is rejected at the boundary rather
	// than silently truncated.
	MaxLLMRawEnvelopeBytes = 1 << 20 // 1 MiB
	// MaxLLMRedactedStringBytes bounds each individual redacted string value.
	// Content longer than this is truncated in the stored/returned evidence
	// (Truncated=true); the redactor still scans up to this bound.
	MaxLLMRedactedStringBytes = 16 << 10 // 16 KiB
	// MaxLLMRedactedTotalBytes bounds the redacted InputRedacted map. Kept
	// below edge.MaxInputRedactedBytes (64 KiB) for headroom.
	MaxLLMRedactedTotalBytes = 48 << 10 // 48 KiB
	// MaxLLMLabelEntries bounds the labels carried per envelope.
	MaxLLMLabelEntries = 16
	// MaxLLMMessages bounds the structured messages carried per envelope.
	MaxLLMMessages = 64
	// MaxLLMShortFieldBytes bounds short metadata fields (provider, model,
	// direction, role) before redaction.
	MaxLLMShortFieldBytes = 256
)

// Acceptable source-supplied LLM event kinds. The gateway-internal outcome
// kinds (llm.data.redacted, llm.policy.denied) are NOT accepted from a proxy —
// they describe gateway-side decisions, not observed traffic.
const (
	KindRequestPre   = string(edgecore.EventKindLLMRequestPre)
	KindRequestPost  = string(edgecore.EventKindLLMRequestPost)
	KindStreamChunk  = string(edgecore.EventKindLLMStreamChunk)
	KindCostRecorded = string(edgecore.EventKindLLMCostRecorded)
)

// Outcome status values a source may stamp on an envelope. "" defaults to ok.
const (
	OutcomeStatusOK       = "ok"
	OutcomeStatusFailed   = "failed"
	OutcomeStatusDegraded = "degraded"
)

// Direction values for a content-bearing envelope.
const (
	DirectionPrompt   = "prompt"
	DirectionResponse = "response"
)

// Advisory per-event decisions returned to the proxy. These are NOT policy
// (allow/deny) decisions — the proxy gets allow/deny from /api/v1/edge/evaluate.
const (
	// DecisionRecord means no secrets were detected; the event is recorded as
	// evidence and the original content is safe to forward as-is.
	DecisionRecord = "record"
	// DecisionRedact means secrets were detected and masked; the proxy SHOULD
	// forward RedactedContent instead of the original (subject to Truncated).
	DecisionRedact = "redact"
)

// SourceIdentity identifies the trusted LLM proxy that produced the batch.
// Sources are bound to the API-key tenant by the gateway; this is the wire
// shape, not the auth model.
type SourceIdentity struct {
	ID string `json:"source_id"`
}

// LLMMessage is one role-tagged message of a chat prompt or completion. The
// content is redacted by the adapter; raw secrets never persist.
type LLMMessage struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// LLMTokens carries optional token accounting for cost/usage evidence.
type LLMTokens struct {
	Input  int `json:"input_tokens,omitempty"`
	Output int `json:"output_tokens,omitempty"`
	Total  int `json:"total_tokens,omitempty"`
}

// LLMEventEnvelope is one intercepted LLM interaction record. Required:
// TenantID, SessionID, ExecutionID, SourceEventID, ObservedAt, Kind. Content
// and Messages are optional (a cost.recorded event carries neither); when
// present they are redacted before persistence.
type LLMEventEnvelope struct {
	TenantID      string                     `json:"tenant_id"`
	SessionID     string                     `json:"session_id"`
	ExecutionID   string                     `json:"execution_id"`
	SourceEventID string                     `json:"source_event_id"`
	ObservedAt    time.Time                  `json:"observed_at"`
	Kind          string                     `json:"kind"`
	OutcomeStatus string                     `json:"outcome_status,omitempty"`
	AgentProduct  string                     `json:"agent_product,omitempty"`
	Provider      string                     `json:"provider,omitempty"`
	Model         string                     `json:"model,omitempty"`
	Direction     string                     `json:"direction,omitempty"`
	Content       string                     `json:"content,omitempty"`
	Messages      []LLMMessage               `json:"messages,omitempty"`
	Tokens        *LLMTokens                 `json:"tokens,omitempty"`
	CostUSD       float64                    `json:"cost_usd,omitempty"`
	Labels        map[string]string          `json:"labels,omitempty"`
	ArtifactPtrs  []edgecore.ArtifactPointer `json:"artifact_ptrs,omitempty"`
}

// LLMBatch is the wire batch: one source identity, an optional batch ID for
// operator correlation, an optional replay nonce, and N envelopes.
type LLMBatch struct {
	Source  SourceIdentity     `json:"source"`
	Nonce   string             `json:"nonce,omitempty"`
	BatchID string             `json:"batch_id,omitempty"`
	Events  []LLMEventEnvelope `json:"events"`
}

// EventDecision is the per-event advisory outcome returned to the proxy.
type EventDecision struct {
	SourceEventID   string   `json:"source_event_id"`
	Kind            string   `json:"kind"`
	Decision        string   `json:"decision"`
	Redacted        bool     `json:"redacted"`
	Truncated       bool     `json:"truncated,omitempty"`
	RedactedContent string   `json:"redacted_content,omitempty"`
	Findings        []string `json:"findings,omitempty"`
}

// DropReport explains why one envelope was dropped from a successful Map call.
// Reserved for forward compatibility; the LLM adapter never samples, so a
// successful Map currently returns no drops.
type DropReport struct {
	SourceEventID string `json:"source_event_id"`
	Kind          string `json:"kind"`
	Reason        string `json:"reason"`
}

// MapResult is the outcome of Adapter.Map. Events[i] corresponds to
// Decisions[i]; both are in input order over the accepted envelopes.
type MapResult struct {
	Events    []edgecore.AgentActionEvent
	Decisions []EventDecision
	Dropped   []DropReport
}

// ContentRedactor redacts a single prompt/response content string and returns
// the redacted text plus the finding TYPES detected (never raw values). It is
// INJECTED by the gateway so the LLM-proxy path reuses the shared Safety Kernel
// scanners — the edge layer never imports the control plane (see
// safetykernel.RedactContent / the scanners_export injection pattern). When the
// adapter has no redactor, content falls back to the generic edge secret
// redactor so the package stays usable and testable standalone.
type ContentRedactor func(content string) (redacted string, findings []string)

// AdapterOptions configures the adapter. There is intentionally no sampling
// knob (every chat is governed).
type AdapterOptions struct {
	// Redactor, when set, redacts prompt/response content via the shared kernel
	// scanners. Nil falls back to the generic edge secret redactor.
	Redactor ContentRedactor
}

// Adapter validates + maps LLM envelopes into edge.AgentActionEvent records.
type Adapter struct {
	opts AdapterOptions
}

// NewAdapter constructs an adapter with the supplied (optional) injected redactor.
func NewAdapter(opts AdapterOptions) *Adapter { return &Adapter{opts: opts} }

// Map validates the batch and maps every envelope into an edge.AgentActionEvent
// plus a per-event advisory decision. The first invalid envelope aborts the
// entire batch — partial persistence is forbidden.
func (a *Adapter) Map(batch LLMBatch) (MapResult, error) {
	sourceID := strings.TrimSpace(batch.Source.ID)
	if sourceID == "" {
		return MapResult{}, fmt.Errorf("%w: source.source_id required", ErrInvalidBatch)
	}
	if len(sourceID) > MaxLLMShortFieldBytes {
		return MapResult{}, fmt.Errorf("%w: source.source_id too long", ErrInvalidBatch)
	}
	if len(batch.Events) == 0 {
		return MapResult{}, fmt.Errorf("%w: events required", ErrInvalidBatch)
	}
	if len(batch.Events) > MaxLLMBatchEvents {
		return MapResult{}, fmt.Errorf("%w: batch has %d events, max %d", ErrLLMBatchTooLarge, len(batch.Events), MaxLLMBatchEvents)
	}
	events := make([]edgecore.AgentActionEvent, 0, len(batch.Events))
	decisions := make([]EventDecision, 0, len(batch.Events))
	for i, env := range batch.Events {
		if err := validateEnvelope(env); err != nil {
			return MapResult{}, fmt.Errorf("events[%d]: %w", i, err)
		}
		// Pre-mapping size check on the raw envelope so a very large content
		// field is rejected before it reaches the redactor.
		if raw, err := json.Marshal(env); err != nil {
			return MapResult{}, fmt.Errorf("events[%d]: %w: marshal envelope", i, ErrInvalidEnvelope)
		} else if len(raw) > MaxLLMRawEnvelopeBytes {
			return MapResult{}, fmt.Errorf("events[%d]: %w: envelope %d bytes > cap %d", i, ErrLLMBatchTooLarge, len(raw), MaxLLMRawEnvelopeBytes)
		}
		evt, decision, err := a.mapEnvelope(sourceID, env)
		if err != nil {
			return MapResult{}, fmt.Errorf("events[%d]: %w", i, err)
		}
		if err := evt.Validate(); err != nil {
			return MapResult{}, fmt.Errorf("events[%d]: %w: %v", i, ErrInvalidEnvelope, err)
		}
		events = append(events, evt)
		decisions = append(decisions, decision)
	}
	return MapResult{Events: events, Decisions: decisions}, nil
}

func validateEnvelope(env LLMEventEnvelope) error {
	if strings.TrimSpace(env.TenantID) == "" {
		return fmt.Errorf("%w: tenant_id required", ErrInvalidEnvelope)
	}
	if strings.TrimSpace(env.SessionID) == "" {
		return fmt.Errorf("%w: session_id required", ErrInvalidEnvelope)
	}
	if strings.TrimSpace(env.ExecutionID) == "" {
		return fmt.Errorf("%w: execution_id required", ErrInvalidEnvelope)
	}
	if strings.TrimSpace(env.SourceEventID) == "" {
		return fmt.Errorf("%w: source_event_id required", ErrInvalidEnvelope)
	}
	if env.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed_at required", ErrInvalidEnvelope)
	}
	if !isKnownKind(env.Kind) {
		return fmt.Errorf("%w: unknown or unsupported kind %q", ErrInvalidEnvelope, env.Kind)
	}
	if !isValidOutcomeStatus(env.OutcomeStatus) {
		return fmt.Errorf("%w: unknown outcome_status %q", ErrInvalidEnvelope, env.OutcomeStatus)
	}
	if !isValidDirection(env.Direction) {
		return fmt.Errorf("%w: unknown direction %q", ErrInvalidEnvelope, env.Direction)
	}
	if len(env.Provider) > MaxLLMShortFieldBytes {
		return fmt.Errorf("%w: provider too long", ErrInvalidEnvelope)
	}
	if len(env.Model) > MaxLLMShortFieldBytes {
		return fmt.Errorf("%w: model too long", ErrInvalidEnvelope)
	}
	if len(env.Messages) > MaxLLMMessages {
		return fmt.Errorf("%w: messages has %d entries, max %d", ErrInvalidEnvelope, len(env.Messages), MaxLLMMessages)
	}
	for j, m := range env.Messages {
		if len(m.Role) > MaxLLMShortFieldBytes {
			return fmt.Errorf("%w: messages[%d] role too long", ErrInvalidEnvelope, j)
		}
	}
	if len(env.Labels) > MaxLLMLabelEntries {
		return fmt.Errorf("%w: labels has %d entries, max %d", ErrInvalidEnvelope, len(env.Labels), MaxLLMLabelEntries)
	}
	tenantID := strings.TrimSpace(env.TenantID)
	sessionID := strings.TrimSpace(env.SessionID)
	executionID := strings.TrimSpace(env.ExecutionID)
	for j, art := range env.ArtifactPtrs {
		if strings.TrimSpace(art.TenantID) != tenantID {
			return fmt.Errorf("%w: artifact_ptrs[%d] tenant mismatch", ErrInvalidEnvelope, j)
		}
		if strings.TrimSpace(art.SessionID) != sessionID {
			return fmt.Errorf("%w: artifact_ptrs[%d] session mismatch", ErrInvalidEnvelope, j)
		}
		if strings.TrimSpace(art.ExecutionID) != executionID {
			return fmt.Errorf("%w: artifact_ptrs[%d] execution mismatch", ErrInvalidEnvelope, j)
		}
	}
	return nil
}

func isKnownKind(kind string) bool {
	switch kind {
	case KindRequestPre, KindRequestPost, KindStreamChunk, KindCostRecorded:
		return true
	}
	return false
}

func isValidOutcomeStatus(status string) bool {
	switch status {
	case "", OutcomeStatusOK, OutcomeStatusFailed, OutcomeStatusDegraded:
		return true
	}
	return false
}

func isValidDirection(direction string) bool {
	switch direction {
	case "", DirectionPrompt, DirectionResponse:
		return true
	}
	return false
}

func (a *Adapter) mapEnvelope(sourceID string, env LLMEventEnvelope) (edgecore.AgentActionEvent, EventDecision, error) {
	inputRedacted, findingTypes, redactedFlag, truncatedFlag, err := a.redactInput(env)
	if err != nil {
		return edgecore.AgentActionEvent{}, EventDecision{}, err
	}

	eventID := stableEventID(sourceID, env)
	tenantID := strings.TrimSpace(env.TenantID)
	sessionID := strings.TrimSpace(env.SessionID)
	executionID := strings.TrimSpace(env.ExecutionID)

	labels, err := sanitizeLLMLabels(env.Labels)
	if err != nil {
		return edgecore.AgentActionEvent{}, EventDecision{}, err
	}
	labels["llm.source_id"] = sourceID
	if d := strings.TrimSpace(env.Direction); d != "" {
		labels["llm.direction"] = d
	}
	if redactedFlag || len(findingTypes) > 0 {
		labels["llm.redacted"] = "true"
		for _, ft := range findingTypes {
			if len(labels) >= edgecore.MaxLabelEntries {
				break
			}
			labels["llm.finding."+ft] = "true"
		}
	}

	event := edgecore.AgentActionEvent{
		EventID:       eventID,
		SessionID:     sessionID,
		ExecutionID:   executionID,
		TenantID:      tenantID,
		Timestamp:     env.ObservedAt.UTC(),
		Layer:         edgecore.LayerLLM,
		Kind:          edgecore.EventKind(env.Kind),
		AgentProduct:  strings.TrimSpace(env.AgentProduct),
		Decision:      edgecore.DecisionRecorded,
		RuleTier:      "",
		InputRedacted: inputRedacted,
		Status:        outcomeStatusToActionStatus(env.OutcomeStatus),
		Labels:        labels,
	}

	// Classify so the chat turn lands in the audit/session trail with a
	// meaningful action_name (llm.request) + capability + risk tags. These are
	// evidence enrichments; the event decision stays RECORDED (the policy
	// allow/deny decision is made by /api/v1/edge/evaluate, not here).
	classification, err := edgecore.ClassifyEvent(event)
	if err != nil {
		return edgecore.AgentActionEvent{}, EventDecision{}, fmt.Errorf("%w: classify: %v", ErrInvalidEnvelope, err)
	}
	event.ActionName = classification.ActionName
	event.Capability = classification.Capability
	event.RiskTags = classification.RiskTags
	for k, v := range classification.Labels {
		if _, exists := event.Labels[k]; !exists {
			if len(event.Labels) >= edgecore.MaxLabelEntries {
				break
			}
			event.Labels[k] = v
		}
	}

	if len(env.ArtifactPtrs) > 0 {
		pointers := make([]edgecore.ArtifactPointer, len(env.ArtifactPtrs))
		for i, art := range env.ArtifactPtrs {
			ap := art
			ap.TenantID = tenantID
			ap.SessionID = sessionID
			ap.ExecutionID = executionID
			ap.EventID = eventID
			ap.CreatedAt = ap.CreatedAt.UTC()
			pointers[i] = ap
		}
		event.ArtifactPointers = pointers
	}

	decision := EventDecision{
		SourceEventID:   env.SourceEventID,
		Kind:            env.Kind,
		Decision:        DecisionRecord,
		Redacted:        redactedFlag,
		Truncated:       truncatedFlag,
		RedactedContent: redactedContentString(inputRedacted),
		Findings:        findingTypes,
	}
	if redactedFlag || len(findingTypes) > 0 {
		decision.Decision = DecisionRedact
	}
	return event, decision, nil
}

// redactInput produces the redacted InputRedacted map for an envelope plus the
// finding types, whether anything was redacted, and whether content was
// truncated. With an injected ContentRedactor (the shared Safety Kernel
// scanners) it redacts prompt/response content in place, preserving prose;
// otherwise it falls back to the generic edge secret redactor over the whole
// input map so the package remains usable standalone.
func (a *Adapter) redactInput(env LLMEventEnvelope) (map[string]any, []string, bool, bool, error) {
	if a.opts.Redactor == nil {
		input := buildLLMInput(env)
		redacted, err := edgecore.RedactValue(input, edgecore.RedactionOptions{
			HashMode:       edgecore.RedactionHashNone,
			MaxDepth:       6,
			MaxItems:       MaxLLMMessages*2 + 16,
			MaxStringBytes: MaxLLMRedactedStringBytes,
			MaxTotalBytes:  MaxLLMRedactedTotalBytes,
		})
		if err != nil {
			return nil, nil, false, false, fmt.Errorf("%w: redaction failed", ErrInvalidEnvelope)
		}
		inputRedacted, _ := redacted.Value.(map[string]any)
		if inputRedacted == nil {
			// Whole value tripped the too-large guard and collapsed to a string
			// placeholder. Preserve it under a recognized key so the event still
			// classifies and the audit trail records that content was present.
			inputRedacted = map[string]any{"content": redacted.Value}
		}
		return inputRedacted, uniqueFindingTypes(redacted.Findings), redacted.Redacted, redacted.Truncated, nil
	}

	input := map[string]any{}
	if v := strings.TrimSpace(env.Provider); v != "" {
		input["provider"] = v
	}
	if v := strings.TrimSpace(env.Model); v != "" {
		input["model"] = v
	}
	if v := strings.TrimSpace(env.Direction); v != "" {
		input["direction"] = v
	}
	seen := map[string]struct{}{}
	var findingTypes []string
	anyRedacted := false
	truncated := false
	redact := func(s string) string {
		bounded, t := boundLLMString(s)
		if t {
			truncated = true
		}
		out, fs := a.opts.Redactor(bounded)
		for _, f := range fs {
			anyRedacted = true
			if _, ok := seen[f]; !ok {
				seen[f] = struct{}{}
				findingTypes = append(findingTypes, f)
			}
		}
		return out
	}
	if env.Content != "" {
		input["content"] = redact(env.Content)
	}
	if len(env.Messages) > 0 {
		msgs := make([]any, 0, len(env.Messages))
		for _, m := range env.Messages {
			entry := map[string]any{}
			if r := strings.TrimSpace(m.Role); r != "" {
				entry["role"] = r
			}
			if m.Content != "" {
				entry["content"] = redact(m.Content)
			}
			if len(entry) > 0 {
				msgs = append(msgs, entry)
			}
		}
		if len(msgs) > 0 {
			input["messages"] = msgs
		}
	}
	if env.Tokens != nil {
		if env.Tokens.Input > 0 {
			input["input_tokens"] = env.Tokens.Input
		}
		if env.Tokens.Output > 0 {
			input["output_tokens"] = env.Tokens.Output
		}
		if env.Tokens.Total > 0 {
			input["total_tokens"] = env.Tokens.Total
		}
	}
	if env.CostUSD > 0 {
		input["cost_usd"] = env.CostUSD
	}
	sort.Strings(findingTypes)
	return input, findingTypes, anyRedacted, truncated, nil
}

// boundLLMString truncates content to MaxLLMRedactedStringBytes so stored
// evidence stays bounded; returns whether truncation occurred. A trailing
// partial rune left by the byte cut is harmless — JSON marshalling sanitizes it.
func boundLLMString(s string) (string, bool) {
	if len(s) <= MaxLLMRedactedStringBytes {
		return s, false
	}
	return s[:MaxLLMRedactedStringBytes], true
}

// buildLLMInput assembles the pre-redaction input map using keys the edge
// classifier recognizes ("content"/"messages" → data risk tag;
// "input_tokens"/"output_tokens"/"cost_usd" → cost risk tag; "provider"/"model"
// → llm.* labels).
func buildLLMInput(env LLMEventEnvelope) map[string]any {
	input := map[string]any{}
	if v := strings.TrimSpace(env.Provider); v != "" {
		input["provider"] = v
	}
	if v := strings.TrimSpace(env.Model); v != "" {
		input["model"] = v
	}
	if v := strings.TrimSpace(env.Direction); v != "" {
		input["direction"] = v
	}
	if env.Content != "" {
		input["content"] = env.Content
	}
	if len(env.Messages) > 0 {
		msgs := make([]any, 0, len(env.Messages))
		for _, m := range env.Messages {
			entry := map[string]any{}
			if r := strings.TrimSpace(m.Role); r != "" {
				entry["role"] = r
			}
			if m.Content != "" {
				entry["content"] = m.Content
			}
			if len(entry) > 0 {
				msgs = append(msgs, entry)
			}
		}
		if len(msgs) > 0 {
			input["messages"] = msgs
		}
	}
	if env.Tokens != nil {
		if env.Tokens.Input > 0 {
			input["input_tokens"] = env.Tokens.Input
		}
		if env.Tokens.Output > 0 {
			input["output_tokens"] = env.Tokens.Output
		}
		if env.Tokens.Total > 0 {
			input["total_tokens"] = env.Tokens.Total
		}
	}
	if env.CostUSD > 0 {
		input["cost_usd"] = env.CostUSD
	}
	return input
}

// redactedContentString returns the redacted content as a string for the
// proxy to forward, when present. Single-turn envelopes use the "content"
// key directly; multi-message envelopes (built via env.Messages) carry their
// redacted text under "messages" instead, so those are flattened into a
// "role: content" transcript — otherwise RedactedContent would stay empty for
// every chat-style envelope even when Decision == DecisionRedact, and a
// caller following the DecisionRedact contract could fall back to forwarding
// the unredacted original.
func redactedContentString(inputRedacted map[string]any) string {
	if v, ok := inputRedacted["content"].(string); ok {
		return v
	}
	msgs, ok := inputRedacted["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		entry, ok := m.(map[string]any)
		if !ok {
			continue
		}
		content, _ := entry["content"].(string)
		if content == "" {
			continue
		}
		if role, ok := entry["role"].(string); ok && role != "" {
			parts = append(parts, role+": "+content)
			continue
		}
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n")
}

func uniqueFindingTypes(findings []edgecore.RedactionFinding) []string {
	if len(findings) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(findings))
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		t := strings.TrimSpace(f.Type)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func sanitizeLLMLabels(labels map[string]string) (edgecore.Labels, error) {
	if len(labels) == 0 {
		return edgecore.Labels{}, nil
	}
	out := make(edgecore.Labels, len(labels))
	for key, value := range labels {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return nil, fmt.Errorf("%w: label key is required", ErrInvalidEnvelope)
		}
		if len(key) > edgecore.MaxLabelKeyBytes {
			return nil, fmt.Errorf("%w: label key exceeds %d bytes", ErrInvalidEnvelope, edgecore.MaxLabelKeyBytes)
		}
		// Drop reserved keys and any key whose name itself trips redaction.
		if strings.HasPrefix(trimmedKey, "llm.") || llmLabelKeyIsSensitive(key) {
			continue
		}
		redactedValue, err := redactLLMLabelValue(value)
		if err != nil {
			return nil, err
		}
		out[key] = redactedValue
	}
	return out, nil
}

func llmLabelKeyIsSensitive(key string) bool {
	probe := map[string]any{key: "llm-label-probe"}
	result, err := edgecore.RedactValue(probe, edgecore.RedactionOptions{
		HashMode:       edgecore.RedactionHashNone,
		MaxDepth:       1,
		MaxItems:       MaxLLMLabelEntries + 1,
		MaxStringBytes: edgecore.MaxLabelValueBytes,
		MaxTotalBytes:  MaxLLMRedactedTotalBytes,
	})
	return err != nil || result.Redacted
}

func redactLLMLabelValue(value string) (string, error) {
	if len(value) > edgecore.MaxLabelValueBytes {
		return "", fmt.Errorf("%w: label value exceeds %d bytes", ErrInvalidEnvelope, edgecore.MaxLabelValueBytes)
	}
	result, err := edgecore.RedactValue(value, edgecore.RedactionOptions{
		HashMode:       edgecore.RedactionHashNone,
		MaxStringBytes: edgecore.MaxLabelValueBytes,
		MaxTotalBytes:  MaxLLMRedactedTotalBytes,
	})
	if err != nil {
		return "", fmt.Errorf("%w: label redaction failed", ErrInvalidEnvelope)
	}
	if redacted, ok := result.Value.(string); ok {
		return redacted, nil
	}
	return "", fmt.Errorf("%w: label redaction returned %T", ErrInvalidEnvelope, result.Value)
}

func outcomeStatusToActionStatus(status string) edgecore.ActionStatus {
	switch status {
	case OutcomeStatusFailed:
		return edgecore.ActionStatusFailed
	case OutcomeStatusDegraded:
		return edgecore.ActionStatusDegraded
	default:
		return edgecore.ActionStatusOK
	}
}

func stableEventID(sourceID string, env LLMEventEnvelope) string {
	h := fnv.New64a()
	// Seed with tenant/session/execution/kind/source_event_id so a retry from
	// the same proxy with the same source_event_id resolves to the same
	// EventID, making store.AppendEvents idempotent by EventID. fnv writers
	// never error, so the discard is safe.
	_, _ = fmt.Fprintf(h, "%s|%s|%s|%s|%s|%s", sourceID, env.TenantID, env.SessionID, env.ExecutionID, env.Kind, env.SourceEventID)
	return fmt.Sprintf("llm-%016x", h.Sum64())
}

// DecodeBatch decodes the wire batch with strict-schema enforcement so smuggled
// keys (headers, authorization, cookies, api_key, etc.) are rejected at the
// boundary. DisallowUnknownFields applies recursively to every nested struct.
func DecodeBatch(r io.Reader) (LLMBatch, error) {
	var batch LLMBatch
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&batch); err != nil {
		return LLMBatch{}, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return LLMBatch{}, fmt.Errorf("%w: trailing data after JSON value: %v", ErrInvalidEnvelope, err)
		}
		return LLMBatch{}, fmt.Errorf("%w: trailing data after JSON value", ErrInvalidEnvelope)
	}
	if err := validateLLMBatchNonce(batch.Nonce); err != nil {
		return LLMBatch{}, err
	}
	return batch, nil
}

func validateLLMBatchNonce(nonce string) error {
	required, err := llmReplayRequired()
	if err != nil {
		return err
	}
	nonce = strings.TrimSpace(nonce)
	if nonce == "" {
		if !required {
			return nil
		}
		return fmt.Errorf("%w: nonce required", ErrInvalidBatch)
	}
	if len(nonce) < 16 || len(nonce) > 64 {
		return fmt.Errorf("%w: nonce length must be 16-64 characters", ErrInvalidBatch)
	}
	for _, r := range nonce {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return fmt.Errorf("%w: nonce contains invalid characters", ErrInvalidBatch)
	}
	return nil
}

// llmReplayRequired reports whether a replay nonce is mandatory. Unlike runtime
// telemetry, an LLM proxy issues one synchronous request per chat turn, so
// replay protection is OPTIONAL by default (nonce is deduplicated when present
// but not required). Set CORDUM_EDGE_LLM_REPLAY_REQUIRED=true to mandate it.
func llmReplayRequired() (bool, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("CORDUM_EDGE_LLM_REPLAY_REQUIRED")))
	switch value {
	case "", "false", "0", "no":
		return false, nil
	case "true", "1", "yes":
		return true, nil
	default:
		return false, fmt.Errorf("%w: CORDUM_EDGE_LLM_REPLAY_REQUIRED must be true/false/1/0/yes/no", ErrInvalidBatch)
	}
}
