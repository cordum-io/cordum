package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type runMemOwnerFixture struct {
	s         *server
	bus       *stubBus
	runID     string
	notesID   string
	lists     map[string][]string
	published int
	events    int
}

func TestRunMemoryOwnerTenantBound(t *testing.T) {
	f := runMemOwnerSetup(t)
	t.Run("start-run-memory", func(t *testing.T) { f.startDenied(t, "run:"+f.runID) })
	t.Run("start-tenant-prefix", func(t *testing.T) { f.startDenied(t, "tenant-b/notes") })
	t.Run("job-submit", f.jobDenied)
	t.Run("stored-run-chat", f.chatDenied)
	t.Run("own-tenant", f.ownTenant)
}

func runMemOwnerSetup(t *testing.T) *runMemOwnerFixture {
	t.Helper()
	s, bus, _ := newTestGateway(t)
	f := &runMemOwnerFixture{s: s, bus: bus, lists: make(map[string][]string)}
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		def := &wf.Workflow{ID: "wf-" + tenant, OrgID: tenant, Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		}}
		if err := s.workflowStore.SaveWorkflow(context.Background(), def); err != nil {
			t.Fatal(err)
		}
	}
	f.runID = f.startOK(t, "")
	f.notesID = f.startOK(t, "tenant-b/notes")
	for _, id := range []string{f.runID, f.notesID} {
		rr := f.chat("tenant-b", id, http.MethodPost, `{"content":"runMemOwner-B-marker"}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("seed chat: %d %s", rr.Code, rr.Body.String())
		}
	}
	for _, key := range []string{"mem:run:" + f.runID + ":events", "mem:tenant-b/notes:events"} {
		f.lists[key] = f.readList(t, key)
		if len(f.lists[key]) != 1 || !strings.Contains(f.lists[key][0], "runMemOwner-B-marker") {
			t.Fatalf("seed list %s: %v", key, f.lists[key])
		}
	}
	f.published, f.events = len(bus.published), len(s.eventsCh)
	return f
}

func runMemOwnerRequest(method, path, tenant, id, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-Tenant-ID", tenant)
	r.SetPathValue("id", id)
	return withAuth(r, &auth.AuthContext{Tenant: tenant, PrincipalID: "principal-" + tenant, Role: "admin"})
}

func (f *runMemOwnerFixture) start(t *testing.T, tenant, memoryID string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	body, err := json.Marshal(map[string]string{"memory_id": memoryID})
	if err != nil {
		t.Fatal(err)
	}
	f.s.handleStartRun(rr, runMemOwnerRequest(http.MethodPost, "/api/v1/workflows/wf-"+tenant+"/runs",
		tenant, "wf-"+tenant, string(body)))
	return rr
}

func (f *runMemOwnerFixture) startOK(t *testing.T, memoryID string) string {
	t.Helper()
	rr := f.start(t, "tenant-b", memoryID)
	var result struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusOK || result.RunID == "" {
		t.Fatalf("start own run: %d %s", rr.Code, rr.Body.String())
	}
	return result.RunID
}

func (f *runMemOwnerFixture) chat(tenant, id, method, body string) *httptest.ResponseRecorder {
	r := runMemOwnerRequest(method, "/api/v1/workflow-runs/"+id+"/chat", tenant, id, body)
	rr := httptest.NewRecorder()
	if method == http.MethodGet {
		f.s.handleGetRunChat(rr, r)
	} else {
		f.s.handlePostRunChat(rr, r)
	}
	return rr
}

func (f *runMemOwnerFixture) readList(t *testing.T, key string) []string {
	t.Helper()
	list, err := f.s.jobStore.Client().LRange(context.Background(), key, 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func (f *runMemOwnerFixture) unchanged(t *testing.T) {
	t.Helper()
	for key, before := range f.lists {
		if after := f.readList(t, key); !reflect.DeepEqual(before, after) {
			t.Errorf("B list changed: %s", key)
		}
	}
	if len(f.bus.published) != f.published || len(f.s.eventsCh) != f.events {
		t.Errorf("queues changed: bus=%d events=%d", len(f.bus.published), len(f.s.eventsCh))
	}
}

func runMemOwnerDenied(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Error(err)
	}
	if rr.Code != http.StatusForbidden || body.Code != "MEMORY_POLICY_VIOLATION" || body.Error != "memory id not available to this tenant" {
		t.Errorf("request status/body: %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "runMemOwner-B-marker") {
		t.Error("B marker returned to A")
	}
}

func (f *runMemOwnerFixture) startDenied(t *testing.T, memoryID string) {
	runMemOwnerDenied(t, f.start(t, "tenant-a", memoryID))
	runs, err := f.s.workflowStore.ListRunsByWorkflow(context.Background(), "wf-tenant-a", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("A runs created: %d", len(runs))
	}
	f.unchanged(t)
}

func (f *runMemOwnerFixture) jobDenied(t *testing.T) {
	r := runMemOwnerRequest(http.MethodPost, "/api/v1/jobs", "tenant-a", "",
		`{"prompt":"hello","topic":"job.default","memory_id":"run:`+f.runID+`"}`)
	rr := httptest.NewRecorder()
	f.s.handleSubmitJobHTTP(rr, r)
	runMemOwnerDenied(t, rr)
	resp, err := f.s.SubmitJob(r.Context(), &pb.SubmitJobRequest{
		Prompt: "hello", Topic: "job.default", OrgId: "tenant-a", MemoryId: "run:" + f.runID,
	})
	if resp != nil || status.Code(err) != codes.PermissionDenied || status.Convert(err).Message() != "memory id not available to this tenant" {
		t.Errorf("gRPC result: %v %v", resp, err)
	}
	if strings.Contains(status.Convert(err).Message(), "runMemOwner-B-marker") {
		t.Error("B marker returned to A")
	}
	f.unchanged(t)
}

func (f *runMemOwnerFixture) chatDenied(t *testing.T) {
	run := &wf.WorkflowRun{ID: "runMemOwner-A", WorkflowID: "wf-tenant-a", OrgID: "tenant-a",
		Input: map[string]interface{}{"memory_id": "run:" + f.runID}, Status: wf.RunStatusRunning}
	if err := f.s.workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		runMemOwnerDenied(t, f.chat("tenant-a", run.ID, method, `{"content":"A-message"}`))
		f.unchanged(t)
	}
}

func (f *runMemOwnerFixture) ownTenant(t *testing.T) {
	id := f.startOK(t, "run:"+f.runID)
	for _, runID := range []string{id, f.runID, f.notesID} {
		rr := f.chat("tenant-b", runID, http.MethodGet, "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "runMemOwner-B-marker") {
			t.Errorf("own chat: %d %s", rr.Code, rr.Body.String())
		}
	}
	rr := f.chat("tenant-b", f.runID, http.MethodPost, `{"content":"B-second"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("own post: %d %s", rr.Code, rr.Body.String())
	}
	list := f.readList(t, "mem:run:"+f.runID+":events")
	if !strings.Contains(strings.Join(list, ""), "B-second") {
		t.Error("own post missing from list")
	}
}

func TestRunMemoryOwnerBoundaries(t *testing.T) {
	s, _, _ := newTestGateway(t)
	if err := s.jobStore.Client().Set(context.Background(), "wf:run:runMemOwner-invalid", "{", 0).Err(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     string
		status int
	}{
		{"run:absent", 403}, {"run:", 403}, {" mem: tenant-b/notes ", 403},
		{"tenant-b/", 403}, {"run:runMemOwner-invalid", 503},
		{"tenant-a/", 0}, {"/notes", 0}, {"kb:notes", 0}, {" ", 0},
	} {
		t.Run(tc.id, func(t *testing.T) {
			err := s.enforceMemoryID(context.Background(), "tenant-a", "", "", "", tc.id)
			var perr memoryPolicyError
			if tc.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.As(err, &perr) || perr.status != tc.status {
				t.Fatalf("memory policy: %v, want %d", err, tc.status)
			}
			if tc.status == 403 && perr.msg != "memory id not available to this tenant" {
				t.Errorf("memory message: %q", perr.msg)
			}
		})
	}
}
