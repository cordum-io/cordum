package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/google/uuid"
)

const (
	crossTenantGet, crossTenantPost, crossTenantDelete                   = http.MethodGet, http.MethodPost, http.MethodDelete
	crossTenantWantRefused, crossTenantWantAbsent, crossTenantWantEither = "refused", "absent", "either"
)

// crossTenantProbe is one call. Tenant-A probes expect want (refused, absent,
// either), or, when baseline is set, the same response A gets for that
// never-held body. Owner probes run as B and expect 2xx containing want.
type crossTenantProbe struct {
	name, method, path, body, want, baseline string
	owner                                    bool
	leaks                                    []string
	then                                     func(t *testing.T, sw *crossTenantSweep, rr *httptest.ResponseRecorder)
}

type crossTenantWorkflowFixture struct {
	marker, wf, aWF, run, idem, chatKey, job, jobMarker, trace, ptr, lock, topic, schemaID string
	resolver                                                                               *gatewayResourceResolver
	leaks                                                                                  []string
	snaps                                                                                  []func() any
}

type crossTenantSeed func(t *testing.T, sw *crossTenantSweep, f *crossTenantWorkflowFixture)

type crossTenantProbes func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe

func crossTenantA(name, method, path, body, want string, leaks ...string) crossTenantProbe {
	return crossTenantProbe{name: name, method: method, path: path, body: body, want: want, leaks: leaks}
}

func crossTenantB(method, path, body, want string) crossTenantProbe {
	return crossTenantProbe{name: "owner " + method + " " + path, method: method, path: path, body: body, want: want, owner: true}
}

func registerCrossTenantWorkflowCase(route, name string, probes crossTenantProbes, seeds ...crossTenantSeed) {
	registerCrossTenantFamily("PR1-02-WF", crossTenantCase{route, name, func(t *testing.T, sw *crossTenantSweep) {
		f := &crossTenantWorkflowFixture{marker: "private-" + uuid.NewString()}
		f.leaks = []string{f.marker, sw.tenantB.principalID}
		for _, seed := range seeds {
			seed(t, sw, f)
		}
		crossTenantDrainEvents(sw)
		probeList := probes(sw, f)
		before := f.state(t)
		for _, p := range probeList {
			if !p.owner {
				runCrossTenantProbe(t, sw, f, p)
			}
		}
		if after := f.state(t); after != before {
			t.Errorf("tenant-B state changed:\nbefore=%s\nafter=%s", before, after)
		}
		for _, p := range probeList {
			if !p.owner {
				continue
			}
			rr := sw.as(t, sw.tenantB, p.method, p.path, p.body)
			if rr.Code < 200 || rr.Code >= 300 || !strings.Contains(rr.Body.String(), p.want) {
				t.Errorf("%s: status=%d, want 2xx containing %q; body=%s", p.name, rr.Code, p.want, rr.Body.String())
			}
		}
	}})
}

func runCrossTenantProbe(t *testing.T, sw *crossTenantSweep, f *crossTenantWorkflowFixture, p crossTenantProbe) {
	t.Helper()
	label := "A " + p.name
	leaks := append(append([]string(nil), f.leaks...), p.leaks...)
	mark := sw.publishMark()
	rr := sw.as(t, sw.tenantA, p.method, p.path, p.body)
	ok := rr.Code >= 200 && rr.Code < 300
	switch {
	case p.baseline != "":
		base := sw.as(t, sw.tenantA, p.method, p.path, p.baseline)
		if rr.Code != base.Code || rr.Body.String() != base.Body.String() {
			t.Errorf("%s: status=%d body=%s, want never-held response %d %s", label, rr.Code, rr.Body.String(), base.Code, base.Body.String())
		}
		assertCrossTenantNoLeaks(t, rr, label, leaks...)
	case p.want == crossTenantWantAbsent || p.want == crossTenantWantEither && ok:
		assertCrossTenantAbsent(t, rr, label, leaks...)
	default:
		assertCrossTenantRefused(t, rr, label, leaks...)
	}
	if p.then != nil {
		p.then(t, sw, rr)
	}
	var effects []string
	for _, m := range sw.publishedSince(mark) {
		effects = append(effects, fmt.Sprintf("%s %v", m.subject, m.packet))
	}
	for _, ev := range crossTenantDrainEvents(sw) {
		effects = append(effects, ev.tenant+" "+ev.jobID+" "+string(ev.data))
	}
	if !ok && len(effects) != 0 {
		t.Errorf("%s: refused call emitted %d packets or events", label, len(effects))
	}
	for _, effect := range effects {
		for _, leak := range append(leaks, sw.tenantB.tenantID) {
			if strings.Contains(effect, leak) {
				t.Errorf("%s: emitted packet or event carries tenant-B data %q", label, leak)
			}
		}
	}
}

func crossTenantDrainEvents(sw *crossTenantSweep) []wsEvent {
	var events []wsEvent
	for {
		select {
		case ev := <-sw.server.eventsCh:
			events = append(events, ev)
		default:
			return events
		}
	}
}

func (f *crossTenantWorkflowFixture) state(t *testing.T) string {
	t.Helper()
	parts := make([]any, len(f.snaps))
	for i, snap := range f.snaps {
		parts[i] = snap()
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func crossTenantSnap(v any, err error) any { return []any{v, fmt.Sprint(err)} }

func crossTenantField(t *testing.T, rr *httptest.ResponseRecorder, label, field string) string {
	t.Helper()
	assertCrossTenantAbsent(t, rr, label)
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode: %v; %s", label, err, rr.Body.String())
	}
	value, _ := body[field].(string)
	if value == "" {
		t.Fatalf("%s: missing %s: %s", label, field, rr.Body.String())
	}
	return value
}

func crossTenantWorkflowBody(id, org, name string) string {
	return fmt.Sprintf(`{"id":%q,"org_id":%q,"name":%q,"steps":{"step":{"type":"worker","topic":"job.default"}}}`, id, org, name)
}

func seedCrossTenantWorkflow(t *testing.T, sw *crossTenantSweep, f *crossTenantWorkflowFixture) {
	t.Helper()
	s, ctx := sw.server, context.Background()
	s.workflowEng = wf.NewEngine(s.workflowStore, sw.bus).WithMemory(s.memStore).WithConfig(s.configSvc).WithSchemaRegistry(s.schemaRegistry)
	f.idem = "idem-" + uuid.NewString()
	f.wf = crossTenantField(t, sw.as(t, sw.tenantB, crossTenantPost, "/api/v1/workflows", crossTenantWorkflowBody("", "", f.marker)), "B workflow", "id")
	f.run = crossTenantField(t, sw.as(t, sw.tenantB, crossTenantPost, "/api/v1/workflows/"+f.wf+"/runs?idempotency_key="+f.idem,
		fmt.Sprintf(`{"note":%q}`, f.marker)), "B run", "run_id")
	run, err := s.workflowStore.GetRun(ctx, f.run)
	if err != nil || run.OrgID != sw.tenantB.tenantID ||
		(run.Status != wf.RunStatusPending && run.Status != wf.RunStatusRunning && run.Status != wf.RunStatusWaiting) {
		t.Fatalf("B run unusable: %v %+v", err, run)
	}
	f.chatKey = "mem:run:" + f.run + ":events"
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, crossTenantPost, "/api/v1/workflow-runs/"+f.run+"/chat", fmt.Sprintf(`{"content":%q}`, f.marker)), "B chat")
	client, err := chatRedisClient(s.memStore)
	if err != nil {
		t.Fatal(err)
	}
	st := s.workflowStore
	f.snaps = append(f.snaps,
		func() any { return crossTenantSnap(st.GetWorkflow(ctx, f.wf)) },
		func() any { return crossTenantSnap(st.GetRun(ctx, f.run)) },
		func() any { return crossTenantSnap(st.ListTimelineEvents(ctx, f.run, 100)) },
		func() any { return crossTenantSnap(client.LRange(ctx, f.chatKey, 0, -1).Result()) },
		func() any { return crossTenantSnap(st.ListRunsByWorkflow(ctx, f.wf, 100)) },
		func() any { return crossTenantSnap(st.ListWorkflows(ctx, sw.tenantB.tenantID, 100)) },
		func() any { return crossTenantSnap(st.GetRunByIdempotencyKey(ctx, sw.tenantB.tenantID, f.wf, f.idem)) },
		func() any {
			runs, err := st.ListRuns(ctx, time.Now().Add(time.Hour).Unix(), 1000)
			var ids []string
			for _, r := range runs {
				if r.OrgID == sw.tenantB.tenantID {
					ids = append(ids, r.ID)
				}
			}
			return crossTenantSnap(ids, err)
		})
}

func seedCrossTenantOwnWorkflow(t *testing.T, sw *crossTenantSweep, f *crossTenantWorkflowFixture) {
	t.Helper()
	f.aWF = crossTenantField(t, sw.as(t, sw.tenantA, crossTenantPost, "/api/v1/workflows", crossTenantWorkflowBody("", "", "a-own")), "A workflow", "id")
}

func seedCrossTenantJobFixture(t *testing.T, sw *crossTenantSweep, f *crossTenantWorkflowFixture) {
	t.Helper()
	f.job, f.jobMarker = seedCrossTenantJob(t, sw)
	f.trace = crossTenantJobMeta(t, sw, f.job)["trace_id"]
	if f.trace == "" {
		t.Fatal("B job has no trace id")
	}
	f.leaks = append(f.leaks, f.jobMarker)
	client, err := chatRedisClient(sw.server.memStore)
	if err != nil {
		t.Fatal(err)
	}
	f.snaps = append(f.snaps,
		func() any { return crossTenantJobMeta(t, sw, f.job) },
		func() any { return crossTenantSnap(client.Get(context.Background(), "ctx:"+f.job).Result()) })
}

func seedCrossTenantResolver(t *testing.T, sw *crossTenantSweep, f *crossTenantWorkflowFixture) {
	t.Helper()
	f.resolver = &gatewayResourceResolver{content: []byte(f.marker), media: "text/plain"}
	sw.server.memoryResourceReader.Resolver = f.resolver
	f.snaps = append(f.snaps, func() any { return f.resolver.calls })
}

func seedCrossTenantArtifact(t *testing.T, sw *crossTenantSweep, f *crossTenantWorkflowFixture) {
	t.Helper()
	f.ptr = crossTenantField(t, sw.as(t, sw.tenantB, crossTenantPost, "/api/v1/artifacts", fmt.Sprintf(`{"content":%q}`, f.marker)), "B artifact", "artifact_ptr")
	f.leaks = append(f.leaks, base64.StdEncoding.EncodeToString([]byte(f.marker)))
	client, err := chatRedisClient(sw.server.memStore)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	f.snaps = append(f.snaps,
		func() any {
			content, meta, err := sw.server.artifactStore.Get(ctx, f.ptr)
			return crossTenantSnap([]any{string(content), meta}, err)
		},
		func() any {
			keys, err := client.Keys(ctx, "art:*").Result()
			sort.Strings(keys)
			return crossTenantSnap(keys, err)
		})
}

func crossTenantLockBody(resource, owner string) string {
	return fmt.Sprintf(`{"resource":%q,"owner":%q,"mode":"exclusive","ttl_ms":600000}`, resource, owner)
}

func seedCrossTenantLock(t *testing.T, sw *crossTenantSweep, f *crossTenantWorkflowFixture) {
	t.Helper()
	f.lock = "sweep-lock-" + uuid.NewString()
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, crossTenantPost, "/api/v1/locks/acquire", crossTenantLockBody(f.lock, f.marker)), "B lock")
	key := tenantLockResource(sw.tenantB.tenantID, f.lock)
	f.snaps = append(f.snaps, func() any { return crossTenantSnap(sw.server.lockStore.Get(context.Background(), key)) })
}

func crossTenantTopicBody(name, tag string) string {
	return fmt.Sprintf(`{"name":%q,"status":"disabled","risk_tags":[%q]}`, name, tag)
}

func seedCrossTenantTopic(t *testing.T, sw *crossTenantSweep, f *crossTenantWorkflowFixture) {
	t.Helper()
	f.topic = "job.sweep-" + uuid.NewString()[:8]
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, crossTenantPost, "/api/v1/topics", crossTenantTopicBody(f.topic, f.marker)), "B topic")
	ctx, reg := context.Background(), sw.server.topicRegistry
	f.snaps = append(f.snaps,
		func() any {
			record, found, err := reg.GetForTenant(ctx, sw.tenantB.tenantID, f.topic)
			return crossTenantSnap([]any{record, found}, err)
		},
		func() any { return crossTenantSnap(reg.ListForTenant(ctx, sw.tenantB.tenantID)) })
}

func init() {
	const get, post, del = crossTenantGet, crossTenantPost, crossTenantDelete
	const refused, absent, either = crossTenantWantRefused, crossTenantWantAbsent, crossTenantWantEither
	seedWF := seedCrossTenantWorkflow
	registerCrossTenantWorkflowCase("GET /api/v1/workflows", "workflows-list", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("list", get, "/api/v1/workflows", "", absent, f.wf),
			crossTenantA("list B org", get, "/api/v1/workflows?org_id="+sw.tenantB.tenantID, "", either, f.wf),
			crossTenantB(get, "/api/v1/workflows", "", f.wf),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("POST /api/v1/workflows", "workflow-create", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("overwrite B id", post, "/api/v1/workflows", crossTenantWorkflowBody(f.wf, "", "a-takeover"), refused),
			crossTenantA("create in B org", post, "/api/v1/workflows", crossTenantWorkflowBody("", sw.tenantB.tenantID, "a-inject"), refused),
			crossTenantB(post, "/api/v1/workflows", crossTenantWorkflowBody(f.wf, "", f.marker+"-v2"), f.wf),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("GET /api/v1/workflows/{id}", "workflow-detail", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("detail", get, "/api/v1/workflows/"+f.wf, "", refused, f.run),
			crossTenantB(get, "/api/v1/workflows/"+f.wf, "", f.marker),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("DELETE /api/v1/workflows/{id}", "workflow-delete", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("delete", del, "/api/v1/workflows/"+f.wf, "", refused),
			crossTenantB(del, "/api/v1/workflows/"+f.wf, "", ""),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("POST /api/v1/workflows/{id}/runs", "workflow-start-run", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		ownRunChat := func(t *testing.T, sw *crossTenantSweep, rr *httptest.ResponseRecorder) {
			if rr.Code < 200 || rr.Code >= 300 {
				return
			}
			chat := "/api/v1/workflow-runs/" + crossTenantField(t, rr, "A own run", "run_id") + "/chat"
			assertCrossTenantNoLeaks(t, sw.as(t, sw.tenantA, get, chat, ""), "A own-run chat", f.marker)
			sw.as(t, sw.tenantA, post, chat, `{"content":"a-chat"}`)
		}
		return []crossTenantProbe{
			crossTenantA("start B workflow", post, "/api/v1/workflows/"+f.wf+"/runs?org_id="+sw.tenantB.tenantID+"&idempotency_key="+f.idem, "{}", refused, f.run),
			crossTenantA("reuse B idempotency key", post, "/api/v1/workflows/"+f.aWF+"/runs?idempotency_key="+f.idem, "{}", either, f.run),
			{name: "bind B run memory", method: post, path: "/api/v1/workflows/" + f.aWF + "/runs",
				body: fmt.Sprintf(`{"memory_id":%q}`, "run:"+f.run), want: either, then: ownRunChat},
			crossTenantB(post, "/api/v1/workflows/"+f.wf+"/runs", "{}", "run_id"),
		}
	}, seedWF, seedCrossTenantOwnWorkflow)
	registerCrossTenantWorkflowCase("GET /api/v1/workflows/{id}/runs", "workflow-runs-list", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("runs", get, "/api/v1/workflows/"+f.wf+"/runs", "", refused, f.run),
			crossTenantB(get, "/api/v1/workflows/"+f.wf+"/runs", "", f.run),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("POST /api/v1/workflows/{id}/dry-run", "workflow-dry-run", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("dry-run", post, "/api/v1/workflows/"+f.wf+"/dry-run", "{}", refused),
			crossTenantB(post, "/api/v1/workflows/"+f.wf+"/dry-run", "{}", ""),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("POST /api/v1/workflows/{id}/runs/{run_id}/cancel", "workflow-run-cancel", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("cancel", post, "/api/v1/workflows/"+f.wf+"/runs/"+f.run+"/cancel", "", refused),
			crossTenantA("cancel via A workflow", post, "/api/v1/workflows/"+f.aWF+"/runs/"+f.run+"/cancel", "", refused),
			crossTenantB(post, "/api/v1/workflows/"+f.wf+"/runs/"+f.run+"/cancel", "", ""),
		}
	}, seedWF, seedCrossTenantOwnWorkflow)
	registerCrossTenantWorkflowCase("GET /api/v1/workflow-runs", "runs-list", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("list", get, "/api/v1/workflow-runs", "", absent, f.run, f.wf),
			crossTenantA("list B org", get, "/api/v1/workflow-runs?org_id="+sw.tenantB.tenantID+"&workflow_id="+f.wf, "", either, f.run),
			crossTenantB(get, "/api/v1/workflow-runs", "", f.run),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("GET /api/v1/workflow-runs/{id}", "run-detail", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("detail", get, "/api/v1/workflow-runs/"+f.run, "", refused, f.wf),
			crossTenantB(get, "/api/v1/workflow-runs/"+f.run, "", f.marker),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("GET /api/v1/workflow-runs/{id}/timeline", "run-timeline", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("timeline", get, "/api/v1/workflow-runs/"+f.run+"/timeline", "", refused, f.wf),
			crossTenantB(get, "/api/v1/workflow-runs/"+f.run+"/timeline", "", f.wf),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("GET /api/v1/workflow-runs/{id}/chat", "run-chat-read", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("chat", get, "/api/v1/workflow-runs/"+f.run+"/chat", "", refused),
			crossTenantB(get, "/api/v1/workflow-runs/"+f.run+"/chat", "", f.marker),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("POST /api/v1/workflow-runs/{id}/chat", "run-chat-post", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("chat", post, "/api/v1/workflow-runs/"+f.run+"/chat", `{"content":"a-injected"}`, refused),
			crossTenantB(post, "/api/v1/workflow-runs/"+f.run+"/chat", `{"content":"owner-2"}`, ""),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("DELETE /api/v1/workflow-runs/{id}", "run-delete", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("delete", del, "/api/v1/workflow-runs/"+f.run, "", refused),
			crossTenantB(del, "/api/v1/workflow-runs/"+f.run, "", ""),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("POST /api/v1/workflow-runs/{id}/rerun", "run-rerun", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("rerun", post, "/api/v1/workflow-runs/"+f.run+"/rerun", "{}", refused),
			crossTenantB(post, "/api/v1/workflow-runs/"+f.run+"/rerun", "{}", "run_id"),
		}
	}, seedWF)
	registerCrossTenantWorkflowCase("POST /api/v1/artifacts", "artifact-put", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("put as B", post, "/api/v1/artifacts", fmt.Sprintf(`{"content":"a-data","labels":{"tenant_id":%q}}`, sw.tenantB.tenantID), refused),
			crossTenantB(post, "/api/v1/artifacts", `{"content":"owner-2"}`, "artifact_ptr"),
		}
	}, seedCrossTenantArtifact)
	registerCrossTenantWorkflowCase("GET /api/v1/artifacts/{ptr}", "artifact-get", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("get", get, "/api/v1/artifacts/"+url.PathEscape(f.ptr), "", refused),
			crossTenantB(get, "/api/v1/artifacts/"+url.PathEscape(f.ptr), "", base64.StdEncoding.EncodeToString([]byte(f.marker))),
		}
	}, seedCrossTenantArtifact)
	registerCrossTenantWorkflowCase("GET /api/v1/memory", "memory-get", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		ctxKey, runKey := url.QueryEscape("ctx:"+f.job), url.QueryEscape(f.chatKey)
		return []crossTenantProbe{
			crossTenantA("job context key", get, "/api/v1/memory?key="+ctxKey, "", refused),
			crossTenantA("job context ptr", get, "/api/v1/memory?ptr="+url.QueryEscape("redis://ctx:"+f.job), "", refused),
			crossTenantA("run memory key", get, "/api/v1/memory?key="+runKey, "", refused),
			crossTenantB(get, "/api/v1/memory?key="+ctxKey, "", f.jobMarker),
			crossTenantB(get, "/api/v1/memory?key="+runKey, "", f.marker),
		}
	}, seedCrossTenantJobFixture, seedWF)
	registerCrossTenantWorkflowCase("POST /api/v1/memory/resolve", "memory-resolve", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		body := fmt.Sprintf(`{"job_id":%q,"reference":{"resolverId":"operator"}}`, f.job)
		return []crossTenantProbe{
			crossTenantA("resolve", post, "/api/v1/memory/resolve", body, refused),
			crossTenantB(post, "/api/v1/memory/resolve", body, base64.StdEncoding.EncodeToString([]byte(f.marker))),
		}
	}, seedCrossTenantJobFixture, seedCrossTenantResolver)
	registerCrossTenantWorkflowCase("GET /api/v1/traces/{id}", "trace", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("trace", get, "/api/v1/traces/"+f.trace, "", absent, f.job),
			crossTenantB(get, "/api/v1/traces/"+f.trace, "", f.job),
		}
	}, seedCrossTenantJobFixture)
	registerCrossTenantWorkflowCase("GET /api/v1/locks", "lock-get", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("get", get, "/api/v1/locks?resource="+f.lock, "", refused),
			crossTenantB(get, "/api/v1/locks?resource="+f.lock, "", f.marker),
		}
	}, seedCrossTenantLock)
	registerCrossTenantWorkflowCase("POST /api/v1/locks/acquire", "lock-acquire", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("acquire", post, "/api/v1/locks/acquire", crossTenantLockBody(f.lock, "owner-a"), either),
			crossTenantB(post, "/api/v1/locks/acquire", crossTenantLockBody(f.lock, f.marker), f.marker),
		}
	}, seedCrossTenantLock)
	for _, op := range []struct{ route, name, want string }{
		{"POST /api/v1/locks/release", "lock-release", "released"},
		{"POST /api/v1/locks/renew", "lock-renew", "owners"},
	} {
		path := strings.TrimPrefix(op.route, "POST ")
		registerCrossTenantWorkflowCase(op.route, op.name, func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
			return []crossTenantProbe{
				{name: op.name, method: post, path: path, body: crossTenantLockBody(f.lock, f.marker),
					baseline: crossTenantLockBody("never-held-"+f.lock, f.marker)},
				crossTenantB(post, path, crossTenantLockBody(f.lock, f.marker), op.want),
			}
		}, seedCrossTenantLock)
	}
	registerCrossTenantWorkflowCase("GET /api/v1/topics", "topics-list", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("list", get, "/api/v1/topics", "", absent),
			crossTenantB(get, "/api/v1/topics", "", f.marker),
		}
	}, seedCrossTenantTopic)
	registerCrossTenantWorkflowCase("POST /api/v1/topics", "topic-create", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("create same name", post, "/api/v1/topics", crossTenantTopicBody(f.topic, "a-tag"), either),
			crossTenantB(post, "/api/v1/topics", crossTenantTopicBody(f.topic, f.marker+"-v2"), f.marker),
		}
	}, seedCrossTenantTopic)
	registerCrossTenantWorkflowCase("DELETE /api/v1/topics/{name}", "topic-delete", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("delete", del, "/api/v1/topics/"+f.topic, "", refused),
			crossTenantB(del, "/api/v1/topics/"+f.topic, "", ""),
		}
	}, seedCrossTenantTopic)
}

func TestCrossTenantSweepWorkflowsSharedPoolRoute(t *testing.T) {
	t.Run("topic-delete-shared-pool-route", func(t *testing.T) {
		sw := newCrossTenantSweep(t)
		f := &crossTenantWorkflowFixture{marker: "private-" + uuid.NewString()}
		seedCrossTenantTopic(t, sw, f)
		seedPoolConfig(t, sw.server, map[string]any{f.topic: "sweep-pool"}, map[string]config.PoolConfig{"sweep-pool": {}})
		before, poolsBefore := f.state(t), poolPlatformPools(t, sw)
		if !strings.Contains(poolsBefore, fmt.Sprintf("%q:%q", f.topic, "sweep-pool")) {
			t.Fatal("seeded topic-to-pool entry missing")
		}
		mark := sw.publishMark()
		rr := sw.as(t, sw.tenantA, crossTenantPost, "/api/v1/topics", crossTenantTopicBody(f.topic, "a-tag"))
		switch {
		case rr.Code >= 200 && rr.Code < 300:
			assertCrossTenantAbsent(t, rr, "A same-name topic", f.marker)
			rr = sw.as(t, sw.tenantA, crossTenantDelete, "/api/v1/topics/"+f.topic, "")
			assertCrossTenantAbsent(t, rr, "A own-topic delete", f.marker)
		case rr.Code == http.StatusForbidden || rr.Code == http.StatusNotFound || rr.Code == http.StatusConflict:
			assertCrossTenantNoLeaks(t, rr, "A same-name refusal", f.marker)
			t.Logf("A same-name create refused (%d); no delete attempted", rr.Code)
			if len(sw.publishedSince(mark)) != 0 {
				t.Error("refused create published packets")
			}
		default:
			t.Fatalf("A same-name create: unexpected status=%d body=%s", rr.Code, rr.Body.String())
		}
		if after := f.state(t); after != before {
			t.Errorf("tenant-B topic state changed:\nbefore=%s\nafter=%s", before, after)
		}
		if after := poolPlatformPools(t, sw); after != poolsBefore {
			t.Errorf("shared pool routing changed:\nbefore=%s\nafter=%s", poolsBefore, after)
		}
		for _, m := range sw.publishedSince(mark) {
			effect := fmt.Sprintf("%s %v", m.subject, m.packet)
			if strings.Contains(effect, sw.tenantB.tenantID) || strings.Contains(effect, f.marker) {
				t.Error("A topic calls published tenant-B data")
			}
		}
		assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, crossTenantDelete, "/api/v1/topics/"+f.topic, ""), "B own-topic delete")
	})
}

func TestCrossTenantSweepWorkflows(t *testing.T) { runCrossTenantFamily(t, "PR1-02-WF") }

func crossTenantSchemaBody(id, description string) string {
	return fmt.Sprintf(`{"id":%q,"schema":{"type":"object","description":%q}}`, id, description)
}

func seedCrossTenantSchema(t *testing.T, sw *crossTenantSweep, f *crossTenantWorkflowFixture) {
	t.Helper()
	f.schemaID = "sweep-schema-" + uuid.NewString()
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, crossTenantPost, "/api/v1/schemas", crossTenantSchemaBody(f.schemaID, f.marker)), "B schema")
	ctx, reg := context.Background(), sw.server.schemaRegistry
	f.snaps = append(f.snaps,
		func() any {
			raw, err := reg.Get(ctx, f.schemaID)
			return crossTenantSnap(string(raw), err)
		},
		func() any { return crossTenantSnap(reg.List(ctx, 1000)) })
}

func init() {
	const get, post, del = crossTenantGet, crossTenantPost, crossTenantDelete
	registerCrossTenantWorkflowCase("POST /api/v1/schemas", "schema-register", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("overwrite B id", post, "/api/v1/schemas", crossTenantSchemaBody(f.schemaID, "a-schema"), crossTenantWantRefused),
			crossTenantB(post, "/api/v1/schemas", crossTenantSchemaBody(f.schemaID, f.marker+"-v2"), ""),
		}
	}, seedCrossTenantSchema)
	registerCrossTenantWorkflowCase("GET /api/v1/schemas", "schemas-list", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("list", get, "/api/v1/schemas", "", crossTenantWantAbsent, f.schemaID),
			crossTenantB(get, "/api/v1/schemas", "", f.schemaID),
		}
	}, seedCrossTenantSchema)
	registerCrossTenantWorkflowCase("GET /api/v1/schemas/{id}", "schema-get", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("get", get, "/api/v1/schemas/"+f.schemaID, "", crossTenantWantRefused),
			crossTenantB(get, "/api/v1/schemas/"+f.schemaID, "", f.marker),
		}
	}, seedCrossTenantSchema)
	registerCrossTenantWorkflowCase("DELETE /api/v1/schemas/{id}", "schema-delete", func(sw *crossTenantSweep, f *crossTenantWorkflowFixture) []crossTenantProbe {
		return []crossTenantProbe{
			crossTenantA("delete", del, "/api/v1/schemas/"+f.schemaID, "", crossTenantWantRefused),
			crossTenantB(del, "/api/v1/schemas/"+f.schemaID, "", ""),
		}
	}, seedCrossTenantSchema)
}
