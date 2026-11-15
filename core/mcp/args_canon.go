package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

// Canonical argument encoding for MCP tool-call approval hashing.
//
// The gateway's approval pipeline (task-94b27344) indexes pending
// approvals by (tenant, agent_id, tool_name, args_hash), and the
// approval hold's InputHash is taken over the same canonical bytes.
// The tool handler runs the arguments exactly as the caller sent
// them, so the hash must bind those exact values. Only the encoding
// is canonical:
//   - object keys are sorted
//   - whitespace between tokens is removed
//   - numbers are written as they appeared (json.Number), so ints
//     >= 2^53 don't collide via float64 rounding
//
// Every value is kept: whitespace inside strings, empty strings,
// empty arrays, empty objects and nulls are all part of the hash, so
// an approval for {"cmd":"deploy"} never matches {"cmd":" deploy\n"}.
//
// We deliberately DO NOT inject tool-specific defaults here — the
// gate must stay tool-agnostic so new mutating tools can be added
// without updating the canonicaliser. Tool handlers themselves
// handle defaults (see tools.go's submitJobHandler for an example).

// CanonicaliseArgs returns the canonical encoding of the raw args JSON
// together with its hex-encoded SHA-256 hash.
//
// Two inputs produce the same bytes only when they differ in JSON
// formatting or object key order. Empty raw input canonicalises to
// `{}`.
func CanonicaliseArgs(raw json.RawMessage) (json.RawMessage, string, error) {
	if len(raw) == 0 {
		canonical := json.RawMessage(`{}`)
		sum := sha256.Sum256(canonical)
		return canonical, hex.EncodeToString(sum[:]), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return nil, "", fmt.Errorf("decode args: %w", err)
	}
	canonical, err := marshalCanonical(decoded)
	if err != nil {
		return nil, "", fmt.Errorf("marshal canonical: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}

// marshalCanonical marshals with sorted map keys. Go's encoding/json
// does this by default, but we use an explicit walk so the behaviour
// is independent of stdlib changes and so we can preserve the
// json.Number type that json.Marshal would otherwise quote.
func marshalCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch val := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(val.String())
	case string:
		raw, err := json.Marshal(val)
		if err != nil {
			return err
		}
		buf.Write(raw)
	case []any:
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			rawKey, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(rawKey)
			buf.WriteByte(':')
			if err := writeCanonical(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		// Fallback: delegate to encoding/json. A UseNumber decode only
		// yields the types above, so this path is defensive — the
		// stdlib will handle float64, int, etc. in a stable way.
		raw, err := json.Marshal(val)
		if err != nil {
			return err
		}
		buf.Write(raw)
	}
	return nil
}

// checkUniqueObjectKeys fails when one JSON object in raw, at any depth,
// repeats a key. encoding/json keeps the last value of a repeated key,
// and so do the approval hash and the approver's view, but a fronted
// upstream's parser may keep the first. Refusing the call keeps the
// arguments a tool runs with to the ones that were checked and bound.
func checkUniqueObjectKeys(raw json.RawMessage) error {
	type frame struct {
		keys    map[string]struct{} // nil for an array
		wantKey bool
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Numbers stay json.Number. A float64 decode would refuse 1e400,
	// which CanonicaliseArgs accepts.
	dec.UseNumber()
	var stack []*frame
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var top *frame
		if n := len(stack); n > 0 {
			top = stack[n-1]
		}
		if d, ok := tok.(json.Delim); ok && (d == '}' || d == ']') {
			stack = stack[:len(stack)-1]
			continue
		}
		if key, ok := tok.(string); ok && top != nil && top.keys != nil && top.wantKey {
			if _, seen := top.keys[key]; seen {
				return fmt.Errorf("arguments repeat the object key %q", key)
			}
			top.keys[key] = struct{}{}
			top.wantKey = false
			continue
		}
		if top != nil && top.keys != nil {
			top.wantKey = true // a value: the object's next token is a key
		}
		if d, ok := tok.(json.Delim); ok {
			f := &frame{wantKey: d == '{'}
			if d == '{' {
				f.keys = map[string]struct{}{}
			}
			stack = append(stack, f)
		}
	}
}
