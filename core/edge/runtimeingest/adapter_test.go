package runtimeingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

const (
	testTenantID    = "tenant-rt-1"
	testSessionID   = "edge-session-rt-1"
	testExecutionID = "edge-exec-rt-1"
	testSourceID    = "tetragon-test"
	testNonce       = "018f9f65-1e73-7a33-9ed1-6cb3a5aa0001"
)

func testObservedAt() time.Time {
	return time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
}

func testEnvelope(kind string, mutators ...func(*RuntimeEventEnvelope)) RuntimeEventEnvelope {
	env := RuntimeEventEnvelope{
		TenantID:      testTenantID,
		SessionID:     testSessionID,
		ExecutionID:   testExecutionID,
		SourceEventID: "src-evt-" + kind,
		ObservedAt:    testObservedAt(),
		Kind:          kind,
	}
	switch kind {
	case KindProcessExec:
		env.Process = &ProcessSummary{
			ExecutableBasename: "curl",
			ExecutableSHA256:   "sha256:abc",
			ArgumentCount:      3,
		}
	case KindFileRead, KindFileWrite:
		op := "read"
		if kind == KindFileWrite {
			op = "write"
		}
		env.File = &FileSummary{Operation: op, PathRedacted: "/tmp/data"}
	case KindNetworkConnect:
		env.Network = &NetworkSummary{HostRedacted: "api.example.com", IPPrefix: "10.0.0.0/24", Port: 443, Protocol: "tcp"}
	case KindDNSQuery:
		env.DNS = &DNSSummary{QNameRedacted: "api.example.com", QType: "A"}
	}
	for _, m := range mutators {
		m(&env)
	}
	return env
}

func testBatch(events ...RuntimeEventEnvelope) RuntimeBatch {
	return RuntimeBatch{
		Source:  SourceIdentity{ID: testSourceID},
		Nonce:   testNonce,
		BatchID: "batch-1",
		Events:  events,
	}
}

func mapOne(t *testing.T, kind string, mutators ...func(*RuntimeEventEnvelope)) edgecore.AgentActionEvent {
	t.Helper()
	a := NewAdapter(AdapterOptions{})
	res, err := a.Map(testBatch(testEnvelope(kind, mutators...)))
	if err != nil {
		t.Fatalf("Map %s: %v", kind, err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("Map %s: events = %d, want 1", kind, len(res.Events))
	}
	return res.Events[0]
}

func TestAdapterMapsProcessExec(t *testing.T) {
	got := mapOne(t, KindProcessExec)
	if got.Kind != edgecore.EventKindRuntimeProcessExec {
		t.Fatalf("Kind = %q, want %q", got.Kind, edgecore.EventKindRuntimeProcessExec)
	}
	if got.Layer != edgecore.LayerRuntime {
		t.Fatalf("Layer = %q, want %q", got.Layer, edgecore.LayerRuntime)
	}
	if got.Decision != edgecore.DecisionRecorded {
		t.Fatalf("Decision = %q, want %q", got.Decision, edgecore.DecisionRecorded)
	}
	if got.RuleTier != "" {
		t.Fatalf("RuleTier = %q, want empty", got.RuleTier)
	}
	if got.Status != edgecore.ActionStatusOK {
		t.Fatalf("Status = %q, want %q", got.Status, edgecore.ActionStatusOK)
	}
	if got.TenantID != testTenantID || got.SessionID != testSessionID || got.ExecutionID != testExecutionID {
		t.Fatalf("identity = %q/%q/%q, want %q/%q/%q", got.TenantID, got.SessionID, got.ExecutionID, testTenantID, testSessionID, testExecutionID)
	}
	if got.EventID == "" {
		t.Fatalf("EventID empty")
	}
	if got.Timestamp.IsZero() {
		t.Fatalf("Timestamp zero")
	}
	if got.InputRedacted["executable_basename"] != "curl" {
		t.Fatalf("InputRedacted.executable_basename = %#v, want curl", got.InputRedacted["executable_basename"])
	}
	// Validate against the existing edge contract.
	if err := got.Validate(); err != nil {
		t.Fatalf("AgentActionEvent.Validate: %v", err)
	}
}

func TestAdapterMapsFileRead(t *testing.T) {
	got := mapOne(t, KindFileRead)
	if got.Kind != edgecore.EventKindRuntimeFileRead {
		t.Fatalf("Kind = %q, want %q", got.Kind, edgecore.EventKindRuntimeFileRead)
	}
	if got.Layer != edgecore.LayerRuntime {
		t.Fatalf("Layer = %q", got.Layer)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestAdapterMapsFileWrite(t *testing.T) {
	got := mapOne(t, KindFileWrite)
	if got.Kind != edgecore.EventKindRuntimeFileWrite {
		t.Fatalf("Kind = %q, want %q", got.Kind, edgecore.EventKindRuntimeFileWrite)
	}
}

func TestAdapterMapsNetworkConnect(t *testing.T) {
	got := mapOne(t, KindNetworkConnect)
	if got.Kind != edgecore.EventKindRuntimeNetworkConnect {
		t.Fatalf("Kind = %q, want %q", got.Kind, edgecore.EventKindRuntimeNetworkConnect)
	}
	if _, ok := got.InputRedacted["port"]; !ok {
		t.Fatalf("InputRedacted missing port: %#v", got.InputRedacted)
	}
	if got.InputRedacted["protocol"] != "tcp" {
		t.Fatalf("InputRedacted.protocol = %#v, want tcp", got.InputRedacted["protocol"])
	}
}

func TestAdapterMapsDNSQuery(t *testing.T) {
	got := mapOne(t, KindDNSQuery)
	if got.Kind != edgecore.EventKindRuntimeDNSQuery {
		t.Fatalf("Kind = %q, want %q", got.Kind, edgecore.EventKindRuntimeDNSQuery)
	}
}

func TestAdapterMapsBatchStableEventID(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	env := testEnvelope(KindProcessExec)
	res1, err := a.Map(testBatch(env))
	if err != nil {
		t.Fatalf("first map: %v", err)
	}
	res2, err := a.Map(testBatch(env))
	if err != nil {
		t.Fatalf("second map: %v", err)
	}
	if res1.Events[0].EventID != res2.Events[0].EventID {
		t.Fatalf("EventID not stable: %q vs %q", res1.Events[0].EventID, res2.Events[0].EventID)
	}
}

func TestAdapterRejectsUnknownKind(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	_, err := a.Map(testBatch(testEnvelope("runtime.process.exec", func(e *RuntimeEventEnvelope) { e.Kind = "runtime.bogus" })))
	if err == nil {
		t.Fatalf("expected error for unknown kind")
	}
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestAdapterRejectsMissingTenantID(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	_, err := a.Map(testBatch(testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) { e.TenantID = " " })))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestAdapterRejectsMissingSessionID(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	_, err := a.Map(testBatch(testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) { e.SessionID = "" })))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestAdapterRejectsMissingExecutionID(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	_, err := a.Map(testBatch(testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) { e.ExecutionID = "" })))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestAdapterRejectsMissingSourceID(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	batch := testBatch(testEnvelope(KindProcessExec))
	batch.Source.ID = "  "
	_, err := a.Map(batch)
	if !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("err = %v, want ErrInvalidBatch", err)
	}
}

func TestAdapterRejectsMissingSourceEventID(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	_, err := a.Map(testBatch(testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) { e.SourceEventID = "" })))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestAdapterRejectsMissingObservedAt(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	_, err := a.Map(testBatch(testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) { e.ObservedAt = time.Time{} })))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestAdapterRejectsEmptyBatch(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	_, err := a.Map(testBatch())
	if !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("err = %v, want ErrInvalidBatch", err)
	}
}

func TestAdapterRejectsBatchExceedingMaxEvents(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	events := make([]RuntimeEventEnvelope, MaxRuntimeBatchEvents+1)
	for i := range events {
		events[i] = testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) { e.SourceEventID = "src-" + strings.Repeat("x", i+1) })
	}
	_, err := a.Map(testBatch(events...))
	if !errors.Is(err, ErrRuntimeBatchTooLarge) {
		t.Fatalf("err = %v, want ErrRuntimeBatchTooLarge", err)
	}
}

func TestAdapterRejectsBatchAtBoundaryExactlyMaxIsAccepted(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	events := make([]RuntimeEventEnvelope, MaxRuntimeBatchEvents)
	for i := range events {
		events[i] = testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) {
			e.SourceEventID = "src-bdy-" + strings.Repeat("y", i+1)
		})
	}
	res, err := a.Map(testBatch(events...))
	if err != nil {
		t.Fatalf("boundary batch err = %v", err)
	}
	if len(res.Events) != MaxRuntimeBatchEvents {
		t.Fatalf("boundary batch events = %d, want %d", len(res.Events), MaxRuntimeBatchEvents)
	}
}

func TestAdapterRejectsEnvelopeExceedingMaxBytes(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	big := strings.Repeat("x", MaxRuntimeEnvelopeBytes+10)
	_, err := a.Map(testBatch(testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) {
		e.Labels = map[string]string{"node": big}
	})))
	if !errors.Is(err, ErrRuntimeBatchTooLarge) {
		t.Fatalf("err = %v, want ErrRuntimeBatchTooLarge", err)
	}
}

func TestAdapterRejectsLabelOverflow(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	labels := make(map[string]string, MaxRuntimeLabelEntries+1)
	for i := 0; i <= MaxRuntimeLabelEntries; i++ {
		labels["k"+strings.Repeat("x", i)] = "v"
	}
	_, err := a.Map(testBatch(testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) { e.Labels = labels })))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestAdapterSanitizesSecretLikeRuntimeLabels(t *testing.T) {
	bearerToken := "Bearer cordum_fake_runtime_label_token_0123456789"
	apiKey := "sk-test-runtime-label-secret-123456"
	githubToken := "ghp_testRuntimeLabelToken123456"
	privateKey := "-----BEGIN PRIVATE KEY-----\ncordum_fake_runtime_label_private_key\n-----END PRIVATE KEY-----"

	got := mapOne(t, KindProcessExec, func(e *RuntimeEventEnvelope) {
		e.Labels = map[string]string{
			"node":                "node-a",
			"authorization":       bearerToken,
			"password":            "cordum_fake_password_value",
			"api_key":             "cordum_fake_api_key_value",
			"CLIENT-Secret":       "cordum_fake_client_secret_value",
			"session_token":       githubToken,
			bearerToken:           "bearer-token-as-key",
			apiKey:                "api-key-as-key",
			githubToken:           "github-token-as-key",
			privateKey:            "private-key-as-key",
			"runtime.source_id":   "attacker-supplied",
			" runtime.source_id ": "spaced-attacker-supplied",
			"trace_id":            bearerToken,
			"api_status":          apiKey,
			"note":                githubToken,
			"pem_hint":            privateKey,
		}
	})

	if got.Labels["node"] != "node-a" {
		t.Fatalf("safe label was not preserved")
	}
	if got.Labels["runtime.source_id"] != testSourceID {
		t.Fatalf("runtime.source_id = %q, want authenticated source id", got.Labels["runtime.source_id"])
	}
	if _, ok := got.Labels[" runtime.source_id "]; ok {
		t.Fatalf("spaced runtime.source_id override label was persisted")
	}
	assertNoSecretLikeLabelKeys(t, got.Labels)
	assertNoRuntimeLabelValueContains(t, got.Labels, "bearer token", bearerToken)
	assertNoRuntimeLabelValueContains(t, got.Labels, "api key", apiKey)
	assertNoRuntimeLabelValueContains(t, got.Labels, "github token", githubToken)
	assertNoRuntimeLabelValueContains(t, got.Labels, "private key", privateKey)
	assertNoRuntimeLabelKeyContains(t, got.Labels, "bearer token", bearerToken)
	assertNoRuntimeLabelKeyContains(t, got.Labels, "api key", apiKey)
	assertNoRuntimeLabelKeyContains(t, got.Labels, "github token", githubToken)
	assertNoRuntimeLabelKeyContains(t, got.Labels, "private key", privateKey)
}

func TestAdapterRejectsOversizedSafeRuntimeLabelValue(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	oversized := strings.Repeat("a", edgecore.MaxLabelValueBytes+1)
	_, err := a.Map(testBatch(testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) {
		e.Labels = map[string]string{"safe": oversized}
	})))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
	if strings.Contains(err.Error(), oversized) {
		t.Fatalf("oversized label error leaked raw value")
	}
}

func assertNoSecretLikeLabelKeys(t *testing.T, labels edgecore.Labels) {
	t.Helper()
	for key := range labels {
		normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"), " ", "_"))
		switch {
		case normalized == "authorization",
			normalized == "api_key",
			normalized == "token",
			normalized == "password",
			normalized == "secret",
			normalized == "client_secret",
			strings.HasSuffix(normalized, "_token"),
			strings.HasSuffix(normalized, "_password"),
			strings.HasSuffix(normalized, "_secret"),
			strings.HasSuffix(normalized, "_api_key"),
			strings.HasSuffix(normalized, "_private_key"):
			t.Fatalf("secret-like label key %q was persisted", key)
		}
	}
}

func assertNoRuntimeLabelValueContains(t *testing.T, labels edgecore.Labels, name, forbidden string) {
	t.Helper()
	for key, value := range labels {
		if strings.Contains(value, forbidden) {
			t.Fatalf("runtime label value leaked %s under key %q", name, key)
		}
	}
}

func assertNoRuntimeLabelKeyContains(t *testing.T, labels edgecore.Labels, name, forbidden string) {
	t.Helper()
	for key := range labels {
		if strings.Contains(key, forbidden) {
			t.Fatalf("runtime label key leaked %s", name)
		}
	}
}

func TestAdapterRedactsFilePath(t *testing.T) {
	got := mapOne(t, KindFileRead, func(e *RuntimeEventEnvelope) {
		e.File.PathRedacted = "/srv/AKIAIOSFODNN7EXAMPLE/key"
	})
	raw, _ := json.Marshal(got.InputRedacted)
	if strings.Contains(string(raw), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("redactor leaked AKIA token: %s", raw)
	}
}

func TestAdapterRedactsNetworkHost(t *testing.T) {
	got := mapOne(t, KindNetworkConnect, func(e *RuntimeEventEnvelope) {
		e.Network.HostRedacted = "AKIAIOSFODNN7EXAMPLE.evil.example.com"
	})
	raw, _ := json.Marshal(got.InputRedacted)
	if strings.Contains(string(raw), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("redactor leaked AKIA token in host: %s", raw)
	}
}

func TestAdapterRedactsDNSQname(t *testing.T) {
	got := mapOne(t, KindDNSQuery, func(e *RuntimeEventEnvelope) {
		e.DNS.QNameRedacted = "sk-AKIAIOSFODNN7EXAMPLE.example.com"
	})
	raw, _ := json.Marshal(got.InputRedacted)
	if strings.Contains(string(raw), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("redactor leaked AKIA token: %s", raw)
	}
}

func TestAdapterMapsStatusFailed(t *testing.T) {
	got := mapOne(t, KindProcessExec, func(e *RuntimeEventEnvelope) { e.OutcomeStatus = "failed" })
	if got.Status != edgecore.ActionStatusFailed {
		t.Fatalf("Status = %q, want %q", got.Status, edgecore.ActionStatusFailed)
	}
}

func TestAdapterMapsStatusDegraded(t *testing.T) {
	got := mapOne(t, KindProcessExec, func(e *RuntimeEventEnvelope) { e.OutcomeStatus = "degraded" })
	if got.Status != edgecore.ActionStatusDegraded {
		t.Fatalf("Status = %q, want %q", got.Status, edgecore.ActionStatusDegraded)
	}
}

func TestAdapterRejectsUnknownOutcomeStatus(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	_, err := a.Map(testBatch(testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) { e.OutcomeStatus = "bogus" })))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestAdapterPreservesArtifactPointers(t *testing.T) {
	got := mapOne(t, KindFileWrite, func(e *RuntimeEventEnvelope) {
		// EventID stamped post-map; pre-fill with the deterministic format the
		// adapter would emit so artifact pointer validation can match.
		e.ArtifactPtrs = []edgecore.ArtifactPointer{{
			TenantID:       e.TenantID,
			SessionID:      e.SessionID,
			ExecutionID:    e.ExecutionID,
			ArtifactType:   edgecore.ArtifactTypeEvidenceBundle,
			RetentionClass: edgecore.RetentionClassStandard,
			RedactionLevel: edgecore.RedactionLevelStandard,
			SHA256:         "sha256:rt-artifact",
			URI:            "artifact://edge/rt-artifact",
			CreatedAt:      testObservedAt(),
		}}
	})
	if len(got.ArtifactPointers) != 1 {
		t.Fatalf("ArtifactPointers len = %d, want 1", len(got.ArtifactPointers))
	}
	if got.ArtifactPointers[0].EventID != got.EventID {
		t.Fatalf("artifact event_id = %q, want %q", got.ArtifactPointers[0].EventID, got.EventID)
	}
}

func TestAdapterRejectsArtifactPointerCrossTenant(t *testing.T) {
	a := NewAdapter(AdapterOptions{})
	_, err := a.Map(testBatch(testEnvelope(KindFileWrite, func(e *RuntimeEventEnvelope) {
		e.ArtifactPtrs = []edgecore.ArtifactPointer{{
			TenantID:       "different-tenant",
			SessionID:      e.SessionID,
			ExecutionID:    e.ExecutionID,
			ArtifactType:   edgecore.ArtifactTypeEvidenceBundle,
			RetentionClass: edgecore.RetentionClassStandard,
			RedactionLevel: edgecore.RedactionLevelStandard,
			SHA256:         "sha256:rt",
			URI:            "artifact://edge/rt",
			CreatedAt:      testObservedAt(),
		}}
	})))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestAdapterDeterministicSampling(t *testing.T) {
	// With denominator > 1, sampling-out is observable. Run a small fleet of
	// envelopes through Map twice and assert each event lands in the same
	// accept/drop bucket across runs.
	a := NewAdapter(AdapterOptions{SampleNumerator: 1, SampleDenominator: 4})
	envelopes := make([]RuntimeEventEnvelope, 32)
	for i := range envelopes {
		envelopes[i] = testEnvelope(KindProcessExec, func(e *RuntimeEventEnvelope) {
			e.SourceEventID = "sample-" + strings.Repeat("z", i+1)
		})
	}
	res1, err := a.Map(testBatch(envelopes...))
	if err != nil {
		t.Fatalf("first Map: %v", err)
	}
	res2, err := a.Map(testBatch(envelopes...))
	if err != nil {
		t.Fatalf("second Map: %v", err)
	}
	if len(res1.Events) != len(res2.Events) {
		t.Fatalf("non-deterministic accept count: %d vs %d", len(res1.Events), len(res2.Events))
	}
	if len(res1.Dropped) != len(res2.Dropped) {
		t.Fatalf("non-deterministic drop count: %d vs %d", len(res1.Dropped), len(res2.Dropped))
	}
	if len(res1.Dropped) == 0 {
		t.Fatalf("expected some drops at 1/4 sampling on 32 envelopes (deterministic, may need to retune seed)")
	}
	for i := range res1.Dropped {
		if res1.Dropped[i].SourceEventID != res2.Dropped[i].SourceEventID {
			t.Fatalf("drop order non-deterministic: %q vs %q", res1.Dropped[i].SourceEventID, res2.Dropped[i].SourceEventID)
		}
		if res1.Dropped[i].Reason != DropReasonSampledOut {
			t.Fatalf("drop reason = %q, want %q", res1.Dropped[i].Reason, DropReasonSampledOut)
		}
	}
}

func TestDecodeBatchRejectsForbiddenTopLevelKey(t *testing.T) {
	body := []byte(`{
		"source":{"source_id":"tetragon-test"},
		"events":[{
			"tenant_id":"t","session_id":"s","execution_id":"e",
			"source_event_id":"se","observed_at":"2026-05-17T12:00:00Z",
			"kind":"runtime.process.exec",
			"argv":["curl","https://example.com"]
		}]
	}`)
	_, err := DecodeBatch(bytes.NewReader(body))
	if err == nil {
		t.Fatalf("expected error for argv field")
	}
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestDecodeBatch_RejectsTrailingTokens(t *testing.T) {
	validBatch := `{"source":{"source_id":"x"},"nonce":"018f9f65-1e73-7a33-9ed1-6cb3a5aa0001","events":[]}`
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid", input: validBatch, wantErr: false},
		{name: "trailing_object", input: validBatch + `{"foo":"bar"}`, wantErr: true},
		{name: "trailing_value", input: validBatch + `42`, wantErr: true},
		{name: "trailing_array", input: validBatch + `[1,2,3]`, wantErr: true},
		{name: "trailing_garbage", input: validBatch + `notjson`, wantErr: true},
		{name: "trailing_whitespace_only", input: validBatch + "  \n  \t  ", wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeBatch(strings.NewReader(tc.input))
			if tc.wantErr && err == nil {
				t.Fatalf("DecodeBatch(%q) error = nil, want error", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("DecodeBatch(%q) error = %v, want nil", tc.input, err)
			}
		})
	}
}

func TestDecodeBatch_NonceRequired(t *testing.T) {
	t.Setenv("CORDUM_EDGE_RUNTIME_REPLAY_REQUIRED", "")
	body := []byte(`{"source":{"source_id":"tetragon-test"},"events":[]}`)

	_, err := DecodeBatch(bytes.NewReader(body))
	if !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("err = %v, want ErrInvalidBatch", err)
	}
	if !strings.Contains(err.Error(), "nonce required") {
		t.Fatalf("err = %v, want nonce required message", err)
	}
}

func TestDecodeBatch_NonceLengthBounded(t *testing.T) {
	cases := []string{"short", strings.Repeat("a", 65)}
	for _, nonce := range cases {
		t.Run(nonce, func(t *testing.T) {
			body := []byte(`{"source":{"source_id":"tetragon-test"},"nonce":"` + nonce + `","events":[]}`)
			_, err := DecodeBatch(bytes.NewReader(body))
			if !errors.Is(err, ErrInvalidBatch) {
				t.Fatalf("err = %v, want ErrInvalidBatch", err)
			}
			if !strings.Contains(err.Error(), "nonce length") {
				t.Fatalf("err = %v, want nonce length message", err)
			}
		})
	}
}

func TestDecodeBatch_NonceCharsetValidated(t *testing.T) {
	body := []byte(`{"source":{"source_id":"tetragon-test"},"nonce":"nonce-000000000001!","events":[]}`)
	_, err := DecodeBatch(bytes.NewReader(body))
	if !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("err = %v, want ErrInvalidBatch", err)
	}
	if !strings.Contains(err.Error(), "invalid characters") {
		t.Fatalf("err = %v, want invalid characters message", err)
	}
}

func TestDecodeBatch_NonceOptionalWhenFlagDisabled(t *testing.T) {
	t.Setenv("CORDUM_EDGE_RUNTIME_REPLAY_REQUIRED", "false")
	body := []byte(`{"source":{"source_id":"tetragon-test"},"events":[]}`)

	if _, err := DecodeBatch(bytes.NewReader(body)); err != nil {
		t.Fatalf("DecodeBatch with replay-required flag disabled: %v", err)
	}
}

func TestDecodeBatch_RejectsUnrecognizedReplayRequiredFlag(t *testing.T) {
	t.Setenv("CORDUM_EDGE_RUNTIME_REPLAY_REQUIRED", "disable")
	body := []byte(`{"source":{"source_id":"tetragon-test"},"nonce":"nonce-flag-typo-01","events":[]}`)

	_, err := DecodeBatch(bytes.NewReader(body))
	if !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("DecodeBatch with unrecognized replay-required flag err = %v, want ErrInvalidBatch", err)
	}
}

func TestAdapterRedactionHashMatchesDocs(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/edge/runtime-ingestion.md")
	if err != nil {
		t.Fatalf("read runtime ingestion docs: %v", err)
	}
	text := string(doc)
	if strings.Contains(text, "RedactionHashBoth") {
		t.Fatal("runtime ingestion docs claim RedactionHashBoth; adapter uses RedactionHashNone")
	}
	if !strings.Contains(text, "RedactionHashNone") {
		t.Fatal("runtime ingestion docs must name RedactionHashNone to match adapter")
	}
}

func TestRuntimeIngestionDocsMatchForbiddenRawKeyErrorCode(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/edge/runtime-ingestion.md")
	if err != nil {
		t.Fatalf("read runtime ingestion docs: %v", err)
	}
	text := string(doc)
	if strings.Contains(text, "raw_payload_rejected") {
		t.Fatal("runtime ingestion docs mention raw_payload_rejected; implementation returns invalid_request")
	}
	if !strings.Contains(text, "Forbidden raw-field key present             | 400  | `invalid_request`") {
		t.Fatal("runtime ingestion docs must map forbidden raw-field keys to invalid_request")
	}
}

func TestDecodeBatchRejectsForbiddenKeysVariants(t *testing.T) {
	for _, forbidden := range []string{"args", "cmdline", "command_line", "env", "environment", "file_content", "file_contents", "packet", "payload", "body", "request_body", "response_body", "headers", "header", "cookie", "cookies", "secret", "secrets", "token", "tokens", "password", "passwords", "api_key", "apikey", "private_key", "dns_response", "response"} {
		t.Run(forbidden, func(t *testing.T) {
			body := []byte(`{
				"source":{"source_id":"tetragon-test"},
				"events":[{
					"tenant_id":"t","session_id":"s","execution_id":"e",
					"source_event_id":"se","observed_at":"2026-05-17T12:00:00Z",
					"kind":"runtime.process.exec",
					"` + forbidden + `":"x"
				}]
			}`)
			_, err := DecodeBatch(bytes.NewReader(body))
			if !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("forbidden %q: err = %v, want ErrInvalidEnvelope", forbidden, err)
			}
		})
	}
}

func TestDecodeBatchAcceptsCanonicalShape(t *testing.T) {
	body := []byte(`{
		"source":{"source_id":"tetragon-test"},
		"nonce":"018f9f65-1e73-7a33-9ed1-6cb3a5aa0001",
		"batch_id":"b1",
		"events":[{
			"tenant_id":"t","session_id":"s","execution_id":"e",
			"source_event_id":"se","observed_at":"2026-05-17T12:00:00Z",
			"kind":"runtime.process.exec",
			"process":{"executable_basename":"curl","argument_count":1}
		}]
	}`)
	batch, err := DecodeBatch(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("DecodeBatch: %v", err)
	}
	if batch.Source.ID != "tetragon-test" {
		t.Fatalf("source_id = %q", batch.Source.ID)
	}
	if batch.Nonce != testNonce {
		t.Fatalf("nonce = %q, want %q", batch.Nonce, testNonce)
	}
	if len(batch.Events) != 1 || batch.Events[0].Kind != KindProcessExec {
		t.Fatalf("decoded batch = %#v", batch)
	}
}

func TestDecodeBatchAcceptsDocumentedRuntimeIngestionShape(t *testing.T) {
	body := []byte(`{
		"source":{"source_id":"tetragon-cluster-a"},
		"nonce":"018f9f65-1e73-7a33-9ed1-6cb3a5aa0001",
		"batch_id":"uuid-v4",
		"events":[{
			"tenant_id":"tenant-1234","session_id":"edge-session-abc",
			"execution_id":"edge-exec-xyz",
			"source_event_id":"stable-hash-or-source-uid",
			"observed_at":"2026-05-17T13:42:11.123Z",
			"kind":"runtime.process.exec",
			"process":{"executable_basename":"curl","executable_sha256":"abcdef...","argument_count":3},
			"file":{"operation":"read","path_redacted":"/tmp/[REDACTED]"},
			"network":{"host_redacted":"[REDACTED].example","ip_prefix":"10.0.0.0/24","port":443,"protocol":"tcp"},
			"dns":{"qname_redacted":"[REDACTED].svc.cluster.local","qtype":"A"},
			"labels":{"node":"node-7","container_runtime":"containerd"},
			"artifact_ptrs":[]
		}]
	}`)

	batch, err := DecodeBatch(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("DecodeBatch documented shape: %v", err)
	}
	if batch.Source.ID != "tetragon-cluster-a" {
		t.Fatalf("source.source_id = %q", batch.Source.ID)
	}
	if got := batch.Events[0].Labels["container_runtime"]; got != "containerd" {
		t.Fatalf("labels.container_runtime = %q", got)
	}
}

func TestDecodeBatchRejectsStaleRuntimeIngestionDocShape(t *testing.T) {
	cases := []struct {
		name        string
		body        []byte
		wantMessage string
	}{
		{
			name: "top_level_source_id",
			body: []byte(`{
				"source_id":"tetragon-cluster-a",
				"events":[{
					"tenant_id":"tenant-1234","session_id":"edge-session-abc",
					"execution_id":"edge-exec-xyz","source_event_id":"se",
					"observed_at":"2026-05-17T13:42:11.123Z",
					"kind":"runtime.process.exec",
					"process":{"executable_basename":"curl","argument_count":3}
				}]
			}`),
			wantMessage: `unknown field "source_id"`,
		},
		{
			name: "event_metadata",
			body: []byte(`{
				"source":{"source_id":"tetragon-cluster-a"},
				"events":[{
					"tenant_id":"tenant-1234","session_id":"edge-session-abc",
					"execution_id":"edge-exec-xyz","source_event_id":"se",
					"observed_at":"2026-05-17T13:42:11.123Z",
					"kind":"runtime.process.exec",
					"process":{"executable_basename":"curl","argument_count":3},
					"metadata":{"container_runtime":"containerd"}
				}]
			}`),
			wantMessage: `unknown field "metadata"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeBatch(bytes.NewReader(tc.body))
			if !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
			}
			if !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("err = %v, want message containing %q", err, tc.wantMessage)
			}
		})
	}
}
