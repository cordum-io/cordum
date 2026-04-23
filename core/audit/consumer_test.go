package audit

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
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
		"":            ChainFailStrict,
		"strict":      ChainFailStrict,
		"STRICT":      ChainFailStrict,
		"permissive":  ChainFailPermissive,
		"Permissive":  ChainFailPermissive,
		" permissive ": ChainFailPermissive,
		"garbage":     ChainFailStrict,
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
