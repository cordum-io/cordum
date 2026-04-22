package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Eval dataset hard limits. The caps here are enforced by the model-level
// Validate() and are the canonical contract used by the store and handler
// layers alike. They exist to keep a single Redis hash payload bounded and
// the gateway's in-memory copy predictable under pathological input.
const (
	// MaxEvalDatasetEntries is the hard cap on entries in a single dataset.
	// Datasets that outgrow this cap must be split along a meaningful axis
	// (tenant, topic, risk tier) rather than raised — the rail is that
	// datasets are curated test fixtures, not bulk event dumps.
	MaxEvalDatasetEntries = 10_000

	// MaxEvalDatasetBytes is the hard cap on the canonical-JSON serialization
	// size of a dataset. 16 MiB gives ~1.6 KiB per entry at full capacity,
	// which covers realistic job-request snapshots with headroom.
	MaxEvalDatasetBytes = 16 * 1024 * 1024

	// MaxEvalEntryNotesBytes bounds the operator-provided notes field per
	// entry so a single annotator can't blow past the payload cap alone.
	MaxEvalEntryNotesBytes = 4 * 1024

	// MaxEvalEntryMetadataKeys bounds the metadata map width per entry.
	MaxEvalEntryMetadataKeys = 32
)

// Eval entry origin values. These are stable wire constants — the
// governance replay pipeline (phase 2) treats each origin differently when
// attributing regressions, so renaming one is a breaking wire change.
const (
	EvalEntrySourceManual        = "manual"
	EvalEntrySourceAuditImport   = "audit-import"
	EvalEntrySourceReplayImport  = "replay-import"
)

var validEvalEntrySources = map[string]struct{}{
	EvalEntrySourceManual:       {},
	EvalEntrySourceAuditImport:  {},
	EvalEntrySourceReplayImport: {},
}

// evalDatasetNamePattern pins the dataset name format. The first character
// must be a lowercase alphanumeric so names sort predictably and stay safe
// as Redis key components; the tail allows `_` and `-` for human-friendly
// labels. Length is 3..64 so names fit in indexes without ballooning.
var evalDatasetNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_\-]{2,63}$`)

// EvalDataset is a curated, versioned collection of policy-regression test
// cases. Datasets are immutable once created: mutating an existing (name,
// version) pair is forbidden by the store, and any change of content must
// flow through a new version. See docs/evals/datasets.md for the rail.
type EvalDataset struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Version     int         `json:"version"`
	Tenant      string      `json:"tenant"`
	Description string      `json:"description,omitempty"`
	Entries     []EvalEntry `json:"entries"`
	// CreatedAt is the RFC3339Nano UTC timestamp at which the dataset was
	// first durably stored. The store also indexes the corresponding
	// unix-milli score for cursor pagination; the string form here is the
	// wire contract.
	CreatedAt string `json:"created_at"`
	// UpdatedAt exists solely to satisfy the generic resource envelope used
	// by existing gateway responses; by rail it is always equal to
	// CreatedAt on a freshly-created (and therefore immutable) dataset.
	UpdatedAt   string `json:"updated_at"`
	CreatedBy   string `json:"created_by,omitempty"`
	EntryCount  int    `json:"entry_count"`
	// ContentHash is the sha256 (hex) of the canonical-JSON Entries slice,
	// computed at create time. It is a tamper-evidence aid for operators
	// auditing long-lived datasets, not a security primitive — the store
	// does not re-verify it on read.
	ContentHash string `json:"content_hash"`
}

// EvalEntry is a single policy-regression test case. Input captures a
// snapshot of the originating job-request shape (tenant, topic, caps,
// risk tags, metadata, agent id) as raw JSON so callers retain full
// fidelity without the model having to track every gateway field.
type EvalEntry struct {
	ID               string            `json:"id"`
	Input            json.RawMessage   `json:"input"`
	ExpectedDecision SafetyDecision    `json:"expected_decision"`
	RuleID           string            `json:"rule_id,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	Source           string            `json:"source,omitempty"`
	SourceRef        string            `json:"source_ref,omitempty"`
	Notes            string            `json:"notes,omitempty"`
}

// EvalDatasetFilter narrows a list query. Tenant is always implicit in the
// store (the store is tenant-scoped at the call site); NamePrefix matches
// the start of the name; CreatedAfter/Before bound the creation window in
// unix-milli.
type EvalDatasetFilter struct {
	Tenant         string
	NamePrefix     string
	CreatedAfterMS int64
	CreatedBeforeMS int64
}

// EvalDatasetPage is a single page of list results.
type EvalDatasetPage struct {
	Items      []EvalDataset `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// Normalize trims and lowercases the fields a caller is expected to
// provide without care. It does not generate missing values — the store
// owns ID/timestamp assignment.
func (d *EvalDataset) Normalize() {
	if d == nil {
		return
	}
	d.ID = strings.TrimSpace(d.ID)
	d.Name = strings.ToLower(strings.TrimSpace(d.Name))
	d.Tenant = strings.TrimSpace(d.Tenant)
	d.Description = strings.TrimSpace(d.Description)
	d.CreatedBy = strings.TrimSpace(d.CreatedBy)
	d.ContentHash = strings.ToLower(strings.TrimSpace(d.ContentHash))
	for i := range d.Entries {
		d.Entries[i].normalize()
	}
}

func (e *EvalEntry) normalize() {
	e.ID = strings.TrimSpace(e.ID)
	e.RuleID = strings.TrimSpace(e.RuleID)
	e.Source = strings.ToLower(strings.TrimSpace(e.Source))
	e.SourceRef = strings.TrimSpace(e.SourceRef)
	e.Notes = strings.TrimSpace(e.Notes)
}

// Validate enforces the shape of a dataset. It does not compute or verify
// ContentHash — that is an invariant the store layer is responsible for
// because only it knows the final canonical-JSON form of the record.
func (d *EvalDataset) Validate() error {
	if d == nil {
		return fmt.Errorf("eval dataset is nil")
	}

	if !evalDatasetNamePattern.MatchString(d.Name) {
		return fmt.Errorf("eval dataset name must match %s", evalDatasetNamePattern.String())
	}
	if d.Version < 1 {
		return fmt.Errorf("eval dataset version must be >= 1")
	}
	if d.Tenant == "" {
		return fmt.Errorf("eval dataset tenant is required")
	}
	if len(d.Entries) == 0 {
		return fmt.Errorf("eval dataset must contain at least one entry")
	}
	if len(d.Entries) > MaxEvalDatasetEntries {
		return fmt.Errorf("eval dataset has %d entries (cap %d)", len(d.Entries), MaxEvalDatasetEntries)
	}

	seenEntryIDs := make(map[string]struct{}, len(d.Entries))
	for i := range d.Entries {
		entry := &d.Entries[i]
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("eval dataset entry %d: %w", i, err)
		}
		if _, dup := seenEntryIDs[entry.ID]; dup {
			return fmt.Errorf("eval dataset duplicate entry id %q", entry.ID)
		}
		seenEntryIDs[entry.ID] = struct{}{}
	}

	size, err := canonicalJSONSize(d)
	if err != nil {
		return fmt.Errorf("eval dataset serialize probe: %w", err)
	}
	// Reserve headroom for store-assigned fields the caller hasn't populated
	// yet: ID (ULID-ish, ~26B), CreatedAt / UpdatedAt (RFC3339, ~30B each),
	// EntryCount (int, ~20B), ContentHash (hex, ~64B), plus the JSON
	// envelope (quotes, commas, keys). 256 bytes gives comfortable slack
	// without materially tightening the usable size for payloads.
	const storeAssignedHeadroomBytes = 256
	if size+storeAssignedHeadroomBytes > MaxEvalDatasetBytes {
		return fmt.Errorf("eval dataset serialized size %d (plus %d bytes store headroom) exceeds cap %d bytes", size, storeAssignedHeadroomBytes, MaxEvalDatasetBytes)
	}

	return nil
}

// Validate enforces the shape of a single entry.
func (e *EvalEntry) Validate() error {
	if e == nil {
		return fmt.Errorf("entry is nil")
	}
	if e.ID == "" {
		return fmt.Errorf("entry id is required")
	}
	if len(e.Input) == 0 {
		return fmt.Errorf("entry input is required")
	}
	if !json.Valid(e.Input) {
		return fmt.Errorf("entry input must be valid JSON")
	}
	if !validExpectedDecision(e.ExpectedDecision) {
		return fmt.Errorf("entry expected_decision %q is not a recognized SafetyDecision", e.ExpectedDecision)
	}
	if e.Source != "" {
		if _, ok := validEvalEntrySources[e.Source]; !ok {
			return fmt.Errorf("entry source %q must be one of manual|audit-import|replay-import", e.Source)
		}
	}
	if len(e.Notes) > MaxEvalEntryNotesBytes {
		return fmt.Errorf("entry notes exceeds %d bytes", MaxEvalEntryNotesBytes)
	}
	if len(e.Metadata) > MaxEvalEntryMetadataKeys {
		return fmt.Errorf("entry metadata has %d keys (cap %d)", len(e.Metadata), MaxEvalEntryMetadataKeys)
	}
	return nil
}

// ComputeContentHash returns the hex-encoded sha256 of the canonical-JSON
// form of the Entries slice. The canonical form sorts object keys and
// strips insignificant whitespace so the same logical content always
// hashes identically, independent of how it was constructed.
//
// The hash is captured at create time on EvalDataset.ContentHash and is
// advisory: downstream consumers can compare it against a re-computed hash
// to spot accidental mutation by operators or by recovery tooling. The
// store does not re-verify it on read because doing so would couple every
// read to an O(N) hash pass over the entries.
func (d *EvalDataset) ComputeContentHash() (string, error) {
	if d == nil {
		return "", fmt.Errorf("eval dataset is nil")
	}
	canonical, err := canonicalJSONEntries(d.Entries)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// CreatedAtMilli parses CreatedAt and returns its unix-milli form, used by
// the store for sorted-set scoring. An empty or malformed timestamp yields
// 0 and an error.
func (d *EvalDataset) CreatedAtMilli() (int64, error) {
	if d == nil || strings.TrimSpace(d.CreatedAt) == "" {
		return 0, fmt.Errorf("eval dataset created_at is empty")
	}
	if t, err := time.Parse(time.RFC3339Nano, d.CreatedAt); err == nil {
		return t.UTC().UnixMilli(), nil
	}
	if t, err := time.Parse(time.RFC3339, d.CreatedAt); err == nil {
		return t.UTC().UnixMilli(), nil
	}
	return 0, fmt.Errorf("eval dataset created_at must be RFC3339")
}

func validExpectedDecision(d SafetyDecision) bool {
	switch d {
	case SafetyAllow,
		SafetyDeny,
		SafetyRequireApproval,
		SafetyThrottle,
		SafetyAllowWithConstraints:
		return true
	}
	return false
}

// canonicalJSONEntries marshals the Entries slice with sorted object keys
// so that logically equivalent content always produces the same bytes.
// encoding/json does not guarantee deterministic map ordering; we therefore
// do a JSON round-trip through canonicalJSON below.
func canonicalJSONEntries(entries []EvalEntry) ([]byte, error) {
	raw, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("marshal entries: %w", err)
	}
	return canonicalJSON(raw)
}

// canonicalJSONSize computes the canonical-JSON byte length of a dataset
// without retaining the full buffer — we only need the size for the cap
// check. Using canonical form here keeps the check independent of random
// map ordering so a dataset with flappy map iteration won't intermittently
// bust the cap.
func canonicalJSONSize(d *EvalDataset) (int, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return 0, err
	}
	canonical, err := canonicalJSON(raw)
	if err != nil {
		return 0, err
	}
	return len(canonical), nil
}

// canonicalJSON deterministically re-encodes a JSON payload: maps become
// JSON objects with keys sorted ascending, arrays keep order, and
// whitespace is removed. The implementation is intentionally minimal — we
// parse into interface{} and re-encode with a sorted writer.
func canonicalJSON(raw []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decode for canonicalization: %w", err)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch val := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(val.String())
	case string:
		encoded, err := json.Marshal(val)
		if err != nil {
			return err
		}
		buf.Write(encoded)
	case []any:
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			encoded, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(encoded)
			buf.WriteByte(':')
			if err := writeCanonical(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		// Fall back to encoding/json for types we did not introduce above.
		encoded, err := json.Marshal(val)
		if err != nil {
			return err
		}
		buf.Write(encoded)
	}
	return nil
}
