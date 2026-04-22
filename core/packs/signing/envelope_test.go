package signing

import (
	"crypto/ed25519"
	"reflect"
	"testing"
)

func TestEnvelopeRoundTrip_YAMLAndJSONEqual(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	yamlBytes, err := EncodeEnvelopeYAML(signed)
	if err != nil {
		t.Fatal(err)
	}
	jsonBytes, err := EncodeEnvelopeJSON(signed)
	if err != nil {
		t.Fatal(err)
	}

	yamlEnv, err := DecodeEnvelope(yamlBytes)
	if err != nil {
		t.Fatalf("decode yaml: %v", err)
	}
	jsonEnv, err := DecodeEnvelope(jsonBytes)
	if err != nil {
		t.Fatalf("decode json: %v", err)
	}
	// Both decoded envelopes must be byte-identical SignedManifest
	// values — publishers can freely choose between the two on-disk
	// formats without changing the signed payload semantics.
	if !reflect.DeepEqual(yamlEnv, jsonEnv) {
		t.Fatalf("yaml and json decoded envelopes differ:\nyaml=%+v\njson=%+v", yamlEnv, jsonEnv)
	}
	if !reflect.DeepEqual(yamlEnv, signed) {
		t.Fatalf("yaml decoded envelope differs from input")
	}
}

func TestDecodeEnvelope_EmptyRejected(t *testing.T) {
	if _, err := DecodeEnvelope([]byte("")); err == nil {
		t.Fatal("expected empty envelope rejection")
	}
	if _, err := DecodeEnvelope([]byte("   \n\n  ")); err == nil {
		t.Fatal("expected whitespace-only envelope rejection")
	}
}

func TestDecodeEnvelope_MalformedJSON(t *testing.T) {
	if _, err := DecodeEnvelope([]byte(`{"signature":`)); err == nil {
		t.Fatal("expected malformed json rejection")
	}
}

func TestDecodeEnvelope_MalformedYAML(t *testing.T) {
	// yaml.v3 is permissive about top-level scalars. Use a shape that
	// unambiguously violates YAML syntax (unterminated flow mapping).
	if _, err := DecodeEnvelope([]byte("apiVersion: {foo")); err == nil {
		t.Fatal("expected malformed yaml rejection")
	}
}
