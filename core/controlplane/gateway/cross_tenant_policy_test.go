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
