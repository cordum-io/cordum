package k8s_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow"
	"github.com/cordum/cordum/core/edge/shadow/k8s"
)

const (
	testClusterID      = "cluster-prod-eu1"
	testTenantA        = "tenant-a"
	testQuarantineTen  = "cordum.shadow.quarantine"
	testHeartbeatLabel = "cordum.io/edge-session-id"
	testTenantLabel    = "cordum.io/tenant-id"
)

// detectorFixture wires up a Detector with a miniredis-backed shadow.Store,
// a fake K8s clientset, and a spy Observer so tests can assert exact
// emitted findings + metric/audit calls without any global state.
type detectorFixture struct {
	detector *k8s.Detector
	store    shadow.Store
	client   *fake.Clientset
	observer *spyObserver
	mr       *miniredis.Miniredis
	clock    time.Time
}

func newFixture(t *testing.T, cfg k8s.Config, objs ...runtime.Object) *detectorFixture {
	t.Helper()
	return newFixtureWithObserver(t, cfg, newSpyObserver(), objs...)
}

func newFixtureWithObserver(t *testing.T, cfg k8s.Config, observer k8s.Observer, objs ...runtime.Object) *detectorFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
	t.Cleanup(func() { _ = rdb.Close(); mr.Close() })

	clock := time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC)
	store, err := shadow.NewRedisStore(rdb,
		shadow.WithClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}

	client := fake.NewSimpleClientset(objs...)
	spy, _ := observer.(*spyObserver)

	if cfg.ClusterID == "" {
		cfg.ClusterID = testClusterID
	}
	if cfg.TenantLabelKey == "" {
		cfg.TenantLabelKey = testTenantLabel
	}
	if cfg.HeartbeatLabelKey == "" {
		cfg.HeartbeatLabelKey = testHeartbeatLabel
	}
	if cfg.HeartbeatMissedThreshold == 0 {
		cfg.HeartbeatMissedThreshold = 3
	}
	if len(cfg.KnownAgentImages) == 0 {
		cfg.KnownAgentImages = []string{"anthropic/claude-code", "openai/codex", "cursor/agent"}
	}
	if len(cfg.KnownAgentExecutables) == 0 {
		cfg.KnownAgentExecutables = []string{"claude", "codex", "cursor", "mcp-server", "mcp-gateway"}
	}
	if len(cfg.ImageRegistryAllowlist) == 0 {
		cfg.ImageRegistryAllowlist = []string{"anthropic", "openai", "cursor", "ghcr.io/cordum"}
	}
	if len(cfg.MCPPortNames) == 0 {
		cfg.MCPPortNames = []string{"mcp", "mcp-stdio", "mcp-sse", "mcp-http"}
	}

	detector := k8s.NewDetector(cfg, client, store, observer, nil /* default resolver */)
	detector.SetClock(func() time.Time { return clock })

	return &detectorFixture{
		detector: detector,
		store:    store,
		client:   client,
		observer: spy,
		mr:       mr,
		clock:    clock,
	}
}

type lockedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// listAll fetches every persisted finding for the given tenant; intended
// as a low-overhead read-side assertion helper for tests that just want
// to know what landed in the store.
func (f *detectorFixture) listAll(t *testing.T, tenant string) []shadow.ShadowAgentFinding {
	t.Helper()
	page, err := f.store.ListFindings(context.Background(), shadow.ListFindingsQuery{
		TenantID:           tenant,
		Limit:              100,
		IncludeManagedSkip: true,
	})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	return page.Findings
}

// writeActions returns every fake.Action whose verb mutates state. The
// observe-mode contract is that this list MUST stay empty for the entire
// life of the detector.
func (f *detectorFixture) writeActions() []k8stesting.Action {
	var out []k8stesting.Action
	for _, a := range f.client.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			out = append(out, a)
		}
	}
	return out
}

func TestK8sDetector_ObserveMode_NoMutation(t *testing.T) {
	pod := podWith("foo", "default", "anthropic/claude-code:v1", map[string]string{
		testTenantLabel: testTenantA,
	}, nil)
	ns := nsWith("default", map[string]string{testTenantLabel: testTenantA})
	svc := mcpSvc("mcp-server", "default", "mcp", nil)
	f := newFixture(t, k8s.Config{}, pod, ns, svc)

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := f.writeActions(); len(got) > 0 {
		t.Fatalf("observe mode produced %d write actions; want 0. first=%v", len(got), got[0])
	}
}

func TestK8sDetector_Emit_TypedFields(t *testing.T) {
	// Untrusted agent image — single emit, easiest unambiguous trigger.
	pod := podWith("agent-pod", "agents", "evil.example.com/claude-agent:latest",
		map[string]string{testTenantLabel: testTenantA}, nil)
	// Real K8s UIDs are always 36-char UUIDs; use one so validateShadowExtensions
	// passes its 36-byte cap on pod_uid.
	pod.UID = types.UID("12345678-aaaa-bbbb-cccc-deadbeefcafe")
	ns := nsWith("agents", map[string]string{testTenantLabel: testTenantA})
	f := newFixture(t, k8s.Config{}, pod, ns)

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	if len(findings) == 0 {
		t.Fatalf("expected at least 1 finding for untrusted image; got 0")
	}
	got := findings[0]
	if got.SourceType != shadow.SourceTypeKubernetes {
		t.Errorf("SourceType = %q, want %q", got.SourceType, shadow.SourceTypeKubernetes)
	}
	if got.ClusterID != testClusterID {
		t.Errorf("ClusterID = %q, want %q", got.ClusterID, testClusterID)
	}
	if got.Namespace != "agents" {
		t.Errorf("Namespace = %q, want %q", got.Namespace, "agents")
	}
	if got.WorkloadKind != "Pod" {
		t.Errorf("WorkloadKind = %q, want %q", got.WorkloadKind, "Pod")
	}
	if got.WorkloadName != "agent-pod" {
		t.Errorf("WorkloadName = %q, want %q", got.WorkloadName, "agent-pod")
	}
	if got.PodUID != string(pod.UID) {
		t.Errorf("PodUID = %q, want %q", got.PodUID, pod.UID)
	}
	if len(got.SignalSet) == 0 {
		t.Errorf("SignalSet empty; want at least one signal entry")
	}
	if got.RetentionClass != shadow.ShadowRetentionDefault {
		t.Errorf("RetentionClass = %q, want %q", got.RetentionClass, shadow.ShadowRetentionDefault)
	}
	if got.TenantSource == "" {
		t.Errorf("TenantSource empty; want a §6.1 source")
	}
	// §7.2: hostname MUST be the cluster-id, not the host that ran the
	// detector. Defends against silent regressions that would attribute
	// findings to the detector pod's hostname.
	if got.Hostname != testClusterID {
		t.Errorf("Hostname = %q, want %q (§7.2: hostname = cluster-id)", got.Hostname, testClusterID)
	}
	// §7.2: redacted_path = "k8s://<cluster>/<ns>/<kind>/<name>[/<pod>]".
	// For a Pod-kind candidate the pod-name suffix is implicit in
	// WorkloadName, so the path has 4 segments.
	wantPath := "k8s://" + testClusterID + "/agents/Pod/agent-pod"
	if got.RedactedPath != wantPath {
		t.Errorf("RedactedPath = %q, want %q (§7.2 stable identifier)", got.RedactedPath, wantPath)
	}
	if !strings.HasPrefix(got.RedactedPath, "k8s://") {
		t.Errorf("RedactedPath = %q, want k8s:// scheme", got.RedactedPath)
	}
}

func TestK8sDetector_Observability(t *testing.T) {
	pod := podWith("agent-pod", "agents", "evil.example.com/claude-agent:latest",
		map[string]string{testTenantLabel: testTenantA}, nil)
	ns := nsWith("agents", map[string]string{testTenantLabel: testTenantA})
	f := newFixture(t, k8s.Config{}, pod, ns)

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(f.observer.emits) == 0 {
		t.Fatalf("Observer.RecordFindingEmit was never called; want ≥1")
	}
	emit := f.observer.emits[0]
	if emit.Signal == "" {
		t.Errorf("emit.Signal empty; want bounded enum from §7.1")
	}
	if emit.Risk == "" {
		t.Errorf("emit.Risk empty; want low|medium|high|critical")
	}
	if len(f.observer.audits) == 0 {
		t.Fatalf("Observer.EmitAudit was never called; want ≥1")
	}
	if got := f.observer.audits[0].Decision; got != "observed" {
		t.Errorf("audit.Decision = %q, want %q", got, "observed")
	}
}

func TestDetector_ScanReleasesLockBeforeEmit(t *testing.T) {
	pod := podWith("agent-pod", "agents", "evil.example.com/claude-agent:latest",
		map[string]string{testTenantLabel: testTenantA}, nil)
	ns := nsWith("agents", map[string]string{testTenantLabel: testTenantA})
	observer := newBlockingObserver()
	f := newFixtureWithObserver(t, k8s.Config{}, observer, pod, ns)

	scanDone := make(chan error, 1)
	go func() { scanDone <- f.detector.Scan(context.Background()) }()

	select {
	case <-observer.entered:
	case err := <-scanDone:
		t.Fatalf("Scan returned before blocking observer emit: %v", err)
	case <-time.After(time.Second):
		t.Fatal("observer emit was not reached")
	}

	setClockDone := make(chan struct{})
	go func() {
		f.detector.SetClock(func() time.Time { return f.clock.Add(time.Minute) })
		close(setClockDone)
	}()

	select {
	case <-setClockDone:
	case <-time.After(250 * time.Millisecond):
		close(observer.release)
		t.Fatal("SetClock blocked while emit I/O was in progress; Scan still holds detector mutex")
	}

	close(observer.release)
	if err := <-scanDone; err != nil {
		t.Fatalf("Scan: %v", err)
	}
}

func TestK8sEmit_ObservabilityOnStoreFailure(t *testing.T) {
	pod := podWith("agent-pod", "agents", "evil.example.com/claude-agent:latest",
		map[string]string{testTenantLabel: testTenantA}, nil)
	ns := nsWith("agents", map[string]string{testTenantLabel: testTenantA})
	f := newFixture(t, k8s.Config{}, pod, ns)
	f.mr.Close()

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan should continue in observe mode despite store failure: %v", err)
	}
	if len(f.observer.emits) == 0 {
		t.Fatalf("store failure produced no metric; want RecordFindingEmit for bounded signal/risk labels")
	}
	if len(f.observer.audits) == 0 {
		t.Fatalf("store failure produced no audit event")
	}
	got := f.observer.audits[0]
	if got.Action != "shadow_agent.observe_failed" || got.Decision != "error" {
		t.Fatalf("failure audit = action %q decision %q, want shadow_agent.observe_failed/error", got.Action, got.Decision)
	}
	if got.Extra["signal"] == "" || got.Extra["source_type"] != shadow.SourceTypeKubernetes {
		t.Fatalf("failure audit missing bounded metadata: %+v", got.Extra)
	}
}

func TestDetectorRun_ScanErrorEmitsLog(t *testing.T) {
	var logs lockedLogBuffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	f := newFixture(t, k8s.Config{ScanInterval: time.Hour})
	scanErr := errors.New("k8s api auth lapse")
	f.client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, scanErr
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.detector.Run(ctx) }()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case err := <-done:
			t.Fatalf("Run returned before emitting scan-error log: err=%v logs=%q", err, logs.String())
		case <-ticker.C:
			if strings.Contains(logs.String(), "k8s detector: scan error") &&
				strings.Contains(logs.String(), scanErr.Error()) {
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("Run after cancel = %v, want context.Canceled", err)
				}
				return
			}
		case <-timeout:
			cancel()
			<-done
			t.Fatalf("scan error was not logged; logs=%q", logs.String())
		}
	}
}

func TestK8sDetector_EphemeralIndicator_NeverAutoPromoted(t *testing.T) {
	// §14: ephemeral signals MUST NOT auto-promote without corroboration.
	pod := podWith("ephem-pod", "default", "anthropic/claude-code:v1",
		map[string]string{testTenantLabel: testTenantA}, nil)
	ns := nsWith("default", map[string]string{testTenantLabel: testTenantA})
	f := newFixture(t, k8s.Config{}, pod, ns)

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan 1: %v", err)
	}
	// Delete the pod from the fake clientset to simulate disappearance.
	if err := f.client.CoreV1().Pods("default").Delete(context.Background(), "ephem-pod", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete pod: %v", err)
	}
	// Clear the audit trail of the delete (which the test setup performed —
	// not the detector). Then scan again; ephemeral diff should NOT emit
	// a standalone finding.
	priorEmits := len(f.observer.emits)
	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan 2: %v", err)
	}
	for _, e := range f.observer.emits[priorEmits:] {
		if e.Signal == "k8s_ephemeral_indicator" {
			t.Fatalf("ephemeral indicator emitted without corroboration: %+v", e)
		}
	}
}

// --- spy observer ---

type emitCall struct {
	Signal string
	Risk   string
}

type spyObserver struct {
	emits  []emitCall
	audits []audit.SIEMEvent
}

func newSpyObserver() *spyObserver { return &spyObserver{} }

func (s *spyObserver) RecordFindingEmit(signal, risk string) {
	s.emits = append(s.emits, emitCall{Signal: signal, Risk: risk})
}

func (s *spyObserver) EmitAudit(event audit.SIEMEvent) {
	s.audits = append(s.audits, event)
}

type blockingObserver struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingObserver() *blockingObserver {
	return &blockingObserver{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *blockingObserver) RecordFindingEmit(string, string) {}

func (b *blockingObserver) EmitAudit(audit.SIEMEvent) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
}

// --- fixture builders ---

func podWith(name, ns, image string, labels, annotations map[string]string) *corev1.Pod {
	if labels == nil {
		labels = map[string]string{}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns,
			Labels: labels, Annotations: annotations,
			UID: types.UID("uid-" + name),
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "default",
			Containers: []corev1.Container{{
				Name:  "main",
				Image: image,
			}},
		},
	}
}

func nsWith(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
	}
}

func mcpSvc(name, ns, portName string, labels map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Name: portName, Port: 8080}},
		},
	}
}

func saWith(name, ns string, annotations map[string]string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Annotations: annotations},
	}
}

// containsAny returns the first canary substring that appears in s, or "".
func containsAny(s string, canaries []string) string {
	for _, c := range canaries {
		if strings.Contains(s, c) {
			return c
		}
	}
	return ""
}
