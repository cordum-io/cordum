// Package perfharness defines workload and result contracts, not a load driver.
package perfharness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var KnownRequestKinds = [4]string{"jobs.submit", "jobs.approve", "edge.hook.evaluate", "mcp.tools.call"}
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var templateToken = regexp.MustCompile(`\{\{([^{}]*)\}\}`)

const maxContractBytes = 4 << 20

// maxRecordedConfigBytes bounds environment.configuration as WriteResults encodes it, leaving a run
// maxContractBytes-maxRecordedConfigBytes of its results document for steps, samples and environment.
const maxRecordedConfigBytes = 1 << 20

// requestDepth is a request's depth in a results document (environment.configuration.request_mix[i].requests[j]),
// so the loader applies the nesting limit that ParseResults applies to the recorded configuration.
const requestDepth = 6

type Workload struct {
	Name          string             `yaml:"name" json:"name"`
	Description   string             `yaml:"description" json:"description"`
	LatencyScope  string             `yaml:"latency_scope" json:"latency_scope"`
	Target        Target             `yaml:"target" json:"target"`
	Prerequisites []string           `yaml:"prerequisites" json:"prerequisites"`
	RequestMix    []Sequence         `yaml:"request_mix" json:"request_mix"`
	Profiles      map[string]Profile `yaml:"profiles" json:"profiles"`
	Saturation    Thresholds         `yaml:"saturation" json:"saturation"`
	QueueDepth    QueueSource        `yaml:"queue_depth" json:"queue_depth"`
	Tolerance     Tolerance          `yaml:"tolerance" json:"tolerance"`
}
type Target struct {
	BaseURLEnv string `yaml:"base_url_env" json:"base_url_env"`
	APIKeyEnv  string `yaml:"api_key_env" json:"api_key_env"`
	TenantEnv  string `yaml:"tenant_env" json:"tenant_env"`
}
type Sequence struct {
	Name     string    `yaml:"name" json:"name"`
	Weight   Weight    `yaml:"weight" json:"weight"`
	Requests []Request `yaml:"requests" json:"requests"`
}
type Request struct {
	Kind       string            `yaml:"kind" json:"kind"`
	PathParams map[string]string `yaml:"path_params,omitempty" json:"path_params,omitempty"`
	Payload    map[string]any    `yaml:"payload" json:"payload"`
	Capture    map[string]string `yaml:"capture,omitempty" json:"capture,omitempty"`
}
type Profile struct {
	Steps []ProfileStep `yaml:"steps" json:"steps"`
}
type ProfileStep struct {
	Concurrency Concurrency `yaml:"concurrency" json:"concurrency"`
	Duration    string      `yaml:"duration" json:"duration"`
}
type Thresholds struct {
	P99MS                float64 `yaml:"p99_ms" json:"p99_ms"`
	ErrorRate            float64 `yaml:"error_rate" json:"error_rate"`
	MinThroughputGainPct float64 `yaml:"min_throughput_gain_pct" json:"min_throughput_gain_pct"`
}
type QueueSource struct {
	Kind       string       `yaml:"kind" json:"kind"`
	URLEnv     string       `yaml:"url_env" json:"url_env"`
	Metric     string       `yaml:"metric,omitempty" json:"metric,omitempty"`
	Stream     string       `yaml:"stream,omitempty" json:"stream,omitempty"`
	IntervalMS Milliseconds `yaml:"interval_ms" json:"interval_ms"`
}
type Tolerance struct {
	ThroughputPct float64 `yaml:"throughput_pct" json:"throughput_pct"`
	P99Pct        float64 `yaml:"p99_pct" json:"p99_pct"`
}

// Integer fields reject YAML floats instead of allowing yaml.v3's truncation.
type Weight int
type Concurrency int
type Milliseconds int

func (w *Weight) UnmarshalYAML(n *yaml.Node) error { return decodeInteger(n, (*int)(w), "weight") }
func (c *Concurrency) UnmarshalYAML(n *yaml.Node) error {
	return decodeInteger(n, (*int)(c), "concurrency")
}
func (m *Milliseconds) UnmarshalYAML(n *yaml.Node) error {
	return decodeInteger(n, (*int)(m), "interval_ms")
}

func decodeInteger(node *yaml.Node, out *int, field string) error {
	if node.Tag != "!!int" {
		return fmt.Errorf("%s: must be an integer", field)
	}
	if err := node.Decode(out); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

func LoadWorkload(path string) (*Workload, error) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("workload %q: file: %w", name, err)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxContractBytes+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil || len(data) > maxContractBytes {
		return nil, fmt.Errorf("workload %q: file: unreadable or exceeds size limit", name)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var w Workload
	if err := decoder.Decode(&w); err != nil {
		return nil, fmt.Errorf("workload %q: fields: %w", name, err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("workload %q: trailing YAML document or data", name)
	}
	if w.Name != name {
		return nil, fmt.Errorf("workload %q: name: must match file base name", name)
	}
	if err := w.Validate(); err != nil {
		return nil, err
	}
	return &w, nil
}

func (w *Workload) Validate() error {
	if w == nil {
		return fmt.Errorf("workload: must not be null")
	}
	if w.Name == "" {
		return w.invalid("name", "must not be empty")
	}
	if w.LatencyScope != "http" {
		return w.invalid("latency_scope", "unsupported; only http is accepted")
	}
	for field, value := range map[string]string{"target.base_url_env": w.Target.BaseURLEnv, "target.api_key_env": w.Target.APIKeyEnv, "target.tenant_env": w.Target.TenantEnv, "queue_depth.url_env": w.QueueDepth.URLEnv} {
		if !envName.MatchString(value) {
			return w.invalid(field, "must be an environment variable name")
		}
	}
	if err := w.validateMix(); err != nil {
		return err
	}
	if err := w.validateProfiles(); err != nil {
		return err
	}
	for field, value := range map[string]float64{"saturation.p99_ms": w.Saturation.P99MS, "saturation.error_rate": w.Saturation.ErrorRate, "saturation.min_throughput_gain_pct": w.Saturation.MinThroughputGainPct, "tolerance.throughput_pct": w.Tolerance.ThroughputPct, "tolerance.p99_pct": w.Tolerance.P99Pct} {
		if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return w.invalid(field, "must be finite and above zero")
		}
	}
	if w.Saturation.ErrorRate > 1 {
		return w.invalid("saturation.error_rate", "must be at most one")
	}
	q := w.QueueDepth
	if q.IntervalMS <= 0 {
		return w.invalid("queue_depth.interval_ms", "must be above zero")
	}
	if q.Kind == "prometheus" && q.Metric != "" || q.Kind == "nats_jsz" && q.Stream != "" {
		return w.validateRecordedSize()
	}
	return w.invalid("queue_depth", "kind must be prometheus with metric or nats_jsz with stream")
}

func (w *Workload) invalid(field, rule string) error {
	return fmt.Errorf("workload %q: %s: %s", w.Name, field, rule)
}

func (w *Workload) validateProfiles() error {
	costs := make(map[string]float64)
	for name, profile := range w.Profiles {
		if name == "" {
			return w.invalid("profiles", "names must not be empty")
		}
		if len(profile.Steps) == 0 {
			return w.invalid("profiles."+name+".steps", "must not be empty")
		}
		for _, step := range profile.Steps {
			if step.Concurrency < 1 {
				return w.invalid("profiles."+name+".concurrency", "must be at least one")
			}
			duration, err := time.ParseDuration(step.Duration)
			if err != nil || duration <= 0 {
				return w.invalid("profiles."+name+".duration", "must be a positive duration")
			}
			costs[name] += float64(step.Concurrency) * duration.Seconds()
		}
	}
	smoke, exists := costs["smoke"]
	if !exists {
		return w.invalid("profiles.smoke", "must exist")
	}
	for _, cost := range costs {
		if cost < smoke {
			return w.invalid("profiles.smoke", "must have the smallest concurrency-seconds")
		}
	}
	return nil
}

// validateRecordedSize encodes each profile's recorded configuration, aliases expanded, at its
// depth in a results document (environment.configuration), as WriteResults does.
func (w *Workload) validateRecordedSize() error {
	for name, profile := range w.Profiles {
		config := Configuration{RequestMix: w.RequestMix, ProfileSteps: profile.Steps, Saturation: w.Saturation, QueueDepth: w.QueueDepth}
		data, err := json.MarshalIndent(config, resultsIndent+resultsIndent, resultsIndent)
		if err != nil || len(data) > maxRecordedConfigBytes {
			return w.invalid("profiles."+name, fmt.Sprintf("recorded configuration exceeds %d bytes", maxRecordedConfigBytes))
		}
	}
	return nil
}

func (w *Workload) validateMix() error {
	if len(w.RequestMix) == 0 {
		return w.invalid("request_mix", "must not be empty")
	}
	for i, sequence := range w.RequestMix {
		field := fmt.Sprintf("request_mix[%d]", i)
		if sequence.Name == "" {
			return w.invalid(field+".name", "must not be empty")
		}
		if sequence.Weight < 1 {
			return w.invalid(field+".weight", "must be an integer of at least one")
		}
		if len(sequence.Requests) == 0 {
			return w.invalid(field+".requests", "must not be empty")
		}
		captured := make(map[string]bool)
		for j, request := range sequence.Requests {
			requestField := fmt.Sprintf("%s.requests[%d]", field, j)
			if !slices.Contains(KnownRequestKinds[:], request.Kind) {
				return w.invalid(requestField+".kind", "unknown request kind")
			}
			if request.Payload == nil {
				return w.invalid(requestField+".payload", "must be an object")
			}
			data, err := json.Marshal(request)
			if err != nil {
				return w.invalid(requestField, "payload and path_params must be JSON compatible")
			}
			if err := checkJSON(data, captured, requestDepth); err != nil {
				return w.invalid(requestField, err.Error())
			}
			for name := range request.Capture {
				if !envName.MatchString(name) || captured[name] {
					return w.invalid(requestField+".capture", "must have unique identifier names")
				}
				captured[name] = true
			}
		}
	}
	return nil
}

// checkJSON walks unstructured payload tokens without converting numbers to floats.
// A nil captures map disables template checking for result documents.
func checkJSON(data []byte, captures map[string]bool, depth int) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return checkValue(decoder, captures, depth)
}

func checkValue(decoder *json.Decoder, captures map[string]bool, depth int) error {
	if depth > 64 {
		return fmt.Errorf("payload: exceeds nesting limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("payload: invalid JSON: %w", err)
	}
	if delim, ok := token.(json.Delim); ok {
		for decoder.More() {
			if delim == '{' {
				key, err := decoder.Token()
				if err != nil {
					return fmt.Errorf("payload: invalid key: %w", err)
				}
				if slices.Contains([]string{"authorization", "api_key", "password", "secret", "token"}, strings.ToLower(fmt.Sprint(key))) {
					return fmt.Errorf("payload/path_params: secret keys are prohibited")
				}
			}
			if err := checkValue(decoder, captures, depth+1); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	}
	if value, ok := token.(string); ok && captures != nil {
		return checkTemplate(value, captures)
	}
	return nil
}

func checkTemplate(value string, captures map[string]bool) error {
	for _, match := range templateToken.FindAllStringSubmatch(value, -1) {
		name := match[1]
		if strings.HasPrefix(name, "capture.") {
			if !captures[strings.TrimPrefix(name, "capture.")] {
				return fmt.Errorf("capture: reference must name an earlier request capture")
			}
		} else if name != "uuid" && name != "seq" {
			return fmt.Errorf("payload: unsupported template token")
		}
	}
	rest := templateToken.ReplaceAllString(value, "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return fmt.Errorf("payload: malformed template token")
	}
	return nil
}
