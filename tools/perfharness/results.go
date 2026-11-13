package perfharness

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"

	infraschema "github.com/cordum/cordum/core/infra/schema"
)

//go:embed results.schema.json
var resultsSchema []byte

type Results struct {
	SchemaVersion int              `json:"schema_version"`
	Run           RunInfo          `json:"run"`
	Workload      WorkloadInfo     `json:"workload"`
	Environment   Environment      `json:"environment"`
	Steps         []StepResult     `json:"steps"`
	QueueDepth    QueueDepth       `json:"queue_depth"`
	Saturation    SaturationResult `json:"saturation"`
}
type RunInfo struct {
	ID         string `json:"id"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Profile    string `json:"profile"`
}
type WorkloadInfo struct {
	Name   string `json:"name"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}
type Configuration struct {
	TargetBaseURL string        `json:"target_base_url"`
	RequestMix    []Sequence    `json:"request_mix"`
	ProfileSteps  []ProfileStep `json:"profile_steps"`
	Saturation    Thresholds    `json:"saturation"`
	QueueDepth    QueueSource   `json:"queue_depth"`
	EnvVarsRead   []string      `json:"env_vars_read"`
}
type StepResult struct {
	Index         int     `json:"index"`
	Concurrency   int     `json:"concurrency"`
	DurationMS    float64 `json:"duration_ms"`
	Requests      uint64  `json:"requests"`
	Errors        uint64  `json:"errors"`
	ThroughputRPS float64 `json:"throughput_rps"`
	LatencyMS     Latency `json:"latency_ms"`
}
type Latency struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}
type QueueDepth struct {
	Source     string        `json:"source"`
	IntervalMS int           `json:"interval_ms"`
	Samples    []QueueSample `json:"samples"`
}
type QueueSample struct {
	TMS   float64  `json:"t_ms"`
	Value *float64 `json:"value"`
}
type SaturationResult struct {
	Rule      string `json:"rule"`
	Reached   bool   `json:"reached"`
	StepIndex *int   `json:"step_index"`
	Reason    string `json:"reason"`
}

// WriteResults releases no bytes unless both schema and semantic checks pass.
func WriteResults(r Results) ([]byte, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("results: encode: %w", err)
	}
	if _, err := ParseResults(data); err != nil {
		return nil, err
	}
	return data, nil
}

func ParseResults(data []byte) (*Results, error) {
	if len(data) > maxContractBytes {
		return nil, fmt.Errorf("results: exceeds size limit")
	}
	if err := infraschema.ValidateSchema("results.schema.json", resultsSchema, data); err != nil {
		return nil, fmt.Errorf("results: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var r Results
	if err := decoder.Decode(&r); err != nil {
		return nil, fmt.Errorf("results: decode: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("results: trailing data")
	}
	if err := checkJSON(data, nil); err != nil {
		return nil, fmt.Errorf("results: %w", err)
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

func (r Results) validate() error {
	for i, step := range r.Steps {
		l := step.LatencyMS
		if !slices.IsSorted([]float64{l.P50, l.P90, l.P95, l.P99, l.Max}) {
			return fmt.Errorf("results: steps[%d]: percentile ordering", i)
		}
		if step.Errors > step.Requests {
			return fmt.Errorf("results: steps[%d]: errors exceed requests", i)
		}
		if step.Index != i {
			return fmt.Errorf("results: steps[%d]: index must be sequential", i)
		}
	}
	s := r.Saturation
	if s.Reached && (s.StepIndex == nil || *s.StepIndex < 0 || *s.StepIndex >= len(r.Steps)) || !s.Reached && s.StepIndex != nil {
		return fmt.Errorf("results: saturation.step_index must be in range when reached and null otherwise")
	}
	if r.Environment.Hardware.Fingerprint != hardwareFingerprint(r.Environment.Hardware) {
		return fmt.Errorf("results: hardware.fingerprint mismatch")
	}
	u, err := url.Parse(r.Environment.Configuration.TargetBaseURL)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("results: configuration.target_base_url must be HTTP(S) without credentials, query or fragment")
	}
	return nil
}
