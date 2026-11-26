package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/policysign"
	"github.com/stretchr/testify/require"
)

// outputRuleSlippedIn is a rule appended to signed content after signing.
const outputRuleSlippedIn = `  - id: out-slipped-in
    enabled: true
    description: "Slipped in after signing"
    severity: medium
    decision: redact
    reason: "pii found"
    match:
      detectors:
        - pii
`

// outputRuleUnsignedBundle sorts before storeWriterOutputBundle, so the
// toggle reaches it first.
const outputRuleUnsignedBundle = "storewriter/a-unsigned"

// outputRuleTampered is a bundle signed by the configured key whose content
// gained a rule after signing.
func outputRuleTampered(t *testing.T) map[string]any {
	t.Helper()
	bundle := storeWriterSigned(t, outputPolicyContent, true)
	bundle["content"] = outputPolicyContent + outputRuleSlippedIn
	return bundle
}

// outputRuleRefused asserts the toggle refused bundleID as unverified and
// named the re-sign remedy.
func outputRuleRefused(t *testing.T, rec *httptest.ResponseRecorder, bundleID string) {
	t.Helper()
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, errorCodePolicySigningRequired, body["code"])
	require.Contains(t, body["error"], "cordumctl policy resign --bundle "+bundleID)
}

func TestOutputRuleToggle_EnforceRefusesTamperedSignedBundle(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, _, _ := newTestGateway(t)
	storeWriterSeed(t, s, map[string]any{storeWriterOutputBundle: outputRuleTampered(t)})
	require.Error(t, storeWriterVerify(t, storeWriterBundle(t, s, storeWriterOutputBundle)))
	before := storeWriterKeyspace(t, s)

	outputRuleRefused(t, storeWriterToggle(t, s), storeWriterOutputBundle)

	require.Equal(t, before, storeWriterKeyspace(t, s))
}

func TestOutputRuleToggle_EnforceRefusesUnsignedBundle(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, _, _ := newTestGateway(t)
	storeWriterSeed(t, s, map[string]any{
		outputRuleUnsignedBundle: map[string]any{"content": outputPolicyContent + outputRuleSlippedIn, "enabled": true},
		storeWriterOutputBundle:  storeWriterSigned(t, outputPolicyContent, true),
	})
	require.NoError(t, storeWriterVerify(t, storeWriterBundle(t, s, storeWriterOutputBundle)))
	before := storeWriterKeyspace(t, s)

	outputRuleRefused(t, storeWriterToggle(t, s), outputRuleUnsignedBundle)

	require.Equal(t, before, storeWriterKeyspace(t, s))
}

func TestOutputRuleToggle_WarnStoresUnverifiedBundleUnsigned(t *testing.T) {
	seeds := map[string]func(*testing.T) map[string]any{
		"unsigned": func(*testing.T) map[string]any {
			return map[string]any{"content": outputPolicyContent, "enabled": true}
		},
		"tampered": outputRuleTampered,
	}
	for name, seed := range seeds {
		t.Run(name, func(t *testing.T) {
			storeWriterEnv(t, true, "warn")
			storeWriterKey(t, "trusted")
			logs := storeWriterLogs(t)
			s, _, _ := newTestGateway(t)
			storeWriterSeed(t, s, map[string]any{storeWriterOutputBundle: seed(t)})

			rec := storeWriterToggle(t, s)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			stored := storeWriterBundle(t, s, storeWriterOutputBundle)
			require.True(t, storeWriterOutputEnabled(t, stored))
			require.NotContains(t, stored, policyBundleSignatureKey)
			require.NotEmpty(t, logs.records("WARN", "bundle_id", storeWriterOutputBundle))
		})
	}
}

func TestOutputRuleToggle_EnforceResignAuditsKeyAndHash(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, _, _ := newTestGateway(t)
	storeWriterSeedOutput(t, s)

	rec := storeWriterToggle(t, s)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	stored := storeWriterBundle(t, s, storeWriterOutputBundle)
	require.NoError(t, storeWriterVerify(t, stored))
	content, _ := stored["content"].(string)
	entries, err := s.loadPolicyAudit(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, storeWriterOutputBundle, entries[0].ResourceName)
	require.Equal(t, fmt.Sprintf("set output rule out-secret enabled=true in policy bundle %s; re-signed with key_id=%s hash=%s",
		storeWriterOutputBundle, storeWriterKeyID, policysign.HashPayload([]byte(content))), entries[0].Message)
}
