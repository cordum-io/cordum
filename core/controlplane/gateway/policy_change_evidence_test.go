package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/stretchr/testify/require"
)

func TestPolicyAuditStoreThenExport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		export bool
		fail   bool
	}{
		{name: "store"},
		{name: "append", export: true},
		{name: "store_failure", fail: true},
		{name: "append_failure", export: true, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policyAuditStoreThenExportCheck(t, tc.export, tc.fail)
		})
	}
}

type policyChangeEvidenceCase struct {
	name, action, method, path string
	handle                     func(*server, http.ResponseWriter, *http.Request)
}

func TestPolicyAuditFailureLeavesPolicyUnchanged(t *testing.T) {
	for _, tc := range []policyChangeEvidenceCase{
		{"publish", "publish", http.MethodPost, "/api/v1/policy/publish", (*server).handlePublishPolicyBundles},
		{"rollback", "rollback", http.MethodPost, "/api/v1/policy/rollback", (*server).handleRollbackPolicyBundles},
		{"bundle_put", "edit", http.MethodPut, "/api/v1/policy/bundles/secops/test", (*server).handlePutPolicyBundle},
		{"bundle_delete", "delete", http.MethodDelete, "/api/v1/policy/bundles/secops/test", (*server).handleDeletePolicyBundle},
		{"global_put", "edit", http.MethodPut, "/api/v1/policy/global", (*server).handlePutPolicyGlobal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("record_unavailable", func(t *testing.T) { policyChangeEvidenceCheck(t, tc, true) })
			t.Run("record_written", func(t *testing.T) { policyChangeEvidenceCheck(t, tc, false) })
		})
	}
}

func policyChangeEvidenceCheck(t *testing.T, tc policyChangeEvidenceCase, fail bool) {
	t.Helper()
	s, mr, hooked, _ := auditPersistServer(t)
	body := policyChangeEvidenceSeed(t, s, tc.name)
	before := auditPersistRaw(t, mr, auditPersistPolicyKey)
	identity := policyAuditPublishIdentity(t, s)
	actions := auditPersistActions(t, s)
	sink := &recordingAuditSender{}
	s.auditExporter = sink
	if fail {
		hooked.AddHook(auditPersistHook{failKey: auditPersistWriteKey})
		hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	}
	req := policyAuditPublishRequest(tc.method, tc.path, []byte(body))
	req.SetPathValue("id", "secops/test")
	rec := httptest.NewRecorder()
	tc.handle(s, rec, req)
	after := auditPersistRaw(t, mr, auditPersistPolicyKey)
	afterIdentity := policyAuditPublishIdentity(t, s)
	afterActions := auditPersistActions(t, s)
	if fail {
		require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
		require.Equal(t, before, after)
		require.Equal(t, identity, afterIdentity)
		require.Equal(t, actions, afterActions)
		require.Empty(t, sink.events)
		return
	}
	require.GreaterOrEqual(t, rec.Code, 200, rec.Body.String())
	require.Less(t, rec.Code, 300, rec.Body.String())
	require.NotEqual(t, before, after)
	require.NotEqual(t, identity, afterIdentity)
	require.ElementsMatch(t, append(actions, tc.action), afterActions)
	require.Len(t, sink.events, 1)
	require.Equal(t, tc.action, sink.events[0].Action)
}

func policyChangeEvidenceSeed(t *testing.T, s *server, operation string) string {
	t.Helper()
	policyChangeEvidencePut(t, s, policyContent, operation != "publish")
	changed := strings.ReplaceAll(policyContent, "allow", "deny")
	switch operation {
	case "rollback":
		bundles, _, err := s.loadPolicyBundlesWithDoc(context.Background())
		require.NoError(t, err)
		snapshot, err := buildPolicyBundleSnapshot(bundles, "evidence rollback target")
		require.NoError(t, err)
		require.NoError(t, s.savePolicySnapshots(context.Background(), []policybundles.PolicyBundleSnapshot{snapshot}, nil))
		policyChangeEvidencePut(t, s, changed, true)
		return policyWriteJSON(t, policybundles.PolicyRollbackRequest{SnapshotID: snapshot.ID})
	case "bundle_put":
		return policyWriteJSON(t, policybundles.PolicyBundleUpsertRequest{Content: changed})
	case "global_put":
		return policyWriteJSON(t, globalPolicyPutRequest{Sections: map[string]globalPolicyPutRequestSection{
			globalSectionInputRules: {Content: changed},
		}})
	default:
		return `{}`
	}
}

func policyChangeEvidencePut(t *testing.T, s *server, content string, enabled bool) {
	t.Helper()
	body := policyWriteJSON(t, policybundles.PolicyBundleUpsertRequest{Content: content, Enabled: &enabled})
	req := policyAuditPublishRequest(http.MethodPut, "/api/v1/policy/bundles/secops/test", []byte(body))
	req.SetPathValue("id", "secops/test")
	rec := httptest.NewRecorder()
	s.handlePutPolicyBundle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func policyAuditStoreThenExportCheck(t *testing.T, export, fail bool) {
	t.Helper()
	s, mr, hooked, _ := auditPersistServer(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink
	if fail {
		hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	}
	entry := auditPersistEntry("policy.evidence")
	var stored policybundles.PolicyAuditEntry
	var err error
	if export {
		err = s.appendPolicyAudit(context.Background(), entry)
	} else {
		stored, err = s.storePolicyAudit(context.Background(), entry)
	}
	if fail {
		require.Error(t, err)
		require.False(t, mr.Exists(auditPersistLogKey))
		require.Empty(t, auditPersistActions(t, s))
		require.Empty(t, sink.events)
		return
	}
	require.NoError(t, err)
	require.Equal(t, []string{entry.Action}, auditPersistActions(t, s))
	entries, err := s.loadPolicyAudit(context.Background())
	require.NoError(t, err)
	if export {
		require.Len(t, sink.events, 1)
		require.Equal(t, policybundles.AuditEntryToSIEM(entries[0], s.tenant), sink.events[0])
	} else {
		require.Equal(t, entries[0], stored)
		require.NotEmpty(t, stored.ID)
		require.NotEmpty(t, stored.CreatedAt)
		require.Empty(t, sink.events)
	}
}
