package mcp

import (
	"encoding/json"
	"testing"
)

// Hash-equality tests: inputs that differ only in JSON formatting or
// key order hash the same; any difference in a value hashes differently.

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

func TestCanonicaliseArgs_StringWhitespaceIsSignificant(t *testing.T) {
	t.Parallel()
	if canonHash(t, `{"cmd":"deploy"}`) == canonHash(t, `{"cmd":"  deploy\n"}`) {
		t.Fatalf("a padded string must hash differently from the unpadded one")
	}
	if canonHash(t, `{"spec":{"steps":[{"name":"demo"}]}}`) == canonHash(t, `{"spec":{"steps":[{"name":" demo "}]}}`) {
		t.Fatalf("a padded nested string must hash differently from the unpadded one")
	}
}

func TestCanonicaliseArgs_EmptyValuesAreSignificant(t *testing.T) {
	t.Parallel()
	absent := canonHash(t, `{"id":"x"}`)
	for _, present := range []string{
		`{"id":"x","reason":""}`,
		`{"id":"x","allowed_servers":[]}`,
		`{"id":"x","labels":{}}`,
		`{"id":"x","idempotency_key":null}`,
	} {
		if canonHash(t, present) == absent {
			t.Errorf("%s must hash differently from the field being absent", present)
		}
	}
	if canonHash(t, `{"ids":[1,2]}`) == canonHash(t, `{"ids":[null,1,2]}`) {
		t.Errorf("a null array element must be part of the hash")
	}
}

func TestCanonicaliseArgs_CanonicalFormKeepsValues(t *testing.T) {
	t.Parallel()
	canonical, _, err := CanonicaliseArgs(json.RawMessage(`{ "b" : [1, null, ""], "a" : " x ", "c" : {} }`))
	if err != nil {
		t.Fatalf("canonicalise: %v", err)
	}
	const want = `{"a":" x ","b":[1,null,""],"c":{}}`
	if string(canonical) != want {
		t.Fatalf("canonical form = %s, want %s", canonical, want)
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

// The approval binding hashes the same canonical bytes, so an approval
// matches only a call that sends the same argument values.
func TestBuildMCPApprovalBinding_InputHashBindsExactArgs(t *testing.T) {
	t.Parallel()
	inputHash := func(args string) string {
		_, in := BuildMCPApprovalBinding("tnt_a", "cordum.builtin",
			ToolCallParams{Name: "shell.exec", Arguments: json.RawMessage(args)}, "p")
		return in
	}
	approved := inputHash(`{"cmd":"deploy","env":"prod"}`)
	if got := inputHash(`{ "env" : "prod", "cmd" : "deploy", "_approval_ref" : "edge_appr_xyz" }`); got != approved {
		t.Errorf("formatting, key order and a stripped _approval_ref must keep InputHash: %q vs %q", got, approved)
	}
	if inputHash(`{"cmd":"deploy ","env":"prod"}`) == approved {
		t.Errorf("a padded value must change InputHash")
	}
	if inputHash(`{"cmd":"deploy","env":"prod","dry_run":null}`) == approved {
		t.Errorf("an added null field must change InputHash")
	}
}
