package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/copilot"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/evals/runner"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", sw.tenantB.tenantID)
	admin := &auth.AuthContext{Role: "admin", Tenant: sw.tenantB.tenantID, AllowCrossTenant: true}
	rr := httptest.NewRecorder()
	sw.mux.ServeHTTP(rr, withAuth(req, admin))
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
