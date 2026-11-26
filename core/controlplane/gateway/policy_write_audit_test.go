package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
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
// snapshots are the policy snapshots stored before the request.
type policyWriteCase struct {
	name                             string
	bundles                          map[string]any
	snapshots                        []policybundles.PolicyBundleSnapshot
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
	globalBody := policyWriteJSON(t, map[string]any{"sections": map[string]any{globalSectionInputRules: map[string]any{"content": policyContent}}})
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
		{
			name:    "publish",
			bundles: policyWritePublishBundles(policyContent),
			call:    func(s *server) *httptest.ResponseRecorder { return policyWritePublish(s) },
			status:  http.StatusOK, action: "publish", resourceType: "policy",
		},
		{
			name:    "rollback",
			bundles: policyWritePublishBundles(policyContent),
			snapshots: []policybundles.PolicyBundleSnapshot{{
				ID: "2026-01-01T00:00:00Z-seed", CreatedAt: "2026-01-01T00:00:00Z", Bundles: map[string]any{"secops/old": map[string]any{"content": policyContent}},
			}},
			call: func(s *server) *httptest.ResponseRecorder {
				return policyWriteCall(s.handleRollbackPolicyBundles, http.MethodPost, "/api/v1/policy/rollback", "", `{"snapshot_id":"2026-01-01T00:00:00Z-seed"}`)
			},
			status: http.StatusOK, action: "rollback", resourceType: "policy",
		},
		{
			name: "global put",
			call: func(s *server) *httptest.ResponseRecorder {
				return policyWriteCall(s.handlePutPolicyGlobal, http.MethodPut, "/api/v1/policy/global", "", globalBody)
			},
			status: http.StatusOK, action: "edit", resourceType: "policy", resourceID: "global",
		},
	}
}

// policyWritePublishBundles is one unpublished policy bundle with content.
func policyWritePublishBundles(content string) map[string]any {
	return map[string]any{"secops/test": map[string]any{"content": content, "enabled": false}}
}

func policyWritePublish(s *server) *httptest.ResponseRecorder {
	return policyWriteCall(s.handlePublishPolicyBundles, http.MethodPost, "/api/v1/policy/publish", "", `{}`)
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
	if c.snapshots != nil {
		require.NoError(t, s.savePolicySnapshots(context.Background(), c.snapshots, nil))
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

func TestPolicySnapshotCapture_AuditFailureSavesNoSnapshot(t *testing.T) {
	s, mr, hooked, _ := policyWriteServer(t, policyWriteCase{bundles: policyWritePublishBundles(policyContent)})
	before := auditPersistKeyspace(t, mr)
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})

	rec := policyWriteCall(s.handleCapturePolicyBundleSnapshot, http.MethodPost, "/api/v1/policy/bundles/snapshots", "", `{"note":"capture"}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.Equal(t, before, auditPersistKeyspace(t, mr), "a capture whose audit entry failed changed stored documents")
}

func TestPolicyPublish_StoresTheSnapshotsItsEntryNames(t *testing.T) {
	s, _, _, _ := policyWriteServer(t, policyWriteCase{bundles: policyWritePublishBundles(policyContent)})

	rec := policyWritePublish(s)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	entries := policyWriteAuditItems(t, s)
	require.Len(t, entries, 1)
	require.NotEmpty(t, entries[0].SnapshotBefore)
	require.NotEmpty(t, entries[0].SnapshotAfter)
	snapshots, _, err := s.loadPolicySnapshots(context.Background())
	require.NoError(t, err)
	ids := make([]string, len(snapshots))
	for i, snap := range snapshots {
		ids[i] = snap.ID
	}
	require.Equal(t, []string{entries[0].SnapshotAfter, entries[0].SnapshotBefore}, ids, "newest first")
}

func TestPolicyPublish_RefusedPublishStoresNoSnapshot(t *testing.T) {
	s, mr, _, _ := policyWriteServer(t, policyWriteCase{bundles: policyWritePublishBundles("rules: [")})
	before := auditPersistKeyspace(t, mr)

	rec := policyWritePublish(s)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, before, auditPersistKeyspace(t, mr), "a refused publish changed stored documents")
}

// gatewayConfigWriters names every function in this package that writes a
// config document, with why it may do so outside commitPolicyWithAudit. A new
// or removed writer fails TestGatewayConfigWriters_EachHasAPinnedReason until
// it is classified here.
var gatewayConfigWriters = map[string]string{
	"migrateLegacyPolicyBundles":       "startup migration of legacy bundles; no request actor",
	"deleteSystemDefaultKeyWithRetry":  "startup migration step on system/default; no request actor",
	"handleUpdateOIDCGroupRoleMapping": "writes the OIDC role mapping in system/default",
	"handleSetConfig":                  "managedConfigKey refuses writes to system/policy",
	"applyConfigOverlay":               "pack config overlay; only the pools and timeouts keys",
	"removeConfigOverlay":              "pack config overlay; only the pools and timeouts keys",
	"restoreConfigOverlay":             "pack config overlay; only the pools and timeouts keys",
	"applyPolicyOverlay":               "pending task-b2c7a413",
	"removePolicyOverlay":              "pending task-b2c7a413",
	"restorePolicyOverlay":             "pending task-b2c7a413",
	"savePackRegistry":                 "writes the pack registry",
	"savePolicySnapshots":              "stores snapshots after the entry naming them is stored",
	"appendPolicyAudit":                "writes the policy audit log itself",
	"commitPolicyWithAudit":            "stores system/policy together with its audit entry",
	"handleCreatePool":                 "writes the pools config in system/default",
	"handleUpdatePool":                 "writes the pools config in system/default",
	"deletePoolFromConfig":             "writes the pools config in system/default",
	"handleDrainPool":                  "writes the pools config in system/default",
	"handleAddTopicToPool":             "writes the pools config in system/default",
	"handleRemoveTopicFromPool":        "writes the pools config in system/default",
	"removeTopicFromPoolConfigOnce":    "removes a deleted topic from the pools config",
	"transitionToInactive":             "marks a drained pool inactive in the pools config",
}

// policyCommitHandlers store their system/policy change through
// commitPolicyBundles, so none of them may write config directly.
var policyCommitHandlers = []string{
	"handlePutPolicyOutputRule", "handlePutPolicyBundle", "handleDeletePolicyBundle",
	"handlePublishPolicyBundles", "handleRollbackPolicyBundles", "handlePutPolicyGlobal",
	"handleCreateVelocityRule", "handlePutVelocityRule", "handleDeleteVelocityRule",
}

var configWriteMethods = []string{"Set", "SetWithRetry", "ReplaceWithRetry", "SetAll"}

// isConfigService reports whether expr is the type *configsvc.Service.
func isConfigService(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "configsvc" && sel.Sel.Name == "Service"
}

// isConfigSvcField reports whether expr selects a configSvc field.
func isConfigSvcField(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "configSvc"
}

// writesConfig reports whether fn calls a config write method on a configSvc
// field or on a name fn declares as a *configsvc.Service.
func writesConfig(fn *ast.FuncDecl) bool {
	services := map[string]bool{}
	writes := false
	ast.Inspect(fn, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			for _, id := range n.Names {
				services[id.Name] = services[id.Name] || isConfigService(n.Type)
			}
		case *ast.ValueSpec:
			for _, id := range n.Names {
				services[id.Name] = services[id.Name] || isConfigService(n.Type)
			}
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				if id, ok := n.Lhs[i].(*ast.Ident); ok && isConfigSvcField(rhs) {
					services[id.Name] = true
				}
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || !slices.Contains(configWriteMethods, sel.Sel.Name) {
				return true
			}
			id, isIdent := sel.X.(*ast.Ident)
			writes = writes || isConfigSvcField(sel.X) || (isIdent && services[id.Name])
		}
		return true
	})
	return writes
}

func TestGatewayConfigWriters_EachHasAPinnedReason(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var writers []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && writesConfig(fn) {
				writers = append(writers, fn.Name.Name)
			}
		}
	}
	require.ElementsMatch(t, slices.Collect(maps.Keys(gatewayConfigWriters)), writers)
	for _, name := range policyCommitHandlers {
		require.NotContains(t, gatewayConfigWriters, name)
	}
}

// policyBumpHook stores another bundle in system/policy through other after
// every GET of the policy key, so every commit attempt meets a newer revision.
type policyBumpHook struct {
	other *redis.Client
	bumps *atomic.Int32
}

func (h policyBumpHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h policyBumpHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		args := cmd.Args()
		if strings.EqualFold(cmd.Name(), "get") && len(args) > 1 && args[1] == auditPersistPolicyKey {
			var runs atomic.Int32
			id := fmt.Sprintf("secops/bump-%d", h.bumps.Add(1))
			_ = configsvc.NewFromClient(h.other).SetWithRetry(context.Background(), configsvc.ScopeSystem, packs.PolicyConfigID, 3, auditPersistAddBundle(id, &runs))
		}
		return err
	}
}

func (h policyBumpHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestCommitPolicyWithAudit_ReturnsAConflictAndStoresNothingWhenRetriesRunOut(t *testing.T) {
	s, mr, hooked, plain := auditPersistServer(t)
	auditPersistSeedPolicy(t, mr, plain)
	var bumps, runs atomic.Int32
	hooked.AddHook(policyBumpHook{other: plain, bumps: &bumps})
	logs := storeWriterLogs(t)

	err := s.commitPolicyWithAudit(context.Background(), auditPersistAddBundle("secops/new", &runs), auditPersistEntry("policy.exhaust"))
	require.True(t, errors.Is(err, configsvc.ErrRevisionConflict), "err=%v", err)
	require.Equal(t, int32(policyAuditAttempts), runs.Load(), "apply runs")
	require.False(t, mr.Exists(auditPersistWriteKey), "the co-located audit doc was stored")
	require.False(t, mr.Exists(auditPersistLogKey), "the shared audit log was stored")
	policy, err := configsvc.NewFromClient(plain).Get(context.Background(), configsvc.ScopeSystem, packs.PolicyConfigID)
	require.NoError(t, err)
	bundles, _ := policy.Data[packs.PolicyConfigKey].(map[string]any)
	require.NotContains(t, bundles, "secops/new", "the policy change was stored")
	require.Len(t, logs.records("ERROR", "msg", "policy write and audit entry not committed"), 1)
	require.Len(t, logs.records("ERROR", "level", "ERROR"), 1, "exactly one ERROR line overall")
	require.NotContains(t, auditPersistActions(t, s), "policy.exhaust")
}

// policyWriteUnreadableAudit stores a co-located audit doc whose entries are
// not a list.
func policyWriteUnreadableAudit(t *testing.T, plain *redis.Client) {
	t.Helper()
	require.NoError(t, configsvc.NewFromClient(plain).Set(context.Background(), &configsvc.Document{
		Scope: configsvc.ScopeSystem, ScopeID: policybundles.PolicyWriteAuditID, Data: map[string]any{policybundles.PolicyAuditKey: "not-a-list"},
	}))
}

func TestCommitPolicyWithAudit_RefusesAnUnreadableAuditDocument(t *testing.T) {
	s, mr, _, plain := auditPersistServer(t)
	auditPersistSeedPolicy(t, mr, plain)
	policyWriteUnreadableAudit(t, plain)
	before := auditPersistKeyspace(t, mr)
	var runs atomic.Int32

	require.Error(t, s.commitPolicyWithAudit(context.Background(), auditPersistAddBundle("secops/new", &runs), auditPersistEntry("policy.x")))
	require.Equal(t, before, auditPersistKeyspace(t, mr))
}

func TestLoadPolicyAudit_FailsOnAnUnreadableAuditDocument(t *testing.T) {
	s, _, _, plain := auditPersistServer(t)
	policyWriteUnreadableAudit(t, plain)

	_, err := s.loadPolicyAudit(context.Background())
	require.Error(t, err)
}
