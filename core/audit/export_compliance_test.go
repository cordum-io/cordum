package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

func newExportClient(t *testing.T) (redis.UniversalClient, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	client := redis.NewClient(testredis.Options(mr.Addr()))
	return client, func() {
		_ = client.Close()
		mr.Close()
	}
}

func seedChainEvents(t *testing.T, client redis.UniversalClient, tenant string, count int, shape func(i int, ev *SIEMEvent)) []SIEMEvent {
	t.Helper()
	chainer := NewChainer(client, "")
	out := make([]SIEMEvent, 0, count)
	for i := 0; i < count; i++ {
		ev := SIEMEvent{
			Timestamp:   time.Now().UTC(),
			EventType:   EventSafetyDecision,
			Severity:    SeverityInfo,
			TenantID:    tenant,
			AgentID:     fmt.Sprintf("agent-%d", i),
			Action:      fmt.Sprintf("action-%d", i),
			Decision:    "allow",
			MatchedRule: fmt.Sprintf("rule-%d", i),
			Reason:      fmt.Sprintf("reason %d", i),
		}
		if shape != nil {
			shape(i, &ev)
		}
		if err := chainer.Append(context.Background(), &ev); err != nil {
			t.Fatalf("append[%d]: %v", i, err)
		}
		out = append(out, ev)
	}
	return out
}

// (a) JSON flow: manifest first, events with soc2_controls, footer last.
func TestWriteComplianceExport_JSONOrderingAndSOC2(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()

	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	seedChainEvents(t, client, "default", 3, nil)

	var buf bytes.Buffer
	_, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatJSON,
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("WriteComplianceExport: %v", err)
	}

	scanner := bufio.NewScanner(&buf)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	var manifestLine, footerLine map[string]any
	var eventCount int
	idx := 0
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("line %d unmarshal: %v", idx, err)
		}
		switch line["type"] {
		case "manifest":
			if idx != 0 {
				t.Errorf("manifest not first line (idx=%d)", idx)
			}
			manifestLine = line
		case "event":
			eventCount++
			if _, ok := line["soc2_controls"]; !ok {
				t.Errorf("event missing soc2_controls: %v", line)
			}
		case "footer":
			footerLine = line
		default:
			t.Errorf("unexpected type: %v", line["type"])
		}
		idx++
	}
	if manifestLine == nil {
		t.Fatal("missing manifest")
	}
	if eventCount != 3 {
		t.Errorf("events = %d, want 3", eventCount)
	}
	if footerLine == nil {
		t.Fatal("missing footer")
	}
	if fc, _ := footerLine["event_count"].(float64); int(fc) != 3 {
		t.Errorf("footer.event_count = %v, want 3", footerLine["event_count"])
	}
}

// (b) CSV flow: `# cordum-manifest:` comment, RFC-4180 body with
// commas/quotes/newlines in Reason round-trips.
func TestWriteComplianceExport_CSVRFC4180RoundTrip(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()

	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	// Reason with comma + quote + newline — all three RFC-4180 special
	// characters. encoding/csv quotes cells that contain any of them.
	danger := `line1, "value", note
line2`
	seedChainEvents(t, client, "default", 1, func(_ int, ev *SIEMEvent) {
		ev.Reason = danger
	})

	var buf bytes.Buffer
	_, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatCSV,
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("WriteComplianceExport: %v", err)
	}
	body := buf.String()
	firstLine, rest, _ := strings.Cut(body, "\n")
	if !strings.HasPrefix(firstLine, "# cordum-manifest: ") {
		t.Fatalf("missing manifest comment: %q", firstLine)
	}
	rdr := csv.NewReader(strings.NewReader(rest))
	rdr.FieldsPerRecord = -1
	rows, err := rdr.ReadAll()
	if err != nil {
		t.Fatalf("csv round-trip: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected header+1 data row, got %d rows", len(rows))
	}
	// Reason column index 11 (per complianceCSVHeaders).
	if rows[1][11] != danger {
		t.Errorf("reason did not round-trip\n got=%q\nwant=%q", rows[1][11], danger)
	}
}

// (c) CSV injection: each dangerous prefix neutralised with an apostrophe.
func TestWriteComplianceExport_CSVInjectionPrefixes(t *testing.T) {
	prefixes := []string{"=", "+", "-", "@", "\t", "\r"}
	for _, pref := range prefixes {
		client, cleanup := newExportClient(t)
		chainer := NewChainer(client, "")
		streamKey := chainer.StreamKey("default")
		payload := pref + "malicious"
		seedChainEvents(t, client, "default", 1, func(_ int, ev *SIEMEvent) {
			ev.Reason = payload
		})
		var buf bytes.Buffer
		_, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
			TenantID:  "default",
			StreamKey: streamKey,
			Format:    ComplianceExportFormatCSV,
			From:      time.Now().Add(-1 * time.Hour),
			To:        time.Now().Add(1 * time.Hour),
		})
		cleanup()
		if err != nil {
			t.Fatalf("prefix %q: %v", pref, err)
		}
		body := buf.String()
		// encoding/csv quotes the cell because it contains a special char
		// (e.g. tab, or the = that follows an embedded newline). The
		// apostrophe prefix must appear BEFORE the dangerous character,
		// whether or not the cell is quoted.
		needle := "'" + pref
		if !strings.Contains(body, needle) {
			t.Errorf("prefix %q not neutralised:\n%s", pref, body)
		}
	}
}

// (d) Excel toggle prepends UTF-8 BOM to CSV only.
func TestWriteComplianceExport_ExcelBOMToggle(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	seedChainEvents(t, client, "default", 1, nil)

	for _, excel := range []bool{false, true} {
		var buf bytes.Buffer
		_, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
			TenantID:  "default",
			StreamKey: streamKey,
			Format:    ComplianceExportFormatCSV,
			Excel:     excel,
			From:      time.Now().Add(-1 * time.Hour),
			To:        time.Now().Add(1 * time.Hour),
		})
		if err != nil {
			t.Fatalf("excel=%v: %v", excel, err)
		}
		hasBOM := bytes.HasPrefix(buf.Bytes(), []byte("\xef\xbb\xbf"))
		if hasBOM != excel {
			t.Errorf("excel=%v: hasBOM=%v", excel, hasBOM)
		}
	}
}

// (e) MaxEvents cap truncates at the configured limit.
func TestWriteComplianceExport_MaxEventsCap(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	seedChainEvents(t, client, "default", 10, nil)

	var buf bytes.Buffer
	manifest, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatJSON,
		MaxEvents: 4,
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("WriteComplianceExport: %v", err)
	}
	if manifest.EventCount != 4 || !manifest.TruncatedAtMax {
		t.Errorf("manifest cap wrong: %+v", manifest)
	}
}

// (f) Verifier injection: a mock returning status=compromised propagates
// into the manifest's ChainVerification field.
func TestWriteComplianceExport_VerifierInjection(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	seedChainEvents(t, client, "default", 2, nil)

	mockVerifier := func(_ context.Context, _ redis.UniversalClient, _ string, _ VerifyOptions) (*VerifyResult, error) {
		return &VerifyResult{
			Status:      VerifyStatusCompromised,
			TotalEvents: 2,
			Gaps: []VerifyGap{
				{AtSeq: 1, Type: GapTypeHashMismatch},
			},
		}, nil
	}

	var buf bytes.Buffer
	manifest, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatJSON,
		Verifier:  mockVerifier,
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("WriteComplianceExport: %v", err)
	}
	if manifest.ChainVerification == nil || manifest.ChainVerification.Status != VerifyStatusCompromised {
		t.Errorf("verifier result not propagated: %+v", manifest.ChainVerification)
	}
}

// (g) BundleLookup failure surfaces as error; no partial bytes were
// written (we fail BEFORE emitting the manifest line).
func TestWriteComplianceExport_BundleLookupFailureAborts(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	seedChainEvents(t, client, "default", 1, nil)

	sentinel := errors.New("bundle store down")
	var buf bytes.Buffer
	_, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatJSON,
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
		BundleLookup: func(context.Context, string, time.Time, time.Time) ([]SignedBundleSnapshot, error) {
			return nil, sentinel
		},
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected %v, got %v", sentinel, err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no bytes written on early bundle-lookup failure, got %d", buf.Len())
	}
}

// (h) Empty range: still produces a valid manifest + footer with
// event_count=0 and no events.
func TestWriteComplianceExport_EmptyRange(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	// No events seeded.

	var buf bytes.Buffer
	manifest, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatJSON,
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("WriteComplianceExport: %v", err)
	}
	if manifest.EventCount != 0 {
		t.Errorf("event_count = %d, want 0", manifest.EventCount)
	}

	scanner := bufio.NewScanner(&buf)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	// manifest + footer = 2 lines when no events.
	if lines != 2 {
		t.Errorf("lines = %d, want 2 (manifest + footer)", lines)
	}
}

// (bonus) ComplianceExportFormat rejects garbage.
func TestWriteComplianceExport_InvalidFormatRejected(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	var buf bytes.Buffer
	_, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID: "default",
		Format:   "xml",
	})
	if err == nil {
		t.Fatal("expected error on unsupported format")
	}
}

// (bonus) ControlsFor dedup test via full export — verifies an
// event's SOC2 controls end up in the emitted line sorted + unique.
func TestBuildNDJSONEventLine_SOC2SortedAndNonEmpty(t *testing.T) {
	ev := SIEMEvent{
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		TenantID:  "tenant",
		Action:    "act",
		Decision:  "deny",
	}
	line, err := buildNDJSONEventLine(ev, []string{"CC7.2", "CC7.3"})
	if err != nil {
		t.Fatalf("buildNDJSONEventLine: %v", err)
	}
	var parsed map[string]any
	// Drop trailing newline before unmarshaling.
	if err := json.Unmarshal(bytes.TrimRight(line, "\n"), &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	arr, _ := parsed["soc2_controls"].([]any)
	if len(arr) != 2 {
		t.Errorf("soc2_controls = %v, want 2 entries", arr)
	}
	if parsed["type"] != "event" {
		t.Errorf("type = %v, want event", parsed["type"])
	}
}

// ---------------------------------------------------------------------------
// Row filter (event_type / severity / category) — task-7f0f57bf
// ---------------------------------------------------------------------------

// TestWriteComplianceExport_UnfilteredByteIdentical is the DoD-2 regression:
// with NO row filter set, the export is byte-identical to today. Each emitted
// event line must equal a fresh buildNDJSONEventLine recomputation, the
// manifest must carry row_filter_applied:false with no row_filter object, and
// no rows may be dropped.
func TestWriteComplianceExport_UnfilteredByteIdentical(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()

	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	seeded := seedChainEvents(t, client, "default", 3, nil) // all governance (safety.decision)

	var buf bytes.Buffer
	manifest, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatJSON,
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("WriteComplianceExport: %v", err)
	}

	if manifest.RowFilterApplied {
		t.Errorf("unfiltered: RowFilterApplied = true, want false")
	}
	if manifest.RowFilter != nil {
		t.Errorf("unfiltered: RowFilter = %+v, want nil", manifest.RowFilter)
	}
	if manifest.EventCount != 3 {
		t.Errorf("unfiltered: EventCount = %d, want 3 (no rows dropped)", manifest.EventCount)
	}

	// Collect emitted event lines + the raw manifest line.
	var manifestLine string
	var eventLines []string
	scanner := bufio.NewScanner(&buf)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	idx := 0
	for scanner.Scan() {
		text := scanner.Text()
		var line map[string]any
		if err := json.Unmarshal([]byte(text), &line); err != nil {
			t.Fatalf("line %d unmarshal: %v", idx, err)
		}
		switch line["type"] {
		case "manifest":
			manifestLine = text
		case "event":
			eventLines = append(eventLines, text)
		}
		idx++
	}

	// Manifest: additive field present-and-false; row_filter object omitted.
	if !strings.Contains(manifestLine, `"row_filter_applied":false`) {
		t.Errorf("manifest missing row_filter_applied:false:\n%s", manifestLine)
	}
	if strings.Contains(manifestLine, `"row_filter":`) {
		t.Errorf("manifest unexpectedly contains a row_filter object:\n%s", manifestLine)
	}

	// Byte-identical: each event line equals a fresh recomputation.
	if len(eventLines) != len(seeded) {
		t.Fatalf("emitted %d event lines, want %d", len(eventLines), len(seeded))
	}
	for i, ev := range seeded {
		want, berr := buildNDJSONEventLine(ev, DefaultSOC2Mapping().ControlsFor(ev))
		if berr != nil {
			t.Fatalf("recompute line %d: %v", i, berr)
		}
		wantLine := strings.TrimRight(string(want), "\n")
		if eventLines[i] != wantLine {
			t.Errorf("event line %d not byte-identical:\n got=%s\nwant=%s", i, eventLines[i], wantLine)
		}
	}
}

// TestWriteComplianceExport_CSVColumnsUnchanged guards the export-format
// stability rail: the unfiltered CSV header row must still equal
// complianceCSVHeaders verbatim (no reorder/rename).
func TestWriteComplianceExport_CSVColumnsUnchanged(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	seedChainEvents(t, client, "default", 2, nil)

	var buf bytes.Buffer
	if _, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatCSV,
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("WriteComplianceExport: %v", err)
	}
	_, rest, _ := strings.Cut(buf.String(), "\n") // drop manifest comment
	rdr := csv.NewReader(strings.NewReader(rest))
	rows, err := rdr.ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no CSV rows")
	}
	if !reflectStringSliceEqual(rows[0], complianceCSVHeaders) {
		t.Errorf("CSV header drifted:\n got=%v\nwant=%v", rows[0], complianceCSVHeaders)
	}
}

// TestWriteComplianceExport_CategoryGovernanceFilter is the DoD-6 / DoD-1 E2E:
// category=governance emits ONLY governance rows, the manifest records the
// filter, event_count is the post-filter count, and — critically — chain
// verification still covers the FULL range (all seeded rows), proving the
// filter never reached the verifier.
func TestWriteComplianceExport_CategoryGovernanceFilter(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	// 5 seeded: i=0,2,4 routine (system.auth); i=1,3 governance (safety.decision).
	seedChainEvents(t, client, "default", 5, func(i int, ev *SIEMEvent) {
		if i%2 == 0 {
			ev.EventType = EventSystemAuth
		} else {
			ev.EventType = EventSafetyDecision
		}
	})

	var filteredBuf, fullBuf bytes.Buffer
	opts := ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatJSON,
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
	}
	// Filtered (category=governance) and unfiltered exports over the SAME data.
	filteredOpts := opts
	filteredOpts.Category = CategoryGovernance
	filtered, err := WriteComplianceExport(context.Background(), &filteredBuf, client, filteredOpts)
	if err != nil {
		t.Fatalf("filtered export: %v", err)
	}
	full, err := WriteComplianceExport(context.Background(), &fullBuf, client, opts)
	if err != nil {
		t.Fatalf("unfiltered export: %v", err)
	}

	// Only governance rows emitted under the filter; all rows unfiltered.
	emitted := collectExportedEventTypes(t, &filteredBuf)
	if len(emitted) != 2 {
		t.Errorf("filtered emitted %d events, want 2 governance", len(emitted))
	}
	for _, et := range emitted {
		if CategoryFor(et) != CategoryGovernance {
			t.Errorf("filtered emitted non-governance event_type %q", et)
		}
	}
	if got := len(collectExportedEventTypes(t, &fullBuf)); got != 5 {
		t.Errorf("unfiltered emitted %d events, want 5", got)
	}

	// Manifest records the filter; event_count is the POST-filter count.
	if !filtered.RowFilterApplied {
		t.Error("filtered RowFilterApplied = false, want true")
	}
	if filtered.RowFilter == nil || filtered.RowFilter.Category != CategoryGovernance {
		t.Errorf("filtered RowFilter = %+v, want Category=governance", filtered.RowFilter)
	}
	if filtered.EventCount != 2 {
		t.Errorf("filtered EventCount = %d, want 2 (post-filter)", filtered.EventCount)
	}
	if full.EventCount != 5 {
		t.Errorf("unfiltered EventCount = %d, want 5", full.EventCount)
	}

	// CRITICAL invariant: the row filter has ZERO effect on chain
	// verification. The filtered export's ChainVerification must be identical
	// to the unfiltered one — same total/verified/status over the FULL range
	// (5), never the filtered subset (2). This is what makes a filtered export
	// distinguishable from a tamper gap.
	fv, uv := filtered.ChainVerification, full.ChainVerification
	if fv == nil || uv == nil {
		t.Fatalf("ChainVerification nil (filtered=%v unfiltered=%v)", fv, uv)
	}
	if fv.TotalEvents != 5 {
		t.Errorf("filtered ChainVerification.TotalEvents = %d, want 5 (full range)", fv.TotalEvents)
	}
	if fv.TotalEvents != uv.TotalEvents || fv.VerifiedEvents != uv.VerifiedEvents || fv.Status != uv.Status {
		t.Errorf("filter changed verification: filtered{total=%d verified=%d status=%q} vs unfiltered{total=%d verified=%d status=%q}",
			fv.TotalEvents, fv.VerifiedEvents, fv.Status, uv.TotalEvents, uv.VerifiedEvents, uv.Status)
	}
	if fv.Status == VerifyStatusCompromised {
		t.Errorf("filtered export reported compromised chain — filter leaked into verifier")
	}
}

// TestWriteComplianceExport_EventTypeFilter exercises the event_type filter
// (case-insensitive) and its manifest record.
func TestWriteComplianceExport_EventTypeFilter(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	seedChainEvents(t, client, "default", 4, func(i int, ev *SIEMEvent) {
		if i < 2 {
			ev.EventType = EventSafetyDecision
		} else {
			ev.EventType = EventSystemAuth
		}
	})

	var buf bytes.Buffer
	manifest, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatJSON,
		EventType: "SAFETY.DECISION", // upper-case: eq-fold must still match
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("WriteComplianceExport: %v", err)
	}
	emitted := collectExportedEventTypes(t, &buf)
	if len(emitted) != 2 {
		t.Errorf("emitted %d, want 2 safety.decision", len(emitted))
	}
	for _, et := range emitted {
		if !strings.EqualFold(et, EventSafetyDecision) {
			t.Errorf("emitted %q, want safety.decision", et)
		}
	}
	if manifest.RowFilter == nil || manifest.RowFilter.EventType != "SAFETY.DECISION" {
		t.Errorf("manifest.RowFilter = %+v, want EventType=SAFETY.DECISION", manifest.RowFilter)
	}
	if manifest.EventCount != 2 {
		t.Errorf("EventCount = %d, want 2", manifest.EventCount)
	}
}

// TestWriteComplianceExport_SeverityFilter exercises the severity filter
// (case-insensitive).
func TestWriteComplianceExport_SeverityFilter(t *testing.T) {
	client, cleanup := newExportClient(t)
	defer cleanup()
	chainer := NewChainer(client, "")
	streamKey := chainer.StreamKey("default")
	seedChainEvents(t, client, "default", 4, func(i int, ev *SIEMEvent) {
		if i < 3 {
			ev.Severity = SeverityHigh
		} else {
			ev.Severity = SeverityInfo
		}
	})

	var buf bytes.Buffer
	manifest, err := WriteComplianceExport(context.Background(), &buf, client, ComplianceExportOptions{
		TenantID:  "default",
		StreamKey: streamKey,
		Format:    ComplianceExportFormatJSON,
		Severity:  "high", // lower-case: eq-fold must match HIGH
		From:      time.Now().Add(-1 * time.Hour),
		To:        time.Now().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("WriteComplianceExport: %v", err)
	}
	if manifest.EventCount != 3 {
		t.Errorf("EventCount = %d, want 3 HIGH-severity", manifest.EventCount)
	}
	if manifest.RowFilter == nil || manifest.RowFilter.Severity != "high" {
		t.Errorf("manifest.RowFilter = %+v, want Severity=high", manifest.RowFilter)
	}
}

// collectExportedEventTypes drains an NDJSON export buffer and returns the
// event_type of every type=="event" line.
func collectExportedEventTypes(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var out []string
	scanner := bufio.NewScanner(buf)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if line["type"] == "event" {
			et, _ := line["event_type"].(string)
			out = append(out, et)
		}
	}
	return out
}

func reflectStringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
