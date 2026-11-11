package gateway

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	wf "github.com/cordum/cordum/core/workflow"
)

// policyDocGet is fmt %v of the args of configsvc's GET for scope system,
// id policy (cfgKey, core/configsvc/service.go).
const policyDocGet = "[get cfg:system:policy]"

// redisCmdRecorder is a go-redis hook that records every command sent on the
// client it is attached to, including pipelined ones. A per-client hook is
// the reliable counter: on Windows the gateway tests share one miniredis, so
// its own command counter would include other tests' traffic.
type redisCmdRecorder struct {
	mu   sync.Mutex
	cmds []string
}

func (r *redisCmdRecorder) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *redisCmdRecorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		r.record(cmd)
		return next(ctx, cmd)
	}
}

func (r *redisCmdRecorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		r.record(cmds...)
		return next(ctx, cmds)
	}
}

func (r *redisCmdRecorder) record(cmds ...redis.Cmder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range cmds {
		r.cmds = append(r.cmds, fmt.Sprintf("%v", c.Args()))
	}
}

// take returns the recorded commands and clears the recorder.
func (r *redisCmdRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.cmds
	r.cmds = nil
	return out
}

// recordPolicyStores points the server's config service and workflow store
// at one recorded client.
func recordPolicyStores(t *testing.T, s *server) (*redisCmdRecorder, *redis.Client) {
	t.Helper()
	client := newSharedTestRedisClient(t, newTestRedisBackend(t))
	rec := &redisCmdRecorder{}
	client.AddHook(rec)
	s.configSvc = configsvc.NewFromClient(client)
	s.workflowStore = wf.NewRedisWorkflowStoreFromClient(client)
	return rec, client
}

// seedPolicyDoc writes the policy config doc and returns its updated-at in
// the format the snapshot must carry.
func seedPolicyDoc(t *testing.T, s *server) string {
	t.Helper()
	doc := &configsvc.Document{
		Scope:   configsvc.Scope(packs.PolicyConfigScope),
		ScopeID: packs.PolicyConfigID,
		Data:    map[string]any{packs.PolicyConfigKey: map[string]any{}},
	}
	if err := s.configSvc.Set(context.Background(), doc); err != nil {
		t.Fatalf("seed policy doc: %v", err)
	}
	return doc.Updated.UTC().Format(time.RFC3339)
}

// seedSnapshotWorkflows saves workflows wf-snap-<from> .. wf-snap-<to-1>,
// each carrying a policy override so the oracle folds them into its bundles.
func seedSnapshotWorkflows(t *testing.T, s *server, from, to int) {
	t.Helper()
	for i := from; i < to; i++ {
		id := fmt.Sprintf("wf-snap-%d", i)
		if err := s.workflowStore.SaveWorkflow(context.Background(), &wf.Workflow{
			ID:             id,
			OrgID:          "default",
			Name:           id,
			PolicyOverride: validWorkflowPolicyOverride(id),
			Steps:          map[string]*wf.Step{"start": {ID: "start", Type: wf.StepTypeApproval}},
		}); err != nil {
			t.Fatalf("save workflow %s: %v", id, err)
		}
	}
}

// preFixSnapshot is the provider body from before task-2688feeb, kept as the
// parity oracle for "same value as today". loadPolicyBundles is unchanged.
func preFixSnapshot(s *server) func(context.Context) string {
	return func(ctx context.Context) string {
		_, snapshot, err := s.loadPolicyBundles(ctx)
		if err != nil {
			return ""
		}
		return snapshot
	}
}

func TestMCPPolicySnapshot_MatchesPolicyDocUpdatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &server{}
	rec, _ := recordPolicyStores(t, s)
	want := seedPolicyDoc(t, s)
	seedSnapshotWorkflows(t, s, 0, 3)

	bundles, _, err := s.loadPolicyBundles(ctx)
	if err != nil {
		t.Fatalf("oracle loadPolicyBundles: %v", err)
	}
	if _, ok := bundles["workflow/wf-snap-0/policy"]; !ok {
		t.Fatalf("oracle does not fold workflows in: %+v", bundles)
	}
	if oracle := preFixSnapshot(s)(ctx); oracle != want {
		t.Fatalf("oracle snapshot = %q, want %q", oracle, want)
	}

	rec.take()
	got := s.mcpPolicySnapshotFunc()(ctx)
	if got != want {
		t.Fatalf("snapshot = %q, want %q", got, want)
	}
	if cmds := rec.take(); !reflect.DeepEqual(cmds, []string{policyDocGet}) {
		t.Fatalf("redis commands = %q, want exactly [%q]", cmds, policyDocGet)
	}
}

func TestMCPPolicySnapshot_FailsClosedEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(t *testing.T, s *server)
	}{
		{"missing_doc", func(t *testing.T, s *server) { recordPolicyStores(t, s) }},
		{"zero_updated_at", func(t *testing.T, s *server) {
			_, client := recordPolicyStores(t, s)
			raw := `{"scope":"system","scope_id":"policy","data":{"bundles":{}}}`
			if err := client.Set(ctx, "cfg:system:policy", raw, 0).Err(); err != nil {
				t.Fatalf("seed raw doc: %v", err)
			}
		}},
		{"corrupt_doc", func(t *testing.T, s *server) {
			_, client := recordPolicyStores(t, s)
			if err := client.Set(ctx, "cfg:system:policy", "{not json", 0).Err(); err != nil {
				t.Fatalf("seed corrupt doc: %v", err)
			}
		}},
		{"redis_unavailable", func(t *testing.T, s *server) {
			// A closed client fails every command with "redis: client is
			// closed" without dialing. Never SetError or close the shared
			// miniredis.
			dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
			_ = dead.Close()
			s.configSvc = configsvc.NewFromClient(dead)
			s.workflowStore = wf.NewRedisWorkflowStoreFromClient(dead)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &server{}
			tc.setup(t, s)
			got := s.mcpPolicySnapshotFunc()(ctx)
			if got != "" {
				t.Fatalf("snapshot = %q, want empty (fail closed)", got)
			}
			if oracle := preFixSnapshot(s)(ctx); got != oracle {
				t.Fatalf("snapshot = %q, pre-fix oracle = %q", got, oracle)
			}
		})
	}
}

func TestMCPPolicySnapshot_NilConfigServiceFailsClosed(t *testing.T) {
	t.Parallel()
	if got := (&server{}).mcpPolicySnapshotFunc()(context.Background()); got != "" {
		t.Fatalf("snapshot with nil config service = %q, want empty", got)
	}
}

func TestMCPPolicySnapshot_OneRedisReadRegardlessOfWorkflowCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &server{}
	rec, _ := recordPolicyStores(t, s)
	want := seedPolicyDoc(t, s)
	snapshot := s.mcpPolicySnapshotFunc()

	seeded := 0
	for _, n := range []int{0, 1, 50} {
		seedSnapshotWorkflows(t, s, seeded, n)
		seeded = n
		rec.take()
		got := snapshot(ctx)
		cmds := rec.take()
		if got != want {
			t.Fatalf("n=%d: snapshot = %q, want %q", n, got, want)
		}
		if !reflect.DeepEqual(cmds, []string{policyDocGet}) {
			t.Fatalf("n=%d: redis commands = %q, want exactly [%q]", n, cmds, policyDocGet)
		}
	}
}

func TestMCPPolicySnapshot_WiredGateSnapshotIsOneRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _, _ := newTestGateway(t)
	// Swap the stores BEFORE wiring so the result does not depend on when the
	// closure reads s.configSvc.
	rec, _ := recordPolicyStores(t, s)
	configurePolicyGateBootDeps(t, s)
	gate := NewGatewayApprovalGate(newTestMCPStore(t)).(*gatewayApprovalGate)

	// attachMCPPolicyDepsNamed hands this same closure to WithApprovalHold
	// (consume) and the Edge mint, and task-63400922's per-pre-event stamping
	// reads it through policySnapshotForEvents.
	if _, reason := s.attachMCPPolicyDepsNamed(pwNewMCPServer(), gate, ""); reason != "" {
		t.Fatalf("attachMCPPolicyDepsNamed reason = %q, want empty", reason)
	}
	if gate.policySnapshot == nil {
		t.Fatal("gate.policySnapshot not wired")
	}
	want := seedPolicyDoc(t, s)
	seedSnapshotWorkflows(t, s, 0, 20)
	rec.take()
	got := gate.policySnapshot(ctx)
	if got != want {
		t.Fatalf("gate snapshot = %q, want %q", got, want)
	}
	if cmds := rec.take(); !reflect.DeepEqual(cmds, []string{policyDocGet}) {
		t.Fatalf("redis commands = %q, want exactly [%q]", cmds, policyDocGet)
	}
}
