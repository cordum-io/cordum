package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const (
	packAuditDefaultKey  = "cfg:system:default"
	packAuditRegistryKey = "cfg:system:packs"
)

// packAuditServer returns auditPersistServer's gateway with system/default
// seeded for the test pack and system/policy holding one other bundle.
func packAuditServer(t *testing.T) (*server, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	s, mr, hooked, plain := auditPersistServer(t)
	seedTestPackConfig(t, s)
	auditPersistSeedPolicy(t, mr, plain)
	return s, mr, hooked
}

func packUninstall(s *server) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/packs/test-pack/uninstall", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", "test-pack")
	rr := httptest.NewRecorder()
	s.handleUninstallPack(rr, req)
	return rr
}

// packPolicyEntries returns the audit API's policy entries about the test pack.
func packPolicyEntries(t *testing.T, s *server) []policybundles.PolicyAuditEntry {
	t.Helper()
	var out []policybundles.PolicyAuditEntry
	for _, entry := range policyWriteAuditItems(t, s) {
		if entry.ResourceType == "policy" && entry.ResourceID == "test-pack" {
			out = append(out, entry)
		}
	}
	return out
}

func packPolicyBundlesNow(t *testing.T, s *server) any {
	t.Helper()
	doc, err := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, packs.PolicyConfigID)
	require.NoError(t, err)
	return doc.Data[packs.PolicyConfigKey]
}

func TestPackInstall_PolicyCommitFailureChangesNothing(t *testing.T) {
	s, mr, hooked := packAuditServer(t)
	before := auditPersistKeyspace(t, mr)
	hooked.AddHook(auditPersistHook{failKey: auditPersistWriteKey})

	rr := postTestPack(t, s)
	require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
	after := auditPersistKeyspace(t, mr)
	// The informational verification entry is stored before any change, so
	// the shared audit log gains exactly that entry.
	require.Equal(t, []string{PackVerificationEventVerified}, auditPersistActions(t, s))
	delete(before, auditPersistLogKey)
	delete(after, auditPersistLogKey)
	require.Equal(t, before, after, "an install whose policy commit failed changed stored documents")
}

func TestPackUninstall_PolicyCommitFailureChangesNothing(t *testing.T) {
	s, mr, hooked := packAuditServer(t)
	rr := postTestPack(t, s)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	before := auditPersistKeyspace(t, mr)
	hooked.AddHook(auditPersistHook{failKey: auditPersistWriteKey})

	rr = packUninstall(s)
	require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
	require.Equal(t, before, auditPersistKeyspace(t, mr), "an uninstall whose policy commit failed changed stored documents")
}

func TestPackInstall_RollbackRestoresPolicyWithItsEntry(t *testing.T) {
	s, mr, hooked := packAuditServer(t)
	before := auditPersistKeyspace(t, mr)
	bundles := packPolicyBundlesNow(t, s)
	hooked.AddHook(auditPersistHook{failKey: packAuditDefaultKey})

	rr := postTestPack(t, s)
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	after := auditPersistKeyspace(t, mr)
	require.Equal(t, before[packAuditDefaultKey], after[packAuditDefaultKey], "config overlay document changed")
	require.NotContains(t, after, packAuditRegistryKey, "pack registry written")
	require.Equal(t, bundles, packPolicyBundlesNow(t, s), "rollback left the policy bundles changed")
	entries := packPolicyEntries(t, s)
	actions := make([]string, len(entries))
	for i, entry := range entries {
		actions[i] = entry.Action
		require.Equal(t, []string{"test-pack/safety"}, entry.BundleIDs, entry.Action)
	}
	require.ElementsMatch(t, []string{"edit", "rollback"}, actions)
}

func TestPackInstall_StoresOneEditEntryAndSendsItOnce(t *testing.T) {
	s, _, _ := packAuditServer(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink

	rr := postTestPack(t, s)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	entries := packPolicyEntries(t, s)
	require.Len(t, entries, 1)
	require.Equal(t, "edit", entries[0].Action)
	require.Equal(t, []string{"test-pack/safety"}, entries[0].BundleIDs)
	want := policybundles.AuditEntryToSIEM(entries[0], s.tenant)
	sent := 0
	for _, ev := range sink.events {
		if reflect.DeepEqual(ev, want) {
			sent++
		}
	}
	require.Equal(t, 1, sent, "SIEM events for the edit entry")
}

func TestPackInstallAndUninstall_UnstoredEntryAnswers500(t *testing.T) {
	for _, c := range []struct {
		name, want, status string
		uninstall          bool
	}{
		{name: "install", want: "pack test-pack installed but its audit entry was not stored", status: "ACTIVE"},
		{name: "uninstall", want: "pack test-pack uninstalled but its audit entry was not stored", status: "DISABLED", uninstall: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _, hooked := packAuditServer(t)
			call := postTestPack
			if c.uninstall {
				rr := postTestPack(t, s)
				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
				call = func(*testing.T, *server) *httptest.ResponseRecorder { return packUninstall(s) }
			}
			hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})

			rr := call(t, s)
			require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
			require.Equal(t, c.want, decodeBody(t, rr)["error"])
			records, _, err := s.loadPackRegistry(context.Background())
			require.NoError(t, err)
			require.Equal(t, c.status, records["test-pack"].Status, "the registry must show what the body says")
		})
	}
}
