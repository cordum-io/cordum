package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge/shadow"
)

func TestShadowAgents_CreateSanitizesCallerMetadata(t *testing.T) {
	s := newShadowGateway(t)
	body := validShadowCreateBody("tenant-a")
	secretLike := "cordum_fake_" + "sk-" + "cordumtest2026gateway0123"
	githubLike := "cordum_fake_" + "ghp_" + "cordumtest2026gateway0123"
	bearerLike := "Authorization: " + "Bearer " + "cordum_fake_gateway_token_0123456789"
	body.Metadata = map[string]string{
		"source":        "github_actions",
		"cluster_id":    "cluster-a",
		"namespace":     "payments",
		"audit_id":      "audit-123",
		"owner_label":   "platform",
		"notes":         "caller saw " + secretLike,
		"token":         secretLike,
		"Secret":        githubLike,
		"authorization": bearerLike,
	}

	rec := postShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created shadow.ShadowAgentFinding
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	assertGatewayMetadataSanitized(t, created.Metadata, secretLike, githubLike, bearerLike)

	getRec := getShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents/"+created.FindingID)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", getRec.Code, getRec.Body.String())
	}
	var loaded shadow.ShadowAgentFinding
	if err := json.Unmarshal(getRec.Body.Bytes(), &loaded); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	assertGatewayMetadataSanitized(t, loaded.Metadata, secretLike, githubLike, bearerLike)
}

func assertGatewayMetadataSanitized(t *testing.T, got map[string]string, forbidden ...string) {
	t.Helper()
	for key, want := range map[string]string{
		"source":      "github_actions",
		"cluster_id":  "cluster-a",
		"namespace":   "payments",
		"audit_id":    "audit-123",
		"owner_label": "platform",
	} {
		if got[key] != want {
			t.Fatalf("metadata[%q] = %q, want %q in %#v", key, got[key], want, got)
		}
	}
	for _, key := range []string{"token", "Secret", "authorization"} {
		if _, ok := got[key]; ok {
			t.Fatalf("metadata retained sensitive key %q in %#v", key, got)
		}
	}
	joined := strings.Join(gatewayMetadataValues(got), "\n")
	for _, value := range forbidden {
		if strings.Contains(joined, value) {
			t.Fatalf("metadata leaked forbidden value %q in %#v", value, got)
		}
	}
	if !strings.Contains(got["notes"], "<REDACTED>") {
		t.Fatalf("metadata notes = %q, want redaction marker", got["notes"])
	}
}

func gatewayMetadataValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, value := range m {
		out = append(out, value)
	}
	return out
}
