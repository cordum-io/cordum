package safetykernel

import (
	"testing"
)

func TestAmountThresholdDeriver_MockBankThresholds(t *testing.T) {
	deriver := MockBankTransferDeriver()

	tests := []struct {
		name        string
		payloadJSON string
		wantTags    []string
	}{
		{
			name:        "low_amount_50",
			payloadJSON: `{"amount": 50, "currency": "USD"}`,
			wantTags:    []string{"finance", "transfer", "low"},
		},
		{
			name:        "zero_amount_fails_closed",
			payloadJSON: `{"amount": 0}`,
			// Amount 0 is invalid (workflow only routes >0) → fail-closed
			wantTags: []string{"finance", "transfer", "blocked"},
		},
		{
			name:        "low_amount_99",
			payloadJSON: `{"amount": 99.99}`,
			wantTags:    []string{"finance", "transfer", "low"},
		},
		{
			name:        "review_amount_100",
			payloadJSON: `{"amount": 100}`,
			wantTags:    []string{"finance", "transfer", "review"},
		},
		{
			name:        "review_amount_200",
			payloadJSON: `{"amount": 200}`,
			wantTags:    []string{"finance", "transfer", "review"},
		},
		{
			name:        "review_amount_299",
			payloadJSON: `{"amount": 299}`,
			wantTags:    []string{"finance", "transfer", "review"},
		},
		{
			name:        "blocked_amount_300",
			payloadJSON: `{"amount": 300}`,
			wantTags:    []string{"finance", "transfer", "blocked"},
		},
		{
			name:        "blocked_amount_500",
			payloadJSON: `{"amount": 500}`,
			wantTags:    []string{"finance", "transfer", "blocked"},
		},
		{
			name:        "blocked_amount_10000",
			payloadJSON: `{"amount": 10000}`,
			wantTags:    []string{"finance", "transfer", "blocked"},
		},
		{
			name:        "negative_amount_fails_closed",
			payloadJSON: `{"amount": -50}`,
			// Negative amount is invalid → fail-closed
			wantTags: []string{"finance", "transfer", "blocked"},
		},
		{
			name:        "string_amount_parsed",
			payloadJSON: `{"amount": "500"}`,
			wantTags:    []string{"finance", "transfer", "blocked"},
		},
		{
			name:        "missing_amount_fails_closed",
			payloadJSON: `{"currency": "USD"}`,
			// No amount → fail-closed → highest risk tag
			wantTags: []string{"finance", "transfer", "blocked"},
		},
		{
			name:        "non_numeric_amount_fails_closed",
			payloadJSON: `{"amount": "not-a-number"}`,
			wantTags:    []string{"finance", "transfer", "blocked"},
		},
		{
			name:        "null_amount_fails_closed",
			payloadJSON: `{"amount": null}`,
			wantTags:    []string{"finance", "transfer", "blocked"},
		},
		{
			name:        "empty_object_fails_closed",
			payloadJSON: `{}`,
			wantTags:    []string{"finance", "transfer", "blocked"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := map[string]string{
				"_content.payload_json": tt.payloadJSON,
			}
			tags := deriver("job.demo-mock-bank.transfer", labels, nil)
			if len(tags) != len(tt.wantTags) {
				t.Fatalf("expected %d tags %v, got %d tags %v", len(tt.wantTags), tt.wantTags, len(tags), tags)
			}
			for i := range tags {
				if tags[i] != tt.wantTags[i] {
					t.Errorf("tag[%d]: expected %q, got %q", i, tt.wantTags[i], tags[i])
				}
			}
		})
	}
}

func TestAmountThresholdDeriver_RawPayloadFallback(t *testing.T) {
	deriver := MockBankTransferDeriver()

	// When _content.payload_json is absent, deriver falls back to raw payload bytes.
	tags := deriver("job.demo-mock-bank.transfer", nil, []byte(`{"amount": 500}`))
	if len(tags) != 3 || tags[2] != "blocked" {
		t.Fatalf("expected [finance transfer blocked], got %v", tags)
	}
}

func TestAmountThresholdDeriver_InvalidJSON(t *testing.T) {
	deriver := MockBankTransferDeriver()

	// Invalid JSON → fail-closed → highest risk tag.
	labels := map[string]string{
		"_content.payload_json": "not json at all",
	}
	tags := deriver("job.demo-mock-bank.transfer", labels, nil)
	if len(tags) != 3 || tags[2] != "blocked" {
		t.Fatalf("expected fail-closed [finance transfer blocked], got %v", tags)
	}
}

func TestTagDeriverRegistry_Derive(t *testing.T) {
	registry := NewTagDeriverRegistry()

	// No deriver registered → returns false.
	tags, ok := registry.Derive("job.unknown.topic", nil, nil)
	if ok || tags != nil {
		t.Fatalf("expected no derivation for unknown topic, got %v, %v", tags, ok)
	}

	// Register a deriver.
	registry.Register("job.test.topic", func(topic string, labels map[string]string, payload []byte) []string {
		return []string{"derived-tag"}
	})

	tags, ok = registry.Derive("job.test.topic", nil, nil)
	if !ok || len(tags) != 1 || tags[0] != "derived-tag" {
		t.Fatalf("expected [derived-tag], got %v (ok=%v)", tags, ok)
	}

	// Other topics still unaffected.
	tags, ok = registry.Derive("job.other.topic", nil, nil)
	if ok || tags != nil {
		t.Fatalf("expected no derivation for other topic")
	}
}

func TestTagDeriverRegistry_HasDeriver(t *testing.T) {
	registry := NewTagDeriverRegistry()
	if registry.HasDeriver("job.test") {
		t.Fatal("expected false for unregistered topic")
	}
	registry.Register("job.test", func(string, map[string]string, []byte) []string {
		return []string{"x"}
	})
	if !registry.HasDeriver("job.test") {
		t.Fatal("expected true for registered topic")
	}
}

func TestTagDeriverRegistry_NilReturn(t *testing.T) {
	registry := NewTagDeriverRegistry()
	registry.Register("job.test", func(string, map[string]string, []byte) []string {
		return nil // deriver returns nil → no derivation
	})
	tags, ok := registry.Derive("job.test", nil, nil)
	if ok || tags != nil {
		t.Fatalf("expected no derivation when deriver returns nil, got %v (ok=%v)", tags, ok)
	}
}

func TestParseAmountFromJSON_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   float64
		wantOk bool
	}{
		{"integer", `{"amount": 42}`, 42, true},
		{"float", `{"amount": 99.99}`, 99.99, true},
		{"string_number", `{"amount": "250"}`, 250, true},
		{"string_with_spaces", `{"amount": " 150 "}`, 150, true},
		{"zero", `{"amount": 0}`, 0, true},
		{"negative", `{"amount": -10}`, -10, true},
		{"missing_field", `{"price": 100}`, 0, false},
		{"null_value", `{"amount": null}`, 0, false},
		{"boolean_value", `{"amount": true}`, 0, false},
		{"array_value", `{"amount": [1,2]}`, 0, false},
		{"object_value", `{"amount": {"val": 1}}`, 0, false},
		{"empty_string", `{"amount": ""}`, 0, false},
		{"non_numeric_string", `{"amount": "abc"}`, 0, false},
		{"invalid_json", `not json`, 0, false},
		{"empty_input", ``, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAmountFromJSON([]byte(tt.input))
			if ok != tt.wantOk {
				t.Fatalf("ok: expected %v, got %v", tt.wantOk, ok)
			}
			if ok && got != tt.want {
				t.Fatalf("amount: expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestExtractAmount_LabelPriority(t *testing.T) {
	// Label takes priority over raw payload.
	labels := map[string]string{
		"_content.payload_json": `{"amount": 100}`,
	}
	amount, ok := extractAmount(labels, []byte(`{"amount": 999}`))
	if !ok || amount != 100 {
		t.Fatalf("expected 100 from label, got %v (ok=%v)", amount, ok)
	}
}

func TestExtractAmount_FallbackToPayload(t *testing.T) {
	// No label → fall back to raw payload.
	amount, ok := extractAmount(nil, []byte(`{"amount": 42}`))
	if !ok || amount != 42 {
		t.Fatalf("expected 42 from payload, got %v (ok=%v)", amount, ok)
	}
}

func TestExtractAmount_NothingAvailable(t *testing.T) {
	amount, ok := extractAmount(nil, nil)
	if ok {
		t.Fatalf("expected false, got amount=%v", amount)
	}
}

func TestBuiltinTagDerivers_MockBankRegistered(t *testing.T) {
	registry := NewTagDeriverRegistry()
	registerBuiltinTagDerivers(registry)

	if !registry.HasDeriver("job.demo-mock-bank.transfer") {
		t.Fatal("mock-bank transfer deriver not registered")
	}

	// Verify it derives correctly for the red-team scenario: $500 with spoofed "low" tag.
	labels := map[string]string{
		"_content.payload_json": `{"amount": 500}`,
	}
	tags, ok := registry.Derive("job.demo-mock-bank.transfer", labels, nil)
	if !ok {
		t.Fatal("expected derivation for mock-bank topic")
	}
	// Must return "blocked", not "low"
	foundBlocked := false
	for _, tag := range tags {
		if tag == "blocked" {
			foundBlocked = true
		}
		if tag == "low" {
			t.Fatal("derived tags must NOT contain 'low' for $500 transfer — spoofing vulnerability")
		}
	}
	if !foundBlocked {
		t.Fatalf("derived tags must contain 'blocked' for $500 transfer, got %v", tags)
	}
}

func TestNamedDerivers_AmountThresholdExists(t *testing.T) {
	fn, ok := NamedDerivers["amount-threshold"]
	if !ok || fn == nil {
		t.Fatal("expected 'amount-threshold' named deriver to be registered")
	}
}

func TestLoadTagDeriversFromTopics(t *testing.T) {
	registry := NewTagDeriverRegistry()

	// Simulate pack-installed topic registrations with riskTagDeriver.
	entries := []topicDeriverEntry{
		{TopicName: "job.demo-mock-bank.transfer", DeriverName: "amount-threshold"},
		{TopicName: "job.no-deriver-topic", DeriverName: ""},           // no deriver
		{TopicName: "job.unknown-deriver-topic", DeriverName: "bogus"}, // unknown deriver
	}

	n := loadTagDeriversFromTopics(registry, entries)
	if n != 1 {
		t.Fatalf("expected 1 deriver registered, got %d", n)
	}

	// Verify the mock-bank topic got its deriver from the pack manifest path.
	if !registry.HasDeriver("job.demo-mock-bank.transfer") {
		t.Fatal("expected mock-bank deriver to be registered via pack manifest path")
	}

	// Verify unknown deriver name was skipped.
	if registry.HasDeriver("job.unknown-deriver-topic") {
		t.Fatal("expected unknown deriver name to be skipped")
	}

	// Verify empty deriver name was skipped.
	if registry.HasDeriver("job.no-deriver-topic") {
		t.Fatal("expected empty deriver name to be skipped")
	}

	// Verify the registered deriver actually works (derives "blocked" for $500).
	tags, ok := registry.Derive("job.demo-mock-bank.transfer", map[string]string{
		"_content.payload_json": `{"amount": 500}`,
	}, nil)
	if !ok {
		t.Fatal("expected derivation to succeed")
	}
	foundBlocked := false
	for _, tag := range tags {
		if tag == "blocked" {
			foundBlocked = true
		}
	}
	if !foundBlocked {
		t.Fatalf("expected 'blocked' tag for $500, got %v", tags)
	}
}

func TestLoadTagDeriversFromTopics_RuntimeReload(t *testing.T) {
	// Simulate the runtime reload path: a server starts with no pack-installed
	// derivers, then a pack install adds a deriver via the topic registry,
	// and the reload path picks it up without a restart.
	registry := NewTagDeriverRegistry()

	// Initially: no deriver for a custom topic.
	if registry.HasDeriver("job.custom-pack.process") {
		t.Fatal("expected no deriver before reload")
	}

	// Simulate pack install writing to topic registry: new topic with deriver.
	entries := []topicDeriverEntry{
		{TopicName: "job.custom-pack.process", DeriverName: "amount-threshold"},
	}
	n := loadTagDeriversFromTopics(registry, entries)
	if n != 1 {
		t.Fatalf("expected 1 deriver after reload, got %d", n)
	}

	// Now the deriver should be active.
	if !registry.HasDeriver("job.custom-pack.process") {
		t.Fatal("expected deriver after reload")
	}

	// Verify it produces correct tags.
	tags, ok := registry.Derive("job.custom-pack.process", map[string]string{
		"_content.payload_json": `{"amount": 500}`,
	}, nil)
	if !ok || len(tags) == 0 {
		t.Fatal("expected derivation after runtime reload")
	}
}

func TestLoadTagDeriversFromTopics_RemovalOnReload(t *testing.T) {
	// Regression test: after pack uninstall or riskTagDeriver cleared, the
	// reload must remove stale derivers. A running kernel must stop overriding
	// risk_tags for topics that no longer declare a deriver.
	registry := NewTagDeriverRegistry()

	// Phase 1: pack installed with deriver.
	entries := []topicDeriverEntry{
		{TopicName: "job.custom-pack.process", DeriverName: "amount-threshold"},
	}
	loadTagDeriversFromTopics(registry, entries)
	if !registry.HasDeriver("job.custom-pack.process") {
		t.Fatal("expected deriver after initial load")
	}

	// Phase 2: pack uninstalled — topic registry no longer has the entry.
	loadTagDeriversFromTopics(registry, nil)
	if registry.HasDeriver("job.custom-pack.process") {
		t.Fatal("stale deriver persists after pack uninstall — registry must be authoritative")
	}

	// Built-in mock-bank deriver should survive reload (it's re-applied).
	if !registry.HasDeriver("job.demo-mock-bank.transfer") {
		t.Fatal("built-in mock-bank deriver lost after reload")
	}
}

func TestLoadTagDeriversFromTopics_AtomicReload(t *testing.T) {
	// Regression test: concurrent evaluations must never observe an empty
	// or partially built registry during reload. The Swap approach guarantees
	// atomicity — readers see old map or new map, never intermediate.
	registry := NewTagDeriverRegistry()
	entries := []topicDeriverEntry{
		{TopicName: "job.demo-mock-bank.transfer", DeriverName: "amount-threshold"},
	}
	loadTagDeriversFromTopics(registry, entries)

	// Run concurrent reloads and evaluations.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			loadTagDeriversFromTopics(registry, entries)
		}
	}()

	failures := 0
	for i := 0; i < 1000; i++ {
		if !registry.HasDeriver("job.demo-mock-bank.transfer") {
			failures++
		}
	}
	<-done

	if failures > 0 {
		t.Fatalf("TRANSIENT BYPASS: %d/%d evaluations saw empty registry during reload", failures, 1000)
	}
}

func TestLoadTagDeriversFromTopics_UpdateDeriverOnReload(t *testing.T) {
	// When a topic's riskTagDeriver is cleared (but topic still exists),
	// the deriver must be removed on reload.
	registry := NewTagDeriverRegistry()

	// Phase 1: topic with deriver.
	entries := []topicDeriverEntry{
		{TopicName: "job.custom.topic", DeriverName: "amount-threshold"},
	}
	loadTagDeriversFromTopics(registry, entries)
	if !registry.HasDeriver("job.custom.topic") {
		t.Fatal("expected deriver after load")
	}

	// Phase 2: same topic, deriver cleared.
	entriesCleared := []topicDeriverEntry{
		{TopicName: "job.custom.topic", DeriverName: ""},
	}
	loadTagDeriversFromTopics(registry, entriesCleared)
	if registry.HasDeriver("job.custom.topic") {
		t.Fatal("deriver should be removed after riskTagDeriver cleared")
	}
}

// equalTagSet compares two tag slices in order. NewConfigMCPOpDeriver returns a
// sorted slice, so callers pass sorted wants.
func equalTagSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestNewConfigMCPOpDeriver_Matching(t *testing.T) {
	rules := []MCPOpTagRule{
		{OpGlob: "all_monday_api", Labels: map[string]string{"mutation": "true"}, Tags: []string{"destructive"}},
		{OpGlob: "*delete*", Tags: []string{"destructive"}},
		{OpGlob: "*item*", Tags: []string{"destructive"}}, // dedups with *delete* for board_item_delete
		{OpGlob: "bulk_*", Tags: []string{"batch"}},       // unions with *delete* for bulk_delete
		{OpGlob: "webhook_*", Tags: []string{"external-callback"}},
	}
	deriver := NewConfigMCPOpDeriver(rules, nil)
	tests := []struct {
		name   string
		labels map[string]string
		want   []string
	}{
		{name: "op+label match -> destructive", labels: map[string]string{"mcp.tool_name": "all_monday_api", "mutation": "true"}, want: []string{"destructive"}},
		{name: "op matches but required label missing -> empty", labels: map[string]string{"mcp.tool_name": "all_monday_api"}, want: []string{}},
		{name: "two destructive rules dedup to one", labels: map[string]string{"mcp.tool_name": "board_item_delete"}, want: []string{"destructive"}},
		{name: "two rules union (sorted)", labels: map[string]string{"mcp.tool_name": "bulk_delete"}, want: []string{"batch", "destructive"}},
		{name: "external callback", labels: map[string]string{"mcp.tool_name": "webhook_register"}, want: []string{"external-callback"}},
		{name: "no match -> empty (non-nil, replaces client tags)", labels: map[string]string{"mcp.tool_name": "get_board_info"}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriver("job.x.tool", tt.labels, nil)
			if got == nil {
				t.Fatalf("deriver returned nil; must be non-nil so it replaces client risk_tags (anti-spoof)")
			}
			if !equalTagSet(got, tt.want) {
				t.Fatalf("tags = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewConfigMCPOpDeriver_DefaultsAndAntiSpoof(t *testing.T) {
	deriver := NewConfigMCPOpDeriver(
		[]MCPOpTagRule{{OpGlob: "*delete*", Tags: []string{"destructive"}}},
		[]string{"external-callback"},
	)
	// No op match -> defaults only (still non-nil).
	if got := deriver("t", map[string]string{"mcp.tool_name": "get_board"}, nil); !equalTagSet(got, []string{"external-callback"}) {
		t.Fatalf("no-match tags = %v, want [external-callback]", got)
	}
	// Match -> default + matched, deduped + sorted.
	if got := deriver("t", map[string]string{"mcp.tool_name": "hard_delete"}, nil); !equalTagSet(got, []string{"destructive", "external-callback"}) {
		t.Fatalf("match tags = %v, want [destructive external-callback]", got)
	}
	// nil rules + nil defaults -> non-nil EMPTY (replaces client tags with nothing).
	if empty := NewConfigMCPOpDeriver(nil, nil)("t", map[string]string{"mcp.tool_name": "anything"}, nil); empty == nil || len(empty) != 0 {
		t.Fatalf("empty deriver = %v, want non-nil empty slice", empty)
	}
}

func TestNewConfigMCPOpDeriver_MalformedAndPayloadFallback(t *testing.T) {
	// An invalid glob ("[") fails closed (no match, no panic); the valid rule still applies.
	deriver := NewConfigMCPOpDeriver([]MCPOpTagRule{
		{OpGlob: "[", Tags: []string{"never"}},
		{OpGlob: "*delete*", Tags: []string{"destructive"}},
	}, nil)
	if got := deriver("t", map[string]string{"mcp.tool_name": "do_delete"}, nil); !equalTagSet(got, []string{"destructive"}) {
		t.Fatalf("invalid-glob tags = %v, want [destructive] (bad pattern must fail closed)", got)
	}
	// Op name resolved from a JSON payload "tool" field when no label is set.
	if got := deriver("t", nil, []byte(`{"tool":"hard_delete"}`)); !equalTagSet(got, []string{"destructive"}) {
		t.Fatalf("payload-op tags = %v, want [destructive]", got)
	}
	// Non-JSON payload + no labels -> op "" -> no match -> empty, no panic.
	if got := deriver("t", nil, []byte("not json")); len(got) != 0 {
		t.Fatalf("garbage payload tags = %v, want empty", got)
	}
}

func TestLoadTagDeriversFromTopics_ConfigMCPOpDispatch(t *testing.T) {
	registry := NewTagDeriverRegistry()
	topics := []topicDeriverEntry{
		{
			TopicName:   "job.monday.tool",
			DeriverName: configMCPOpDeriverName, // "mcp-op"
			MCPOpRules:  []MCPOpTagRule{{OpGlob: "all_monday_api", Tags: []string{"destructive"}}},
			DefaultTags: []string{"external-callback"},
		},
		{TopicName: "job.demo-mock-bank.transfer", DeriverName: "amount-threshold"}, // legacy named, must still bind
		{TopicName: "job.unknown", DeriverName: "does-not-exist"},                   // unknown -> skipped
	}
	count := loadTagDeriversFromTopics(registry, topics)
	if count != 2 {
		t.Fatalf("registered count = %d, want 2 (mcp-op + amount-threshold; unknown skipped)", count)
	}
	// The config-driven deriver is constructed from the topic's rules and works
	// end-to-end via the registry (Derive returns the server-derived set -> anti-spoof).
	tags, ok := registry.Derive("job.monday.tool", map[string]string{"mcp.tool_name": "all_monday_api"}, nil)
	if !ok {
		t.Fatal("mcp-op deriver not registered for job.monday.tool")
	}
	if !equalTagSet(tags, []string{"destructive", "external-callback"}) {
		t.Fatalf("mcp-op derived tags = %v, want [destructive external-callback]", tags)
	}
	// Legacy named deriver binding unchanged (DoD#3 — additive).
	if !registry.HasDeriver("job.demo-mock-bank.transfer") {
		t.Fatal("amount-threshold binding lost (regression)")
	}
	// Unknown deriver name skipped, not registered.
	if registry.HasDeriver("job.unknown") {
		t.Fatal("unknown deriver name should be skipped, not registered")
	}
}
