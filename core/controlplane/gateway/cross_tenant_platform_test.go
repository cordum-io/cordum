package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/copilot"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/evals/runner"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/telemetry"
)

func init() {
	registerCrossTenantFamily("PR1-02-PLATFORM",
		crossTenantCase{"PUT /api/v1/config", "config-put", crossTenantPlatformConfigWrites(http.MethodPut)},
		crossTenantCase{"POST /api/v1/config", "config-post", crossTenantPlatformConfigWrites(http.MethodPost)},
		crossTenantCase{"GET /api/v1/config", "config-reads", crossTenantPlatformConfigReads},
		crossTenantCase{"GET /api/v1/config/effective", "config-effective", crossTenantPlatformConfigEffective},
		crossTenantCase{"GET /api/v1/license/usage", "license-usage", crossTenantPlatformUsage},
		crossTenantCase{"GET /api/v1/copilot/sessions/{sessionId}", "copilot-session", crossTenantPlatformCopilot},
		crossTenantPlatformGovernance("/api/v1/governance/health", "governance-health"),
		crossTenantPlatformGovernance("/api/v1/governance/decisions", "governance-decisions"),
		crossTenantPlatformGovernance("/api/v1/governance/approvals/analytics", "approval-analytics"),
		crossTenantCase{"GET /api/v1/evals/datasets", "datasets-list", func(t *testing.T, sw *crossTenantSweep) {
			crossTenantPlatformEvalRequest(t, sw, http.MethodGet, "/api/v1/evals/datasets?tenant="+sw.tenantB.tenantID, "", true)
		}},
		crossTenantCase{"POST /api/v1/evals/datasets", "datasets-create", crossTenantPlatformEvalCreate},
		crossTenantCase{"POST /api/v1/evals/datasets/from-incidents", "datasets-incidents", crossTenantPlatformEvalIncidents},
		crossTenantCase{"ANY /api/v1/evals/datasets/", "datasets-subroutes", crossTenantPlatformEvalSubroutes},
		crossTenantCase{"GET /api/v1/evals/runs/{run_id}", "eval-run-get", func(t *testing.T, sw *crossTenantSweep) {
			crossTenantPlatformEvalRequest(t, sw, http.MethodGet, "/api/v1/evals/runs/{run}", "", false)
		}},
		crossTenantCase{"DELETE /api/v1/evals/runs/{run_id}", "eval-run-delete", func(t *testing.T, sw *crossTenantSweep) {
			crossTenantPlatformEvalRequest(t, sw, http.MethodDelete, "/api/v1/evals/runs/{run}?force=true", "", false)
		}},
		crossTenantCase{"POST /api/v1/license/reload", "license-reload", crossTenantPlatformLicenseReload},
		crossTenantCase{"POST /api/v1/telemetry/consent", "telemetry-consent", crossTenantPlatformConsent},
		crossTenantCase{"POST /api/v1/packs/install", "pack-install", crossTenantPlatformPackInstall},
		crossTenantCase{"POST /api/v1/packs/{id}/uninstall", "pack-uninstall", crossTenantPlatformPackAction("uninstall", `{"purge":true}`)},
		crossTenantCase{"POST /api/v1/packs/{id}/verify", "pack-verify", crossTenantPlatformPackAction("verify", "")},
		crossTenantCase{"POST /api/v1/marketplace/install", "marketplace-install", crossTenantPlatformMarketplace},
	)
}

func TestCrossTenantSweepPlatform(t *testing.T) {
	runCrossTenantFamily(t, "PR1-02-PLATFORM")
}

func crossTenantPlatformUsage(t *testing.T, sw *crossTenantSweep) {
	id, marker := seedCrossTenantJob(t, sw)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sw.server.jobStore.SetStateWithContext(ctx, id, model.JobStateScheduled, nil); err != nil {
		t.Fatal(err)
	}
	before := crossTenantJobMeta(t, sw, id)
	path := "/api/v1/license/usage?tenant=" + sw.tenantB.tenantID
	rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, rr, "A usage", marker, sw.tenantB.tenantID)
	assertCrossTenantPlatformUsage(t, rr, sw.tenantA.tenantID, 0)
	if !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) {
		t.Error("usage read changed B job")
	}
	owner := sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, owner, "B usage control")
	assertCrossTenantPlatformUsage(t, owner, sw.tenantB.tenantID, 1)
}

func assertCrossTenantPlatformUsage(t *testing.T, rr *httptest.ResponseRecorder, tenant string, want int) {
	t.Helper()
	var body struct {
		TenantID string `json:"tenant_id"`
		Usage    struct {
			Jobs struct {
				Current *int `json:"current"`
			} `json:"concurrent_jobs"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.TenantID != tenant || body.Usage.Jobs.Current == nil || *body.Usage.Jobs.Current != want {
		t.Errorf("usage must describe tenant %s with %d active jobs: %s", tenant, want, rr.Body.String())
	}
}

func crossTenantPlatformCopilot(t *testing.T, sw *crossTenantSweep) {
	id, marker := seedCrossTenantJob(t, sw)
	before := crossTenantJobMeta(t, sw, id)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := copilot.NewRedisStore(sw.server.jobStore.Client())
	sw.server.copilotStore = store
	const sessionID = "sess-platform-b"
	msg := copilot.CopilotMessage{ID: "message-b", Role: "user", Content: marker, JobIDs: []string{id}}
	if err := store.AppendMessage(ctx, sw.tenantB.tenantID, sw.tenantB.principalID, sessionID, msg); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetSession(ctx, sw.tenantB.tenantID, sessionID, sw.tenantB.principalID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/copilot/sessions/" + sessionID
	rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
	assertCrossTenantRefused(t, rr, "A copilot", marker, sw.tenantB.tenantID)
	after, err := store.GetSession(ctx, sw.tenantB.tenantID, sessionID, sw.tenantB.principalID)
	if err != nil || !reflect.DeepEqual(saved, after) || !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) {
		t.Errorf("copilot read changed B records: %v", err)
	}
	owner := sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, owner, "B copilot control")
	if !strings.Contains(owner.Body.String(), marker) {
		t.Error("B copilot control missing seeded session")
	}
}

func crossTenantPlatformGovernance(path, name string) crossTenantCase {
	return crossTenantCase{"GET " + path, name, func(t *testing.T, sw *crossTenantSweep) {
		id, marker := seedCrossTenantJob(t, sw)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		record := model.DecisionLogRecord{
			JobID: id, Tenant: sw.tenantB.tenantID, Topic: "job.platform", AgentID: "platform-b-agent",
			Verdict: model.SafetyRequireApproval, RuleID: marker, Reason: marker, Timestamp: time.Now().Add(-time.Second).UnixMilli(),
		}
		if err := sw.server.decisionLogStore.AppendDecision(ctx, record); err != nil {
			t.Fatal(err)
		}
		before, meta := crossTenantPlatformDecisions(t, sw), crossTenantJobMeta(t, sw, id)
		query := path + "?tenant=" + sw.tenantB.tenantID
		switch name {
		case "governance-decisions":
			query += "&agent_id=platform-b-agent"
		case "approval-analytics":
			query += "&group_by=rule"
		}
		rr := sw.as(t, sw.tenantA, http.MethodGet, query, "")
		if name == "governance-health" {
			assertCrossTenantRefused(t, rr, name, marker)
		} else {
			assertCrossTenantAbsent(t, rr, name, marker, id, sw.tenantB.tenantID)
		}
		if !reflect.DeepEqual(before, crossTenantPlatformDecisions(t, sw)) || !reflect.DeepEqual(meta, crossTenantJobMeta(t, sw, id)) {
			t.Error("governance read changed B records")
		}
		owner := sw.as(t, sw.tenantB, http.MethodGet, query, "")
		assertCrossTenantAbsent(t, owner, "B governance control")
		if name != "governance-health" && !strings.Contains(owner.Body.String(), marker) {
			t.Error("B governance control missing seeded decision")
		}
	}}
}

func crossTenantPlatformDecisions(t *testing.T, sw *crossTenantSweep) model.DecisionPage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	page, err := sw.server.decisionLogStore.QueryDecisions(ctx, model.DecisionQuery{Tenant: sw.tenantB.tenantID})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("B decision snapshot missing: %v (%d items)", err, len(page.Items))
	}
	return page
}

func seedCrossTenantPlatformEval(t *testing.T, sw *crossTenantSweep) (model.EvalDataset, runner.RunResult, string) {
	t.Helper()
	_, marker := seedCrossTenantJob(t, sw)
	evalRunRateLimiter.reset()
	t.Cleanup(evalRunRateLimiter.reset)
	seedPolicyBundle(t, sw.server, "secops/platform", "rules:\n  - id: allow\n    match:\n      topics: [job.*]\n    decision: allow\n")
	body := strings.ReplaceAll(string(evalCreateBody("platform-eval-b", 1, 1)), testEvalTenant, sw.tenantB.tenantID)
	body = strings.ReplaceAll(body, "test dataset", marker)
	body = strings.ReplaceAll(body, "entry-0", marker)
	rr := sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/evals/datasets", body)
	assertCrossTenantAbsent(t, rr, "B dataset seed")
	var dataset model.EvalDataset
	if err := json.Unmarshal(rr.Body.Bytes(), &dataset); err != nil || dataset.ID == "" {
		t.Fatalf("decode dataset: %v", err)
	}
	rr = sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/evals/datasets/"+dataset.ID+"/run", "{}")
	assertCrossTenantAbsent(t, rr, "B run seed")
	var run runner.RunResult
	if err := json.Unmarshal(rr.Body.Bytes(), &run); err != nil || run.RunID == "" {
		t.Fatalf("decode run: %v", err)
	}
	return dataset, run, marker
}

func crossTenantPlatformEvalSnapshot(t *testing.T, sw *crossTenantSweep) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	datasets, err := sw.server.evalDatasetStore.ListEvalDatasets(ctx, sw.tenantB.tenantID, model.EvalDatasetFilter{}, "", 200)
	if err != nil || len(datasets.Items) == 0 {
		t.Fatalf("B datasets snapshot: %v", err)
	}
	runs, err := sw.server.evalRunStore.ListRuns(ctx, sw.tenantB.tenantID, store.RunFilter{}, "", 200)
	if err != nil || len(runs.Items) == 0 {
		t.Fatalf("B runs snapshot: %v", err)
	}
	data, err := json.Marshal(struct {
		Datasets model.EvalDatasetPage
		Runs     store.RunPage
	}{datasets, runs})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func crossTenantPlatformEvalRequest(t *testing.T, sw *crossTenantSweep, method, path, body string, absent bool) {
	t.Helper()
	dataset, run, marker := seedCrossTenantPlatformEval(t, sw)
	path = strings.NewReplacer("{id}", dataset.ID, "{name}", dataset.Name, "{run}", run.RunID).Replace(path)
	before, mark := crossTenantPlatformEvalSnapshot(t, sw), sw.publishMark()
	rr := sw.as(t, sw.tenantA, method, path, body)
	if absent {
		assertCrossTenantAbsent(t, rr, "A eval", marker, sw.tenantB.tenantID)
	} else {
		assertCrossTenantRefused(t, rr, "A eval", marker, sw.tenantB.tenantID)
	}
	if before != crossTenantPlatformEvalSnapshot(t, sw) || len(sw.publishedSince(mark)) != 0 {
		t.Error("A request changed B eval records or published")
	}
	owner := sw.as(t, sw.tenantB, method, path, body)
	assertCrossTenantAbsent(t, owner, "B eval control")
	if method == http.MethodGet && !strings.Contains(owner.Body.String(), marker) {
		t.Error("B eval control missing its seeded data")
	}
}

func crossTenantPlatformEvalSubroutes(t *testing.T, _ *crossTenantSweep) {
	for _, tc := range []struct {
		method, path, body string
		absent             bool
	}{
		{http.MethodGet, "{id}", "", false},
		{http.MethodPut, "{id}", string(evalCreateBody("successor", 2, 1)), false},
		{http.MethodDelete, "{id}?force=true", "", false},
		{http.MethodGet, "by-name/{name}", "", true},
		{http.MethodGet, "by-name/{name}/versions/1", "", false},
		{http.MethodPost, "{id}/run", "{}", false},
		{http.MethodGet, "{id}/runs", "", false},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			crossTenantPlatformEvalRequest(t, newCrossTenantSweep(t), tc.method, "/api/v1/evals/datasets/"+tc.path, tc.body, tc.absent)
		})
	}
}

func crossTenantPlatformEvalCreate(t *testing.T, sw *crossTenantSweep) {
	_, _, marker := seedCrossTenantPlatformEval(t, sw)
	body := fmt.Sprintf(`{"tenant":%q,`, sw.tenantB.tenantID) + strings.TrimPrefix(string(evalCreateBody("platform-new", 1, 1)), "{")
	before, mark := crossTenantPlatformEvalSnapshot(t, sw), sw.publishMark()
	rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/evals/datasets", body)
	assertCrossTenantAbsent(t, rr, "A create", marker, sw.tenantB.tenantID)
	var created model.EvalDataset
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil || created.Tenant != sw.tenantA.tenantID {
		t.Fatalf("A dataset tenant: %v, %q", err, created.Tenant)
	}
	if before != crossTenantPlatformEvalSnapshot(t, sw) || len(sw.publishedSince(mark)) != 0 {
		t.Error("A create changed B records or published")
	}
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/evals/datasets/"+created.ID, ""), "A stored dataset", marker, sw.tenantB.tenantID)
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/evals/datasets", body), "B create control")
}

func crossTenantPlatformEvalIncidents(t *testing.T, sw *crossTenantSweep) {
	_, _, marker := seedCrossTenantPlatformEval(t, sw)
	id, incidentMarker := seedCrossTenantJob(t, sw)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	incidentExtractionRateLimiter.reset()
	t.Cleanup(incidentExtractionRateLimiter.reset)
	if err := sw.server.decisionLogStore.AppendDecision(ctx, model.DecisionLogRecord{
		JobID: id, Tenant: sw.tenantB.tenantID, Topic: "job." + incidentMarker, Verdict: model.SafetyDeny,
		RuleID: incidentMarker, Reason: incidentMarker, Timestamp: time.Now().Add(-time.Second).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	before, decisions, mark := crossTenantPlatformEvalSnapshot(t, sw), crossTenantPlatformDecisions(t, sw), sw.publishMark()
	body := fmt.Sprintf(`{"name":"platform-incidents","tenant":%q}`, sw.tenantB.tenantID)
	path := "/api/v1/evals/datasets/from-incidents"
	rr := sw.as(t, sw.tenantA, http.MethodPost, path, body)
	assertCrossTenantRefused(t, rr, "A extraction", marker, incidentMarker)
	if before != crossTenantPlatformEvalSnapshot(t, sw) || !reflect.DeepEqual(decisions, crossTenantPlatformDecisions(t, sw)) || len(sw.publishedSince(mark)) != 0 {
		t.Error("A extraction changed B records or published")
	}
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/evals/datasets", ""), "A datasets", marker, incidentMarker)
	owner := sw.as(t, sw.tenantB, http.MethodPost, path, body)
	assertCrossTenantAbsent(t, owner, "B extraction control")
	if !strings.Contains(crossTenantPlatformEvalSnapshot(t, sw), incidentMarker) {
		t.Error("B extraction control did not retain its incident")
	}
}

func crossTenantPlatformAdmin(t *testing.T, sw *crossTenantSweep, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return crossTenantPlatformSend(t, sw, crossTenantPlatformRoot(sw), crossTenantPlatformJSON(method, path, body)())
}

// crossTenantPlatformRoot is the AllowCrossTenant platform admin that seeds
// system state and runs the positive controls.
func crossTenantPlatformRoot(sw *crossTenantSweep) *auth.AuthContext {
	return &auth.AuthContext{Role: "admin", Tenant: sw.tenantB.tenantID, AllowCrossTenant: true}
}

func crossTenantPlatformJSON(method, path, body string) func() *http.Request {
	return func() *http.Request {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return req
	}
}

// crossTenantPlatformSend serves req with tenant A's API key when ac is nil,
// otherwise with ac through the mux.
func crossTenantPlatformSend(t *testing.T, sw *crossTenantSweep, ac *auth.AuthContext, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	if ac == nil {
		req.Header.Set("X-API-Key", sw.tenantA.apiKey)
		req.Header.Set("X-Tenant-ID", sw.tenantA.tenantID)
		sw.handler.ServeHTTP(rr, req)
		return rr
	}
	req.Header.Set("X-Tenant-ID", ac.Tenant)
	sw.mux.ServeHTTP(rr, withAuth(req, ac))
	return rr
}

func crossTenantPlatformConfigBody(target systemConfigTarget, value string) string {
	data := fmt.Sprintf(`{"private_%s":%q}`, target.id, value)
	if target.scope == "system" && target.id == "default" {
		data = fmt.Sprintf(`{"private_default":%q,"pools":{"topics":{},"pools":{}}}`, value)
	}
	if target.scope == "system" && target.id == "topics" {
		data = fmt.Sprintf(`{"job.platform-config-control":{"name":"job.platform-config-control","pack_id":%q}}`, value)
	}
	if target.name == "flat" {
		return data
	}
	return fmt.Sprintf(`{"scope":%q,"scope_id":%q,"data":%s}`, target.scope, target.id, data)
}

func seedCrossTenantPlatformConfig(t *testing.T, sw *crossTenantSweep) (map[string]systemConfigTarget, string) {
	t.Helper()
	id, marker := seedCrossTenantJob(t, sw)
	targets := make(map[string]systemConfigTarget)
	for _, scope := range []string{"org", "team", "workflow", "step"} {
		scopeID := "platform-" + scope + "-" + id
		if scope == "org" {
			scopeID = sw.tenantB.tenantID
		}
		target := systemConfigTarget{scope, scope, scopeID}
		targets[scope] = target
		body := crossTenantPlatformConfigBody(target, marker+"-"+scope)
		assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPut, "/api/v1/config", body), "B config seed")
	}
	for _, name := range []string{"default", "policy", "policy_snapshots", "policy_audit", "topics"} {
		target := systemConfigTarget{name, "system", name}
		targets[name] = target
		if name == "topics" {
			continue // B registers this document through the topic route below.
		}
		body := crossTenantPlatformConfigBody(target, marker+"-"+name)
		assertCrossTenantAbsent(t, crossTenantPlatformAdmin(t, sw, http.MethodPut, "/api/v1/config", body), "platform config seed")
	}
	targets["flat"] = systemConfigTarget{"flat", "system", "default"}
	topic := "job." + marker + "-topics"
	for _, seed := range []struct{ path, body string }{
		{"/api/v1/pools/platform-config-b", fmt.Sprintf(`{"description":%q}`, marker+"-pool")},
		{"/api/v1/pools/platform-config-b/topics/" + topic, ""},
	} {
		assertCrossTenantAbsent(t, crossTenantPlatformAdmin(t, sw, http.MethodPut, seed.path, seed.body), "platform pool seed")
	}
	body := fmt.Sprintf(`{"name":%q,"pool":"platform-config-b"}`, topic)
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/topics", body), "B topic seed")
	return targets, marker
}

func crossTenantPlatformConfigState(t *testing.T, sw *crossTenantSweep) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rdb := sw.server.jobStore.Client()
	keys, err := rdb.Keys(ctx, "cfg:*").Result()
	if err != nil || len(keys) == 0 {
		t.Fatalf("config keys snapshot: %v", err)
	}
	state := make(map[string]string, len(keys))
	for _, key := range keys {
		state[key], err = rdb.Get(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
	}
	return state
}

func crossTenantPlatformConfigRead(t *testing.T, sw *crossTenantSweep, name string, effective bool) {
	t.Helper()
	targets, marker := seedCrossTenantPlatformConfig(t, sw)
	target := targets[name]
	path := "/api/v1/config?scope=" + target.scope + "&scope_id=" + target.id
	if name == "default" {
		path = "/api/v1/config?scope=system"
	}
	if effective {
		path = "/api/v1/config/effective?org_id=" + sw.tenantB.tenantID
		if target.scope != "org" && target.scope != "system" {
			path += "&" + target.scope + "_id=" + target.id
		}
	}
	before, mark := crossTenantPlatformConfigState(t, sw), sw.publishMark()
	rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusNotFound {
		assertCrossTenantRefused(t, rr, "A config read", marker)
	} else {
		assertCrossTenantAbsent(t, rr, "A config read", marker)
	}
	if !reflect.DeepEqual(before, crossTenantPlatformConfigState(t, sw)) || len(sw.publishedSince(mark)) != 0 {
		t.Error("A read changed config data, revisions, Redis bytes or published")
	}
	var owner *httptest.ResponseRecorder
	if effective || target.scope == "system" {
		owner = crossTenantPlatformAdmin(t, sw, http.MethodGet, path, "")
	} else {
		owner = sw.as(t, sw.tenantB, http.MethodGet, path, "")
	}
	assertCrossTenantAbsent(t, owner, "config owner control")
	if owner.Code != http.StatusOK || !strings.Contains(owner.Body.String(), marker+"-"+name) {
		t.Error("config owner control missing its stored scope data")
	}
	if (name == "default" || name == "topics") && !strings.Contains(owner.Body.String(), marker+"-topics") {
		t.Error("platform control missing B topic mapping or registration")
	}
}

func crossTenantPlatformConfigReads(t *testing.T, _ *crossTenantSweep) {
	for _, name := range []string{"org", "default", "topics"} {
		t.Run(name, func(t *testing.T) {
			crossTenantPlatformConfigRead(t, newCrossTenantSweep(t), name, false)
		})
	}
}

func crossTenantPlatformConfigEffective(t *testing.T, _ *crossTenantSweep) {
	for _, name := range []string{"org", "team", "workflow", "step", "default"} {
		t.Run(name, func(t *testing.T) {
			crossTenantPlatformConfigRead(t, newCrossTenantSweep(t), name, true)
		})
	}
}

func crossTenantPlatformConfigWriteEffects(t *testing.T, sw *crossTenantSweep, before map[string]string, mark int, rr *httptest.ResponseRecorder, target systemConfigTarget, marker string) {
	t.Helper()
	after := crossTenantPlatformConfigState(t, sw)
	// Compare every pre-existing B/system document; A may create its own scoped document.
	for key, raw := range before {
		if after[key] != raw {
			t.Errorf("A write changed B/system config data, revision or Redis bytes: %s", key)
		}
	}
	messages := sw.publishedSince(mark)
	if (rr.Code >= 400 || target.scope == "system") && len(messages) != 0 {
		t.Error("A refused or system-scope write published messages")
	}
	for _, message := range messages {
		if strings.Contains(message.packet.String(), sw.tenantB.tenantID) || strings.Contains(message.packet.String(), marker) {
			t.Error("A write published B tenant or marker")
		}
	}
}

func crossTenantPlatformConfigWrite(t *testing.T, sw *crossTenantSweep, name, method string) {
	t.Helper()
	targets, marker := seedCrossTenantPlatformConfig(t, sw)
	target := targets[name]
	body := crossTenantPlatformConfigBody(target, "A-update")
	before, mark := crossTenantPlatformConfigState(t, sw), sw.publishMark()
	rr := sw.as(t, sw.tenantA, method, "/api/v1/config", body)
	crossTenantPlatformConfigWriteEffects(t, sw, before, mark, rr, target, marker)
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusNotFound {
		assertCrossTenantRefused(t, rr, "A config write", marker)
	} else {
		assertCrossTenantAbsent(t, rr, "A config write", marker)
	}
	body = crossTenantPlatformConfigBody(target, marker+"-owner-update")
	var owner *httptest.ResponseRecorder
	if target.scope == "system" {
		owner = crossTenantPlatformAdmin(t, sw, method, "/api/v1/config", body)
	} else {
		owner = sw.as(t, sw.tenantB, method, "/api/v1/config", body)
	}
	assertCrossTenantAbsent(t, owner, "config write owner control")
	path := "/api/v1/config?scope=" + target.scope + "&scope_id=" + target.id
	if target.scope == "system" {
		owner = crossTenantPlatformAdmin(t, sw, http.MethodGet, path, "")
	} else {
		owner = sw.as(t, sw.tenantB, http.MethodGet, path, "")
	}
	assertCrossTenantAbsent(t, owner, "stored config owner control")
	if !strings.Contains(owner.Body.String(), marker+"-owner-update") {
		t.Error("config write owner control did not persist the update")
	}
}

func crossTenantPlatformConfigWrites(method string) func(*testing.T, *crossTenantSweep) {
	return func(t *testing.T, _ *crossTenantSweep) {
		for _, name := range []string{"org", "default", "policy", "policy_snapshots", "policy_audit", "topics", "flat"} {
			t.Run(name, func(t *testing.T) {
				crossTenantPlatformConfigWrite(t, newCrossTenantSweep(t), name, method)
			})
		}
	}
}

// seedCrossTenantPlatformSystem turns Enterprise RBAC on, so the built-in
// operator role holds packs.install without config.write, and seeds B's config.
func seedCrossTenantPlatformSystem(t *testing.T, sw *crossTenantSweep) (map[string]systemConfigTarget, string) {
	t.Helper()
	setTestEntitlements(t, sw.server, licensing.PlanEnterprise, nil)
	return seedCrossTenantPlatformConfig(t, sw)
}

// crossTenantPlatformSystemState covers the license, telemetry mode, config
// documents (pack registry, topics, workers, B's documents), schemas with
// their schema:url: aliases and owners, and workflows.
func crossTenantPlatformSystemState(t *testing.T, sw *crossTenantSweep) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rdb := sw.server.jobStore.Client()
	state := map[string]any{"license": sw.server.currentLicenseInfo()}
	if sw.server.telemetry != nil {
		state["telemetry"] = sw.server.telemetry.Mode()
	}
	for _, pattern := range []string{"cfg:*", "schema:*", "cordum:schema:*", "wf:def:*", "wf:index:*", "cordum:telemetry:consent:*"} {
		keys, err := rdb.Keys(ctx, pattern).Result()
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			var value any
			switch kind := rdb.Type(ctx, key).Val(); kind {
			case "string":
				value, err = rdb.Get(ctx, key).Result()
			case "hash":
				value, err = rdb.HGetAll(ctx, key).Result()
			case "zset":
				value, err = rdb.ZRangeWithScores(ctx, key, 0, -1).Result()
			default:
				t.Fatalf("system state key %s has unexpected type %q", key, kind)
			}
			if err != nil {
				t.Fatal(err)
			}
			state[key] = value
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// crossTenantPlatformSystemAttempts sends req as A's admin API key and as a
// tenant-A operator. Each attempt must leave the system state and B's
// documents unchanged, publish nothing, and return no B data.
func crossTenantPlatformSystemAttempts(t *testing.T, sw *crossTenantSweep, marker string, req func() *http.Request) {
	t.Helper()
	operator := &auth.AuthContext{Role: "operator", Tenant: sw.tenantA.tenantID, PrincipalID: "principal-operator-a"}
	for label, ac := range map[string]*auth.AuthContext{"A admin": nil, "A operator": operator} {
		before, mark := crossTenantPlatformSystemState(t, sw), sw.publishMark()
		rr := crossTenantPlatformSend(t, sw, ac, req())
		if rr.Code == http.StatusForbidden || rr.Code == http.StatusNotFound {
			assertCrossTenantRefused(t, rr, label, marker, sw.tenantB.tenantID)
		} else {
			assertCrossTenantAbsent(t, rr, label, marker, sw.tenantB.tenantID)
		}
		if before != crossTenantPlatformSystemState(t, sw) || len(sw.publishedSince(mark)) != 0 {
			t.Errorf("%s changed system state or B documents, or published", label)
		}
	}
}

func assertCrossTenantPlatformControl(t *testing.T, rr *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), want) {
		t.Errorf("platform control: status=%d, want 200 with %q; body=%s", rr.Code, want, rr.Body.String())
	}
}

func crossTenantPlatformLicenseReload(t *testing.T, sw *crossTenantSweep) {
	_, marker := seedCrossTenantPlatformSystem(t, sw)
	installReloadableLicense(t, licensing.Claims{OrgID: "platform-org", LicenseID: "platform-license", Plan: "enterprise",
		ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339)})
	req := crossTenantPlatformJSON(http.MethodPost, "/api/v1/license/reload", "")
	crossTenantPlatformSystemAttempts(t, sw, marker, req)
	assertCrossTenantPlatformControl(t, crossTenantPlatformSend(t, sw, crossTenantPlatformRoot(sw), req()), "platform-license")
}

func crossTenantPlatformConsent(t *testing.T, sw *crossTenantSweep) {
	_, marker := seedCrossTenantPlatformSystem(t, sw)
	seedTelemetryCollector(t, sw.server)
	sw.server.telemetryState = telemetry.NewStoreWithClient(sw.server.jobStore.Client())
	if err := sw.server.telemetryState.SetConsentMode(context.Background(), "local_only"); err != nil {
		t.Fatal(err)
	}
	req := crossTenantPlatformJSON(http.MethodPost, "/api/v1/telemetry/consent", `{"mode":"off"}`)
	crossTenantPlatformSystemAttempts(t, sw, marker, req)
	assertCrossTenantPlatformControl(t, crossTenantPlatformSend(t, sw, crossTenantPlatformRoot(sw), req()), "updated")
	if sw.server.telemetry.Mode() != telemetry.ModeOff {
		t.Error("platform consent control was not applied")
	}
}

// crossTenantPlatformPackBundle builds a pack whose pools overlay targets
// scope/scopeID and which carries schemaID with a $id URL alias.
func crossTenantPlatformPackBundle(t *testing.T, id, scope, scopeID, schemaID string) []byte {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: cordum.io/v1alpha1
kind: Pack
metadata: {id: %[1]s, version: 0.1.0}
compatibility: {protocolVersion: 1}
topics: [{name: job.%[1]s.collect}]
resources:
  schemas: [{id: %[4]q, path: schemas/s.json}]
overlays:
  config: [{name: pools, scope: %[2]s, scope_id: %[3]q, key: pools, strategy: json_merge_patch, path: overlays/pools.yaml}]
tests:
  policySimulations: [{name: simulate, request: {topic: job.%[1]s.collect}}]
`, id, scope, scopeID, schemaID)
	return buildTarGz(t, map[string]string{
		"pack.yaml":           manifest,
		"schemas/s.json":      fmt.Sprintf(`{"$id":"https://schemas.example.test/%s","type":"object"}`, id),
		"overlays/pools.yaml": fmt.Sprintf("topics: {job.%[1]s.collect: [%[1]s]}\npools: {%[1]s: {requires: []}}\n", id),
	})
}

func crossTenantPlatformInstall(t *testing.T, bundle []byte) func() *http.Request {
	return func() *http.Request {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("bundle", "pack.tgz")
		if err == nil {
			_, err = part.Write(bundle)
		}
		if err == nil {
			err = writer.WriteField("upgrade", "true")
		}
		if err == nil {
			err = writer.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/packs/install", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		return req
	}
}

// seedCrossTenantPlatformPack has the platform admin install a pack with an
// overlay on B's org document.
func seedCrossTenantPlatformPack(t *testing.T, sw *crossTenantSweep, id string) {
	t.Helper()
	crossTenantPlatformMapTopic(t, sw, id)
	bundle := crossTenantPlatformPackBundle(t, id, "org", sw.tenantB.tenantID, id+"/S")
	assertCrossTenantPlatformControl(t, crossTenantPlatformSend(t, sw, crossTenantPlatformRoot(sw), crossTenantPlatformInstall(t, bundle)()), id)
	assertCrossTenantPlatformOrgOverlay(t, sw, id, true)
}

// crossTenantPlatformMapTopic maps the pack's topic to the seeded pool, which
// the install's topic registration requires.
func crossTenantPlatformMapTopic(t *testing.T, sw *crossTenantSweep, id string) {
	t.Helper()
	path := "/api/v1/pools/platform-config-b/topics/job." + id + ".collect"
	assertCrossTenantAbsent(t, crossTenantPlatformAdmin(t, sw, http.MethodPut, path, ""), "platform topic seed")
}

func assertCrossTenantPlatformOrgOverlay(t *testing.T, sw *crossTenantSweep, id string, want bool) {
	t.Helper()
	topic := "job." + id + ".collect"
	rr := sw.as(t, sw.tenantB, http.MethodGet, "/api/v1/config?scope=org&scope_id="+sw.tenantB.tenantID, "")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), topic) != want {
		t.Errorf("B org document: status=%d, want overlay topic %s present=%t; body=%s", rr.Code, topic, want, rr.Body.String())
	}
}

func crossTenantPlatformPackInstall(t *testing.T, sw *crossTenantSweep) {
	targets, marker := seedCrossTenantPlatformSystem(t, sw)
	schemaID := "platform-b/" + marker
	body := fmt.Sprintf(`{"id":%q,"schema":{"type":"object","description":%q}}`, schemaID, marker)
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/schemas", body), "B schema seed")
	overlays := []systemConfigTarget{{"org", "org", sw.tenantB.tenantID}, {"system", "system", "default"}}
	for _, scope := range []string{"team", "workflow", "step"} {
		id := targets[scope].id
		overlays = append(overlays, systemConfigTarget{scope, scope, id}, systemConfigTarget{scope, scope, sw.tenantB.tenantID + "/" + id})
	}
	for i, overlay := range overlays {
		id := fmt.Sprintf("xt-install-%d", i)
		crossTenantPlatformMapTopic(t, sw, id)
		bundle := crossTenantPlatformPackBundle(t, id, overlay.scope, overlay.id, schemaID)
		crossTenantPlatformSystemAttempts(t, sw, marker, crossTenantPlatformInstall(t, bundle))
	}
	seedCrossTenantPlatformPack(t, sw, "xt-install-control")
}

// crossTenantPlatformPackAction covers uninstall and verify of a pack the
// platform admin installed with an overlay on B's org document.
func crossTenantPlatformPackAction(action, body string) func(*testing.T, *crossTenantSweep) {
	return func(t *testing.T, sw *crossTenantSweep) {
		_, marker := seedCrossTenantPlatformSystem(t, sw)
		id := "xt-" + action
		seedCrossTenantPlatformPack(t, sw, id)
		req := crossTenantPlatformJSON(http.MethodPost, "/api/v1/packs/"+id+"/"+action, body)
		crossTenantPlatformSystemAttempts(t, sw, marker, req)
		if action == "verify" {
			assertCrossTenantPlatformControl(t, crossTenantPlatformSend(t, sw, crossTenantPlatformRoot(sw), req()), "simulate")
			return
		}
		assertCrossTenantPlatformControl(t, crossTenantPlatformSend(t, sw, crossTenantPlatformRoot(sw), req()), "DISABLED")
		assertCrossTenantPlatformOrgOverlay(t, sw, id, false)
	}
}

func crossTenantPlatformMarketplace(t *testing.T, sw *crossTenantSweep) {
	_, marker := seedCrossTenantPlatformSystem(t, sw)
	const id = "xt-market"
	crossTenantPlatformMapTopic(t, sw, id)
	bundle := crossTenantPlatformPackBundle(t, id, "org", sw.tenantB.tenantID, id+"/S")
	sum := sha256.Sum256(bundle)
	catalog := packs.MarketplaceCatalogFile{UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Packs: []packs.MarketplaceCatalogPack{{ID: id, Version: "0.1.0", Title: id, Sha256: hex.EncodeToString(sum[:])}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog.json" {
			_ = json.NewEncoder(w).Encode(catalog)
			return
		}
		_, _ = w.Write(bundle)
	}))
	t.Cleanup(srv.Close)
	catalog.Packs[0].URL = srv.URL + "/" + id + ".tgz"
	if err := sw.server.configSvc.Set(context.Background(), &configsvc.Document{Scope: configsvc.ScopeSystem, ScopeID: packs.PackCatalogID,
		Data: map[string]any{"catalogs": []any{map[string]any{"id": "official", "url": srv.URL + "/catalog.json", "enabled": true}}}}); err != nil {
		t.Fatal(err)
	}
	req := crossTenantPlatformJSON(http.MethodPost, "/api/v1/marketplace/install", `{"catalog_id":"official","pack_id":"`+id+`"}`)
	crossTenantPlatformSystemAttempts(t, sw, marker, req)
	assertCrossTenantPlatformControl(t, crossTenantPlatformSend(t, sw, crossTenantPlatformRoot(sw), req()), id)
	assertCrossTenantPlatformOrgOverlay(t, sw, id, true)
}
