package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policyshadow"
)

func init() {
	for _, mode := range []string{"evaluate", "simulate", "explain"} {
		registerCrossTenantFamily("PR1-02-POLICY", crossTenantPolicyCheck(mode))
	}
	registerCrossTenantFamily("PR1-02-POLICY", crossTenantCase{"POST /api/v1/policy/bundles/{id}/simulate", "bundle-simulate", crossTenantBundleSimulate})
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		registerCrossTenantFamily("PR1-02-POLICY", crossTenantShadowWrite(method))
	}
	registerCrossTenantFamily("PR1-02-POLICY", crossTenantCase{"GET /api/v1/policy/bundles/{id}", "bundle-detail", crossTenantBundleDetail})
	registerCrossTenantFamily("PR1-02-POLICY", crossTenantCase{"GET /api/v1/policy/output/stats", "output-stats", crossTenantOutputStats})
	registerCrossTenantFamily("PR1-02-POLICY", crossTenantCase{"POST /api/v1/policy/replay", "replay", crossTenantPolicyReplay})
	for _, route := range []struct{ method, path, name string }{
		{http.MethodPut, "/bundles/{id}", "bundle-put"},
		{http.MethodDelete, "/bundles/{id}", "bundle-delete"},
		{http.MethodPost, "/publish", "publish"},
		{http.MethodPost, "/rollback", "rollback"},
		{http.MethodPost, "/bundles/snapshots", "snapshot"},
		{http.MethodPut, "/global", "global"},
		{http.MethodPut, "/output/rules/{id}", "output-rule"},
		{http.MethodPost, "/velocity-rules", "velocity-create"},
		{http.MethodPut, "/velocity-rules/{id}", "velocity-put"},
		{http.MethodDelete, "/velocity-rules/{id}", "velocity-delete"},
	} {
		registerCrossTenantFamily("PR1-02-POLICY", crossTenantCase{
			route.method + " /api/v1/policy" + route.path, "system-" + route.name,
			crossTenantSystemPolicyWrite(route.method, route.name),
		})
	}
}

func TestCrossTenantSweepPolicy(t *testing.T) {
	runCrossTenantFamily(t, "PR1-02-POLICY")
}

func crossTenantPolicyCheck(mode string) crossTenantCase {
	path := "/api/v1/policy/" + mode
	return crossTenantCase{"POST " + path, mode, func(t *testing.T, sw *crossTenantSweep) {
		bundleID, marker := seedCrossTenantPolicy(t, sw)
		before := crossTenantPolicyState(t, sw, bundleID)
		mark := sw.publishMark()
		body := fmt.Sprintf(`{"tenant":%q,"topic":"job.policy-sweep"}`, sw.tenantB.tenantID)
		rr := sw.as(t, sw.tenantA, http.MethodPost, path, body)
		if !reflect.DeepEqual(before, crossTenantPolicyState(t, sw, bundleID)) {
			t.Error("cross-tenant policy check changed protected policy state")
		}
		if len(sw.publishedSince(mark)) != 0 {
			t.Error("cross-tenant policy check published a packet")
		}
		safety, ok := sw.server.safetyClient.(*stubSafetyClient)
		if !ok {
			t.Fatal("policy fixture has no safety client stub")
		}
		safety.mu.Lock()
		forwarded := safety.lastReq
		safety.mu.Unlock()
		if forwarded != nil {
			t.Error("cross-tenant policy check reached the safety kernel")
		}
		assertCrossTenantRefused(t, rr, mode, marker)
		assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPost, path, body), "owner "+mode)
		safety.mu.Lock()
		forwarded = safety.lastReq
		safety.mu.Unlock()
		if forwarded == nil || forwarded.Tenant != sw.tenantB.tenantID {
			t.Fatal("owner policy check did not reach the safety kernel with B's tenant")
		}
	}}
}

func seedCrossTenantPolicy(t *testing.T, sw *crossTenantSweep) (string, string) {
	t.Helper()
	setTestSigningEnv(t, "warn")
	bundleID, marker := "secops/policy-sweep", "private-policy-shadow-owner"
	admin := &auth.AuthContext{Role: "admin", Tenant: sw.tenantB.tenantID, AllowCrossTenant: true}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body := fmt.Sprintf(`{"content":%q,"enabled":true}`, shadowTestValidYAML)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/"+url.PathEscape(bundleID), strings.NewReader(body))
	req.SetPathValue("id", bundleID)
	req = withAuth(req.WithContext(ctx), admin)
	rr := httptest.NewRecorder()
	sw.server.handlePutPolicyBundle(rr, req)
	assertCrossTenantAbsent(t, rr, "seed system policy")
	req = httptest.NewRequest(http.MethodPost, "/api/v1/policy/bundles/snapshots", strings.NewReader(`{}`))
	rr = httptest.NewRecorder()
	sw.server.handleCapturePolicyBundleSnapshot(rr, withAuth(req.WithContext(ctx), admin))
	assertCrossTenantAbsent(t, rr, "seed system snapshot")
	body = fmt.Sprintf(`{"content":%q,"metadata":{"private_marker":%q}}`, shadowTestValidYAML, marker)
	path := "/api/v1/policy/shadows/" + url.PathEscape(bundleID)
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPut, path, body), "seed B shadow")
	rr = sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, rr, "owner shadow")
	if !strings.Contains(rr.Body.String(), marker) {
		t.Fatal("owner shadow control is missing its private marker")
	}
	return bundleID, marker
}

type crossTenantPolicySnapshot struct {
	system    *configsvc.Document
	snapshots []policybundles.PolicyBundleSnapshot
	shadow    *policyshadow.ShadowPolicy
}

func crossTenantPolicyState(t *testing.T, sw *crossTenantSweep, bundleID string) crossTenantPolicySnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	doc, err := sw.server.configSvc.Get(ctx, configsvc.Scope(packs.PolicyConfigScope), packs.PolicyConfigID)
	if err != nil || doc == nil {
		t.Fatalf("read system policy: %v", err)
	}
	snapshots, _, err := sw.server.loadPolicySnapshots(ctx)
	if err != nil || len(snapshots) == 0 {
		t.Fatalf("read seeded snapshots: %v", err)
	}
	shadow, err := sw.server.policyShadowStore.Get(ctx, sw.tenantB.tenantID, bundleID)
	if err != nil || shadow == nil || shadow.TenantID != sw.tenantB.tenantID {
		t.Fatalf("read B shadow: %v", err)
	}
	return crossTenantPolicySnapshot{doc, snapshots, shadow}
}

func assertCrossTenantPolicyUnchanged(t *testing.T, sw *crossTenantSweep, id string, before crossTenantPolicySnapshot, mark int) {
	t.Helper()
	if !reflect.DeepEqual(before, crossTenantPolicyState(t, sw, id)) {
		t.Error("cross-tenant call changed system policy, snapshots or B's shadow")
	}
	if len(sw.publishedSince(mark)) != 0 {
		t.Error("cross-tenant call published a packet")
	}
}

func crossTenantBundleSimulate(t *testing.T, sw *crossTenantSweep) {
	id, marker := seedCrossTenantPolicy(t, sw)
	before, mark := crossTenantPolicyState(t, sw, id), sw.publishMark()
	path := "/api/v1/policy/bundles/" + url.PathEscape(id) + "/simulate"
	body := fmt.Sprintf(`{"request":{"tenant":%q,"topic":"job.policy-sweep"}}`, sw.tenantB.tenantID)
	rr := sw.as(t, sw.tenantA, http.MethodPost, path, body)
	assertCrossTenantPolicyUnchanged(t, sw, id, before, mark)
	assertCrossTenantRefused(t, rr, "A bundle simulation", marker)
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPost, path, body), "owner bundle simulation")
}

func crossTenantShadowWrite(method string) crossTenantCase {
	return crossTenantCase{method + " /api/v1/policy/shadows/{id}", "shadow-" + method, func(t *testing.T, sw *crossTenantSweep) {
		id, marker := seedCrossTenantPolicy(t, sw)
		before, mark := crossTenantPolicyState(t, sw, id), sw.publishMark()
		path := "/api/v1/policy/shadows/" + url.PathEscape(id)
		body := fmt.Sprintf(`{"content":%q}`, shadowTestReplacementYAML)
		if method == http.MethodDelete {
			body = ""
		}
		rr := sw.as(t, sw.tenantA, method, path, body)
		assertCrossTenantPolicyUnchanged(t, sw, id, before, mark)
		if method == http.MethodDelete {
			assertCrossTenantRefused(t, rr, "A shadow delete", marker)
		} else {
			assertCrossTenantAbsent(t, rr, "A shadow write", marker, before.shadow.ShadowBundleID)
		}
		assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, method, path, body), "owner shadow write")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		after, err := sw.server.policyShadowStore.Get(ctx, sw.tenantB.tenantID, id)
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodDelete && after != nil {
			t.Error("owner deletion left B's shadow")
		}
		expected := policybundles.SanitizePolicyBundleYAML(strings.TrimSpace(shadowTestReplacementYAML))
		if method == http.MethodPut && (after == nil || after.Content != expected) {
			t.Error("owner replacement did not persist")
		}
	}}
}

func crossTenantBundleDetail(t *testing.T, sw *crossTenantSweep) {
	id, _ := seedCrossTenantPolicy(t, sw)
	before := crossTenantPolicyState(t, sw, id)
	path := "/api/v1/policy/bundles/" + url.PathEscape(id)
	rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, rr, "A bundle detail", before.shadow.ShadowBundleID)
	var detail policyBundleDetailWithShadow
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil || detail.Shadow != nil {
		t.Fatalf("A bundle contains shadow: %v, %v", detail.Shadow, err)
	}
	rr = sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, rr, "owner bundle detail")
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil || detail.Shadow == nil || detail.Shadow.ShadowBundleID != before.shadow.ShadowBundleID {
		t.Fatalf("owner bundle missing B's shadow: %v", err)
	}
}

func crossTenantOutputStats(t *testing.T, sw *crossTenantSweep) {
	id, marker := seedCrossTenantJob(t, sw)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	record := model.OutputSafetyRecord{Decision: model.OutputQuarantine, Reason: marker, CheckedAt: time.Now().UTC().UnixMicro(), CheckDurationMs: 731}
	if err := sw.server.jobStore.SetOutputDecision(ctx, id, record); err != nil {
		t.Fatal(err)
	}
	for _, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
		rr := sw.as(t, who, http.MethodGet, "/api/v1/policy/output/stats", "")
		assertCrossTenantAbsent(t, rr, "output stats", marker)
		var stats struct {
			Checks      int64  `json:"total_checks_24h"`
			Quarantined int64  `json:"quarantined_24h"`
			Latency     int64  `json:"avg_latency_ms"`
			Last        string `json:"last_check_at"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
			t.Fatal(err)
		}
		if who == sw.tenantA && (stats.Checks != 0 || stats.Quarantined != 0 || stats.Latency != 0 || stats.Last != "") {
			t.Errorf("A stats include B's output: %+v", stats)
		}
		if who == sw.tenantB && (stats.Checks != 1 || stats.Quarantined != 1 || stats.Latency != 731 || stats.Last == "") {
			t.Errorf("owner stats missing B's output: %+v", stats)
		}
	}
}

func crossTenantSystemPolicyWrite(method, name string) func(*testing.T, *crossTenantSweep) {
	return func(t *testing.T, sw *crossTenantSweep) {
		id, marker := seedCrossTenantPolicy(t, sw)
		path, body := prepareCrossTenantSystemWrite(t, sw, id, name)
		before, mark := crossTenantPolicyState(t, sw, id), sw.publishMark()
		rr := sw.as(t, sw.tenantA, method, path, body)
		after := crossTenantPolicyState(t, sw, id)
		if !reflect.DeepEqual(before.system, after.system) {
			t.Error("tenant-bound admin changed the system policy document")
		}
		if !reflect.DeepEqual(before.snapshots, after.snapshots) {
			t.Error("tenant-bound admin changed policy snapshots")
		}
		if !reflect.DeepEqual(before.shadow, after.shadow) {
			t.Error("tenant-bound admin changed B's shadow")
		}
		if len(sw.publishedSince(mark)) != 0 {
			t.Error("tenant-bound admin published a packet")
		}
		if rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound {
			t.Errorf("tenant-bound admin status=%d, want 403 or 404", rr.Code)
		}
		assertCrossTenantNoLeaks(t, rr, "system write", marker)
		// A separate fixture keeps a destructive violation from masking the control.
		owner := newCrossTenantSweep(t)
		ownerID, _ := seedCrossTenantPolicy(t, owner)
		ownerPath, ownerBody := prepareCrossTenantSystemWrite(t, owner, ownerID, name)
		ownerBefore := crossTenantPolicyState(t, owner, ownerID)
		rr = crossTenantPolicyAdmin(t, owner, method, ownerPath, ownerBody)
		assertCrossTenantAbsent(t, rr, "cross-tenant admin control")
		if reflect.DeepEqual(ownerBefore, crossTenantPolicyState(t, owner, ownerID)) {
			t.Error("cross-tenant admin control did not change policy state")
		}
		t.Logf("cross-tenant admin control status=%d", rr.Code)
	}
}

func crossTenantPolicyAdmin(t *testing.T, sw *crossTenantSweep, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", sw.tenantB.tenantID)
	req = withAuth(req, &auth.AuthContext{Role: "admin", Tenant: sw.tenantB.tenantID, AllowCrossTenant: true})
	rr := httptest.NewRecorder()
	sw.mux.ServeHTTP(rr, req)
	return rr
}

func prepareCrossTenantSystemWrite(t *testing.T, sw *crossTenantSweep, id, name string) (string, string) {
	t.Helper()
	base := "/api/v1/policy"
	bundle := base + "/bundles/" + url.PathEscape(id)
	replacement := fmt.Sprintf(`{"content":%q,"enabled":false}`, shadowTestReplacementYAML)
	switch name {
	case "bundle-put":
		return bundle, replacement
	case "bundle-delete":
		return bundle, ""
	case "publish":
		assertCrossTenantAbsent(t, crossTenantPolicyAdmin(t, sw, http.MethodPut, bundle, replacement), "seed disabled bundle")
		return base + "/publish", fmt.Sprintf(`{"bundle_ids":[%q],"message":"publish control"}`, id)
	case "rollback":
		snapshotID := crossTenantPolicyState(t, sw, id).snapshots[0].ID
		assertCrossTenantAbsent(t, crossTenantPolicyAdmin(t, sw, http.MethodPut, bundle, replacement), "seed modified bundle")
		return base + "/rollback", fmt.Sprintf(`{"snapshot_id":%q}`, snapshotID)
	case "snapshot":
		return base + "/bundles/snapshots", `{"note":"capture control"}`
	case "global":
		return base + "/global", fmt.Sprintf(`{"sections":{"input_rules":{"content":%q}}}`, globalInputContent)
	case "output-rule":
		body := fmt.Sprintf(`{"content":%q,"enabled":true}`, outputPolicyContent)
		assertCrossTenantAbsent(t, crossTenantPolicyAdmin(t, sw, http.MethodPut, base+"/bundles/secops%2Foutput", body), "seed output rule")
		return base + "/output/rules/out-secret", `{"enabled":true}`
	default:
		return prepareCrossTenantVelocityWrite(t, sw, name)
	}
}

func prepareCrossTenantVelocityWrite(t *testing.T, sw *crossTenantSweep, name string) (string, string) {
	t.Helper()
	setTestEntitlements(t, sw.server, licensing.PlanTeam, func(e *licensing.Entitlements) { e.VelocityRules = true })
	path := "/api/v1/policy/velocity-rules"
	payload := velocityRulePayload("policy-sweep")
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if name == "velocity-create" {
		return path, string(body)
	}
	assertCrossTenantAbsent(t, crossTenantPolicyAdmin(t, sw, http.MethodPost, path, string(body)), "seed velocity rule")
	if name == "velocity-delete" {
		return path + "/policy-sweep", ""
	}
	payload["threshold"] = 7
	body, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return path + "/policy-sweep", string(body)
}

func crossTenantPolicyReplay(t *testing.T, sw *crossTenantSweep) {
	id, marker := seedCrossTenantPolicy(t, sw)
	jobID, jobMarker := seedCrossTenantJob(t, sw)
	before, meta := crossTenantPolicyState(t, sw, id), crossTenantJobMeta(t, sw, jobID)
	now := time.Now().UTC()
	candidate := "version: \"1\"\nrules:\n  - id: deny-replay\n    match:\n      topics: [\"job.*\"]\n    decision: deny\n"
	for _, filter := range []string{"null", fmt.Sprintf(`{"tenant":%q}`, sw.tenantB.tenantID)} {
		body := fmt.Sprintf(`{"from":%q,"to":%q,"candidate_content":%q,"filters":%s}`, now.Add(-time.Hour).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339), candidate, filter)
		mark := sw.publishMark()
		rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/policy/replay", body)
		assertCrossTenantPolicyUnchanged(t, sw, id, before, mark)
		if !reflect.DeepEqual(meta, crossTenantJobMeta(t, sw, jobID)) {
			t.Error("cross-tenant replay changed B's job")
		}
		if rr.Code == http.StatusForbidden || rr.Code == http.StatusNotFound {
			assertCrossTenantRefused(t, rr, "A replay", marker, jobID, jobMarker)
		} else {
			assertCrossTenantAbsent(t, rr, "A replay", marker, jobID, jobMarker)
			var response policyReplayResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Summary.TotalJobs != 0 || response.Summary.Evaluated != 0 || len(response.Changes) != 0 || len(response.RuleHits) != 0 {
				t.Errorf("A replay included B's job with filter %s: %s", filter, rr.Body.String())
			}
		}
		rr = sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/policy/replay", body)
		assertCrossTenantAbsent(t, rr, "owner replay")
		var response policyReplayResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil || response.Summary.Evaluated != 1 || len(response.Changes) != 1 || response.Changes[0].JobID != jobID {
			t.Fatalf("owner replay did not evaluate B's job: %v; %s", err, rr.Body.String())
		}
	}
}
