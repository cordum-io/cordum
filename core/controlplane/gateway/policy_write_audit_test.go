package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// policyWriteCase is one handler that writes system/policy. bundles is the
// system/policy bundle map stored before the request; nil stores no document.
type policyWriteCase struct {
	name                             string
	bundles                          map[string]any
	call                             func(s *server) *httptest.ResponseRecorder
	status                           int
	action, resourceType, resourceID string
}

// policyWriteCall sends an admin request with body to handler.
func policyWriteCall(handler http.HandlerFunc, method, target, pathID, body string) *httptest.ResponseRecorder {
	req := adminCtx(httptest.NewRequest(method, target, strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	if pathID != "" {
		req.SetPathValue("id", pathID)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func policyWriteJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}

func policyWriteCases(t *testing.T) []policyWriteCase {
	t.Helper()
	putBody := policyWriteJSON(t, map[string]any{"content": policyContent})
	velocity := velocityRulePayload(storeWriterRuleID)
	velocityBody := policyWriteJSON(t, velocity)
	velocityBundles := map[string]any{velocityRuleBundleID(storeWriterRuleID): storeWriterVelocityBundle(t, velocity, map[string]any{})}
	velocityPath := "/api/v1/policy/velocity-rules/" + storeWriterRuleID
	return []policyWriteCase{
		{
			name:    "output rule toggle",
			bundles: map[string]any{"secops/output": map[string]any{"content": outputPolicyContent, "enabled": true}},
			call: func(s *server) *httptest.ResponseRecorder {
				return policyWriteCall(s.handlePutPolicyOutputRule, http.MethodPut, "/api/v1/policy/output/rules/out-secret", "out-secret", `{"enabled":true}`)
			},
			status: http.StatusOK, action: "edit", resourceType: "output_rule", resourceID: "out-secret",
		},
		{
			name: "bundle put without a stored policy",
			call: func(s *server) *httptest.ResponseRecorder {
				return policyWriteCall(s.handlePutPolicyBundle, http.MethodPut, "/api/v1/policy/bundles/secops/test", "secops/test", putBody)
			},
			status: http.StatusOK, action: "edit", resourceType: "policy", resourceID: "secops/test",
		},
		{
			name:    "bundle delete",
			bundles: map[string]any{"secops/test": map[string]any{"content": policyContent}},
			call: func(s *server) *httptest.ResponseRecorder {
				return policyWriteCall(s.handleDeletePolicyBundle, http.MethodDelete, "/api/v1/policy/bundles/secops/test", "secops/test", "")
			},
			status: http.StatusNoContent, action: "delete", resourceType: "policy", resourceID: "secops/test",
		},
		{
			name: "velocity rule create",
			call: func(s *server) *httptest.ResponseRecorder {
				return policyWriteCall(s.handleCreateVelocityRule, http.MethodPost, "/api/v1/policy/velocity-rules", "", velocityBody)
			},
			status: http.StatusCreated, action: "create", resourceType: "velocity_rule", resourceID: storeWriterRuleID,
		},
		{
			name:    "velocity rule update",
			bundles: velocityBundles,
			call: func(s *server) *httptest.ResponseRecorder {
				return policyWriteCall(s.handlePutVelocityRule, http.MethodPut, velocityPath, storeWriterRuleID, velocityBody)
			},
			status: http.StatusOK, action: "edit", resourceType: "velocity_rule", resourceID: storeWriterRuleID,
		},
		{
			name:    "velocity rule delete",
			bundles: velocityBundles,
			call: func(s *server) *httptest.ResponseRecorder {
				return policyWriteCall(s.handleDeleteVelocityRule, http.MethodDelete, velocityPath, storeWriterRuleID, "")
			},
			status: http.StatusNoContent, action: "delete", resourceType: "velocity_rule", resourceID: storeWriterRuleID,
		},
	}
}

// policyWriteServer returns auditPersistServer's gateway with c's bundles
// stored, velocity rules licensed and the development profile with policy
// signing off, as TestMain sets it.
func policyWriteServer(t *testing.T, c policyWriteCase) (*server, *miniredis.Miniredis, *redis.Client, *redis.Client) {
	t.Helper()
	storeWriterEnv(t, false, "off")
	s, mr, hooked, plain := auditPersistServer(t)
	setTestEntitlements(t, s, licensing.PlanTeam, func(e *licensing.Entitlements) { e.VelocityRules = true })
	if c.bundles != nil {
		require.NoError(t, configsvc.NewFromClient(plain).Set(context.Background(), &configsvc.Document{
			Scope: configsvc.ScopeSystem, ScopeID: packs.PolicyConfigID, Data: map[string]any{packs.PolicyConfigKey: c.bundles},
		}))
	}
	return s, mr, hooked, plain
}

// policyWriteAuditItems returns the entries GET /api/v1/policy/audit lists.
func policyWriteAuditItems(t *testing.T, s *server) []policybundles.PolicyAuditEntry {
	t.Helper()
	rec := policyWriteCall(s.handleListPolicyAudit, http.MethodGet, "/api/v1/policy/audit", "", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Items []policybundles.PolicyAuditEntry `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Items
}

func TestPolicyWriters_AuditCommitFailureChangesNothing(t *testing.T) {
	for _, c := range policyWriteCases(t) {
		t.Run(c.name, func(t *testing.T) {
			s, mr, hooked, _ := policyWriteServer(t, c)
			before := auditPersistKeyspace(t, mr)
			hooked.AddHook(auditPersistHook{failKey: auditPersistWriteKey})

			rec := c.call(s)
			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			require.Equal(t, before, auditPersistKeyspace(t, mr), "a failed audit commit changed stored documents")
			require.Zero(t, storeWriterPublished(s.bus.(*stubBus)), "a failed write published a config change")
		})
	}
}

func TestPolicyWriters_RefuseAPolicyChangedAfterTheirRead(t *testing.T) {
	for _, c := range policyWriteCases(t) {
		t.Run(c.name, func(t *testing.T) {
			s, mr, hooked, plain := policyWriteServer(t, c)
			var fired atomic.Bool
			var runs atomic.Int32
			var afterChange map[string]string
			hooked.AddHook(auditPersistHook{getKey: auditPersistPolicyKey, fired: &fired, afterGet: func() {
				require.NoError(t, configsvc.NewFromClient(plain).SetWithRetry(context.Background(), configsvc.ScopeSystem, packs.PolicyConfigID, 1, auditPersistAddBundle("secops/concurrent", &runs)))
				afterChange = auditPersistKeyspace(t, mr)
			}})

			rec := c.call(s)
			require.True(t, fired.Load(), "the concurrent writer never ran")
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), errorCodePolicyVersionConflict)
			require.Equal(t, afterChange, auditPersistKeyspace(t, mr), "the request stored something over the concurrent change")
		})
	}
}

func TestPolicyWriters_StoreAndExportTheirEntryOnce(t *testing.T) {
	for _, c := range policyWriteCases(t) {
		t.Run(c.name, func(t *testing.T) {
			s, _, _, _ := policyWriteServer(t, c)
			sink := &recordingAuditSender{}
			s.auditExporter = sink

			rec := c.call(s)
			require.Equal(t, c.status, rec.Code, rec.Body.String())
			entries := policyWriteAuditItems(t, s)
			require.Len(t, entries, 1)
			got := entries[0]
			require.Equal(t, []string{c.action, c.resourceType, c.resourceID}, []string{got.Action, got.ResourceType, got.ResourceID})
			require.Equal(t, []audit.SIEMEvent{policybundles.AuditEntryToSIEM(got, s.tenant)}, sink.events)
		})
	}
}
