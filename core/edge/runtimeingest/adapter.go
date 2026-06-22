package runtimeingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

// Errors returned by the adapter. Gateway handlers route on these via errors.Is.
var (
	// ErrInvalidBatch is returned when the batch envelope (source identity,
	// non-empty events, batch size) is malformed.
	ErrInvalidBatch = errors.New("runtime ingest: invalid batch")
	// ErrInvalidEnvelope is returned when a single envelope is missing a
	// required field, names an unknown kind, carries a forbidden raw-field
	// key, or fails edge.AgentActionEvent.Validate after mapping.
	ErrInvalidEnvelope = errors.New("runtime ingest: invalid envelope")
	// ErrRuntimeBatchTooLarge is returned when the batch exceeds
	// MaxRuntimeBatchEvents or a single envelope's JSON encoding exceeds
	// MaxRuntimeEnvelopeBytes.
	ErrRuntimeBatchTooLarge = errors.New("runtime ingest: too large")
)

// Caps. Tuned to keep the worst-case batch bounded in both event count and
// byte size while leaving headroom for realistic Tetragon / Falco-style
// per-pod batches.
const (
	// MaxRuntimeBatchEvents bounds the number of events one Adapter.Map call
	// (or one HTTP POST body) may carry.
	MaxRuntimeBatchEvents = 256
	// MaxRuntimeBatchBodyBytes bounds the HTTP body size before JSON decode.
	MaxRuntimeBatchBodyBytes int64 = 1 << 20 // 1 MiB
	// MaxRuntimeEnvelopeBytes bounds the JSON encoding of one envelope.
	// Pre-redaction (raw input) and post-mapping (final AgentActionEvent)
	// sizes are both checked.
	MaxRuntimeEnvelopeBytes = 4 << 10 // 4 KiB
	// MaxRuntimeRedactedStringBytes bounds individual redacted string
	// values handed to edge.RedactValue.
	MaxRuntimeRedactedStringBytes = 256
	// MaxRuntimeLabelEntries bounds the labels carried per envelope.
	MaxRuntimeLabelEntries = 16
)

// Runtime kind constants the adapter knows how to map. Anything else is
// rejected at envelope validation; the AgentActionEvent.Kind constants live
// in core/edge/event.go.
const (
	KindProcessExec    = string(edgecore.EventKindRuntimeProcessExec)
	KindFileRead       = string(edgecore.EventKindRuntimeFileRead)
	KindFileWrite      = string(edgecore.EventKindRuntimeFileWrite)
	KindNetworkConnect = string(edgecore.EventKindRuntimeNetworkConnect)
	KindDNSQuery       = string(edgecore.EventKindRuntimeDNSQuery)
)

// Outcome status values a source may stamp on an envelope. Anything else is
// rejected before mapping. "" defaults to OutcomeStatusOK.
const (
	OutcomeStatusOK       = "ok"
	OutcomeStatusFailed   = "failed"
	OutcomeStatusDegraded = "degraded"
)

// DropReasonSampledOut identifies an envelope dropped by the deterministic
// sampler. Future drop reasons (e.g. "rate_limited") should be added here.
const DropReasonSampledOut = "sampled_out"

// SourceIdentity identifies the trusted runtime sidecar that produced the
// batch. Sources are bound to the API key tenant by the gateway; this struct
// is the wire shape, not the auth model.
type SourceIdentity struct {
	ID string `json:"source_id"`
}

// ProcessSummary carries the minimum redacted summary of a process exec
// event. argv, env, command_line, cmdline, and full executable paths are
// forbidden (see DecodeBatch).
type ProcessSummary struct {
	ExecutableBasename string `json:"executable_basename,omitempty"`
	ExecutableSHA256   string `json:"executable_sha256,omitempty"`
	ArgumentCount      int    `json:"argument_count,omitempty"`
}

// FileSummary carries a redacted summary of a file read/write. Contents are
// never carried; large blobs use artifact_ptrs.
type FileSummary struct {
	Operation    string `json:"operation,omitempty"`
	PathRedacted string `json:"path_redacted,omitempty"`
}

// NetworkSummary carries a bounded summary of a network connection. Packet
// payloads and request bodies are never carried.
type NetworkSummary struct {
	HostRedacted string `json:"host_redacted,omitempty"`
	IPPrefix     string `json:"ip_prefix,omitempty"`
	Port         int    `json:"port,omitempty"`
	Protocol     string `json:"protocol,omitempty"`
}

// DNSSummary carries a bounded summary of a DNS query. Response bodies are
// never carried.
type DNSSummary struct {
	QNameRedacted string `json:"qname_redacted,omitempty"`
	QType         string `json:"qtype,omitempty"`
}

// RuntimeEventEnvelope is one runtime telemetry record. Required:
// TenantID, SessionID, ExecutionID, SourceEventID, ObservedAt, Kind. All
// summary structs are optional; at least one will typically be populated
// for the corresponding Kind.
type RuntimeEventEnvelope struct {
	TenantID      string                     `json:"tenant_id"`
	SessionID     string                     `json:"session_id"`
	ExecutionID   string                     `json:"execution_id"`
	SourceEventID string                     `json:"source_event_id"`
	ObservedAt    time.Time                  `json:"observed_at"`
	Kind          string                     `json:"kind"`
	OutcomeStatus string                     `json:"outcome_status,omitempty"`
	Process       *ProcessSummary            `json:"process,omitempty"`
	File          *FileSummary               `json:"file,omitempty"`
	Network       *NetworkSummary            `json:"network,omitempty"`
	DNS           *DNSSummary                `json:"dns,omitempty"`
	Labels        map[string]string          `json:"labels,omitempty"`
	ArtifactPtrs  []edgecore.ArtifactPointer `json:"artifact_ptrs,omitempty"`
}

// RuntimeBatch is the wire batch: one source identity, an optional batch
// ID for operator correlation, and N envelopes.
type RuntimeBatch struct {
	Source  SourceIdentity         `json:"source"`
	Nonce   string                 `json:"nonce,omitempty"`
	BatchID string                 `json:"batch_id,omitempty"`
	Events  []RuntimeEventEnvelope `json:"events"`
}

// DropReport explains why one envelope was dropped from a successful Map
// call. The mapped event is not persisted; the operator sees the report
// in the gateway response.
type DropReport struct {
	SourceEventID string `json:"source_event_id"`
	Kind          string `json:"kind"`
	Reason        string `json:"reason"`
}

// MapResult is the outcome of Adapter.Map: zero-or-more mapped events
// ready for edge.Store.AppendEvents, plus zero-or-more drops.
type MapResult struct {
	Events  []edgecore.AgentActionEvent
	Dropped []DropReport
}

// AdapterOptions configures the adapter. Zero-value options accept all
// envelopes (1/1 sampling).
type AdapterOptions struct {
	// SampleNumerator and SampleDenominator gate deterministic sampling.
	// numerator/denominator of envelopes are accepted; the rest are dropped
	// with DropReasonSampledOut. Buckets are stable across retries because
	// the bucket is computed from a hash of (source_id|kind|source_event_id).
	SampleNumerator   int
	SampleDenominator int
}

// Adapter validates + maps runtime envelopes into edge.AgentActionEvent.
type Adapter struct {
	sampleNum int
	sampleDen int
}

// NewAdapter constructs an adapter with the supplied sampling policy.
// Zero or invalid sampling falls back to accept-all.
func NewAdapter(opts AdapterOptions) *Adapter {
	a := &Adapter{
		sampleNum: opts.SampleNumerator,
		sampleDen: opts.SampleDenominator,
	}
	if a.sampleDen <= 0 {
		a.sampleNum, a.sampleDen = 1, 1
	}
	if a.sampleNum < 0 {
		a.sampleNum = 0
	}
	if a.sampleNum > a.sampleDen {
		a.sampleNum = a.sampleDen
	}
	return a
}

// Map validates the batch, applies sampling, and maps surviving envelopes
// into edge.AgentActionEvent records. The first invalid envelope aborts
// the entire batch — partial persistence is forbidden.
func (a *Adapter) Map(batch RuntimeBatch) (MapResult, error) {
	sourceID := strings.TrimSpace(batch.Source.ID)
	if sourceID == "" {
		return MapResult{}, fmt.Errorf("%w: source.source_id required", ErrInvalidBatch)
	}
	if len(sourceID) > MaxRuntimeRedactedStringBytes {
		return MapResult{}, fmt.Errorf("%w: source.source_id too long", ErrInvalidBatch)
	}
	if len(batch.Events) == 0 {
		return MapResult{}, fmt.Errorf("%w: events required", ErrInvalidBatch)
	}
	if len(batch.Events) > MaxRuntimeBatchEvents {
		return MapResult{}, fmt.Errorf("%w: batch has %d events, max %d", ErrRuntimeBatchTooLarge, len(batch.Events), MaxRuntimeBatchEvents)
	}
	events := make([]edgecore.AgentActionEvent, 0, len(batch.Events))
	var drops []DropReport
	for i, env := range batch.Events {
		if err := validateEnvelope(env); err != nil {
			return MapResult{}, fmt.Errorf("events[%d]: %w", i, err)
		}
		// Pre-mapping size check on the raw envelope so very large input
		// fields are rejected before they reach the redactor.
		if raw, err := json.Marshal(env); err != nil {
			return MapResult{}, fmt.Errorf("events[%d]: %w: marshal envelope", i, ErrInvalidEnvelope)
		} else if len(raw) > MaxRuntimeEnvelopeBytes {
			return MapResult{}, fmt.Errorf("events[%d]: %w: envelope %d bytes > cap %d", i, ErrRuntimeBatchTooLarge, len(raw), MaxRuntimeEnvelopeBytes)
		}
		if !a.shouldAccept(sourceID, env) {
			drops = append(drops, DropReport{
				SourceEventID: env.SourceEventID,
				Kind:          env.Kind,
				Reason:        DropReasonSampledOut,
			})
			continue
		}
		evt, err := mapEnvelope(sourceID, env)
		if err != nil {
			return MapResult{}, fmt.Errorf("events[%d]: %w", i, err)
		}
		if err := evt.Validate(); err != nil {
			return MapResult{}, fmt.Errorf("events[%d]: %w: %v", i, ErrInvalidEnvelope, err)
		}
		events = append(events, evt)
	}
	return MapResult{Events: events, Dropped: drops}, nil
}

func validateEnvelope(env RuntimeEventEnvelope) error {
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
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidEnvelope, env.Kind)
	}
	if !isValidOutcomeStatus(env.OutcomeStatus) {
		return fmt.Errorf("%w: unknown outcome_status %q", ErrInvalidEnvelope, env.OutcomeStatus)
	}
	if len(env.Labels) > MaxRuntimeLabelEntries {
		return fmt.Errorf("%w: labels has %d entries, max %d", ErrInvalidEnvelope, len(env.Labels), MaxRuntimeLabelEntries)
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
	case KindProcessExec, KindFileRead, KindFileWrite, KindNetworkConnect, KindDNSQuery:
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

func mapEnvelope(sourceID string, env RuntimeEventEnvelope) (edgecore.AgentActionEvent, error) {
	input := buildInputFromSummaries(env)
	redacted, err := edgecore.RedactValue(input, edgecore.RedactionOptions{
		HashMode:       edgecore.RedactionHashNone,
		MaxDepth:       4,
		MaxItems:       32,
		MaxStringBytes: MaxRuntimeRedactedStringBytes,
		MaxTotalBytes:  MaxRuntimeEnvelopeBytes,
	})
	if err != nil {
		return edgecore.AgentActionEvent{}, fmt.Errorf("%w: redaction failed", ErrInvalidEnvelope)
	}
	inputRedacted, _ := redacted.Value.(map[string]any)
	if inputRedacted == nil {
		inputRedacted = map[string]any{}
	}
	eventID := stableEventID(sourceID, env)
	tenantID := strings.TrimSpace(env.TenantID)
	sessionID := strings.TrimSpace(env.SessionID)
	executionID := strings.TrimSpace(env.ExecutionID)
	labels, err := sanitizeRuntimeLabels(env.Labels)
	if err != nil {
		return edgecore.AgentActionEvent{}, err
	}
	labels["runtime.source_id"] = sourceID
	var pointers []edgecore.ArtifactPointer
	if len(env.ArtifactPtrs) > 0 {
		pointers = make([]edgecore.ArtifactPointer, len(env.ArtifactPtrs))
		for i, art := range env.ArtifactPtrs {
			ap := art
			ap.TenantID = tenantID
			ap.SessionID = sessionID
			ap.ExecutionID = executionID
			ap.EventID = eventID
			ap.CreatedAt = ap.CreatedAt.UTC()
			pointers[i] = ap
		}
	}
	return edgecore.AgentActionEvent{
		EventID:          eventID,
		SessionID:        sessionID,
		ExecutionID:      executionID,
		TenantID:         tenantID,
		Timestamp:        env.ObservedAt.UTC(),
		Layer:            edgecore.LayerRuntime,
		Kind:             edgecore.EventKind(env.Kind),
		Decision:         edgecore.DecisionRecorded,
		RuleTier:         "",
		InputRedacted:    inputRedacted,
		Status:           outcomeStatusToActionStatus(env.OutcomeStatus),
		Labels:           labels,
		ArtifactPointers: pointers,
	}, nil
}

func sanitizeRuntimeLabels(labels map[string]string) (edgecore.Labels, error) {
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
		if trimmedKey == "runtime.source_id" || runtimeLabelKeyIsSensitive(key) {
			continue
		}
		redacted, err := redactRuntimeLabelValue(value)
		if err != nil {
			return nil, err
		}
		out[key] = redacted
	}
	return out, nil
}

func runtimeLabelKeyIsSensitive(key string) bool {
	if runtimeLabelKeyNameIsSensitive(key) {
		return true
	}
	result, err := edgecore.RedactValue(key, edgecore.RedactionOptions{
		HashMode:       edgecore.RedactionHashNone,
		MaxStringBytes: edgecore.MaxLabelKeyBytes,
		MaxTotalBytes:  MaxRuntimeEnvelopeBytes,
	})
	return err != nil || result.Redacted
}

func runtimeLabelKeyNameIsSensitive(key string) bool {
	probe := map[string]any{key: "runtime-label-probe"}
	result, err := edgecore.RedactValue(probe, edgecore.RedactionOptions{
		HashMode:       edgecore.RedactionHashNone,
		MaxDepth:       1,
		MaxItems:       MaxRuntimeLabelEntries + 1,
		MaxStringBytes: edgecore.MaxLabelValueBytes,
		MaxTotalBytes:  MaxRuntimeEnvelopeBytes,
	})
	return err != nil || result.Redacted
}

func redactRuntimeLabelValue(value string) (string, error) {
	if len(value) > edgecore.MaxLabelValueBytes {
		return "", fmt.Errorf("%w: label value exceeds %d bytes", ErrInvalidEnvelope, edgecore.MaxLabelValueBytes)
	}
	result, err := edgecore.RedactValue(value, edgecore.RedactionOptions{
		HashMode:       edgecore.RedactionHashNone,
		MaxStringBytes: edgecore.MaxLabelValueBytes,
		MaxTotalBytes:  MaxRuntimeEnvelopeBytes,
	})
	if err != nil {
		return "", fmt.Errorf("%w: label redaction failed", ErrInvalidEnvelope)
	}
	if redacted, ok := result.Value.(string); ok {
		return redacted, nil
	}
	return "", fmt.Errorf("%w: label redaction returned %T", ErrInvalidEnvelope, result.Value)
}

func buildInputFromSummaries(env RuntimeEventEnvelope) map[string]any {
	input := map[string]any{}
	if env.Process != nil {
		if v := strings.TrimSpace(env.Process.ExecutableBasename); v != "" {
			input["executable_basename"] = v
		}
		if v := strings.TrimSpace(env.Process.ExecutableSHA256); v != "" {
			input["executable_sha256"] = v
		}
		if env.Process.ArgumentCount > 0 {
			input["argument_count"] = env.Process.ArgumentCount
		}
	}
	if env.File != nil {
		if v := strings.TrimSpace(env.File.Operation); v != "" {
			input["operation"] = v
		}
		if v := strings.TrimSpace(env.File.PathRedacted); v != "" {
			input["path_redacted"] = v
		}
	}
	if env.Network != nil {
		if v := strings.TrimSpace(env.Network.HostRedacted); v != "" {
			input["host_redacted"] = v
		}
		if v := strings.TrimSpace(env.Network.IPPrefix); v != "" {
			input["ip_prefix"] = v
		}
		if env.Network.Port > 0 {
			input["port"] = env.Network.Port
		}
		if v := strings.TrimSpace(env.Network.Protocol); v != "" {
			input["protocol"] = v
		}
	}
	if env.DNS != nil {
		if v := strings.TrimSpace(env.DNS.QNameRedacted); v != "" {
			input["qname_redacted"] = v
		}
		if v := strings.TrimSpace(env.DNS.QType); v != "" {
			input["qtype"] = v
		}
	}
	return input
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

func stableEventID(sourceID string, env RuntimeEventEnvelope) string {
	h := fnv.New64a()
	// The seed mirrors shouldAccept's hash input plus tenant/session/execution
	// so that retries from the same source with the same source_event_id
	// resolve to the same EventID — making the gateway's
	// store.AppendEvents path idempotent by EventID. fnv.Hash64 writers
	// never return an error so the discard is safe.
	_, _ = fmt.Fprintf(h, "%s|%s|%s|%s|%s|%s", sourceID, env.TenantID, env.SessionID, env.ExecutionID, env.Kind, env.SourceEventID)
	return fmt.Sprintf("runtime-%016x", h.Sum64())
}

func (a *Adapter) shouldAccept(sourceID string, env RuntimeEventEnvelope) bool {
	if a.sampleDen <= 0 || a.sampleNum >= a.sampleDen {
		return true
	}
	if a.sampleNum <= 0 {
		return false
	}
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s|%s|%s", sourceID, env.Kind, env.SourceEventID)
	return int(h.Sum64()%uint64(a.sampleDen)) < a.sampleNum // #nosec G115 -- modulo by the positive int sampleDen yields a value in [0, sampleDen), which always fits int
}

// DecodeBatch decodes the wire batch with strict-schema enforcement so
// smuggled-in keys (argv, args, body, headers, secret, token, password, etc.)
// are rejected at the boundary. DisallowUnknownFields applies recursively to
// every nested struct via go's encoding/json strict mode.
func DecodeBatch(r io.Reader) (RuntimeBatch, error) {
	var batch RuntimeBatch
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&batch); err != nil {
		return RuntimeBatch{}, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return RuntimeBatch{}, fmt.Errorf(
				"%w: trailing data after JSON value: %v", ErrInvalidEnvelope, err,
			)
		}
		return RuntimeBatch{}, fmt.Errorf("%w: trailing data after JSON value", ErrInvalidEnvelope)
	}
	if err := validateRuntimeBatchNonce(batch.Nonce); err != nil {
		return RuntimeBatch{}, err
	}
	return batch, nil
}

func validateRuntimeBatchNonce(nonce string) error {
	required, err := runtimeReplayRequired()
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

func runtimeReplayRequired() (bool, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("CORDUM_EDGE_RUNTIME_REPLAY_REQUIRED")))
	switch value {
	case "", "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%w: CORDUM_EDGE_RUNTIME_REPLAY_REQUIRED must be true/false/1/0/yes/no", ErrInvalidBatch)
	}
}
