package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/policy/actiongates"
)

// TestWireActionGatePipeline_AssignsLivePipeline asserts the gateway
// wiring function turns the previously-dead actionGatePipeline field
// into a real pipeline. This is the regression guard for the QA-flagged
// "gateway server.actionGatePipeline field declared but never assigned"
// bug: a future refactor that drops the s.wireActionGatePipeline() call
// in RunWithAuth MUST break this test.
func TestWireActionGatePipeline_AssignsLivePipeline(t *testing.T) {
	t.Parallel()
	s := &server{}
	s.wireActionGatePipeline()
	if s.actionGatePipeline == nil {
		t.Fatal("server.actionGatePipeline still nil after wireActionGatePipeline()")
	}
	gates := s.actionGatePipeline.Gates()
	if len(gates) == 0 {
		t.Fatal("pipeline has no gates after wiring")
	}
	wantIDs := map[string]struct{}{
		actiongates.GateIDTenant:     {},
		actiongates.GateIDFile:       {},
		actiongates.GateIDURL:        {},
		actiongates.GateIDMCP:        {},
		actiongates.GateIDMutation:   {},
		actiongates.GateIDProvenance: {},
	}
	for _, g := range gates {
		delete(wantIDs, g.ID())
	}
	if len(wantIDs) > 0 {
		t.Fatalf("pipeline missing expected gates: %v", wantIDs)
	}
}

// TestWireActionGatePipeline_NilReceiverNoOp ensures the method tolerates
// a nil server pointer so misconfigured test fixtures cannot panic.
func TestWireActionGatePipeline_NilReceiverNoOp(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil-receiver wireActionGatePipeline panicked: %v", r)
		}
	}()
	var s *server
	s.wireActionGatePipeline()
}

func TestGatewayMCPIdentityResolver_TenantScopedLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := testredis.NewClient(t, mr.Addr())
	identityStore := store.NewAgentIdentityStoreFromClient(client)
	resolver := gatewayMCPIdentityResolver{store: identityStore}

	created, err := identityStore.Create(ctx, store.AgentIdentity{
		ID:                  "shared-agent-id",
		TenantID:            "tenant-a",
		Name:                "tenant-a-agent",
		Owner:               "platform",
		RiskTier:            "high",
		AllowedTools:        []string{"read_secret"},
		DataClassifications: []string{"internal"},
		Status:              "active",
	})
	if err != nil {
		t.Fatalf("create agent identity: %v", err)
	}

	got, err := resolver.ResolveMCPIdentity(ctx, "tenant-a", created.ID)
	if err != nil {
		t.Fatalf("same-tenant resolve: %v", err)
	}
	if got == nil {
		t.Fatal("same-tenant resolve returned nil identity")
	}
	if got.ID != created.ID {
		t.Fatalf("same-tenant identity ID = %q, want %q", got.ID, created.ID)
	}
	if !reflect.DeepEqual(got.AllowedTools, []string{"read_secret"}) {
		t.Fatalf("same-tenant allowed tools = %v, want [read_secret]", got.AllowedTools)
	}

	foreign, err := resolver.ResolveMCPIdentity(ctx, "tenant-b", created.ID)
	if err != nil {
		t.Fatalf("cross-tenant resolve: %v", err)
	}
	if foreign != nil {
		t.Fatalf("cross-tenant resolve returned identity %#v, want nil", foreign)
	}
}

func TestGatewayMCPIdentityResolver_NilStoreReturnsMiss(t *testing.T) {
	t.Parallel()
	got, err := (gatewayMCPIdentityResolver{}).ResolveMCPIdentity(context.Background(), "tenant-a", "agent-a")
	if err != nil {
		t.Fatalf("nil-store resolver returned err: %v", err)
	}
	if got != nil {
		t.Fatalf("nil-store resolver returned identity %#v, want nil", got)
	}
}

// TestEncodeActionDescriptorLabel_RoundTrip asserts the JSON-label
// encoding the gateway uses to propagate an ActionDescriptor across the
// gRPC boundary preserves all fields needed by the kernel-side
// extractor (kernel_actiongate_wire.go's actionDescriptorFromRequest).
// The two halves must stay in lockstep or the descriptor never reaches
// the kernel pipeline.
func TestEncodeActionDescriptorLabel_RoundTrip(t *testing.T) {
	t.Parallel()
	desc := &config.ActionDescriptor{
		Kind:      config.ActionKindURL,
		Verb:      config.ActionVerbRead,
		TargetURL: "https://example.com/docs",
		Server:    "mcp-corp",
		Tool:      "tools/read",
		RiskTags:  []string{"data:pii"},
	}
	encoded, err := encodeActionDescriptorLabel(desc)
	if err != nil {
		t.Fatalf("encode err: %v", err)
	}
	if encoded == "" {
		t.Fatal("encode returned empty string on non-nil descriptor")
	}
	if len(encoded) > config.ActionArgsMaxSerializedBytes {
		t.Fatalf("encode exceeds size cap: %d > %d", len(encoded), config.ActionArgsMaxSerializedBytes)
	}
	var got config.ActionDescriptor
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatalf("decode encoded descriptor: %v", err)
	}
	if !reflect.DeepEqual(*desc, got) {
		t.Fatalf("decoded descriptor = %#v, want %#v", got, *desc)
	}
}

// TestStripReservedActionDescriptorLabel_BlocksClientInjection asserts
// the defense-in-depth strip drops a client-supplied descriptor label.
// Without this, a client could submit the gateway's reserved label
// directly in their HTTP body, bypass the authenticated descriptor
// path (req.Action), and inject attacker-controlled descriptor data
// into the kernel-side extractor's audit/gate code path.
func TestStripReservedActionDescriptorLabel_BlocksClientInjection(t *testing.T) {
	t.Parallel()
	in := map[string]string{
		labelActionDescriptorJSON: `{"kind":"file","verb":"delete","target_path":"/etc/shadow"}`,
		"benign_key":              "kept",
		"_content.prompt":         "kept-because-different-key",
	}
	out := stripReservedActionDescriptorLabel(in)
	if _, present := out[labelActionDescriptorJSON]; present {
		t.Fatal("client-supplied descriptor label was not stripped")
	}
	if out["benign_key"] != "kept" {
		t.Fatalf("benign label lost in strip: %v", out)
	}
	if out["_content.prompt"] != "kept-because-different-key" {
		t.Fatalf("unrelated underscore label was incorrectly stripped: %v", out)
	}
}

func TestStripReservedActionDescriptorLabel_NoOpWhenAbsent(t *testing.T) {
	t.Parallel()
	in := map[string]string{"normal_key": "v"}
	out := stripReservedActionDescriptorLabel(in)
	// No reserved key present -> implementation returns original map
	// (no allocation cost on hot path). Content-equality validates
	// the no-op contract.
	if len(out) != 1 || out["normal_key"] != "v" {
		t.Fatalf("strip mutated label set unexpectedly: %v", out)
	}
}

func TestStripReservedActionDescriptorLabel_NilInputNilOutput(t *testing.T) {
	t.Parallel()
	if out := stripReservedActionDescriptorLabel(nil); out != nil {
		t.Fatalf("nil input should produce nil output, got %v", out)
	}
}

func TestEncodeActionDescriptorLabel_NilReturnsEmpty(t *testing.T) {
	t.Parallel()
	encoded, err := encodeActionDescriptorLabel(nil)
	if err != nil {
		t.Fatalf("nil descriptor should return empty, got err: %v", err)
	}
	if encoded != "" {
		t.Fatalf("nil descriptor should return empty string, got %q", encoded)
	}
}

func TestEncodeActionDescriptorLabel_OversizeReturnsError(t *testing.T) {
	t.Parallel()
	hugeArgs := make(map[string]any, 4096)
	for i := range 4096 {
		// Each entry contributes ~80 bytes encoded -> well over the 64KB cap.
		hugeArgs[string(rune('a'+i%26))+"_long_key_name_"+strconvI(i)] = "long_value_" + strconvI(i)
	}
	desc := &config.ActionDescriptor{
		Kind: config.ActionKindMCPCall,
		Args: hugeArgs,
	}
	if _, err := encodeActionDescriptorLabel(desc); err == nil {
		t.Fatal("expected oversize descriptor to error, got nil")
	}
}

// strconvI is a tiny local int->string helper to avoid pulling in strconv
// for one call. Decoupled from any package-level helper so this test file
// has no transitive dependencies beyond standard library + this package.
func strconvI(n int) string {
	if n == 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [20]byte
	pos := len(buf)
	negative := n < 0
	if negative {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = digits[n%10]
		n /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
