package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func tenantObjectRequest(method, path, tenant, id string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("X-Tenant-ID", tenant)
	r.SetPathValue("id", id)
	r.SetPathValue("job_id", id)
	return withAuth(r, &auth.AuthContext{Tenant: tenant, PrincipalID: "reader", Role: "admin"})
}

// Snapshot values, not TTLs: time passing is not a mutation by the caller.
func tenantObjectSnapshot(t *testing.T, s *server) map[string]string {
	t.Helper()
	ctx := context.Background()
	c := s.jobStore.Client()
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		kind, err := c.Type(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "string":
			v, e := c.Get(ctx, key).Result()
			values[key], err = v, e
		case "hash":
			v, e := c.HGetAll(ctx, key).Result()
			values[key], err = fmt.Sprint(v), e
		case "list":
			v, e := c.LRange(ctx, key, 0, -1).Result()
			values[key], err = fmt.Sprint(v), e
		case "zset":
			v, e := c.ZRangeWithScores(ctx, key, 0, -1).Result()
			values[key], err = fmt.Sprint(v), e
		case "set":
			v, e := c.SMembers(ctx, key).Result()
			sort.Strings(v)
			values[key], err = fmt.Sprint(v), e
		default:
			t.Fatalf("unexpected snapshot type %q for %q", kind, key)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return values
}

func tenantObjectUnchanged(t *testing.T, s *server) {
	t.Helper()
	before := tenantObjectSnapshot(t, s)
	t.Cleanup(func() {
		if after := tenantObjectSnapshot(t, s); !reflect.DeepEqual(before, after) {
			t.Error("request changed stored records")
		}
	})
}

func seedTenantObjectJob(t *testing.T, s *server) string {
	t.Helper()
	ctx := context.Background()
	id := "private-job-b"
	req := &pb.JobRequest{JobId: id, TenantId: "tenant-b", Topic: "job.private-b",
		ContextPtr: "redis://ctx:" + id, Labels: map[string]string{"private": "payload-b"}}
	for _, err := range []error{
		s.jobStore.SetJobMeta(ctx, req), s.jobStore.SetJobRequest(ctx, req),
		s.jobStore.SetState(ctx, id, model.JobStateRunning),
		s.jobStore.SetState(ctx, id, model.JobStateQuarantined),
		s.jobStore.SetResultPtr(ctx, id, "redis://res:"+id),
		s.jobStore.SetSafetyDecision(ctx, id, model.SafetyDecisionRecord{Decision: model.SafetyDeny, Reason: "private-decision-b"}),
		s.jobStore.SetOutputSafety(ctx, id, model.OutputSafetyRecord{Decision: model.OutputQuarantine, RuleID: "private-rule-b", Reason: "private-output-b"}),
		s.dlqStore.Add(ctx, store.DLQEntry{JobID: id, Topic: req.Topic, Reason: "private-dlq-b", CreatedAt: time.Now().UTC()}),
		s.memStore.PutContext(ctx, "ctx:"+id, []byte(`{"secret":"private-input-b"}`)),
		s.memStore.PutResult(ctx, "res:"+id, []byte(`{"secret":"private-result-b"}`)),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	// Runtime state takes precedence over the legacy keys in GetJobStatus.
	key := "job:{" + base64.RawURLEncoding.EncodeToString([]byte(id)) + "}:runtime"
	if err := s.jobStore.Client().HSet(ctx, key, "state", string(model.JobStateQuarantined), "result_ptr", "redis://res:"+id).Err(); err != nil {
		t.Fatal(err)
	}
	return id
}

func expireTenantObjectJobMeta(t *testing.T, s *server, id string) {
	t.Helper()
	ctx := context.Background()
	c := s.jobStore.Client()
	if ok, err := c.PExpire(ctx, "job:meta:"+id, -time.Second).Result(); err != nil || !ok {
		t.Fatalf("expire metadata: %v, %v", ok, err)
	}
	if n, err := c.Exists(ctx, "job:meta:"+id).Result(); err != nil || n != 0 {
		t.Fatalf("metadata still present: %d, %v", n, err)
	}
	// Expiring the parent must not remove the child being tested.
	if entry, err := s.dlqStore.Get(ctx, id); err != nil || entry == nil || entry.Reason != "private-dlq-b" {
		t.Fatalf("DLQ child unavailable: %v, %v", entry, err)
	}
	if ptr, err := s.jobStore.GetResultPtr(ctx, id); err != nil || ptr != "redis://res:"+id {
		t.Fatalf("result child unavailable: %q, %v", ptr, err)
	}
	if decision, err := s.jobStore.GetOutputSafety(ctx, id); err != nil || decision.RuleID != "private-rule-b" {
		t.Fatalf("output child unavailable: %v, %v", decision, err)
	}
}

func TestTenantIsolationGatewayJobReads(t *testing.T) {
	for _, expired := range []bool{false, true} {
		for _, surface := range []string{"detail", "decisions", "stream", "dlq-list", "dlq-page", "dlq-delete", "policy-output"} {
			t.Run(fmt.Sprintf("%s/expired=%t", surface, expired), func(t *testing.T) {
				s, _, _ := newTestGateway(t)
				s.auth = newBasicAuthForTest(t, nil)
				id := seedTenantObjectJob(t, s)
				handlers := map[string]http.HandlerFunc{"detail": s.handleGetJob, "decisions": s.handleListJobDecisions,
					"stream": s.handleJobStream, "dlq-list": s.handleListDLQ, "dlq-page": s.handleListDLQPage,
					"dlq-delete": s.handleDeleteDLQ, "policy-output": s.handleListPolicyAudit}
				handler := handlers[surface]
				paths := map[string]string{"detail": "/api/v1/jobs/" + id, "decisions": "/api/v1/jobs/" + id + "/decisions",
					"stream": "/api/v1/jobs/" + id + "/stream", "dlq-list": "/api/v1/dlq", "dlq-page": "/api/v1/dlq/page",
					"dlq-delete": "/api/v1/dlq/" + id, "policy-output": "/api/v1/policy/audit?type=output"}
				path := paths[surface]
				if surface != "stream" && surface != "dlq-delete" {
					owner := httptest.NewRecorder()
					handler(owner, tenantObjectRequest(http.MethodGet, path, "tenant-b", id))
					if owner.Code != http.StatusOK || !strings.Contains(owner.Body.String(), "private-") {
						t.Fatalf("owner control did not return seeded data: %d %s", owner.Code, owner.Body.String())
					}
				}
				if expired {
					expireTenantObjectJobMeta(t, s, id)
				}
				tenantObjectUnchanged(t, s)
				rr := httptest.NewRecorder()
				method := http.MethodGet
				if surface == "dlq-delete" {
					method = http.MethodDelete
				}
				handler(rr, tenantObjectRequest(method, path, "tenant-a", id))
				if strings.Contains(rr.Body.String(), "private-") || strings.Contains(rr.Body.String(), "tenant-b") {
					t.Errorf("response released tenant B data: %s", rr.Body.String())
				}
				if rr.Code >= http.StatusInternalServerError {
					t.Errorf("infrastructure error is not an isolation result: %d", rr.Code)
				}
				if surface == "stream" && (rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound || len(s.clients) != 0) {
					t.Errorf("stream was not refused before upgrade: %d", rr.Code)
				}
			})
		}
	}
}

func TestTenantIsolationGatewayJobGRPC(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%t", expired), func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			id := seedTenantObjectJob(t, s)
			req := &pb.GetJobStatusRequest{JobId: id}
			ownerCtx := tenantObjectRequest(http.MethodGet, "/", "tenant-b", id).Context()
			owner, err := s.GetJobStatus(ownerCtx, req)
			if err != nil || owner.GetResultPtr() != "redis://res:"+id || owner.GetStatus() != string(model.JobStateQuarantined) {
				t.Fatalf("owner control: %v, %v", owner, err)
			}
			if expired {
				expireTenantObjectJobMeta(t, s, id)
			}
			tenantObjectUnchanged(t, s)
			ctx := tenantObjectRequest(http.MethodGet, "/", "tenant-a", id).Context()
			got, err := s.GetJobStatus(ctx, req)
			if err == nil || got != nil {
				t.Errorf("cross-tenant status returned data: %v, %v", got, err)
			}
		})
	}
}

func crossTenantJobRequest(method, path, id string) *http.Request {
	r := tenantObjectRequest(method, path, "tenant-a", id)
	auth.FromRequest(r).AllowCrossTenant = true
	return r
}

func tenantObjectJSON(t *testing.T, handler http.HandlerFunc, r *http.Request, out any) {
	t.Helper()
	rr := httptest.NewRecorder()
	handler(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", r.URL.Path, rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
}

type tenantObjectRoute struct {
	method, path string
	handler      http.HandlerFunc
}

func TestTenantIsolationGatewayJobWithoutTenant(t *testing.T) {
	const outputPath, statsPath = "/api/v1/policy/audit?type=output", "/api/v1/policy/output/stats"
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("cross-tenant/expired=%t", expired), func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = newBasicAuthForTest(t, nil)
			id := seedTenantObjectJob(t, s)
			if expired {
				expireTenantObjectJobMeta(t, s, id)
			}
			rr := httptest.NewRecorder()
			s.handleListJobDecisions(rr, crossTenantJobRequest(http.MethodGet, "/api/v1/jobs/"+id+"/decisions", id))
			if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "private-decision-b") {
				t.Errorf("decisions: %d %s", rr.Code, rr.Body.String())
			}
			got, err := s.GetJobStatus(crossTenantJobRequest(http.MethodGet, "/", id).Context(), &pb.GetJobStatusRequest{JobId: id})
			if err != nil || got.GetStatus() != string(model.JobStateQuarantined) || got.GetResultPtr() != "redis://res:"+id {
				t.Errorf("status: %v, %v", got, err)
			}
			var list struct {
				Items []map[string]any `json:"items"`
			}
			tenantObjectJSON(t, s.handleListPolicyAudit, crossTenantJobRequest(http.MethodGet, outputPath, id), &list)
			var stats map[string]any
			tenantObjectJSON(t, s.handlePolicyOutputStats, crossTenantJobRequest(http.MethodGet, statsPath, id), &stats)
			if len(list.Items) != 1 || list.Items[0]["job_id"] != id || stats["quarantined_24h"] != float64(1) {
				t.Errorf("output: %v %v", list.Items, stats)
			}
			rr = httptest.NewRecorder()
			s.handleDeleteDLQ(rr, crossTenantJobRequest(http.MethodDelete, "/api/v1/dlq/"+id, id))
			if _, err := s.dlqStore.Get(context.Background(), id); rr.Code != http.StatusNoContent || !errors.Is(err, redis.Nil) {
				t.Errorf("dlq delete: %d %s, entry lookup %v", rr.Code, rr.Body.String(), err)
			}
		})
	}
	t.Run("owner/expired=true", func(t *testing.T) {
		s, bus, _ := newTestGateway(t)
		s.auth = newBasicAuthForTest(t, nil)
		id := seedTenantObjectJob(t, s)
		expireTenantObjectJobMeta(t, s, id)
		tenantObjectUnchanged(t, s)
		published := len(bus.published)
		routes := []tenantObjectRoute{
			{http.MethodGet, "/api/v1/jobs/" + id + "/decisions", s.handleListJobDecisions},
			{http.MethodPost, "/api/v1/jobs/" + id + "/cancel", s.handleCancelJob},
			{http.MethodDelete, "/api/v1/dlq/" + id, s.handleDeleteDLQ},
			{http.MethodPost, "/api/v1/dlq/" + id + "/retry", s.handleRetryDLQ},
		}
		for _, tenant := range []string{"tenant-b", ""} {
			for _, route := range routes {
				rr := httptest.NewRecorder()
				route.handler(rr, tenantObjectRequest(route.method, route.path, tenant, id))
				if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), "private-") {
					t.Errorf("tenant %q %s %s: %d %s", tenant, route.method, route.path, rr.Code, rr.Body.String())
				}
			}
			ctx := tenantObjectRequest(http.MethodGet, "/", tenant, id).Context()
			if got, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: id}); status.Code(err) != codes.NotFound || got != nil {
				t.Errorf("tenant %q status: %v, %v", tenant, got, err)
			}
			var list struct {
				Items []map[string]any `json:"items"`
			}
			tenantObjectJSON(t, s.handleListPolicyAudit, tenantObjectRequest(http.MethodGet, outputPath, tenant, id), &list)
			var stats map[string]any
			tenantObjectJSON(t, s.handlePolicyOutputStats, tenantObjectRequest(http.MethodGet, statsPath, tenant, id), &stats)
			if len(list.Items) != 0 || stats["total_checks_24h"] != float64(0) {
				t.Errorf("tenant %q output: %v %v", tenant, list.Items, stats)
			}
		}
		if len(bus.published) != published {
			t.Errorf("published %d messages", len(bus.published)-published)
		}
	})
	t.Run("lookup-error", func(t *testing.T) {
		s, _, _ := newTestGateway(t)
		s.auth = newBasicAuthForTest(t, nil)
		id := seedTenantObjectJob(t, s)
		if err := s.jobStore.Client().Close(); err != nil {
			t.Fatal(err)
		}
		for _, route := range []tenantObjectRoute{
			{http.MethodGet, "/api/v1/jobs/" + id + "/decisions", s.handleListJobDecisions},
			{http.MethodDelete, "/api/v1/dlq/" + id, s.handleDeleteDLQ},
		} {
			rr := httptest.NewRecorder()
			route.handler(rr, tenantObjectRequest(route.method, route.path, "tenant-b", id))
			if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "private-") {
				t.Errorf("%s %s: %d %s", route.method, route.path, rr.Code, rr.Body.String())
			}
		}
		ctx := tenantObjectRequest(http.MethodGet, "/", "tenant-b", id).Context()
		if got, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: id}); status.Code(err) != codes.Unavailable || got != nil {
			t.Errorf("status: %v, %v", got, err)
		}
		if entry, err := s.dlqStore.Get(context.Background(), id); err != nil || entry == nil || entry.Reason != "private-dlq-b" {
			t.Errorf("DLQ entry: %v, %v", entry, err)
		}
	})
}
