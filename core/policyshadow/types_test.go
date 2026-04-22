package policyshadow

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewShadowBundleID_FormatAndUniqueness(t *testing.T) {
	t.Parallel()

	const runs = 256
	seen := make(map[string]struct{}, runs)
	for i := 0; i < runs; i++ {
		id := NewShadowBundleID()
		if !strings.HasPrefix(id, ShadowBundleIDPrefix) {
			t.Fatalf("id %q missing prefix %q", id, ShadowBundleIDPrefix)
		}
		suffix := strings.TrimPrefix(id, ShadowBundleIDPrefix)
		if len(suffix) != shadowBundleIDHexLen {
			t.Fatalf("id %q suffix len = %d, want %d", id, len(suffix), shadowBundleIDHexLen)
		}
		for _, r := range suffix {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				t.Fatalf("id %q has non-hex char %q", id, r)
			}
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id generated: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestShadowPolicy_SummaryDropsContent(t *testing.T) {
	t.Parallel()

	sp := &ShadowPolicy{
		ShadowBundleID: "shadow-abcdef012345",
		BundleID:       "foo~default",
		TenantID:       "tenant-a",
		Content:        "version: 1\nrules: []",
		CreatedAt:      time.Unix(1700000000, 0).UTC(),
		ActivatedAt:    time.Unix(1700000000, 0).UTC(),
		CreatedBy:      "op@cordum.io",
		Metadata:       map[string]string{"ticket": "SEC-42"},
	}
	summary := sp.Summary()
	if summary == nil {
		t.Fatal("Summary returned nil")
	}
	if summary.ShadowBundleID != sp.ShadowBundleID ||
		summary.BundleID != sp.BundleID ||
		summary.TenantID != sp.TenantID ||
		summary.CreatedBy != sp.CreatedBy ||
		!summary.CreatedAt.Equal(sp.CreatedAt) ||
		!summary.ActivatedAt.Equal(sp.ActivatedAt) {
		t.Fatalf("summary fields mismatch: %+v vs source %+v", summary, sp)
	}

	// Summary JSON must not contain the Content string even if the source
	// carries it — the type has no Content field.
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "version: 1") {
		t.Fatalf("summary JSON leaks content: %s", raw)
	}
}

func TestShadowPolicy_NilSummaryIsNil(t *testing.T) {
	t.Parallel()

	var sp *ShadowPolicy
	if got := sp.Summary(); got != nil {
		t.Fatalf("nil ShadowPolicy.Summary() = %+v, want nil", got)
	}
}

func TestShadowPolicyJSONRoundTrip(t *testing.T) {
	t.Parallel()

	want := ShadowPolicy{
		ShadowBundleID: "shadow-0123456789ab",
		BundleID:       "demo~default",
		TenantID:       "tenant-a",
		Content:        "version: 1",
		CreatedAt:      time.Unix(1700000000, 0).UTC(),
		ActivatedAt:    time.Unix(1700000100, 0).UTC(),
		CreatedBy:      "alice",
		Metadata:       map[string]string{"experiment": "e1"},
	}
	raw, err := json.Marshal(&want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ShadowPolicy
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ShadowBundleID != want.ShadowBundleID ||
		got.BundleID != want.BundleID ||
		got.TenantID != want.TenantID ||
		got.Content != want.Content ||
		!got.CreatedAt.Equal(want.CreatedAt) ||
		!got.ActivatedAt.Equal(want.ActivatedAt) ||
		got.CreatedBy != want.CreatedBy ||
		got.Metadata["experiment"] != want.Metadata["experiment"] {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}
