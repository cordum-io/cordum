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
