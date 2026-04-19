package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Canonical argument normalization for MCP tool-call approval hashing.
//
// The gateway's approval pipeline (task-94b27344) indexes pending
// approvals by (tenant, agent_id, tool_name, args_hash). When the LLM
// calls a mutating tool, the gate computes args_hash and enqueues a
// record; after a human approves, the LLM retries the SAME call and
// the gate consumes the matching record.
//
// For this flow to be robust, the hash has to be stable across the
// normal noise an LLM introduces between the first call and the
// retry:
//   - whitespace in JSON formatting (json.Decoder + json.Marshal
//     already strip this)
//   - object key ordering (Go's encoding/json marshals in sorted
//     order, so this is free)
//   - trailing/leading whitespace on string values (LLMs are not
//     deterministic about this — we trim on canonicalise)
//   - fields that are present-with-empty-value vs absent (an LLM may
//     drop an empty reason field on retry — collapse both forms by
//     removing null, empty string, empty array, and empty object
//     entries before marshalling)
//
// We deliberately DO NOT inject tool-specific defaults here — the
// gate must stay tool-agnostic so new mutating tools can be added
// without updating the canonicaliser. Tool handlers themselves
// handle defaults (see tools.go's submitJobHandler for an example).

// CanonicaliseArgs normalises the raw args JSON for stable hashing.
// Returns both the canonical bytes and the hex-encoded SHA-256 hash.
//
// Stable properties:
//   - []byte output is identical across whitespace / key-ordering /
//     empty-field variants of the same semantic input.
//   - Numeric literals are preserved via json.Number so ints >= 2^53
//     don't collide via float64 rounding (the same issue the gateway
//     canonicaliser called out).
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
	decoded = normaliseValue(decoded)
	canonical, err := marshalCanonical(decoded)
	if err != nil {
		return nil, "", fmt.Errorf("marshal canonical: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}

// normaliseValue walks the decoded tree and:
//   - trims whitespace on every string
//   - drops nil, empty-string, empty-array, empty-object entries
//     from any object
//   - returns nil when a value collapses to the empty form so the
//     caller can drop the key
//   - recursively applies to arrays and nested objects
func normaliseValue(v any) any {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case []any:
		out := make([]any, 0, len(val))
		for _, item := range val {
			n := normaliseValue(item)
			if isEmptyNormalised(n) {
				// Preserve positional semantics for arrays — an
				// empty string in an array is still a valid element
				// (e.g. a JSON-schema 'enum' containing ""). Keep
				// nil out though.
				if n == nil {
					continue
				}
			}
			out = append(out, n)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			n := normaliseValue(item)
			if isEmptyNormalised(n) {
				continue
			}
			out[k] = n
		}
		return out
	default:
		return val
	}
}

func isEmptyNormalised(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case []any:
		return len(val) == 0
	case map[string]any:
		return len(val) == 0
	default:
		return false
	}
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
		// Fallback: delegate to encoding/json. This path is only hit
		// for unusual types that slipped past normaliseValue — the
		// stdlib will handle float64, int, etc. in a stable way.
		raw, err := json.Marshal(val)
		if err != nil {
			return err
		}
		buf.Write(raw)
	}
	return nil
}
