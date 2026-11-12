// Package releasequal binds source SHAs to artifact digests, gates, results and
// open exceptions. No release workflow produces or checks this record yet.
package releasequal

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	infraschema "github.com/cordum/cordum/core/infra/schema"
)

const SchemaName = "qualification-record.schema.json"

//go:embed qualification-record.schema.json
var recordSchema []byte

// SchemaJSON returns a copy of the embedded qualification record schema.
func SchemaJSON() []byte { return append([]byte(nil), recordSchema...) }

const (
	SchemaVersion = 1
	KindImage     = "image"
	KindBinary    = "binary"
	ResultPass    = "pass"
	ResultFail    = "fail"
)

type Record struct {
	SchemaVersion int         `json:"schema_version"`
	Release       Release     `json:"release"`
	Source        Source      `json:"source"`
	Artifacts     []Artifact  `json:"artifacts"`
	Gates         []Gate      `json:"gates"`
	Exceptions    []Exception `json:"exceptions"`
}
type Release struct {
	Ref    string `json:"ref"`
	DryRun bool   `json:"dry_run"`
	RunURL string `json:"run_url"`
}
type Source struct {
	Repository string `json:"repository"`
	SHA        string `json:"sha"`
}
type Artifact struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type Gate struct {
	ID       string `json:"id"`
	Artifact string `json:"artifact"`
	Digest   string `json:"digest"`
	Result   string `json:"result"`
	Log      string `json:"log"`
}
type Exception struct {
	ID        string   `json:"id"`
	Artifacts []string `json:"artifacts"`
	Reason    string   `json:"reason"`
}

// Parse validates the schema, strictly decodes JSON and checks record references.
func Parse(data []byte) (*Record, error) {
	if err := infraschema.ValidateSchema(SchemaName, recordSchema, data); err != nil {
		return nil, fmt.Errorf("qualification record: %w", err)
	}
	var r Record
	if err := strictDecode(data, &r); err != nil {
		return nil, err
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

func strictDecode[T Record](data []byte, value *T) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("qualification record: decode: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return fmt.Errorf("qualification record: trailing data: %w", err)
		}
		return errors.New("qualification record: trailing data")
	}
	return nil
}

func (r *Record) validate() error {
	var problems []error
	artifacts, covered := make(map[string]Artifact), make(map[string]bool)
	for _, a := range r.Artifacts {
		if _, exists := artifacts[a.Name]; exists {
			problems = append(problems, fmt.Errorf("qualification record: duplicate artifact %q", a.Name))
		}
		artifacts[a.Name] = a
	}
	problems = append(problems, r.validateGates(artifacts, covered)...)
	problems = append(problems, r.validateExceptions(artifacts, covered)...)
	for _, a := range r.Artifacts {
		if !covered[a.Name] {
			problems = append(problems, fmt.Errorf("qualification record: artifact %q has neither a gate nor an exception", a.Name))
		}
	}
	if !r.Release.DryRun && !strings.HasPrefix(r.Release.Ref, "refs/tags/v") {
		problems = append(problems, fmt.Errorf("qualification record: non-dry-run ref %q must start with refs/tags/v", r.Release.Ref))
	}
	return errors.Join(problems...)
}

func (r *Record) validateGates(artifacts map[string]Artifact, covered map[string]bool) []error {
	var problems []error
	seen := make(map[[2]string]bool)
	for _, g := range r.Gates {
		key := [2]string{g.ID, g.Artifact}
		if seen[key] {
			problems = append(problems, fmt.Errorf("qualification record: duplicate gate %q for artifact %q", g.ID, g.Artifact))
		}
		seen[key], covered[g.Artifact] = true, true
		a, exists := artifacts[g.Artifact]
		if !exists {
			problems = append(problems, fmt.Errorf("qualification record: gate %q: unknown artifact %q", g.ID, g.Artifact))
		} else if g.Digest != a.Digest {
			problems = append(problems, fmt.Errorf("qualification record: gate %q artifact %q: digest %s differs from artifact digest %s", g.ID, g.Artifact, g.Digest, a.Digest))
		}
	}
	return problems
}

func (r *Record) validateExceptions(artifacts map[string]Artifact, covered map[string]bool) []error {
	var problems []error
	seen := make(map[string]bool)
	for _, e := range r.Exceptions {
		if seen[e.ID] {
			problems = append(problems, fmt.Errorf("qualification record: duplicate exception %q", e.ID))
		}
		seen[e.ID] = true
		for _, name := range e.Artifacts {
			if _, exists := artifacts[name]; !exists {
				problems = append(problems, fmt.Errorf("qualification record: exception %q: unknown artifact %q", e.ID, name))
			}
			covered[name] = true
		}
	}
	return problems
}
