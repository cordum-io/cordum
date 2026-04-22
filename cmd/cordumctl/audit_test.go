package main

import (
	"bytes"
	"strings"
	"testing"

	sdk "github.com/cordum/cordum/sdk/client"
)

// TestRenderAuditVerifyTable_Intact pins the human output for the
// everything-ok path. CI dashboards grep for "none" under gaps so
// that column is load-bearing.
func TestRenderAuditVerifyTable_Intact(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	renderAuditVerifyTable(&buf, &sdk.AuditVerifyResult{
		Status:               "ok",
		TotalEvents:          42,
		VerifiedEvents:       42,
		FirstSeq:             1,
		LastSeq:              42,
		RetentionBoundarySeq: 1,
		RetentionWindowHours: 168,
	}, "default")

	out := buf.String()
	for _, want := range []string{
		"tenant default",
		"status:                 ok",
		"events checked:         42",
		"events verified:        42",
		"seq range observed:     1..42",
		"retention boundary:     seq 1",
		"retention window:       168.0 hours",
		"gaps:                   none",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

// TestRenderAuditVerifyTable_SeparatesTrimmedFromTampering verifies the
// renderer splits retention_trimmed from missing/hash_mismatch so
// operators see "it's retention, not an incident" or the opposite at
// a glance.
func TestRenderAuditVerifyTable_SeparatesTrimmedFromTampering(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	renderAuditVerifyTable(&buf, &sdk.AuditVerifyResult{
		Status:               "compromised",
		TotalEvents:          5,
		VerifiedEvents:       4,
		FirstSeq:             4,
		LastSeq:              8,
		RetentionBoundarySeq: 4,
		Gaps: []sdk.AuditVerifyGap{
			{AtSeq: 1, Type: "retention_trimmed"},
			{AtSeq: 2, Type: "retention_trimmed"},
			{AtSeq: 3, Type: "retention_trimmed"},
			{AtSeq: 6, Type: "hash_mismatch"},
			{AtSeq: 7, Type: "missing"},
		},
	}, "")

	out := buf.String()
	// Empty tenant renders as (default).
	if !strings.Contains(out, "tenant (default)") {
		t.Errorf("empty tenant not rendered as (default):\n%s", out)
	}
	for _, want := range []string{
		"retention_trimmed:    3",
		"hash_mismatch:        1",
		"missing (tampering):  1",
		// Seq lists appear so operators can jump straight to the bad seqs.
		"[1, 2, 3]",
		"[6]",
		"[7]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

// TestFormatGapSeqs_TruncatesLongLists ensures a big retention-trimmed
// prefix does not flood the CLI.
func TestFormatGapSeqs_TruncatesLongLists(t *testing.T) {
	t.Parallel()
	gaps := make([]sdk.AuditVerifyGap, 0, 25)
	for i := int64(1); i <= 25; i++ {
		gaps = append(gaps, sdk.AuditVerifyGap{AtSeq: i, Type: "retention_trimmed"})
	}
	got := formatGapSeqs(gaps)
	if !strings.HasPrefix(got, "[1, 2, 3, 4, 5, 6, 7, 8, 9, 10]") {
		t.Errorf("head of list wrong: %q", got)
	}
	if !strings.Contains(got, "(+15 more)") {
		t.Errorf("truncation tail missing: %q", got)
	}
}
