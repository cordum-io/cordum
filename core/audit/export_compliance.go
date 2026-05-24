package audit

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// ComplianceExportFormat discriminates NDJSON and CSV output streams.
type ComplianceExportFormat string

const (
	ComplianceExportFormatJSON ComplianceExportFormat = "json"
	ComplianceExportFormatCSV  ComplianceExportFormat = "csv"
)

// DefaultComplianceExportMaxEvents bounds a single export call when the
// caller does not override. 1M events ≈ a few hundred MB of NDJSON at
// typical field widths — enough for enterprise quarterly audits while
// still small enough to fit a single-request streaming response.
const DefaultComplianceExportMaxEvents = 1_000_000

// complianceStreamChunkSize is the XRangeN page size used while walking
// the tenant's audit stream. 10_000 balances Redis memory pressure
// against round-trip overhead.
const complianceStreamChunkSize = int64(10_000)

// SignedBundleSnapshot is the policy-bundle evidence row embedded in
// every compliance export manifest. Every field matches the wire shape
// the gateway's bundle store already persists; populating this struct
// is the one place where the bundle store's signing metadata (task
// fcd39725's BundleSignature accessor) is consulted.
type SignedBundleSnapshot struct {
	BundleID         string `json:"bundle_id"`
	Name             string `json:"name,omitempty"`
	Version          string `json:"version,omitempty"`
	ActivatedAt      string `json:"activated_at,omitempty"`
	DeactivatedAt    string `json:"deactivated_at,omitempty"`
	ContentSHA256Hex string `json:"content_sha256,omitempty"`
	Ed25519SigBase64 string `json:"ed25519_signature,omitempty"`
	PublicKeyID      string `json:"public_key_id,omitempty"`
	SignedBy         string `json:"signed_by,omitempty"`
	// Note captures a human-readable annotation when the bundle is
	// unsigned (non-strict mode) or when signature metadata is missing.
	Note string `json:"note,omitempty"`
}

// ExportManifest describes the export in machine-readable form. The
// NDJSON path writes this as the first line with type="manifest"; the
// CSV path emits it as a leading `# cordum-manifest: {...}` comment.
type ExportManifest struct {
	Type                 string                 `json:"type"`
	GeneratedAt          time.Time              `json:"generated_at"`
	TenantID             string                 `json:"tenant_id"`
	From                 time.Time              `json:"from"`
	To                   time.Time              `json:"to"`
	Format               ComplianceExportFormat `json:"format"`
	SOC2Legend           map[string]string      `json:"soc2_legend"`
	ChainVerification    *VerifyResult          `json:"chain_verification,omitempty"`
	PolicySnapshots      []SignedBundleSnapshot `json:"policy_snapshots"`
	EventCount           int                    `json:"event_count"`
	TruncatedAtMax       bool                   `json:"truncated_at_max"`
	MaxEventsCap         int                    `json:"max_events_cap"`
	RetentionBoundarySeq int64                  `json:"retention_boundary_seq,omitempty"`
	// RowFilter records the event_type/severity/category filter applied to
	// the EVENT ROWS, when any. It is additive and self-describing: EventCount
	// above is the POST-filter row count, while ChainVerification covers the
	// FULL range — so a filtered export is never mistaken for a tamper gap.
	// Omitted entirely when no filter is set.
	RowFilter *ExportRowFilter `json:"row_filter,omitempty"`
	// RowFilterApplied is always present so an auditor can distinguish "no
	// filter" from "old export format" without ambiguity. False on every
	// unfiltered export (byte-stable companion to TruncatedAtMax).
	RowFilterApplied bool `json:"row_filter_applied"`
}

// ExportRowFilter is the machine-readable record of the row filter applied to
// a compliance export. Fields mirror the ComplianceExportOptions filter knobs;
// empty fields are omitted so the manifest only advertises active predicates.
type ExportRowFilter struct {
	EventType string `json:"event_type,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Category  string `json:"category,omitempty"`
}

// BundleLookupFn resolves the signed policy bundles that were active
// inside [from, to] for a tenant. Caller-provided so core/audit stays
// decoupled from core/controlplane/gateway's bundle store.
type BundleLookupFn func(ctx context.Context, tenantID string, from, to time.Time) ([]SignedBundleSnapshot, error)

// ChainVerifierFn is the chain-integrity callback. The export writer
// passes the same stream key it walked so the verification result
// reflects the exported range.
type ChainVerifierFn func(ctx context.Context, client redis.UniversalClient, streamKey string, opts VerifyOptions) (*VerifyResult, error)

// ComplianceExportOptions configures a single export call.
type ComplianceExportOptions struct {
	TenantID  string
	StreamKey string
	From      time.Time
	To        time.Time
	Format    ComplianceExportFormat
	Excel     bool
	MaxEvents int
	// EventType / Severity / Category filter the EMITTED ROWS only — never
	// the chain verification, which always runs over the full range. EventType
	// and Severity match case-insensitively; Category ("governance"|"routine")
	// is resolved per row via CategoryFor(ev.EventType). Empty means "no
	// constraint on that dimension"; all empty means every in-range row emits.
	EventType    string
	Severity     string
	Category     string
	SOC2Mapping  SOC2Mapping
	SOC2Legend   map[string]string
	BundleLookup BundleLookupFn
	Verifier     ChainVerifierFn
}

// rowMatches reports whether ev passes the configured row filter. Returns true
// when no filter is set (the common, unfiltered path). EventType and Severity
// compare case-insensitively; Category is derived from CategoryFor so the
// caller filters by governance/routine without enumerating event types.
func (o ComplianceExportOptions) rowMatches(ev SIEMEvent) bool {
	if o.EventType != "" && !strings.EqualFold(ev.EventType, o.EventType) {
		return false
	}
	if o.Severity != "" && !strings.EqualFold(ev.Severity, o.Severity) {
		return false
	}
	if o.Category != "" && CategoryFor(ev.EventType) != o.Category {
		return false
	}
	return true
}

// rowFilter returns the typed manifest record of the active row filter, or nil
// when no dimension is constrained. Mirrors rowMatches: a nil result means
// every in-range row is emitted.
func (o ComplianceExportOptions) rowFilter() *ExportRowFilter {
	if o.EventType == "" && o.Severity == "" && o.Category == "" {
		return nil
	}
	return &ExportRowFilter{
		EventType: o.EventType,
		Severity:  o.Severity,
		Category:  o.Category,
	}
}

// ---------------------------------------------------------------------------
// CSV column contract
// ---------------------------------------------------------------------------

// complianceCSVHeaders defines the exact column order embedded in every
// CSV export. Exported for tests and for the handler's OpenAPI example.
var complianceCSVHeaders = []string{
	"timestamp",
	"event_type",
	"severity",
	"tenant_id",
	"agent_id",
	"agent_name",
	"agent_risk_tier",
	"job_id",
	"action",
	"decision",
	"matched_rule",
	"reason",
	"risk_tags",
	"capabilities",
	"policy_version",
	"identity",
	"seq",
	"event_hash",
	"prev_hash",
	"soc2_controls",
	"extra_json",
}

// utf8BOM is prepended to CSV output when Excel mode is requested.
const utf8BOM = "\xef\xbb\xbf"

// csvInjectionPrefixes is the OWASP-recommended set of characters that
// start a spreadsheet formula when a cell begins with them. Prefixing
// with a single apostrophe neutralises the exploit.
var csvInjectionPrefixes = [...]byte{'=', '+', '-', '@', '\t', '\r'}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// WriteComplianceExport streams a compliance audit export to w.
//
// Flow:
//  1. Walk the tenant's stream in chunks of 10k entries, bounded by
//     [from, to] and capped at opts.MaxEvents.
//  2. Emit each event immediately in the requested format so the caller
//     can stream gigabyte-scale exports without buffering.
//  3. After the walk, call the chain verifier over the same range to
//     produce the manifest's integrity attestation.
//  4. Look up the signed policy bundles active in the range.
//  5. For NDJSON: the manifest is the FIRST line (operators see the
//     attestation before the event bodies). For CSV: because streaming
//     writes the header+rows as they come, the manifest is written as a
//     leading hash-prefixed comment line BEFORE the CSV writer starts —
//     a CSV reader that ignores lines starting with `#` sees a clean
//     RFC-4180 payload.
//
// Returning a non-nil manifest lets the HTTP handler correlate log
// fields even on error paths.
func WriteComplianceExport(
	ctx context.Context,
	w io.Writer,
	client redis.UniversalClient,
	opts ComplianceExportOptions,
) (*ExportManifest, error) {
	if w == nil {
		return nil, errors.New("compliance export: writer is nil")
	}
	if client == nil {
		return nil, errors.New("compliance export: redis client is nil")
	}
	format := opts.Format
	if format == "" {
		format = ComplianceExportFormatJSON
	}
	if format != ComplianceExportFormatJSON && format != ComplianceExportFormatCSV {
		return nil, fmt.Errorf("compliance export: unsupported format %q", format)
	}
	if opts.MaxEvents <= 0 {
		opts.MaxEvents = DefaultComplianceExportMaxEvents
	}
	if opts.SOC2Mapping == nil {
		opts.SOC2Mapping = DefaultSOC2Mapping()
	}
	if opts.SOC2Legend == nil {
		opts.SOC2Legend = DefaultSOC2Legend()
	}
	if opts.Verifier == nil {
		opts.Verifier = func(ctx context.Context, c redis.UniversalClient, key string, vopts VerifyOptions) (*VerifyResult, error) {
			return VerifyChain(ctx, c, key, vopts)
		}
	}

	manifest := &ExportManifest{
		Type:         "manifest",
		GeneratedAt:  time.Now().UTC(),
		TenantID:     opts.TenantID,
		From:         opts.From,
		To:           opts.To,
		Format:       format,
		SOC2Legend:   cloneStringMap(opts.SOC2Legend),
		MaxEventsCap: opts.MaxEvents,
	}
	// Record any row filter up-front so it is present in the manifest line the
	// NDJSON path writes BEFORE the walk (and in the CSV comment). The filter
	// only affects EVENT ROWS — ChainVerification below still covers the full
	// range, making a filtered export self-describing rather than a tamper gap.
	if filter := opts.rowFilter(); filter != nil {
		manifest.RowFilter = filter
		manifest.RowFilterApplied = true
	}

	// --- 1) Policy snapshots. Surfaced even on an empty event stream
	// so a dev tenant with no activity still shows the current bundle
	// signatures in the manifest — useful evidence on its own.
	if opts.BundleLookup != nil {
		snapshots, err := opts.BundleLookup(ctx, opts.TenantID, opts.From, opts.To)
		if err != nil {
			return manifest, fmt.Errorf("compliance export: bundle lookup: %w", err)
		}
		// Normalise to a non-nil slice so JSON marshals [] not null.
		if snapshots == nil {
			snapshots = []SignedBundleSnapshot{}
		}
		manifest.PolicySnapshots = snapshots
	} else {
		manifest.PolicySnapshots = []SignedBundleSnapshot{}
	}

	// --- 2) Set up the format-specific emitter + its manifest write
	// hook. CSV emits its manifest as a `#` comment BEFORE writing any
	// rows, so the manifest is captured in-place once we've built it.
	switch format {
	case ComplianceExportFormatJSON:
		return writeNDJSONExport(ctx, w, client, opts, manifest)
	case ComplianceExportFormatCSV:
		return writeCSVExport(ctx, w, client, opts, manifest)
	}
	return manifest, fmt.Errorf("compliance export: unreachable format %q", format)
}

// ---------------------------------------------------------------------------
// NDJSON path
// ---------------------------------------------------------------------------

// writeNDJSONExport drains the stream while emitting events as NDJSON
// lines. The manifest is emitted LAST on the happy path by buffering
// events through an io.Writer wrapper: because callers want the
// manifest on line 1 but the chain-verification status belongs AFTER
// the walk, we materialise the manifest first (with placeholder
// verification), emit events as we go, then flush the final manifest
// to a secondary line marked "manifest_final".
//
// Actually — operators overwhelmingly want the manifest BEFORE they
// start parsing events (so downstream pipelines can short-circuit on
// status=compromised). We therefore do two small walks: one verify
// pass (cheap: the chain is already bounded by since/until) then the
// event walk. This keeps the manifest line at position 0.
func writeNDJSONExport(
	ctx context.Context,
	w io.Writer,
	client redis.UniversalClient,
	opts ComplianceExportOptions,
	manifest *ExportManifest,
) (*ExportManifest, error) {
	// Chain verification first so the manifest reflects integrity at
	// export time. The verifier uses the same time bounds as the walk.
	verifyOpts := VerifyOptions{
		SinceMs: timeToMillis(opts.From),
		UntilMs: timeToMillis(opts.To),
		Limit:   int64(opts.MaxEvents),
	}
	if verifyOpts.Limit > MaxVerifyLimit {
		verifyOpts.Limit = MaxVerifyLimit
	}
	verifyResult, err := opts.Verifier(ctx, client, opts.StreamKey, verifyOpts)
	if err != nil {
		return manifest, fmt.Errorf("compliance export: chain verify: %w", err)
	}
	manifest.ChainVerification = verifyResult
	if verifyResult != nil {
		manifest.RetentionBoundarySeq = verifyResult.RetentionBoundarySeq
	}

	// Manifest line first. We'll patch the event count + truncation
	// flag in a trailing footer line once the walk completes, but the
	// headline integrity info is already authoritative here.
	if err := writeJSONLine(w, manifest); err != nil {
		return manifest, err
	}

	count := 0
	truncated := false
	err = walkChainStream(ctx, client, opts.StreamKey, opts.From, opts.To, int64(opts.MaxEvents), func(ev SIEMEvent) error {
		// Row filter applies ONLY here, at the top of the emit callback —
		// never in the opts.Verifier call above or in walkChainStream's cursor
		// advance, so chain verification stays full-range. `count` below
		// increments only on a successful write, so EventCount automatically
		// becomes the POST-filter total.
		if !opts.rowMatches(ev) {
			return nil
		}
		controls := opts.SOC2Mapping.ControlsFor(ev)
		line, err := buildNDJSONEventLine(ev, controls)
		if err != nil {
			return err
		}
		if _, werr := w.Write(line); werr != nil {
			return werr
		}
		count++
		return nil
	})
	if err != nil {
		// Emit a partial footer so a downstream pipeline can distinguish
		// a broken stream from a complete one, then surface the error.
		footer := map[string]any{
			"type":        "footer",
			"event_count": count,
			"error":       err.Error(),
			"truncated":   false,
		}
		_ = writeJSONLine(w, footer)
		manifest.EventCount = count
		return manifest, err
	}
	if count >= opts.MaxEvents {
		truncated = true
	}
	manifest.EventCount = count
	manifest.TruncatedAtMax = truncated
	// Trailing footer mirrors the headline manifest so a caller
	// streaming the file can assert integrity AFTER confirming the
	// byte count matches.
	footer := map[string]any{
		"type":           "footer",
		"event_count":    count,
		"truncated":      truncated,
		"max_events_cap": opts.MaxEvents,
		"generated_at":   manifest.GeneratedAt,
	}
	if err := writeJSONLine(w, footer); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// buildNDJSONEventLine marshals a SIEMEvent + its SOC2 controls into a
// newline-terminated NDJSON line. Uses json.RawMessage interleaving so
// we don't re-hash field names or build an intermediate map per event
// on the hot path.
func buildNDJSONEventLine(ev SIEMEvent, controls []string) ([]byte, error) {
	eventJSON, err := json.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	ctrlsJSON, err := json.Marshal(controls)
	if err != nil {
		return nil, fmt.Errorf("marshal controls: %w", err)
	}
	// Strip the trailing `}` off the event JSON so we can graft the
	// extra fields onto the same object. Every encoding/json object
	// ends with `}` — guaranteed by json.Marshal.
	if len(eventJSON) == 0 || eventJSON[len(eventJSON)-1] != '}' {
		return nil, errors.New("event JSON did not end with '}'")
	}
	prefix := eventJSON[:len(eventJSON)-1]
	// If the object was empty (`{}`), skip the leading comma.
	sep := []byte{','}
	if len(prefix) == 1 && prefix[0] == '{' {
		sep = sep[:0]
	}
	line := make([]byte, 0)
	line = append(line, prefix...)
	line = append(line, sep...)
	line = append(line, []byte(`"type":"event","soc2_controls":`)...)
	line = append(line, ctrlsJSON...)
	line = append(line, '}', '\n')
	return line, nil
}

// ---------------------------------------------------------------------------
// CSV path
// ---------------------------------------------------------------------------

// writeCSVExport emits a RFC-4180 CSV preceded by a `#` comment line
// carrying the manifest JSON. Spreadsheet consumers ignore the comment;
// CSV libraries that honour comments (Python pandas, Go's encoding/csv
// with Comment='#') skip it cleanly. Excel mode prepends a UTF-8 BOM so
// Excel recognises the file as UTF-8.
func writeCSVExport(
	ctx context.Context,
	w io.Writer,
	client redis.UniversalClient,
	opts ComplianceExportOptions,
	manifest *ExportManifest,
) (*ExportManifest, error) {
	// Verify BEFORE writing so the comment-line manifest is authoritative.
	verifyOpts := VerifyOptions{
		SinceMs: timeToMillis(opts.From),
		UntilMs: timeToMillis(opts.To),
		Limit:   int64(opts.MaxEvents),
	}
	if verifyOpts.Limit > MaxVerifyLimit {
		verifyOpts.Limit = MaxVerifyLimit
	}
	verifyResult, err := opts.Verifier(ctx, client, opts.StreamKey, verifyOpts)
	if err != nil {
		return manifest, fmt.Errorf("compliance export: chain verify: %w", err)
	}
	manifest.ChainVerification = verifyResult
	if verifyResult != nil {
		manifest.RetentionBoundarySeq = verifyResult.RetentionBoundarySeq
	}

	if opts.Excel {
		if _, err := io.WriteString(w, utf8BOM); err != nil {
			return manifest, err
		}
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return manifest, fmt.Errorf("compliance export: manifest marshal: %w", err)
	}
	// CSV comment line: `# cordum-manifest: {...}\n`
	if _, err := fmt.Fprintf(w, "# cordum-manifest: %s\n", manifestBytes); err != nil {
		return manifest, err
	}

	csvW := csv.NewWriter(w)
	if err := csvW.Write(complianceCSVHeaders); err != nil {
		return manifest, fmt.Errorf("compliance export: write header: %w", err)
	}

	count := 0
	truncated := false
	err = walkChainStream(ctx, client, opts.StreamKey, opts.From, opts.To, int64(opts.MaxEvents), func(ev SIEMEvent) error {
		// Row filter applies ONLY here (see writeNDJSONExport for the full
		// invariant note): the opts.Verifier pass above already covered the
		// full range; this gate only decides which rows are written.
		if !opts.rowMatches(ev) {
			return nil
		}
		controls := opts.SOC2Mapping.ControlsFor(ev)
		row := buildCSVRow(ev, controls)
		if err := csvW.Write(row); err != nil {
			return err
		}
		count++
		// Flush periodically so clients see bytes on long exports.
		if count%1000 == 0 {
			csvW.Flush()
			if ferr := csvW.Error(); ferr != nil {
				return ferr
			}
		}
		return nil
	})
	csvW.Flush()
	if ferr := csvW.Error(); ferr != nil && err == nil {
		err = ferr
	}
	if err != nil {
		manifest.EventCount = count
		return manifest, err
	}
	if count >= opts.MaxEvents {
		truncated = true
	}
	manifest.EventCount = count
	manifest.TruncatedAtMax = truncated
	return manifest, nil
}

// buildCSVRow renders one SIEMEvent as a string slice aligned with
// complianceCSVHeaders. Every cell is run through csvSafe first so
// spreadsheet formula-injection is neutralised at the source.
func buildCSVRow(ev SIEMEvent, controls []string) []string {
	extraJSON := ""
	if len(ev.Extra) > 0 {
		keys := make([]string, 0, len(ev.Extra))
		for k := range ev.Extra {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		// Marshal in a deterministic order so exports are reproducible.
		stable := make(map[string]string, len(keys))
		for _, k := range keys {
			stable[k] = ev.Extra[k]
		}
		if b, err := json.Marshal(stable); err == nil {
			extraJSON = string(b)
		}
	}
	row := []string{
		ev.Timestamp.UTC().Format(time.RFC3339Nano),
		ev.EventType,
		ev.Severity,
		ev.TenantID,
		ev.AgentID,
		ev.AgentName,
		ev.AgentRiskTier,
		ev.JobID,
		ev.Action,
		ev.Decision,
		ev.MatchedRule,
		ev.Reason,
		strings.Join(ev.RiskTags, "|"),
		strings.Join(ev.Capabilities, "|"),
		ev.PolicyVersion,
		ev.Identity,
		strconv.FormatInt(ev.Seq, 10),
		ev.EventHash,
		ev.PrevHash,
		strings.Join(controls, "|"),
		extraJSON,
	}
	for i, cell := range row {
		row[i] = csvSafe(cell)
	}
	return row
}

// csvSafe prefixes cells starting with a formula-trigger character with
// a single apostrophe. Also guards against cells that begin with a tab
// or carriage-return — some spreadsheet software strips those and ends
// up executing the following content.
func csvSafe(cell string) string {
	if cell == "" {
		return cell
	}
	first := cell[0]
	for _, bad := range csvInjectionPrefixes {
		if first == bad {
			return "'" + cell
		}
	}
	return cell
}

// ---------------------------------------------------------------------------
// Stream walk
// ---------------------------------------------------------------------------

// walkChainStream invokes fn once per event in [from, to] (inclusive),
// capped at maxEvents. XRangeN is called in chunks of 10k entries to
// cap Redis memory. ctx cancellation is checked between chunks.
func walkChainStream(
	ctx context.Context,
	client redis.UniversalClient,
	streamKey string,
	from, to time.Time,
	maxEvents int64,
	fn func(SIEMEvent) error,
) error {
	minID := "-"
	if !from.IsZero() {
		minID = strconv.FormatInt(timeToMillis(from), 10) + "-0"
	}
	maxID := "+"
	if !to.IsZero() {
		maxID = strconv.FormatInt(timeToMillis(to), 10) + "-18446744073709551615"
	}
	emitted := int64(0)
	cursor := minID
	for emitted < maxEvents {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := maxEvents - emitted
		pageSize := complianceStreamChunkSize
		if remaining < pageSize {
			pageSize = remaining
		}
		entries, err := client.XRangeN(ctx, streamKey, cursor, maxID, pageSize).Result()
		if err != nil {
			return fmt.Errorf("xrange %s: %w", streamKey, err)
		}
		if len(entries) == 0 {
			return nil
		}
		for _, entry := range entries {
			if emitted >= maxEvents {
				return nil
			}
			payload, ok := entry.Values[chainStreamFieldEvent].(string)
			if !ok {
				// Non-chain entry — skip (exotic streams may hold legacy
				// data). Do NOT abort the whole export.
				continue
			}
			var ev SIEMEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				// Malformed payload: increment a per-export metric at
				// the HTTP layer if desired; don't fail the full export.
				continue
			}
			if err := fn(ev); err != nil {
				return err
			}
			emitted++
		}
		// Advance the cursor: use exclusive start "(<last-id>" so we
		// don't re-emit the last row we just handled.
		cursor = "(" + entries[len(entries)-1].ID
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal ndjson line: %w", err)
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func timeToMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func cloneStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
