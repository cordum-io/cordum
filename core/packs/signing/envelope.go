package signing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// EncodeEnvelopeYAML serialises a SignedManifest to the human-diffable
// YAML format written to pack.yaml.sig by default.
func EncodeEnvelopeYAML(signed SignedManifest) ([]byte, error) {
	body, err := yaml.Marshal(signed)
	if err != nil {
		return nil, fmt.Errorf("encode yaml envelope: %w", err)
	}
	return body, nil
}

// EncodeEnvelopeJSON serialises a SignedManifest to the JSON envelope
// form written to pack.yaml.sig.json by tooling.
func EncodeEnvelopeJSON(signed SignedManifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(signed); err != nil {
		return nil, fmt.Errorf("encode json envelope: %w", err)
	}
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}

// DecodeEnvelope parses either a YAML or JSON envelope into a
// SignedManifest. Callers typically do not know in advance which
// format a publisher chose; this tagged-union helper picks the right
// codec from the first non-whitespace byte.
func DecodeEnvelope(raw []byte) (SignedManifest, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return SignedManifest{}, fmt.Errorf("%w: empty envelope", ErrManifestMalformed)
	}
	var envelope SignedManifest
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return SignedManifest{}, fmt.Errorf("decode json envelope: %w", err)
		}
		return envelope, nil
	}
	if err := yaml.Unmarshal(raw, &envelope); err != nil {
		return SignedManifest{}, fmt.Errorf("decode yaml envelope: %w", err)
	}
	return envelope, nil
}
