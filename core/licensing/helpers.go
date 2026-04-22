package licensing

import (
	"bytes"
	"encoding/json"
	"strings"
)

// isLegacyLicenseEnvelope reports whether the JSON payload is the
// pre-GA top-level `features` + `limits` shape that the migration layer
// used to project onto the current Claims/Rights/Entitlements record.
// Callers return ErrUnsupportedLegacyLicenseFormat on a true result —
// the migration layer was removed in the pre-GA legacy sweep so such
// envelopes must be regenerated in the current schema.
func isLegacyLicenseEnvelope(raw []byte) bool {
	var legacy struct {
		Features map[string]bool  `json:"features"`
		Limits   map[string]int64 `json:"limits"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return false
	}
	return len(legacy.Features) > 0 || len(legacy.Limits) > 0
}

// normalizeJSON compacts a JSON payload so signature verification can
// operate on a canonical byte form regardless of whitespace variation
// between the signer and the verifier.
func normalizeJSON(raw []byte) ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

// normalizeName canonicalizes a feature/limit name by lowercasing and
// collapsing hyphens and spaces to underscores. Used by the license
// parser and enforcement layer so callers may reference entitlements
// with human-friendly spellings (e.g. "Audit Export" or "audit-export"
// both resolve to the canonical "audit_export" key).
func normalizeName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}
