package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/registry"
)

const fleetWorkerID, fleetPool, fleetTopic = "fleet-worker-b", "fleet-pool-b", "job.fleet-b"
const fleetCapability = "private-fleet-capability-b"

func init() {
	registerCrossTenantFamily("PR1-02-FLEET",
		crossTenantCase{Route: "GET /api/v1/workers/{id}", Name: "worker-detail", Run: crossTenantFleetWorkerDetail},
		crossTenantCase{Route: "GET /api/v1/workers/{id}/jobs", Name: "worker-jobs", Run: crossTenantFleetWorkerJobs},
		crossTenantCase{Route: "POST /api/v1/workers/{id}/revoke-session", Name: "worker-session", Run: crossTenantFleetWorkerSession},
		crossTenantCase{Route: "POST /api/v1/workers/credentials", Name: "worker-credential", Run: crossTenantFleetWorkerCredential},
		crossTenantCase{Route: "PUT /api/v1/pools/{name}", Name: "pool-create", Run: crossTenantFleetPoolCreate},
	)
}

func TestCrossTenantSweepFleet(t *testing.T) { runCrossTenantFamily(t, "PR1-02-FLEET") }

type crossTenantFleetSeed struct {
	ctx                      context.Context
	credentialToken, session string
	jobID                    string
}

type crossTenantFleetState struct {
	credential *workercredentials.Credential
	config     *configsvc.Document
	job        map[string]string
}

func seedCrossTenantFleet(t *testing.T, sw *crossTenantSweep) crossTenantFleetSeed {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	seed := crossTenantFleetSeed{ctx: ctx}
	seedWorkerCredentialAccessConfig(t, sw.server)
	rr := sw.as(t, sw.tenantB, http.MethodPut, "/api/v1/pools/"+fleetPool, `{"description":"private-fleet-description-b"}`)
	assertCrossTenantAbsent(t, rr, "owner pool seed")
	rr = sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/topics", fmt.Sprintf(`{"name":%q,"pool":%q,"status":"active"}`, fleetTopic, fleetPool))
	assertCrossTenantAbsent(t, rr, "owner topic seed")
	rr = sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/workers/credentials", fleetCredentialBody())
	assertCrossTenantAbsent(t, rr, "owner credential seed")
	var issued issueWorkerCredentialResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &issued); err != nil || issued.WorkerID != fleetWorkerID || issued.Token == "" {
		t.Fatalf("owner credential decode: %v", err)
	}
	seed.credentialToken = issued.Token
	issuer, resolver := newTrustResolverForTest(t, sw.server)
	sw.server.sessionIssuer = issuer
	sw.server.WithTrustResolver(resolver)
	var err error
	seed.session, _, err = issuer.Issue(ctx, fleetWorkerID, sw.tenantB.tenantID, "v1")
	if err != nil {
		t.Fatalf("owner session seed: %v", err)
	}
	seedSnapshot(t, sw.server, registry.Snapshot{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		Workers: []registry.WorkerSummary{{WorkerID: fleetWorkerID, Pool: fleetPool,
			MaxParallelJobs: 1, Capabilities: []string{fleetCapability}}},
		Pools:  map[string]registry.PoolSnapshot{fleetPool: {Workers: 1, Capacity: 1}},
		Topics: map[string]registry.TopicSnapshot{fleetTopic: {Pool: fleetPool, Workers: 1, Capacity: 1, Available: true}},
	})
	rr = sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/jobs", fmt.Sprintf(`{"prompt":"private-fleet-job-b","topic":%q}`, fleetTopic))
	assertCrossTenantAbsent(t, rr, "owner job seed")
	var job struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &job); err != nil || job.JobID == "" {
		t.Fatalf("owner job decode: %v", err)
	}
	seed.jobID = job.JobID
	fleetState(t, sw, seed)
	return seed
}

func fleetCredentialBody() string {
	return fmt.Sprintf(`{"worker_id":%q,"allowed_pools":[%q],"allowed_topics":["job.external"]}`, fleetWorkerID, fleetPool)
}

func fleetState(t *testing.T, sw *crossTenantSweep, seed crossTenantFleetSeed) crossTenantFleetState {
	t.Helper()
	record, valid, err := sw.server.workerCredentialStore.Verify(seed.ctx, fleetWorkerID, seed.credentialToken)
	if err != nil || !valid || record == nil || record.TenantID != sw.tenantB.tenantID {
		t.Fatalf("owner credential invalid: valid=%v, err=%v", valid, err)
	}
	claims, err := sw.server.sessionIssuer.Verify(seed.ctx, seed.session, true)
	if err != nil || claims.Subject != fleetWorkerID || claims.Tenant != sw.tenantB.tenantID {
		t.Fatalf("owner session invalid: %v", err)
	}
	doc, err := sw.server.configSvc.Get(seed.ctx, configsvc.ScopeSystem, "default")
	if err != nil || doc == nil {
		t.Fatalf("owner pool config unavailable: %v", err)
	}
	return crossTenantFleetState{record, doc, crossTenantJobMeta(t, sw, seed.jobID)}
}

func assertFleetUnchanged(t *testing.T, sw *crossTenantSweep, seed crossTenantFleetSeed, before crossTenantFleetState, mark int) {
	t.Helper()
	if !reflect.DeepEqual(before, fleetState(t, sw, seed)) {
		t.Error("tenant B records changed after tenant A write")
	}
	if got := sw.publishedSince(mark); len(got) != 0 {
		t.Errorf("tenant A write published %d messages", len(got))
	}
}

func crossTenantFleetWorkerDetail(t *testing.T, sw *crossTenantSweep) {
	seedCrossTenantFleet(t, sw)
	path := "/api/v1/workers/" + fleetWorkerID
	assertCrossTenantRefused(t, sw.as(t, sw.tenantA, http.MethodGet, path, ""), "A worker", fleetCapability)
	rr := sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, rr, "B worker")
	if !strings.Contains(rr.Body.String(), fleetCapability) {
		t.Fatal("owner worker missing private capability")
	}
}

func crossTenantFleetWorkerJobs(t *testing.T, sw *crossTenantSweep) {
	seed := seedCrossTenantFleet(t, sw)
	path := "/api/v1/workers/" + fleetWorkerID + "/jobs"
	assertCrossTenantRefused(t, sw.as(t, sw.tenantA, http.MethodGet, path, ""), "A worker jobs", seed.jobID, fleetTopic)
	rr := sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, rr, "B worker jobs")
	if !strings.Contains(rr.Body.String(), seed.jobID) {
		t.Fatal("owner worker jobs missing seeded job")
	}
}

func crossTenantFleetWorkerSession(t *testing.T, sw *crossTenantSweep) {
	seed := seedCrossTenantFleet(t, sw)
	before, mark := fleetState(t, sw, seed), sw.publishMark()
	path := "/api/v1/workers/" + fleetWorkerID + "/revoke-session"
	rr := sw.as(t, sw.tenantA, http.MethodPost, path, "")
	assertFleetUnchanged(t, sw, seed, before, mark)
	assertCrossTenantAbsent(t, rr, "A session revoke", seed.session, fleetCapability)
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPost, path, ""), "B session revoke")
	if _, err := sw.server.sessionIssuer.Verify(seed.ctx, seed.session, true); err == nil {
		t.Fatal("owner session revoke had no effect")
	}
}

func crossTenantFleetWorkerCredential(t *testing.T, sw *crossTenantSweep) {
	seed := seedCrossTenantFleet(t, sw)
	before, mark := fleetState(t, sw, seed), sw.publishMark()
	path := "/api/v1/workers/credentials"
	rr := sw.as(t, sw.tenantA, http.MethodPost, path, fleetCredentialBody())
	assertFleetUnchanged(t, sw, seed, before, mark)
	assertCrossTenantRefused(t, rr, "A credential", seed.credentialToken, sw.tenantB.tenantID)
	rr = sw.as(t, sw.tenantB, http.MethodPost, path, fleetCredentialBody())
	assertCrossTenantAbsent(t, rr, "B credential rotation")
	if _, valid, err := sw.server.workerCredentialStore.Verify(seed.ctx, fleetWorkerID, seed.credentialToken); err != nil || valid {
		t.Fatalf("owner credential rotation did not invalidate old token: valid=%v, err=%v", valid, err)
	}
}

func crossTenantFleetPoolCreate(t *testing.T, sw *crossTenantSweep) {
	seed := seedCrossTenantFleet(t, sw)
	before, mark := fleetState(t, sw, seed), sw.publishMark()
	path, body := "/api/v1/pools/"+fleetPool, `{"description":"replacement"}`
	rr := sw.as(t, sw.tenantA, http.MethodPut, path, body)
	assertFleetUnchanged(t, sw, seed, before, mark)
	assertCrossTenantNoLeaks(t, rr, "A duplicate pool", "private-fleet-description-b")
	if rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound && rr.Code != http.StatusConflict {
		t.Errorf("A duplicate pool: status=%d, want refusal", rr.Code)
	}
	// Creation needs an unused name; the B control starts with the same name absent.
	owner := newCrossTenantSweep(t)
	seedWorkerCredentialAccessConfig(t, owner.server)
	assertCrossTenantAbsent(t, owner.as(t, owner.tenantB, http.MethodPut, path, body), "B pool creation")
}
