package policybundles

import (
	"fmt"
	"strings"
)

// StringFromAny extracts a string from an arbitrary value.
func StringFromAny(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// StringSliceFromAny coerces a value to a string slice.
func StringSliceFromAny(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string{}, v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

// MergeUniqueStrings merges multiple string slices, deduplicating entries.
func MergeUniqueStrings(values ...[]string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, list := range values {
		for _, raw := range list {
			item := strings.TrimSpace(raw)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

// ParseBool parses common boolean string representations.
func ParseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
