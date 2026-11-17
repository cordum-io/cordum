package gateway

import (
	"context"
	"encoding/base64"
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
	for _, tc := range []struct {
		surface string
		expired bool
	}{
		{"detail", false}, {"decisions", false}, {"stream", false},
		{"dlq-list", false}, {"dlq-page", false}, {"dlq-delete", false}, {"policy-output", false},
		{"detail", true}, {"stream", true}, {"dlq-list", true}, {"dlq-page", true},
	} {
		surface, expired := tc.surface, tc.expired
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

func TestTenantIsolationGatewayJobGRPC(t *testing.T) {
	s, _, _ := newTestGateway(t)
	id := seedTenantObjectJob(t, s)
	req := &pb.GetJobStatusRequest{JobId: id}
	ownerCtx := tenantObjectRequest(http.MethodGet, "/", "tenant-b", id).Context()
	owner, err := s.GetJobStatus(ownerCtx, req)
	if err != nil || owner.GetResultPtr() != "redis://res:"+id || owner.GetStatus() != string(model.JobStateQuarantined) {
		t.Fatalf("owner control: %v, %v", owner, err)
	}
	tenantObjectUnchanged(t, s)
	ctx := tenantObjectRequest(http.MethodGet, "/", "tenant-a", id).Context()
	got, err := s.GetJobStatus(ctx, req)
	if err == nil || got != nil {
		t.Errorf("cross-tenant status returned data: %v, %v", got, err)
	}
}
