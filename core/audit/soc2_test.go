package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestDefaultSOC2Mapping_CoversAllEventTypes guards against a new
// EventType constant landing in exporter.go without a SOC2 control
// entry. Compliance exports rely on every shipped event having a
// non-empty mapping.
func TestDefaultSOC2Mapping_CoversAllEventTypes(t *testing.T) {
	m := DefaultSOC2Mapping()
	every := []string{
		EventSafetyDecision,
		EventSafetyApproval,
		EventPolicyChange,
		EventSafetyViolation,
		EventSystemAuth,
		EventMCPToolApproval,
		EventMCPToolDenied,
		EventShadowEval,
	}
	for _, et := range every {
		if controls, ok := m[et]; !ok || len(controls) == 0 {
			t.Errorf("EventType %q has no SOC2 controls mapped (add to DefaultSOC2Mapping)", et)
		}
	}
}

// TestControlsFor_EmptyOnUnknownType pins the contract that downstream
// JSON serialisation emits [] not null for unknown event types. If the
// contract ever flips to returning nil, the export writer must be
// updated to handle both shapes.
func TestControlsFor_EmptyOnUnknownType(t *testing.T) {
	m := DefaultSOC2Mapping()
	ev := SIEMEvent{EventType: "some.unknown.type"}
	got := m.ControlsFor(ev)
	if got == nil {
		t.Fatal("ControlsFor returned nil; must be non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
	// JSON round-trip: must serialise as [].
	b, err := json.Marshal(map[string][]string{"soc2_controls": got})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{"soc2_controls":[]}` {
		t.Errorf("expected []; got %s", b)
	}
}

// TestControlsFor_SafetyDecisionDenyOverlay exercises the
// decision-overlay path: a safety.decision with Decision=deny adds
// CC7.3 on top of the base CC7.2.
func TestControlsFor_SafetyDecisionDenyOverlay(t *testing.T) {
	m := DefaultSOC2Mapping()

	allow := SIEMEvent{EventType: EventSafetyDecision, Decision: "allow"}
	if got := m.ControlsFor(allow); !reflect.DeepEqual(got, []string{"CC7.2"}) {
		t.Errorf("allow controls = %v, want [CC7.2]", got)
	}

	deny := SIEMEvent{EventType: EventSafetyDecision, Decision: "deny"}
	if got := m.ControlsFor(deny); !reflect.DeepEqual(got, []string{"CC7.2", "CC7.3"}) {
		t.Errorf("deny controls = %v, want [CC7.2 CC7.3]", got)
	}

	// Case-insensitive: the gateway uppercases decisions in some paths.
	denyUpper := SIEMEvent{EventType: EventSafetyDecision, Decision: "DENY"}
	if got := m.ControlsFor(denyUpper); !reflect.DeepEqual(got, []string{"CC7.2", "CC7.3"}) {
		t.Errorf("DENY controls = %v, want [CC7.2 CC7.3]", got)
	}
}

// TestControlsFor_MCPToolApprovalRevokeOverlay verifies the Extra-based
// overlay: outcome=revoke adds CC6.3.
func TestControlsFor_MCPToolApprovalRevokeOverlay(t *testing.T) {
	m := DefaultSOC2Mapping()

	approved := SIEMEvent{
		EventType: EventMCPToolApproval,
		Extra:     map[string]string{"outcome": "approved"},
	}
	if got := m.ControlsFor(approved); !reflect.DeepEqual(got, []string{"CC6.1", "CC7.2"}) {
		t.Errorf("approved outcome controls = %v", got)
	}

	revoke := SIEMEvent{
		EventType: EventMCPToolApproval,
		Extra:     map[string]string{"outcome": "revoke"},
	}
	if got := m.ControlsFor(revoke); !reflect.DeepEqual(got, []string{"CC6.1", "CC6.3", "CC7.2"}) {
		t.Errorf("revoke outcome controls = %v", got)
	}
}

// TestControlsFor_DeduplicatesAndSorts ensures a mapping with
// overlapping controls (overlay == base) does not emit duplicates and
// the output is deterministic.
func TestControlsFor_DeduplicatesAndSorts(t *testing.T) {
	m := SOC2Mapping{
		EventSafetyDecision: {"CC7.3", "CC7.2", "CC7.2"}, // duplicates + unsorted
	}
	ev := SIEMEvent{EventType: EventSafetyDecision, Decision: "deny"}
	got := m.ControlsFor(ev)
	want := []string{"CC7.2", "CC7.3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("controls = %v, want %v", got, want)
	}
}

// TestLoadSOC2Mapping_EmptyPathReturnsDefault is the happy path when
// the env var isn't set.
func TestLoadSOC2Mapping_EmptyPathReturnsDefault(t *testing.T) {
	m, err := LoadSOC2Mapping("")
	if err != nil {
		t.Fatalf("LoadSOC2Mapping: %v", err)
	}
	if !reflect.DeepEqual(m, DefaultSOC2Mapping()) {
		t.Errorf("empty-path override did not return default")
	}
}

// TestLoadSOC2Mapping_OverrideMergesWithDefault checks that an
// override YAML merges OVER the default rather than replacing it, so
// a partial override doesn't silently lose controls on unreferenced
// event types.
func TestLoadSOC2Mapping_OverrideMergesWithDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "override.yaml")
	content := []byte(`
safety.decision:
  - CC9.1
custom.event_type:
  - CC10.2
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err := LoadSOC2Mapping(path)
	if err != nil {
		t.Fatalf("LoadSOC2Mapping: %v", err)
	}

	// Override replaces safety.decision entirely.
	decision := SIEMEvent{EventType: EventSafetyDecision}
	if got := m.ControlsFor(decision); !reflect.DeepEqual(got, []string{"CC9.1"}) {
		t.Errorf("override safety.decision = %v, want [CC9.1]", got)
	}

	// Custom event type picked up.
	custom := SIEMEvent{EventType: "custom.event_type"}
	if got := m.ControlsFor(custom); !reflect.DeepEqual(got, []string{"CC10.2"}) {
		t.Errorf("custom override = %v, want [CC10.2]", got)
	}

	// Unreferenced default (safety.approval) must still be present.
	approval := SIEMEvent{EventType: EventSafetyApproval}
	if got := m.ControlsFor(approval); len(got) == 0 {
		t.Errorf("default safety.approval lost after partial override")
	}
}

// TestLoadSOC2Mapping_MissingPathFallsBack covers the "operator set
// the env var but the file isn't there" case — we fall back to default
// with a warn, never error.
func TestLoadSOC2Mapping_MissingPathFallsBack(t *testing.T) {
	m, err := LoadSOC2Mapping("/nonexistent/path/soc2.yaml")
	if err != nil {
		t.Fatalf("LoadSOC2Mapping returned error on missing path: %v", err)
	}
	if !reflect.DeepEqual(m, DefaultSOC2Mapping()) {
		t.Errorf("missing path did not fall back to default")
	}
}

// TestLoadSOC2Mapping_MalformedYAMLFallsBack ensures malformed YAML is
// warned-about, not fatal — a compliance misconfig shouldn't take out
// the gateway.
func TestLoadSOC2Mapping_MalformedYAMLFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(path, []byte("not valid: yaml: [}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err := LoadSOC2Mapping(path)
	if err != nil {
		t.Fatalf("LoadSOC2Mapping: %v", err)
	}
	if !reflect.DeepEqual(m, DefaultSOC2Mapping()) {
		t.Errorf("malformed YAML did not fall back to default")
	}
}

// TestLoadSOC2MappingFromEnv reads the env var and honours it without
// requiring explicit plumbing.
func TestLoadSOC2MappingFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.yaml")
	if err := os.WriteFile(path, []byte("system.auth: [\"CC9.9\"]\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(EnvSOC2MappingPath, path)
	m := LoadSOC2MappingFromEnv()
	ev := SIEMEvent{EventType: EventSystemAuth}
	if got := m.ControlsFor(ev); !reflect.DeepEqual(got, []string{"CC9.9"}) {
		t.Errorf("env-loaded override missed: got %v", got)
	}
}

// TestDefaultSOC2Legend_CoversEveryControlInDefaultMapping keeps the
// legend honest: if a new control code shows up in the default map we
// must add a description here.
func TestDefaultSOC2Legend_CoversEveryControlInDefaultMapping(t *testing.T) {
	legend := DefaultSOC2Legend()
	m := DefaultSOC2Mapping()
	// Also include overlay controls.
	needed := map[string]struct{}{}
	for _, ctrls := range m {
		for _, c := range ctrls {
			needed[c] = struct{}{}
		}
	}
	needed["CC7.3"] = struct{}{} // deny overlay
	needed["CC6.3"] = struct{}{} // revoke overlay
	for c := range needed {
		if _, ok := legend[c]; !ok {
			t.Errorf("SOC2 legend missing description for control %q", c)
		}
	}
}

// TestSOC2Mapping_StringDeterministic pins the String() shape so
// manifest logging stays grep-friendly.
func TestSOC2Mapping_StringDeterministic(t *testing.T) {
	m := SOC2Mapping{
		"b": {"x", "y"},
		"a": {"z"},
	}
	got1 := m.String()
	got2 := m.String()
	if got1 != got2 {
		t.Errorf("String() non-deterministic: %q vs %q", got1, got2)
	}
	// Sanity-check the shape.
	if got1 != "{a=[z] b=[x,y]}" {
		t.Errorf("String() = %q, want {a=[z] b=[x,y]}", got1)
	}
}

// TestControlsFor_NilMapReturnsEmpty — calling ControlsFor on a nil
// receiver must not panic and must return [].
func TestControlsFor_NilMapReturnsEmpty(t *testing.T) {
	var m SOC2Mapping
	got := m.ControlsFor(SIEMEvent{EventType: EventSystemAuth})
	if got == nil || len(got) != 0 {
		t.Errorf("nil map ControlsFor = %v, want empty non-nil slice", got)
	}
}

// TestEventCategories_CoversAllEventTypes is the CI guard for governance
// filtering: every Event* constant enumerated in AllEventTypes MUST have an
// explicit entry in the eventCategories map. CategoryFor fail-opens to
// governance, so a missing mapping would NOT surface via CategoryFor alone —
// this test inspects the map directly so a newly-added Event* constant left
// uncategorised fails the build rather than silently defaulting.
func TestEventCategories_CoversAllEventTypes(t *testing.T) {
	if len(AllEventTypes) < 46 {
		t.Fatalf("AllEventTypes has %d entries, want >= 46 (a new Event* constant must be appended to AllEventTypes)", len(AllEventTypes))
	}
	seen := make(map[string]struct{}, len(AllEventTypes))
	for _, et := range AllEventTypes {
		if et == "" {
			t.Error("AllEventTypes contains an empty string")
			continue
		}
		if _, dup := seen[et]; dup {
			t.Errorf("AllEventTypes contains duplicate %q", et)
		}
		seen[et] = struct{}{}
		cat, ok := eventCategories[et]
		if !ok {
			t.Errorf("event type %q has no explicit eventCategories entry (add it to eventCategories in soc2.go)", et)
			continue
		}
		if cat != CategoryGovernance && cat != CategoryRoutine {
			t.Errorf("event type %q mapped to invalid category %q", et, cat)
		}
	}
}

// TestEventCategories_AllValuesValid pins that every value in the map (incl.
// the bare-string event types emitted outside exporter.go) is one of the two
// known categories, and that the three known bare strings are routine.
func TestEventCategories_AllValuesValid(t *testing.T) {
	for et, cat := range eventCategories {
		if cat != CategoryGovernance && cat != CategoryRoutine {
			t.Errorf("eventCategories[%q] = %q, want governance|routine", et, cat)
		}
	}
	for _, bare := range []string{"audit.read.events", "mcp.tool_called", "worker_handshake"} {
		if got := eventCategories[bare]; got != CategoryRoutine {
			t.Errorf("bare event %q category = %q, want routine", bare, got)
		}
	}
}

// TestCategoryFor covers the routine/governance split plus the FAIL-OPEN
// contract: any unknown or empty event type resolves to governance so a new
// or caller-supplied event is never silently hidden from a governance export.
func TestCategoryFor(t *testing.T) {
	cases := []struct {
		name string
		et   string
		want string
	}{
		{"unknown fail-open to governance", "some.brand.new.event", CategoryGovernance},
		{"empty fail-open to governance", "", CategoryGovernance},
		{"routine system.auth", EventSystemAuth, CategoryRoutine},
		{"routine audit.read.events bare", "audit.read.events", CategoryRoutine},
		{"routine mcp.tool_invocation", EventMCPToolInvocation, CategoryRoutine},
		{"routine topic_registered", EventTopicRegistered, CategoryRoutine},
		{"routine edge.session_started", EventEdgeSessionStarted, CategoryRoutine},
		{"governance safety.decision", EventSafetyDecision, CategoryGovernance},
		{"governance governance.decision", EventGovernanceDecision, CategoryGovernance},
		{"governance auth.api_key_revoked", EventAuthAPIKeyRevoked, CategoryGovernance},
		{"governance edge.action_denied", EventEdgeActionDenied, CategoryGovernance},
		{"governance shadow_agent.detected", EventShadowAgentDetected, CategoryGovernance},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CategoryFor(tc.et); got != tc.want {
				t.Errorf("CategoryFor(%q) = %q, want %q", tc.et, got, tc.want)
			}
		})
	}
}

// TestIsGovernanceEvent is the boolean façade over CategoryFor.
func TestIsGovernanceEvent(t *testing.T) {
	if !IsGovernanceEvent(EventSafetyDecision) {
		t.Error("safety.decision must be governance")
	}
	if IsGovernanceEvent(EventSystemAuth) {
		t.Error("system.auth must not be governance (routine)")
	}
	if !IsGovernanceEvent("unknown.event.type") {
		t.Error("unknown event must fail-open to governance")
	}
}
