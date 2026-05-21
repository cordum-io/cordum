package shadow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCreateFinding_SanitizesMetadataBeforeRedisPersistence(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateReq(
		"tenant-a",
		"owner-1",
		"principal-1",
		"claude-code",
		FindingRiskHigh,
		"config_file",
		"metadata redaction regression",
	)
	metadata, forbidden := shadowMetadataRedactionFixture("metadata")
	req.Metadata = metadata

	created, err := s.CreateFinding(ctx, req)
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	assertSafeMetadataRetained(t, created.Metadata)
	assertUnsafeMetadataSanitized(t, created.Metadata, forbidden...)

	loaded, err := s.GetFinding(ctx, "tenant-a", created.FindingID)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	assertSafeMetadataRetained(t, loaded.Metadata)
	assertUnsafeMetadataSanitized(t, loaded.Metadata, forbidden...)

	raw, err := s.client.Get(ctx, findingKey(created.FindingID)).Result()
	if err != nil {
		t.Fatalf("Get raw Redis finding: %v", err)
	}
	assertRawRedisFindingOmits(t, raw, forbidden)
}

func TestCreateFinding_SafeMetadataSurvivesFilteredList(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateReq("tenant-a", "owner-1", "principal-1", "claude-code", FindingRiskHigh, "config_file", "safe metadata")
	req.SourceType = SourceTypeKubernetes
	req.ClusterID = "cluster-a"
	req.Namespace = "payments"
	req.Metadata = map[string]string{
		"source":      "github_actions",
		"cluster_id":  "cluster-a",
		"namespace":   "payments",
		"audit_id":    "audit-123",
		"owner_label": "platform",
	}

	created, err := s.CreateFinding(ctx, req)
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	assertSafeMetadataRetained(t, created.Metadata)

	page, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-a", ClusterID: "cluster-a", Namespace: "payments"})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(page.Findings) != 1 || page.Findings[0].FindingID != created.FindingID {
		t.Fatalf("filtered list = %+v, want created finding %q", page.Findings, created.FindingID)
	}
	assertSafeMetadataRetained(t, page.Findings[0].Metadata)
}

func TestCreateFinding_MetadataValueLimitAppliesAfterRedaction(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	longSecret := "cordum_fake_" + "sk-" + strings.Repeat("a", MaxMetadataValueBytes+64)
	req := minimalCreateReq("tenant-a", "owner-1", "principal-1", "claude-code", FindingRiskHigh, "config_file", "metadata limit")
	req.Metadata = map[string]string{"notes": longSecret}
	if got, err := s.CreateFinding(ctx, req); err != nil {
		t.Fatalf("CreateFinding redacted long secret: %v", err)
	} else if strings.Contains(got.Metadata["notes"], longSecret) || len(got.Metadata["notes"]) > MaxMetadataValueBytes {
		t.Fatalf("metadata notes not redacted within cap: %#v", got.Metadata)
	}

	oversizedSafe := req
	oversizedSafe.FindingID = "oversized-safe-metadata"
	oversizedSafe.Metadata = map[string]string{"notes": strings.Repeat("a", MaxMetadataValueBytes+1)}
	if _, err := s.CreateFinding(ctx, oversizedSafe); !errors.Is(err, ErrValidation) {
		t.Fatalf("CreateFinding oversized safe metadata = %v, want ErrValidation", err)
	}
}

func shadowMetadataRedactionFixture(tag string) (map[string]string, []string) {
	secretLike := "cordum_fake_" + "sk-" + "cordumtest2026" + tag + "0123"
	githubLike := "cordum_fake_" + "ghp_" + "cordumtest2026" + tag + "0123"
	bearerLike := "Authorization: " + "Bearer " + "cordum_fake_" + tag + "_token_0123456789"
	privateMarker := "-----BEGIN CORDUM TEST " + "PRIVATE KEY-----"
	metadata := map[string]string{
		"source":        "github_actions",
		"cluster_id":    "cluster-a",
		"namespace":     "payments",
		"audit_id":      "audit-123",
		"owner_label":   "platform",
		"notes":         tag + " saw " + secretLike,
		"token":         secretLike,
		"Secret":        githubLike,
		"authorization": bearerLike,
		"api_key":       privateMarker,
	}
	return metadata, []string{secretLike, githubLike, bearerLike, privateMarker}
}

func assertSafeMetadataRetained(t *testing.T, got map[string]string) {
	t.Helper()
	want := map[string]string{
		"source":      "github_actions",
		"cluster_id":  "cluster-a",
		"namespace":   "payments",
		"audit_id":    "audit-123",
		"owner_label": "platform",
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("metadata[%q] = %q, want %q in %#v", key, got[key], value, got)
		}
	}
}

func assertUnsafeMetadataSanitized(t *testing.T, got map[string]string, forbidden ...string) {
	t.Helper()
	for _, key := range []string{"token", "Secret", "authorization", "api_key"} {
		if _, ok := got[key]; ok {
			t.Fatalf("metadata retained sensitive key %q in %#v", key, got)
		}
	}
	joined := strings.Join(metadataValues(got), "\n")
	for _, value := range forbidden {
		if strings.Contains(joined, value) {
			t.Fatalf("metadata leaked forbidden value %q in %#v", value, got)
		}
	}
	if !strings.Contains(got["notes"], "<REDACTED>") {
		t.Fatalf("metadata notes = %q, want redaction marker", got["notes"])
	}
}

func assertRawRedisFindingOmits(t *testing.T, raw string, forbidden []string) {
	t.Helper()
	for _, value := range append(forbidden, `"token"`, `"Secret"`, `"authorization"`, `"api_key"`) {
		if strings.Contains(raw, value) {
			t.Fatalf("raw Redis finding leaked %q: %s", value, raw)
		}
	}
}

func metadataValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, value := range m {
		out = append(out, value)
	}
	return out
}
