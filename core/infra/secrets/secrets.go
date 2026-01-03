package secrets

import (
	"encoding/json"
	"strings"
)

const secretPrefix = "secret://"

// ContainsSecretRefs returns true if any string value contains a secret reference.
func ContainsSecretRefs(value any) bool {
	_, found := redact(value, false)
	return found
}

// RedactSecretRefs returns a copy with secret refs replaced by "<redacted>".
func RedactSecretRefs(value any) (any, bool) {
	return redact(value, true)
}

// RedactJSON redacts secret references inside a JSON payload.
func RedactJSON(data []byte) ([]byte, bool, error) {
	if len(data) == 0 {
		return data, false, nil
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return data, false, err
	}
	redacted, changed := RedactSecretRefs(payload)
	if !changed {
		return data, false, nil
	}
	out, err := json.Marshal(redacted)
	return out, true, err
}

func redact(value any, replace bool) (any, bool) {
	switch v := value.(type) {
	case nil:
		return v, false
	case string:
		if strings.HasPrefix(strings.TrimSpace(v), secretPrefix) {
			if replace {
				return "<redacted>", true
			}
			return v, true
		}
		return v, false
	case map[string]any:
		changed := false
		out := make(map[string]any, len(v))
		for k, child := range v {
			red, childChanged := redact(child, replace)
			if childChanged {
				changed = true
			}
			out[k] = red
		}
		return out, changed
	case map[string]string:
		changed := false
		out := make(map[string]any, len(v))
		for k, child := range v {
			red, childChanged := redact(child, replace)
			if childChanged {
				changed = true
			}
			out[k] = red
		}
		return out, changed
	case []any:
		changed := false
		out := make([]any, len(v))
		for i, child := range v {
			red, childChanged := redact(child, replace)
			if childChanged {
				changed = true
			}
			out[i] = red
		}
		return out, changed
	case []string:
		changed := false
		out := make([]any, len(v))
		for i, child := range v {
			red, childChanged := redact(child, replace)
			if childChanged {
				changed = true
			}
			out[i] = red
		}
		return out, changed
	default:
		return v, false
	}
}
