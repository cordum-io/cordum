package mcp

import (
	"encoding/json"
	"testing"
)

// Hash-equality tests: inputs that differ only in noise should hash
// the same; inputs that differ semantically must hash differently.

func canonHash(t *testing.T, raw string) string {
	t.Helper()
	_, h, err := CanonicaliseArgs(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("canonicalise %q: %v", raw, err)
	}
	return h
}

func TestCanonicaliseArgs_EmptyAndWhitespace(t *testing.T) {
	t.Parallel()
	if canonHash(t, "") != canonHash(t, "{}") {
		t.Fatalf("empty and {} must hash equal")
	}
	if canonHash(t, `{"a":1}`) != canonHash(t, `{ "a" : 1 }`) {
		t.Fatalf("whitespace variants must hash equal")
	}
}

func TestCanonicaliseArgs_KeyOrdering(t *testing.T) {
	t.Parallel()
	left := canonHash(t, `{"a":1,"b":2,"c":3}`)
	right := canonHash(t, `{"c":3,"a":1,"b":2}`)
	if left != right {
		t.Fatalf("key-order variants must hash equal: %s vs %s", left, right)
	}
}

func TestCanonicaliseArgs_StringTrim(t *testing.T) {
	t.Parallel()
	if canonHash(t, `{"name":"demo"}`) != canonHash(t, `{"name":" demo "}`) {
		t.Fatalf("whitespace-trimmed strings must hash equal")
	}
}

func TestCanonicaliseArgs_DropsEmptyValues(t *testing.T) {
	t.Parallel()
	// Absent vs present-with-empty-string reason: hash equal so an
	// LLM retrying with the reason field dropped hits the same
	// approval record.
	a := canonHash(t, `{"pack_id":"cordum/slack","reason":""}`)
	b := canonHash(t, `{"pack_id":"cordum/slack"}`)
	if a != b {
		t.Fatalf("present-empty vs absent must hash equal: %s vs %s", a, b)
	}
	// Empty array / empty object dropped.
	c := canonHash(t, `{"id":"x","allowed_tools":[]}`)
	d := canonHash(t, `{"id":"x"}`)
	if c != d {
		t.Fatalf("empty array must be dropped: %s vs %s", c, d)
	}
	e := canonHash(t, `{"id":"x","labels":{}}`)
	if e != d {
		t.Fatalf("empty object must be dropped: %s vs %s", e, d)
	}
}

func TestCanonicaliseArgs_NullDropped(t *testing.T) {
	t.Parallel()
	if canonHash(t, `{"k":null,"x":1}`) != canonHash(t, `{"x":1}`) {
		t.Fatalf("null values must be dropped")
	}
}

func TestCanonicaliseArgs_NestedNormalization(t *testing.T) {
	t.Parallel()
	a := canonHash(t, `{"spec":{"steps":[{"name":" demo "}]}}`)
	b := canonHash(t, `{"spec":{"steps":[{"name":"demo"}]}}`)
	if a != b {
		t.Fatalf("nested string trim not applied: %s vs %s", a, b)
	}
}

func TestCanonicaliseArgs_DifferentContent_HashDifferently(t *testing.T) {
	t.Parallel()
	if canonHash(t, `{"pack_id":"cordum/slack"}`) == canonHash(t, `{"pack_id":"cordum/github"}`) {
		t.Fatalf("semantically different args must hash differently")
	}
	if canonHash(t, `{"priority":"high"}`) == canonHash(t, `{"priority":"low"}`) {
		t.Fatalf("different priorities must hash differently")
	}
}

func TestCanonicaliseArgs_BigIntPreserved(t *testing.T) {
	t.Parallel()
	// 2^53 and 2^53+1 would collide via float64 rounding but must
	// stay distinct with json.Number preservation.
	a := canonHash(t, `{"seq":9007199254740992}`)
	b := canonHash(t, `{"seq":9007199254740993}`)
	if a == b {
		t.Fatalf("big-int precision lost: both seqs hash to %s", a)
	}
}

func TestCanonicaliseArgs_InvalidJSONError(t *testing.T) {
	t.Parallel()
	_, _, err := CanonicaliseArgs(json.RawMessage(`{not json`))
	if err == nil {
		t.Fatalf("expected decode error")
	}
}

// Regression: LLMs sometimes emit { "x": null } for an omitted field
// but send { } on retry. The hash must still match so the approval
// record claim succeeds.
func TestCanonicaliseArgs_RetryEquivalence_NullOmittedField(t *testing.T) {
	t.Parallel()
	firstCall := canonHash(t, `{"pack_id":"cordum/slack","idempotency_key":null}`)
	retryCall := canonHash(t, `{"pack_id":"cordum/slack"}`)
	if firstCall != retryCall {
		t.Fatalf("retry equivalence broken: %s vs %s", firstCall, retryCall)
	}
}
