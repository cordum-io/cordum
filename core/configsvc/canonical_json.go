package configsvc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func snapshotHash(data map[string]any) (string, error) {
	if data == nil {
		return "", nil
	}
	encoded, err := canonicalJSON(data)
	if err != nil {
		return "", err
	}
	sum := sha256Sum(encoded)
	return sum, nil
}

func canonicalJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := appendCanonical(&buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func appendCanonical(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case json.RawMessage:
		buf.Write(v)
		return nil
	case map[string]any:
		return appendCanonicalMap(buf, v)
	case map[string]string:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = val
		}
		return appendCanonicalMap(buf, out)
	case []any:
		return appendCanonicalSlice(buf, v)
	case []string:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = val
		}
		return appendCanonicalSlice(buf, out)
	case []int:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = val
		}
		return appendCanonicalSlice(buf, out)
	case []int64:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = val
		}
		return appendCanonicalSlice(buf, out)
	case []float64:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = val
		}
		return appendCanonicalSlice(buf, out)
	case []bool:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = val
		}
		return appendCanonicalSlice(buf, out)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("encode canonical json: %w", err)
		}
		buf.Write(encoded)
		return nil
	}
}

func appendCanonicalMap(buf *bytes.Buffer, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(k)
		buf.Write(keyBytes)
		buf.WriteByte(':')
		if err := appendCanonical(buf, m[k]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

func appendCanonicalSlice(buf *bytes.Buffer, items []any) error {
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := appendCanonical(buf, item); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

func snapshotVersion(revisions map[Scope]int64) string {
	order := []Scope{ScopeSystem, ScopeOrg, ScopeTeam, ScopeWorkflow, ScopeStep}
	parts := make([]string, 0, len(order))
	for _, scope := range order {
		rev := revisions[scope]
		parts = append(parts, fmt.Sprintf("%s:%d", scope, rev))
	}
	return strings.Join(parts, "|")
}

func sha256Sum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
