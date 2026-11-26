package perfharness

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"sync"
	"time"
)

func prepareResults(ctx context.Context, path, profile string) (*Workload, *Results, error) {
	w, err := LoadWorkload(path)
	if err != nil {
		return nil, nil, err
	}
	p, ok := w.Profiles[profile]
	if !ok {
		return nil, nil, fmt.Errorf("unknown workload profile")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot hash workload file")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r := &Results{SchemaVersion: 1, Run: RunInfo{ID: now, StartedAt: now, FinishedAt: now, Profile: profile},
		Workload:    WorkloadInfo{Name: w.Name, File: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))},
		Environment: CaptureEnvironment(ctx), QueueDepth: QueueDepth{Source: w.QueueDepth.Kind, IntervalMS: int(w.QueueDepth.IntervalMS)}}
	names := []string{w.Target.BaseURLEnv, w.Target.APIKeyEnv, w.Target.TenantEnv, w.QueueDepth.URLEnv, "CORDUM_TLS_CA"}
	slices.Sort(names)
	r.Environment.Configuration = Configuration{TargetBaseURL: os.Getenv(w.Target.BaseURLEnv), RequestMix: w.RequestMix,
		ProfileSteps: p.Steps, Saturation: w.Saturation, QueueDepth: w.QueueDepth, EnvVarsRead: slices.Compact(names)}
	encoded, err := json.MarshalIndent(r, "", resultsIndent)
	if err != nil {
		return nil, nil, err
	}
	if err := checkRunBudget(p, w.QueueDepth.IntervalMS, len(encoded)); err != nil {
		return nil, nil, err
	}
	return w, r, nil
}

func summarizeStep(samples []float64, requests, failures uint64, duration time.Duration) StepResult {
	r := StepResult{DurationMS: float64(duration) / float64(time.Millisecond), Requests: requests,
		Errors: failures, ThroughputRPS: float64(requests-failures) / duration.Seconds()}
	if len(samples) == 0 {
		return r
	}
	slices.Sort(samples)
	percentile := func(p float64) float64 { return samples[int(math.Ceil(p*float64(len(samples))))-1] }
	r.LatencyMS = Latency{P50: percentile(.5), P90: percentile(.9), P95: percentile(.95), P99: percentile(.99), Max: samples[len(samples)-1]}
	return r
}

func findSaturation(steps []StepResult, thresholds Thresholds) SaturationResult {
	for i, step := range steps {
		rule := ""
		switch {
		case step.LatencyMS.P99 > thresholds.P99MS:
			rule = "p99_ms"
		case step.Requests > 0 && float64(step.Errors)/float64(step.Requests) > thresholds.ErrorRate:
			rule = "error_rate"
		case i > 0 && steps[i-1].ThroughputRPS > 0 && (step.ThroughputRPS-steps[i-1].ThroughputRPS)*100/steps[i-1].ThroughputRPS < thresholds.MinThroughputGainPct:
			rule = "min_throughput_gain_pct"
		}
		if rule != "" {
			return SaturationResult{Rule: rule, Reached: true, StepIndex: &i, Reason: "threshold crossed: " + rule}
		}
	}
	last := len(steps) - 1
	return SaturationResult{Rule: "not reached", Reason: fmt.Sprintf("not reached; highest step %d (concurrency %d)", last, steps[last].Concurrency)}
}

// Reserve 160 bytes per queue sample and 1024 per step (including JSON indentation).
// HTTP teardown is bounded by the client's two-second timeout, even at a step boundary.
func checkRunBudget(profile Profile, interval Milliseconds, overhead int) error {
	bytes := float64(overhead + len(profile.Steps)*1024 + 160)
	for _, step := range profile.Steps {
		duration, err := time.ParseDuration(step.Duration)
		if err != nil || duration <= 0 || interval <= 0 || step.Concurrency < 1 || step.Concurrency > 4096 {
			return fmt.Errorf("invalid duration, interval or concurrency (maximum 4096)")
		}
		bytes += math.Ceil((duration.Seconds()+2)*1000/float64(interval)) * 160
	}
	if bytes > maxContractBytes-maxRecordedConfigBytes {
		return fmt.Errorf("profile exceeds results run-time budget")
	}
	return nil
}

func runStep(ctx context.Context, spec ProfileStep, index int, execute func(context.Context, func(time.Duration, error))) (StepResult, error) {
	duration, err := time.ParseDuration(spec.Duration)
	if err != nil {
		return StepResult{}, err
	}
	stepCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	var mu sync.Mutex
	var workers sync.WaitGroup
	var requests, failures uint64
	var samples []float64
	var overflow bool
	record := func(latency time.Duration, requestErr error) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if requestErr != nil {
			failures++
			return
		}
		// Exact nearest-rank samples, never a silently truncated percentile estimate.
		if len(samples) == 1<<20 {
			overflow = true
			cancel()
			return
		}
		samples = append(samples, float64(latency)/float64(time.Millisecond))
	}
	for i := 0; i < int(spec.Concurrency); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for stepCtx.Err() == nil {
				execute(stepCtx, record)
			}
		}()
	}
	workers.Wait()
	if overflow {
		return StepResult{}, fmt.Errorf("step exceeds latency sample limit (1048576)")
	}
	if err := ctx.Err(); err != nil {
		return StepResult{}, err
	}
	r := summarizeStep(samples, requests, failures, duration)
	r.Index, r.Concurrency = index, int(spec.Concurrency)
	return r, nil
}
