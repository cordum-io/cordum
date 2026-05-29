package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
)

type auditBusLoopback struct {
	mu       sync.Mutex
	handlers map[string][]func(*pb.BusPacket) error
}

func newAuditBusLoopback() *auditBusLoopback {
	return &auditBusLoopback{handlers: make(map[string][]func(*pb.BusPacket) error)}
}

func (b *auditBusLoopback) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	handlers := append([]func(*pb.BusPacket) error{}, b.handlers[subject]...)
	b.mu.Unlock()
	for _, h := range handlers {
		if err := h(packet); err != nil {
			return err
		}
	}
	return nil
}

func (b *auditBusLoopback) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[subject] = append(b.handlers[subject], handler)
	return nil
}

func TestInitAuditPipeline_NullBackendChainsEvents(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "null")
	t.Setenv("AUDIT_TRANSPORT", "nats")

	s, _, _ := newTestGateway(t)
	bus := newAuditBusLoopback()
	run := &wf.WorkflowRun{
		ID:         "run-pipeline-test",
		WorkflowID: "wf-pipeline-test",
		Status:     wf.RunStatusRunning,
		Steps: map[string]*wf.StepRun{
			"step-1": {StepID: "step-1", Status: wf.StepStatusRunning, JobID: "run-pipeline-test:step-1@1"},
		},
	}
	if err := s.workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	sender, chainer, err := initAuditPipeline(s.jobStore.Client(), bus, nil, s.workflowStore)
	if err != nil {
		t.Fatalf("initAuditPipeline: %v", err)
	}
	if sender == nil {
		t.Fatal("expected non-nil audit sender")
	}
	if chainer == nil {
		t.Fatal("expected non-nil audit chainer")
	}
	t.Cleanup(func() { _ = sender.Close() })

	sender.Send(audit.SIEMEvent{
		EventType: audit.EventSafetyDecision,
		Severity:  audit.SeverityInfo,
		TenantID:  "default",
		Action:    "pipeline-test",
		JobID:     run.Steps["step-1"].JobID,
	})

	streamKey := chainer.StreamKey("default")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result, err := audit.VerifyChain(context.Background(), s.redisClient(), streamKey, audit.VerifyOptions{})
		if err != nil {
			t.Fatalf("VerifyChain: %v", err)
		}
		if result.Status == audit.VerifyStatusOK && result.TotalEvents >= 1 && len(result.Gaps) == 0 {
			updated, err := s.workflowStore.GetRun(context.Background(), run.ID)
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			if updated.Steps["step-1"].AuditHash == "" {
				t.Fatal("expected NATS audit consumer wiring to populate workflow step audit hash")
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	result, err := audit.VerifyChain(context.Background(), s.redisClient(), streamKey, audit.VerifyOptions{})
	if err != nil {
		t.Fatalf("VerifyChain final: %v", err)
	}
	t.Fatalf("verify result = %+v, want status=ok total_events>=1 gaps=0", result)
}

// hmacTestKey64Hex is a 64-hex-char (32-byte) test signing key.
// Generated for tests only; never use a hardcoded key in production.
const hmacTestKey64Hex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestInitAuditPipeline_HMACRequiredInProduction pins BUG-012: in
// production, an empty CORDUM_AUDIT_HMAC_KEY must abort boot unless the
// operator opts in via CORDUM_AUDIT_HMAC_OPTIONAL=true. Without this gate,
// a Redis-write attacker could forge a chain that looks consistent under
// SHA-256 verification, defeating ProvenanceGate's audit_chain_compromised
// signal.
func TestInitAuditPipeline_HMACRequiredInProduction(t *testing.T) {
	t.Setenv("CORDUM_ENV", "production")
	t.Setenv("CORDUM_PRODUCTION", "")
	t.Setenv(audit.EnvHMACKey, "")
	t.Setenv(audit.EnvHMACOptional, "")

	s, _, _ := newTestGateway(t)

	_, _, err := initAuditPipeline(s.jobStore.Client(), nil, nil, s.workflowStore)
	if err == nil {
		t.Fatal("expected initAuditPipeline to refuse production boot without HMAC key")
	}
	msg := err.Error()
	if !strings.Contains(msg, audit.EnvHMACKey) {
		t.Errorf("error %q missing %s", msg, audit.EnvHMACKey)
	}
	if !strings.Contains(msg, audit.EnvHMACOptional) {
		t.Errorf("error %q missing %s", msg, audit.EnvHMACOptional)
	}
	if !strings.Contains(msg, "required in production") {
		t.Errorf("error %q missing \"required in production\"", msg)
	}
}

// TestInitAuditPipeline_HMACOptionalOverrideInProduction verifies the
// escape hatch: with CORDUM_AUDIT_HMAC_OPTIONAL=true, production boot
// proceeds without the HMAC key and the chainer reports HMAC disabled.
// The slog.Warn emitted by the override path is observable via the boot
// log key/value `hmac_enabled` (preserved in initAuditPipeline).
func TestInitAuditPipeline_HMACOptionalOverrideInProduction(t *testing.T) {
	t.Setenv("CORDUM_ENV", "production")
	t.Setenv("CORDUM_PRODUCTION", "")
	t.Setenv(audit.EnvHMACKey, "")
	t.Setenv(audit.EnvHMACOptional, "true")

	s, _, _ := newTestGateway(t)

	sender, chainer, err := initAuditPipeline(s.jobStore.Client(), nil, nil, s.workflowStore)
	if err != nil {
		t.Fatalf("initAuditPipeline with override: %v", err)
	}
	if chainer == nil {
		t.Fatal("expected non-nil chainer when override is active")
	}
	if chainer.HMACEnabled() {
		t.Error("expected HMACEnabled=false when override bypasses the key check")
	}
	if sender != nil {
		t.Cleanup(func() { _ = sender.Close() })
	}
}

// TestInitAuditPipeline_HMACAllowedInDev confirms the production gate does
// not regress the dev default — boot succeeds with no HMAC key in
// development mode.
func TestInitAuditPipeline_HMACAllowedInDev(t *testing.T) {
	t.Setenv("CORDUM_ENV", "development")
	t.Setenv("CORDUM_PRODUCTION", "")
	t.Setenv(audit.EnvHMACKey, "")
	t.Setenv(audit.EnvHMACOptional, "")

	s, _, _ := newTestGateway(t)

	sender, chainer, err := initAuditPipeline(s.jobStore.Client(), nil, nil, s.workflowStore)
	if err != nil {
		t.Fatalf("initAuditPipeline in dev: %v", err)
	}
	if chainer == nil {
		t.Fatal("expected non-nil chainer in dev")
	}
	if chainer.HMACEnabled() {
		t.Error("expected HMACEnabled=false when no HMAC key is set in dev")
	}
	if sender != nil {
		t.Cleanup(func() { _ = sender.Close() })
	}
}

// TestInitAuditPipeline_HMACConfiguredInProduction is the happy path:
// production with a valid 32-byte hex-encoded key boots cleanly and the
// chainer reports HMAC enabled.
func TestInitAuditPipeline_HMACConfiguredInProduction(t *testing.T) {
	t.Setenv("CORDUM_ENV", "production")
	t.Setenv("CORDUM_PRODUCTION", "")
	t.Setenv(audit.EnvHMACKey, hmacTestKey64Hex)
	t.Setenv(audit.EnvHMACOptional, "")

	s, _, _ := newTestGateway(t)

	sender, chainer, err := initAuditPipeline(s.jobStore.Client(), nil, nil, s.workflowStore)
	if err != nil {
		t.Fatalf("initAuditPipeline with valid HMAC key: %v", err)
	}
	if chainer == nil {
		t.Fatal("expected non-nil chainer with HMAC configured")
	}
	if !chainer.HMACEnabled() {
		t.Error("expected HMACEnabled=true with valid 32-byte hex key")
	}
	if sender != nil {
		t.Cleanup(func() { _ = sender.Close() })
	}
}
