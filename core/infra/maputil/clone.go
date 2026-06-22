// Package maputil provides shared map utility functions used across
// the scheduler, workflow engine, and MCP server.
package maputil

// CloneStringMap returns a shallow copy of the map. Returns nil if the
// input is nil, preserving the nil vs empty-map distinction.
func CloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// CloneAnyMap returns a shallow copy of the map. Returns nil if the
// input is nil. Values are NOT deep-copied; use DeepCloneAnyMap if
// nested maps or slices must be isolated from the original.
func CloneAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// DeepCloneAnyMap returns a deep copy of the map, recursively cloning
// nested containers so no reference is shared with the original:
// map[string]any, map[any]any (from yaml.v2), []any, []string, and
// []map[string]any. Primitive types (int, float64, string, bool) are
// copied by value. Unlike JSON round-trip, this preserves Go types —
// int stays int, not float64.
func DeepCloneAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCloneValue(v)
	}
	return out
}

func deepCloneValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return DeepCloneAnyMap(val)
	case []any:
		cp := make([]any, len(val))
		for i, elem := range val {
			cp[i] = deepCloneValue(elem)
		}
		return cp
	case map[any]any:
		// yaml.v2 unmarshals nested objects as map[any]any. Keys are scalars
		// (copied by value); values are cloned recursively.
		cp := make(map[any]any, len(val))
		for mk, mv := range val {
			cp[mk] = deepCloneValue(mv)
		}
		return cp
	case []string:
		return append([]string(nil), val...)
	case []map[string]any:
		cp := make([]map[string]any, len(val))
		for i, m := range val {
			cp[i] = DeepCloneAnyMap(m)
		}
		return cp
	default:
		return v
	}
}
