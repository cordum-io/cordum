// Package qualpin links qualification metadata to a release qualification record.
package qualpin

import (
	_ "embed"
	"encoding/json"
	"fmt"

	infraschema "github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/tools/releasequal"
)

//go:embed pin-manifest.schema.json
var manifestSchema []byte

type Pins struct {
	Qualification struct {
		ID          string `json:"id"`
		Combination string `json:"combination"`
	} `json:"qualification"`
	Policy struct {
		Bundle           string `json:"bundle"`
		Version          string `json:"version"`
		AuthorityVersion string `json:"authority_version"`
	} `json:"policy"`
	Executor struct {
		Kind     string `json:"kind"`
		Version  string `json:"version"`
		Platform struct {
			OS      string `json:"os"`
			Arch    string `json:"arch"`
			Version string `json:"version"`
		} `json:"platform"`
	} `json:"executor"`
	Deployment struct {
		Profile      string `json:"profile"`
		ConfigDigest string `json:"config_digest"`
	} `json:"deployment"`
	Corpus struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Digest  string `json:"digest"`
	} `json:"corpus"`
}

type Manifest struct {
	SchemaVersion int                `json:"schema_version"`
	Record        releasequal.Record `json:"record"`
	Pins
}

type Mismatch struct{ Name, Expected, Running string }

// Parse releases a manifest only after both schemas and record references pass.
func Parse(data []byte) (*Manifest, error) {
	if err := infraschema.ValidateSchema("pin-manifest.schema.json", manifestSchema, data); err != nil {
		return nil, fmt.Errorf("pin manifest: %w", err)
	}
	var wire struct {
		SchemaVersion int             `json:"schema_version"`
		Record        json.RawMessage `json:"record"`
		Pins
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("pin manifest: decode: %w", err)
	}
	record, err := releasequal.Parse(wire.Record)
	if err != nil {
		return nil, err
	}
	return &Manifest{SchemaVersion: wire.SchemaVersion, Record: *record, Pins: wire.Pins}, nil
}

// Assemble builds and validates the complete chain without changing the record.
func Assemble(record releasequal.Record, pins Pins) (*Manifest, error) {
	data, err := json.Marshal(Manifest{SchemaVersion: 1, Record: record, Pins: pins})
	if err != nil {
		return nil, fmt.Errorf("pin manifest: encode: %w", err)
	}
	return Parse(data)
}

// Verify compares image digests in a validated manifest. A missing image has an
// empty Running digest. This does not qualify a deployment or compare versions.
func Verify(m Manifest, running map[string]string) []Mismatch {
	var mismatches []Mismatch
	for _, artifact := range m.Record.Artifacts {
		if artifact.Kind == releasequal.KindImage && running[artifact.Name] != artifact.Digest {
			mismatches = append(mismatches, Mismatch{Name: artifact.Name, Expected: artifact.Digest, Running: running[artifact.Name]})
		}
	}
	return mismatches
}
