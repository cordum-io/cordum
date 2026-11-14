package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/stretchr/testify/require"
)

func resignTestRequest(t *testing.T, s *server, id, role string, status int) *httptest.ResponseRecorder {
	t.Helper()
	keys, err := json.Marshal([]map[string]string{{"key": "resign-test-key", "tenant": "default", "role": role, "principal_id": "resign-operator"}})
	require.NoError(t, err)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": string(keys), "CORDUM_ENV": os.Getenv("CORDUM_ENV"), "CORDUM_PRODUCTION": os.Getenv("CORDUM_PRODUCTION"),
	})
	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/bundles/"+url.PathEscape(strings.ReplaceAll(id, "/", "~"))+"/resign", nil)
	req.Header.Set("X-API-Key", "resign-test-key")
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	apiKeyMiddleware(s.auth, mux).ServeHTTP(rec, req)
	require.Equal(t, status, rec.Code, rec.Body.String())
	return rec
}

func TestResignPolicyBundle_SignsCurrentContentAndKernelLoads(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, _, _ := newTestGateway(t)
	stale := storeWriterSigned(t, "rules: []\n", true)
	stale["content"] = policyContent
	storeWriterSeed(t, s, map[string]any{
		"custom/current": map[string]any{"content": policyContent, "author": "operator"},
		"velocity/stale": stale, "legacy/space %": policyContent,
	})
	_, err := storeWriterKernelLoad(t, s)
	require.Error(t, err)
	for _, id := range []string{"custom/current", "velocity/stale", "legacy/space %"} {
		rec := resignTestRequest(t, s, id, "admin", http.StatusOK)
		var result map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		require.Equal(t, id, result["id"])
		require.Equal(t, storeWriterKeyID, result["key_id"])
		require.Len(t, result, 4)
		bundle := storeWriterBundle(t, s, id)
		require.Equal(t, policyContent, bundle["content"])
		require.NoError(t, storeWriterVerify(t, bundle))
		require.Equal(t, bundle["updated_at"], result["updated_at"])
	}
	require.Equal(t, "operator", storeWriterBundle(t, s, "custom/current")["author"])
	entries, err := s.loadPolicyAudit(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 3)
	ids := []string{}
	for _, entry := range entries {
		require.Equal(t, "resign", entry.Action)
		require.Equal(t, "resign-operator", entry.ActorID)
		ids = append(ids, entry.ResourceID)
	}
	require.ElementsMatch(t, []string{"custom/current", "velocity/stale", "legacy/space %"}, ids)
	snapshots, err := storeWriterKernelLoad(t, s)
	require.NoError(t, err)
	require.Contains(t, strings.Join(snapshots, ","), "cfg:")
}

func TestResignPolicyBundle_RefusesWithoutTrustedSigningKeyBeforeAnyWrite(t *testing.T) {
	for _, tc := range []struct{ mode, trust string }{{"", ""}, {"warn", ""}, {"off", ""}, {"warn", "absent"}, {"enforce", "other"}} {
		t.Run(tc.mode+"/"+tc.trust, func(t *testing.T) {
			storeWriterEnv(t, true, tc.mode)
			if tc.trust != "" {
				storeWriterKey(t, tc.trust)
			}
			s, _, _ := newTestGateway(t)
			storeWriterSeed(t, s, map[string]any{"custom/a": policyContent})
			before := storeWriterKeyspace(t, s)
			rec := resignTestRequest(t, s, "custom/a", "admin", http.StatusServiceUnavailable)
			require.Contains(t, rec.Body.String(), "POLICY_SIGNING_REQUIRED")
			require.Equal(t, before, storeWriterKeyspace(t, s))
		})
	}
}

func TestResignPolicyBundle_RefusesPackOwnedFragment(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, _, _ := newTestGateway(t)
	storeWriterSeed(t, s, map[string]any{"demo/default": policyContent})
	require.NoError(t, s.savePackRegistry(context.Background(), map[string]packs.PackRecord{
		"demo": {ID: "demo", Overlays: packs.PackRecordOverlays{Policy: []packs.PackAppliedPolicyOverlay{{FragmentID: "demo/default"}}}},
	}, nil))
	before := storeWriterKeyspace(t, s)
	rec := resignTestRequest(t, s, "demo/default", "admin", http.StatusBadRequest)
	require.Contains(t, rec.Body.String(), "POLICY_VALIDATION_FAILED")
	require.Contains(t, rec.Body.String(), "demo")
	require.Contains(t, rec.Body.String(), "reinstall")
	require.Equal(t, before, storeWriterKeyspace(t, s))
}

func TestResignPolicyBundle_RequiresAdmin(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	for _, role := range []string{"viewer", "user", "admin", "operator", "secops"} {
		t.Run(role, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			storeWriterSeed(t, s, map[string]any{"custom/a": policyContent})
			before := storeWriterKeyspace(t, s)
			id, status := "custom/a", http.StatusForbidden
			if role == "admin" || role == "operator" || role == "secops" {
				id, status = "missing", http.StatusNotFound
			}
			resignTestRequest(t, s, id, role, status)
			require.Equal(t, before, storeWriterKeyspace(t, s))
		})
	}
}

type resignTestLock struct {
	locks.Store
	onRenew func()
	lost    bool
}

func (l resignTestLock) Renew(ctx context.Context, resource, owner string, ttl time.Duration) (*locks.Lock, bool, error) {
	if l.onRenew != nil {
		l.onRenew()
	}
	if l.lost {
		return nil, false, nil
	}
	return l.Store.Renew(ctx, resource, owner, ttl)
}

func TestResignPolicyBundle_HoldsThePackLockUntilCommit(t *testing.T) {
	for _, mode := range []string{"serialized", "held", "lost"} {
		t.Run(mode, func(t *testing.T) {
			storeWriterEnv(t, true, "")
			storeWriterKey(t, "trusted")
			s, _, _ := newTestGateway(t)
			storeWriterSeed(t, s, map[string]any{"custom/a": policyContent})
			inner, calls := s.lockStore, 0
			s.lockStore = resignTestLock{Store: inner, lost: mode == "lost", onRenew: func() {
				calls++
				release, err := acquirePackLocks(context.Background(), inner, "demo", "resign-test-installer")
				defer release()
				require.ErrorContains(t, err, "global pack lock held")
			}}
			status := http.StatusOK
			if mode != "serialized" {
				status = http.StatusConflict
			}
			if mode == "held" {
				release, err := acquirePackLocks(context.Background(), inner, "demo", "resign-test-installer")
				require.NoError(t, err)
				defer release()
			}
			before := storeWriterKeyspace(t, s)
			rec := resignTestRequest(t, s, "custom/a", "admin", status)
			if status != http.StatusOK {
				require.Contains(t, rec.Body.String(), "POLICY_VERSION_CONFLICT")
				require.Equal(t, before, storeWriterKeyspace(t, s))
			} else {
				require.Equal(t, 1, calls)
				release, err := acquirePackLocks(context.Background(), inner, "demo", "resign-test-installer")
				require.NoError(t, err)
				release()
			}
		})
	}
}

func TestResignPolicyBundle_WritesNothingWhenTheAuditCannotBeStored(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, _, hooked, plain := auditPersistServer(t)
	storeWriterSeed(t, s, map[string]any{"custom/a": policyContent})
	before, err := plain.Get(context.Background(), auditPersistPolicyKey).Result()
	require.NoError(t, err)
	hooked.AddHook(auditPersistHook{failKey: auditPersistWriteKey})
	resignTestRequest(t, s, "custom/a", "admin", http.StatusInternalServerError)
	after, err := plain.Get(context.Background(), auditPersistPolicyKey).Result()
	require.NoError(t, err)
	require.Equal(t, before, after)
	entries, err := s.loadPolicyAudit(context.Background())
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestResignPolicyBundle_SignsTheContentTheStoreSourceReads(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content any
		status  int
	}{
		{"string-field", 7, http.StatusOK}, {"empty-first-string", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storeWriterEnv(t, true, "")
			storeWriterKey(t, "trusted")
			s, _, _ := newTestGateway(t)
			storeWriterSeed(t, s, map[string]any{"custom/a": map[string]any{"content": tc.content, "policy": policyContent}})
			before := storeWriterKeyspace(t, s)
			resignTestRequest(t, s, "custom/a", "admin", tc.status)
			if tc.status == http.StatusBadRequest {
				require.Equal(t, before, storeWriterKeyspace(t, s))
				return
			}
			bundle := storeWriterBundle(t, s, "custom/a")
			require.Equal(t, float64(7), bundle["content"])
			bundle["content"] = bundle["policy"] // Verify the selected string, without changing the store.
			require.NoError(t, storeWriterVerify(t, bundle))
			_, err := storeWriterKernelLoad(t, s)
			require.NoError(t, err)
		})
	}
}

func TestResignPolicyBundle_PreservesConcurrentContent(t *testing.T) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, _, _ := newTestGateway(t)
	storeWriterSeed(t, s, map[string]any{"custom/a": policyContent})
	var expected map[string]string
	s.lockStore = resignTestLock{Store: s.lockStore, onRenew: func() {
		doc, err := s.configSvc.Get(context.Background(), packs.PolicyConfigScope, packs.PolicyConfigID)
		require.NoError(t, err)
		doc.Data[packs.PolicyConfigKey] = map[string]any{"custom/a": "rules: []\n"}
		require.NoError(t, s.configSvc.Set(context.Background(), doc))
		expected = storeWriterKeyspace(t, s)
		delete(expected, "lock:packs:global") // The request releases its temporary lock.
	}}
	rec := resignTestRequest(t, s, "custom/a", "admin", http.StatusConflict)
	require.Contains(t, rec.Body.String(), "POLICY_VERSION_CONFLICT")
	require.Equal(t, expected, storeWriterKeyspace(t, s))
}
