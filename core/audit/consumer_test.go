package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
)

// packetFor builds a BusPacket carrying the given SIEMEvent, matching the
// shape NATSAuditPublisher produces. Helper shared by the chain tests.
func packetFor(t *testing.T, ev SIEMEvent) *pb.BusPacket {
	t.Helper()
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &pb.BusPacket{
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				SourceComponent: "audit-export",
				Message:         string(payload),
			},
		},
	}
}

func newConsumerChainer(t *testing.T) (*Chainer, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewChainer(client, "consumer:chain:"), client
}

// TestNATSAuditConsumer_ChainsBeforeExport verifies that when a Chainer is
// configured the event that reaches the exporter has Seq/PrevHash/EventHash
// populated — i.e. chaining ran BEFORE Export, not after.
func TestNATSAuditConsumer_ChainsBeforeExport(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}
	chainer, _ := newConsumerChainer(t)

	_, err := NewNATSAuditConsumer(bus, mock, WithChainer(chainer))
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	ev := testEvent()
	if err := handler(packetFor(t, ev)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if got := mock.totalEvents(); got != 1 {
		t.Fatalf("exported events = %d, want 1", got)
	}
	mock.mu.Lock()
	exported := mock.batches[0][0]
	mock.mu.Unlock()

	if exported.Seq != 1 {
		t.Errorf("Seq = %d, want 1", exported.Seq)
	}
	if exported.PrevHash != "" {
		t.Errorf("PrevHash = %q, want empty (genesis)", exported.PrevHash)
	}
	if len(exported.EventHash) != chainHashHexLen {
		t.Errorf("EventHash length = %d, want %d", len(exported.EventHash), chainHashHexLen)
	}
}

// TestNATSAuditConsumer_ChainFailStrictDropsEvent verifies strict mode
// acks + drops when Append returns an error, and that the dropped event
// never reaches the exporter.
func TestNATSAuditConsumer_ChainFailStrictDropsEvent(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}

	_, err := NewNATSAuditConsumer(bus, mock,
		WithChainer(newAlwaysFailingChainer()),
		WithChainFailMode(ChainFailStrict),
	)
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	if err := handler(packetFor(t, testEvent())); err != nil {
		t.Fatalf("handler should ack on strict chain failure, got: %v", err)
	}
	if got := mock.totalEvents(); got != 0 {
		t.Fatalf("strict mode must not export un-chained events; got %d", got)
	}
}

// TestNATSAuditConsumer_ChainFailPermissiveExports verifies permissive
// mode forwards to the exporter even when the chain append failed.
func TestNATSAuditConsumer_ChainFailPermissiveExports(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}

	_, err := NewNATSAuditConsumer(bus, mock,
		WithChainer(newAlwaysFailingChainer()),
		WithChainFailMode(ChainFailPermissive),
	)
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	if err := handler(packetFor(t, testEvent())); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got := mock.totalEvents(); got != 1 {
		t.Fatalf("permissive mode must export despite chain failure; got %d", got)
	}
}

func TestParseChainFailMode(t *testing.T) {
	t.Parallel()
	cases := map[string]ChainFailMode{
		"":             ChainFailStrict,
		"strict":       ChainFailStrict,
		"STRICT":       ChainFailStrict,
		"permissive":   ChainFailPermissive,
		"Permissive":   ChainFailPermissive,
		" permissive ": ChainFailPermissive,
		"garbage":      ChainFailStrict,
	}
	for input, want := range cases {
		if got := ParseChainFailMode(input); got != want {
			t.Errorf("ParseChainFailMode(%q) = %v, want %v", input, got, want)
		}
	}
}

// TestNATSAuditConsumer_EnvDrivesFailMode ensures the env var selects the
// default mode when no explicit option is passed.
func TestNATSAuditConsumer_EnvDrivesFailMode(t *testing.T) {
	t.Setenv(EnvChainFailMode, "permissive")
	bus := &mockAuditBus{}
	mock := &mockExporter{}
	c, err := NewNATSAuditConsumer(bus, mock)
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}
	if c.failMode != ChainFailPermissive {
		t.Errorf("failMode = %v, want permissive", c.failMode)
	}
}

// realRecordingSink captures every UpdateAuditHash invocation so tests
// can assert the audit consumer's Option A write-back fires exactly when
// the chain Append succeeds and never on Append-failure or no-job paths.
type realRecordingSink struct {
	calls     []recordedSinkCall
	returnErr error
}

type recordedSinkCall struct {
	jobID     string
	auditHash string
}

func (r *realRecordingSink) UpdateAuditHash(_ context.Context, jobID, auditHash string) error {
	r.calls = append(r.calls, recordedSinkCall{jobID: jobID, auditHash: auditHash})
	return r.returnErr
}

// TestNATSAuditConsumer_StepHashSinkReceivesPostAppendHash verifies the
// Option A write-back per task-a45b8eb1 (architect msg-dc9ac33d): after a
// successful chainer.Append the consumer hands the populated EventHash +
// JobID to a wired sink so the workflow store can back-fill StepRun.AuditHash.
func TestNATSAuditConsumer_StepHashSinkReceivesPostAppendHash(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}
	chainer, _ := newConsumerChainer(t)
	sink := &realRecordingSink{}

	_, err := NewNATSAuditConsumer(bus, mock, WithChainer(chainer), WithStepHashSink(sink))
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	ev := testEvent()
	if err := handler(packetFor(t, ev)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(sink.calls) != 1 {
		t.Fatalf("UpdateAuditHash calls = %d, want 1", len(sink.calls))
	}
	if sink.calls[0].jobID != ev.JobID {
		t.Errorf("UpdateAuditHash jobID = %q, want %q", sink.calls[0].jobID, ev.JobID)
	}
	if len(sink.calls[0].auditHash) != chainHashHexLen {
		t.Errorf("UpdateAuditHash auditHash length = %d, want %d", len(sink.calls[0].auditHash), chainHashHexLen)
	}
}

// TestNATSAuditConsumer_StepHashSinkSkippedWhenNoJobID verifies that
// SIEMEvents without a JobID (tenant-level events like SSO login) do
// NOT invoke the sink — there's no workflow step to back-fill.
func TestNATSAuditConsumer_StepHashSinkSkippedWhenNoJobID(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}
	chainer, _ := newConsumerChainer(t)
	sink := &realRecordingSink{}

	_, err := NewNATSAuditConsumer(bus, mock, WithChainer(chainer), WithStepHashSink(sink))
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	ev := testEvent()
	ev.JobID = "" // tenant-level event, not workflow-scoped
	if err := handler(packetFor(t, ev)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(sink.calls) != 0 {
		t.Errorf("UpdateAuditHash calls = %d, want 0 for empty-JobID event", len(sink.calls))
	}
}

// TestNATSAuditConsumer_StepHashSinkErrorIsNonFatal verifies that a sink
// failure (e.g. transient Redis error during write-back) does NOT break
// the export pipeline — the event still reaches the exporter and the
// audit chain entry is durable. Error logged + swallowed.
func TestNATSAuditConsumer_StepHashSinkErrorIsNonFatal(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}
	chainer, _ := newConsumerChainer(t)
	sink := &realRecordingSink{returnErr: fmt.Errorf("redis: connection refused")}

	_, err := NewNATSAuditConsumer(bus, mock, WithChainer(chainer), WithStepHashSink(sink))
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	if err := handler(packetFor(t, testEvent())); err != nil {
		t.Fatalf("handler: %v (sink error must not propagate)", err)
	}
	if got := mock.totalEvents(); got != 1 {
		t.Errorf("exported events = %d, want 1 (sink error must not block export)", got)
	}
	if len(sink.calls) != 1 {
		t.Errorf("UpdateAuditHash calls = %d, want 1 (must still attempt the write-back)", len(sink.calls))
	}
}

func TestNATSAuditConsumer_PopulatesWorkflowRunStepAuditHash(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	workflowStore, err := wf.NewRedisWorkflowStore("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	t.Cleanup(func() { _ = workflowStore.Close() })

	chainClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = chainClient.Close() })
	chainer := NewChainer(chainClient, "consumer:workflow-chain:")

	run := &wf.WorkflowRun{
		ID:         "run-consumer-audit",
		WorkflowID: "wf-consumer-audit",
		Status:     wf.RunStatusRunning,
		Steps: map[string]*wf.StepRun{
			"step-1":  {StepID: "step-1", Status: wf.StepStatusRunning, JobID: "run-consumer-audit:step-1@1"},
			"skipped": {StepID: "skipped", Status: wf.StepStatusSkipped, SkipReason: "upstream failed"},
			"noevent": {StepID: "noevent", Status: wf.StepStatusRunning, JobID: "run-consumer-audit:noevent@1"},
		},
	}
	if err := workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	bus := &mockAuditBus{}
	mock := &mockExporter{}
	_, err = NewNATSAuditConsumer(bus, mock, WithChainer(chainer), WithStepHashSink(workflowStore))
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}
	handler := subscribedHandler(t, bus)

	ev := testEvent()
	ev.JobID = run.Steps["step-1"].JobID
	if err := handler(packetFor(t, ev)); err != nil {
		t.Fatalf("handler: %v", err)
	}
	exported := exportedEvent(t, mock, 0)

	got, err := workflowStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Steps["step-1"].AuditHash != exported.EventHash {
		t.Fatalf("audit hash = %q, want exported event hash %q", got.Steps["step-1"].AuditHash, exported.EventHash)
	}
	if got.Steps["skipped"].AuditHash != "" {
		t.Fatalf("skipped step audit hash = %q, want empty", got.Steps["skipped"].AuditHash)
	}
	if got.Steps["noevent"].AuditHash != "" {
		t.Fatalf("no-event step audit hash = %q, want empty", got.Steps["noevent"].AuditHash)
	}
}

func TestNATSAuditConsumer_StepHashSinkNoMatchAndReplayAreNonFatal(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	workflowStore, err := wf.NewRedisWorkflowStore("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	t.Cleanup(func() { _ = workflowStore.Close() })
	chainClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = chainClient.Close() })

	run := &wf.WorkflowRun{
		ID:         "run-consumer-replay",
		WorkflowID: "wf-consumer-audit",
		Status:     wf.RunStatusRunning,
		Steps: map[string]*wf.StepRun{
			"step-1": {StepID: "step-1", Status: wf.StepStatusRunning, JobID: "run-consumer-replay:step-1@1"},
		},
	}
	if err := workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	bus := &mockAuditBus{}
	mock := &mockExporter{}
	_, err = NewNATSAuditConsumer(bus, mock,
		WithChainer(NewChainer(chainClient, "consumer:workflow-replay-chain:")),
		WithStepHashSink(workflowStore),
	)
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}
	handler := subscribedHandler(t, bus)

	missing := testEvent()
	missing.JobID = "run-consumer-replay:missing@1"
	if err := handler(packetFor(t, missing)); err != nil {
		t.Fatalf("missing-step handler: %v", err)
	}
	assertWorkflowAuditHash(t, workflowStore, run.ID, "step-1", "")

	ev := testEvent()
	ev.JobID = run.Steps["step-1"].JobID
	if err := handler(packetFor(t, ev)); err != nil {
		t.Fatalf("first handler: %v", err)
	}
	first := exportedEvent(t, mock, 1)
	assertWorkflowAuditHash(t, workflowStore, run.ID, "step-1", first.EventHash)

	if err := handler(packetFor(t, ev)); err != nil {
		t.Fatalf("replay handler: %v", err)
	}
	assertWorkflowAuditHash(t, workflowStore, run.ID, "step-1", first.EventHash)
	if got := mock.totalEvents(); got != 3 {
		t.Fatalf("exported events = %d, want 3", got)
	}
}

func subscribedHandler(t *testing.T, bus *mockAuditBus) func(*pb.BusPacket) error {
	t.Helper()
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if bus.handler == nil {
		t.Fatal("expected subscribed audit handler")
	}
	return bus.handler
}

func exportedEvent(t *testing.T, mock *mockExporter, index int) SIEMEvent {
	t.Helper()
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.batches) <= index || len(mock.batches[index]) == 0 {
		t.Fatalf("missing exported event at batch %d", index)
	}
	return mock.batches[index][0]
}

func assertWorkflowAuditHash(t *testing.T, store *wf.RedisStore, runID, stepID, want string) {
	t.Helper()
	got, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run %s: %v", runID, err)
	}
	if got.Steps[stepID].AuditHash != want {
		t.Fatalf("step %s audit hash = %q, want %q", stepID, got.Steps[stepID].AuditHash, want)
	}
}

// TestNATSAuditConsumer_ChainRealRedisMonotonic wires the real Chainer
// (backed by miniredis) end-to-end through the consumer and asserts three
// events pick up monotonic seqs 1,2,3 with linked prev_hashes.
func TestNATSAuditConsumer_ChainRealRedisMonotonic(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}
	chainer, _ := newConsumerChainer(t)

	_, err := NewNATSAuditConsumer(bus, mock, WithChainer(chainer))
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	for i := 0; i < 3; i++ {
		ev := testEvent()
		ev.JobID = fmt.Sprintf("job-%d", i)
		if err := handler(packetFor(t, ev)); err != nil {
			t.Fatalf("handler[%d]: %v", i, err)
		}
	}

	if got := mock.totalEvents(); got != 3 {
		t.Fatalf("exported = %d, want 3", got)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()

	var prev string
	for i := 0; i < 3; i++ {
		e := mock.batches[i][0]
		if e.Seq != int64(i+1) {
			t.Errorf("Seq[%d] = %d, want %d", i, e.Seq, i+1)
		}
		if e.PrevHash != prev {
			t.Errorf("PrevHash[%d] = %q, want %q", i, e.PrevHash, prev)
		}
		prev = e.EventHash
	}
}

// newAlwaysFailingChainer points a real Chainer at an unreachable Redis
// address so Append returns an error quickly. We want the concrete
// *Chainer type flowing through WithChainer so the consumer exercises
// its real code path — not a bypass.
func newAlwaysFailingChainer() *Chainer {
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1", // guaranteed unreachable
		MaxRetries:  -1,
		DialTimeout: 50 * time.Millisecond,
		ReadTimeout: 50 * time.Millisecond,
	})
	return NewChainer(client, "unreachable:chain:")
}

// TestNATSAuditConsumer_OversizedEventDropped verifies the 1 MiB size
// guard fires before json.Unmarshal. A crafted >1 MiB payload must be
// ack-skipped (nil error so JetStream does NOT redeliver it forever), and
// must never reach the exporter — so a malicious / misconfigured producer
// cannot starve the queue-group worker with a giant allocation.
func TestNATSAuditConsumer_OversizedEventDropped(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}
	if _, err := NewNATSAuditConsumer(bus, mock); err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	// Build a payload just over the cap. The content does not have to be
	// valid JSON — the guard must short-circuit before the unmarshal.
	oversized := make([]byte, maxAuditEventBytes+1)
	for i := range oversized {
		oversized[i] = 'x'
	}
	packet := &pb.BusPacket{
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				SourceComponent: "audit-export",
				Message:         string(oversized),
			},
		},
	}

	if err := handler(packet); err != nil {
		t.Fatalf("handler should ack oversized payload to avoid redelivery loop, got: %v", err)
	}
	if got := mock.totalEvents(); got != 0 {
		t.Fatalf("oversized event must not reach exporter; got %d", got)
	}

	// Subsequent well-formed events must still flow — the subscription
	// loop must not have been poisoned by the oversized drop.
	if err := handler(packetFor(t, testEvent())); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got := mock.totalEvents(); got != 1 {
		t.Fatalf("follow-up legit event must export; got %d", got)
	}
}

// TestNATSAuditConsumer_AtCapStillProcessed verifies the guard is strictly
// `> maxAuditEventBytes` — a payload exactly at the cap is still processed,
// not dropped. This keeps the boundary condition honest and documents that
// a legitimate producer hitting the cap does not get silently discarded.
func TestNATSAuditConsumer_AtCapStillProcessed(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}
	if _, err := NewNATSAuditConsumer(bus, mock); err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	// Serialize a legit event, then pad its Reason field to land exactly
	// at maxAuditEventBytes. We cannot just hand-craft JSON because the
	// consumer validates shape via SIEMEvent unmarshal.
	ev := testEvent()
	base, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal base event: %v", err)
	}
	// Calculate padding to land the re-marshaled payload at the cap.
	// We pad the Reason field; each char of Reason adds one byte (plus
	// zero-width JSON escapes for plain ASCII).
	padLen := maxAuditEventBytes - len(base)
	if padLen <= 0 {
		t.Fatalf("base event already >= cap (%d bytes); adjust testEvent", len(base))
	}
	padding := make([]byte, padLen)
	for i := range padding {
		padding[i] = 'a'
	}
	ev.Reason += string(padding)
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal padded event: %v", err)
	}
	// Trim a couple of bytes if the JSON-encoding added envelope chars so
	// we land at-cap rather than just over.
	if len(payload) > maxAuditEventBytes {
		trim := len(payload) - maxAuditEventBytes
		ev.Reason = ev.Reason[:len(ev.Reason)-trim]
		payload, err = json.Marshal(ev)
		if err != nil {
			t.Fatalf("re-marshal after trim: %v", err)
		}
	}
	if len(payload) > maxAuditEventBytes {
		t.Fatalf("payload sizing overshot cap: %d > %d", len(payload), maxAuditEventBytes)
	}
	packet := &pb.BusPacket{
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				SourceComponent: "audit-export",
				Message:         string(payload),
			},
		},
	}

	if err := handler(packet); err != nil {
		t.Fatalf("handler should accept at-cap payload: %v", err)
	}
	if got := mock.totalEvents(); got != 1 {
		t.Fatalf("at-cap event must reach exporter; got %d", got)
	}
}
