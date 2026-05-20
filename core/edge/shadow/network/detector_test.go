package network_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow"
	"github.com/cordum/cordum/core/edge/shadow/network"
)

const (
	testTenantA       = "tenant-a"
	testQuarantineTen = "cordum.shadow.quarantine"
	testSourceID      = "network-detector-test"
)

// spyObserver captures observer calls so tests can assert exactly which
// metric/audit emissions the detector produced without coupling to a
// prometheus registry.
type spyObserver struct {
	mu             sync.Mutex
	findingEmits   []findingEmitCall
	auditEvents    []audit.SIEMEvent
	piiModeActive  string
	logRecordCalls []logRecordCall
}

type findingEmitCall struct {
	Signal       string
	Risk         string
	IngestSource string
}

type logRecordCall struct {
	IngestSource string
	Result       string
}

func (s *spyObserver) RecordFindingEmit(signal, risk, ingestSource string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.findingEmits = append(s.findingEmits, findingEmitCall{signal, risk, ingestSource})
}

func (s *spyObserver) RecordPIIModeActive(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.piiModeActive = mode
}

func (s *spyObserver) RecordLogRecord(ingestSource, result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logRecordCalls = append(s.logRecordCalls, logRecordCall{ingestSource, result})
}

func (s *spyObserver) EmitAudit(event audit.SIEMEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditEvents = append(s.auditEvents, event)
}

func (s *spyObserver) emits() []findingEmitCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]findingEmitCall, len(s.findingEmits))
	copy(out, s.findingEmits)
	return out
}

func (s *spyObserver) audits() []audit.SIEMEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]audit.SIEMEvent, len(s.auditEvents))
	copy(out, s.auditEvents)
	return out
}

func (s *spyObserver) pii() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.piiModeActive
}

func (s *spyObserver) logCalls() []logRecordCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]logRecordCall, len(s.logRecordCalls))
	copy(out, s.logRecordCalls)
	return out
}

type detectorFixture struct {
	detector *network.Detector
	store    shadow.Store
	observer *spyObserver
	mr       *miniredis.Miniredis
	clock    time.Time
}

func newFixture(t *testing.T, cfg network.Config) *detectorFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
	t.Cleanup(func() { _ = rdb.Close(); mr.Close() })

	clock := time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC)
	store, err := shadow.NewRedisStore(rdb, shadow.WithClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("warm redis client: %v", err)
	}

	observer := &spyObserver{}
	if cfg.SourceID == "" {
		cfg.SourceID = testSourceID
	}
	if cfg.QuarantineTenantID == "" {
		cfg.QuarantineTenantID = testQuarantineTen
	}
	if cfg.HeartbeatStaleThreshold == 0 {
		cfg.HeartbeatStaleThreshold = 5 * time.Minute
	}
	if len(cfg.ProviderHostnames) == 0 {
		cfg.ProviderHostnames = network.DefaultProviderHostnames()
	}

	det, err := network.NewDetector(cfg, store, observer, nil, nil)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}
	det.SetClock(func() time.Time { return clock })

	return &detectorFixture{
		detector: det,
		store:    store,
		observer: observer,
		mr:       mr,
		clock:    clock,
	}
}

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

func TestNetworkDetector_Ingest_FileSource(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "egress.log")
	body := strings.Join([]string{
		"2026-05-17T16:00:00Z hostname=api.anthropic.com workload=runner-prod-eu1 oidc_sub=repo:cordum-io/cordum:ref:main endpoint=H7f8a9b count=42",
		"2026-05-17T16:00:05Z hostname=api.openai.com workload=runner-prod-eu1 oidc_sub=repo:cordum-io/cordum:ref:main endpoint=H1122ab count=3",
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	fx := newFixture(t, network.Config{
		WorkloadTenantMap: map[string]string{"runner-prod-eu1": testTenantA},
		KnownAttachWorkloadIDs: map[string]time.Time{
			"runner-prod-eu1": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
	})

	ing, err := network.NewFileIngestor(logPath)
	if err != nil {
		t.Fatalf("NewFileIngestor: %v", err)
	}
	if err := fx.detector.IngestFrom(context.Background(), ing); err != nil {
		t.Fatalf("IngestFrom: %v", err)
	}

	findings := fx.listAll(t, testTenantA)
	if len(findings) == 0 {
		t.Fatalf("expected findings persisted from file ingestor, got 0")
	}
	wantHostnames := map[string]struct{}{"api.anthropic.com": {}, "api.openai.com": {}}
	for _, f := range findings {
		if _, ok := wantHostnames[f.Hostname]; !ok {
			t.Errorf("unexpected hostname on finding: %q", f.Hostname)
		}
		if f.SourceType != shadow.SourceTypeNetwork {
			t.Errorf("source_type=%q, want %q", f.SourceType, shadow.SourceTypeNetwork)
		}
	}
}

func TestNetworkDetector_Ingest_SyslogSource(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	_ = conn.Close()

	endpoint := fmt.Sprintf("udp://127.0.0.1:%d", port)
	fx := newFixture(t, network.Config{
		WorkloadTenantMap: map[string]string{"runner-syslog-1": testTenantA},
		KnownAttachWorkloadIDs: map[string]time.Time{
			"runner-syslog-1": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
	})

	ing, err := network.NewSyslogIngestor(endpoint)
	if err != nil {
		t.Fatalf("NewSyslogIngestor: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fx.detector.IngestFrom(ctx, ing) }()

	time.Sleep(50 * time.Millisecond)
	client, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	msg := "<134>1 2026-05-17T16:00:00Z host - - - - hostname=api.anthropic.com workload=runner-syslog-1 oidc_sub=repo:o/r:ref:main endpoint=Habcd12 count=7"
	if _, err := client.Write([]byte(msg)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = client.Close()

	deadline := time.After(2 * time.Second)
	for {
		findings := fx.listAll(t, testTenantA)
		if len(findings) >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("syslog ingestor did not emit finding within deadline")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

func TestNetworkDetector_Ingest_StdinStream(t *testing.T) {
	pr, pw := io.Pipe()
	fx := newFixture(t, network.Config{
		WorkloadTenantMap: map[string]string{"runner-stdin-1": testTenantA},
		KnownAttachWorkloadIDs: map[string]time.Time{
			"runner-stdin-1": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
	})

	ing := network.NewStdinIngestor(pr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fx.detector.IngestFrom(ctx, ing) }()

	line := "2026-05-17T16:00:00Z hostname=generativelanguage.googleapis.com workload=runner-stdin-1 oidc_sub=repo:o/r:ref:main endpoint=Hcafe01 count=11\n"
	if _, err := pw.Write([]byte(line)); err != nil {
		t.Fatalf("pipe write: %v", err)
	}
	_ = pw.Close()

	deadline := time.After(2 * time.Second)
	for {
		findings := fx.listAll(t, testTenantA)
		if len(findings) >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("stdin ingestor did not emit finding within deadline")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

func TestNetworkDetector_Ingest_StdinCancelBlockedReader(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	fx := newFixture(t, network.Config{})
	ing := network.NewStdinIngestor(pr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fx.detector.IngestFrom(ctx, ing) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("IngestFrom error = %v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("IngestFrom did not return within 500ms after cancel")
	}
}

func TestNetworkDetector_Ingest_NoCaptureNG7(t *testing.T) {
	// Per NG7 / §9.2 the package MUST NOT perform raw network capture.
	// Walk the package source and assert no banned APIs appear.
	pkgDir := filepath.Join("..", "..", "..", "edge", "shadow", "network")
	// Resolve to absolute via test cwd.
	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	_ = pkgDir
	_ = abs

	banned := []string{
		"pcap.",
		"gopacket",
		"afpacket",
		"net.ListenPacket",
		"syscall.SOCK_RAW",
		"crypto/tls\".Server",
		"crypto/tls.NewListener",
		"x/sys/unix.SOCK_RAW",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", name, err)
		}
		for _, b := range banned {
			if strings.Contains(string(data), b) {
				t.Errorf("file %s contains banned API %q (NG7 violation: package must not capture network traffic)", name, b)
			}
		}
	}
}

func TestNetworkDetector_LawfulMetadata_CatalogEnforced(t *testing.T) {
	// Construct a record with hostile fields outside the §9.1 catalog
	// (RawURL with query string, BearerToken, RemoteIP). After
	// processing, the persisted finding MUST contain NONE of those
	// values verbatim — neither in EvidenceSummary nor in any other
	// string field.
	fx := newFixture(t, network.Config{
		WorkloadTenantMap: map[string]string{"runner-9": testTenantA},
		KnownAttachWorkloadIDs: map[string]time.Time{
			"runner-9": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
	})

	bannedURL := "https://api.anthropic.com/v1/messages?api_key=sk-test-abcdef0123456789abcdef"
	bannedBearer := "Bearer abcdef0123456789secrettoken"
	bannedIP := "203.0.113.42"

	rec := network.LogRecord{
		Timestamp:    fx.clock,
		Hostname:     "api.anthropic.com",
		WorkloadID:   "runner-9",
		OIDCSub:      "repo:o/r:ref:main",
		EndpointHash: "Habcdef0123",
		Count:        42,
		RawURL:       bannedURL,
		BearerToken:  bannedBearer,
		RemoteIP:     bannedIP,
	}

	if err := fx.detector.ProcessRecord(context.Background(), rec, "test"); err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}

	findings := fx.listAll(t, testTenantA)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]

	asJSON := fmt.Sprintf("%+v", f)
	for _, leak := range []string{bannedURL, bannedBearer, bannedIP, "api_key", "sk-ant", "203.0.113"} {
		if strings.Contains(asJSON, leak) {
			t.Errorf("finding contains banned token %q (catalog enforcement failed): %s", leak, asJSON)
		}
	}
	if f.Hostname != "api.anthropic.com" {
		t.Errorf("hostname=%q, want api.anthropic.com", f.Hostname)
	}
}

func TestNetworkDetector_PIIMode_Pseudonymize(t *testing.T) {
	fx := newFixture(t, network.Config{
		PIIMode:           network.PIIModePseudonymize,
		WorkloadTenantMap: map[string]string{"github-actor": testTenantA},
		KnownAttachWorkloadIDs: map[string]time.Time{
			"github-actor": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
	})

	rec := network.LogRecord{
		Timestamp:    fx.clock,
		Hostname:     "api.anthropic.com",
		WorkloadID:   "github-actor",
		EndpointHash: "Hfoo123",
		Count:        1,
	}
	if err := fx.detector.ProcessRecord(context.Background(), rec, "test"); err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}
	findings := fx.listAll(t, testTenantA)
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	pid := findings[0].PrincipalID
	if !strings.HasPrefix(pid, "git") {
		t.Errorf("pseudonymize: principal_id=%q, want prefix 'git'", pid)
	}
	if len(pid) != 3+8 {
		t.Errorf("pseudonymize: principal_id=%q has len %d, want 11 (3-char-prefix + 8-char-hash)", pid, len(pid))
	}
	wantHash := sha256.Sum256([]byte("github-actor"))
	wantSuffix := hex.EncodeToString(wantHash[:])[:8]
	if !strings.HasSuffix(pid, wantSuffix) {
		t.Errorf("pseudonymize: principal_id=%q hash suffix=%q, want %q", pid, pid[3:], wantSuffix)
	}
}

func TestNetworkDetector_PIIMode_Hash(t *testing.T) {
	fx := newFixture(t, network.Config{
		PIIMode:           network.PIIModeHash,
		WorkloadTenantMap: map[string]string{"github-actor": testTenantA},
		KnownAttachWorkloadIDs: map[string]time.Time{
			"github-actor": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
	})
	rec := network.LogRecord{
		Timestamp:    fx.clock,
		Hostname:     "api.anthropic.com",
		WorkloadID:   "github-actor",
		EndpointHash: "Hxx",
		Count:        1,
	}
	if err := fx.detector.ProcessRecord(context.Background(), rec, "test"); err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}
	findings := fx.listAll(t, testTenantA)
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	pid := findings[0].PrincipalID
	wantHash := sha256.Sum256([]byte("github-actor"))
	wantPID := hex.EncodeToString(wantHash[:])[:16]
	if pid != wantPID {
		t.Errorf("hash mode: principal_id=%q, want %q (sha256 prefix 16)", pid, wantPID)
	}
	if strings.Contains(pid, "github-actor") {
		t.Errorf("hash mode: principal_id leaks raw value %q", pid)
	}
}

func TestNetworkDetector_PIIMode_Drop(t *testing.T) {
	fx := newFixture(t, network.Config{
		PIIMode:           network.PIIModeDrop,
		WorkloadTenantMap: map[string]string{"github-actor": testTenantA},
		KnownAttachWorkloadIDs: map[string]time.Time{
			"github-actor": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
	})
	rec := network.LogRecord{
		Timestamp:    fx.clock,
		Hostname:     "api.anthropic.com",
		WorkloadID:   "github-actor",
		EndpointHash: "Hxx",
		Count:        1,
	}
	if err := fx.detector.ProcessRecord(context.Background(), rec, "test"); err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}
	findings := fx.listAll(t, testTenantA)
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	pid := findings[0].PrincipalID
	if pid != network.DroppedPrincipalSentinel {
		t.Errorf("drop mode: principal_id=%q, want sentinel %q", pid, network.DroppedPrincipalSentinel)
	}
	if strings.Contains(pid, "github-actor") {
		t.Errorf("drop mode: principal_id leaks raw value %q", pid)
	}
}

func TestNetworkDetector_RiskClassification_NoAttach(t *testing.T) {
	// Workload has no Cordum Edge attach record => risk=medium.
	fx := newFixture(t, network.Config{
		WorkloadTenantMap: map[string]string{"unattached-runner": testTenantA},
	})
	rec := network.LogRecord{
		Timestamp:    fx.clock,
		Hostname:     "api.anthropic.com",
		WorkloadID:   "unattached-runner",
		EndpointHash: "Hna",
		Count:        1,
	}
	if err := fx.detector.ProcessRecord(context.Background(), rec, "test"); err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}
	findings := fx.listAll(t, testTenantA)
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	if findings[0].Risk != shadow.FindingRiskMedium {
		t.Errorf("no-attach: risk=%q, want medium", findings[0].Risk)
	}
}

func TestNetworkDetector_ConfigMapsImmutable(t *testing.T) {
	attachTime := time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC)
	workloadMap := map[string]string{"runner-immutable": testTenantA}
	attachMap := map[string]time.Time{"runner-immutable": attachTime}
	providerMap := network.DefaultProviderHostnames()
	fx := newFixture(t, network.Config{
		ProviderHostnames:       providerMap,
		WorkloadTenantMap:       workloadMap,
		KnownAttachWorkloadIDs:  attachMap,
		HeartbeatStaleThreshold: time.Hour,
	})

	workloadMap["runner-immutable"] = "tenant-mutated"
	delete(attachMap, "runner-immutable")
	providerMap["api.anthropic.com"] = "mutated_category"

	rec := network.LogRecord{
		Timestamp:    fx.clock,
		Hostname:     "api.anthropic.com",
		WorkloadID:   "runner-immutable",
		EndpointHash: "Himmutable",
		Count:        1,
	}
	if err := fx.detector.ProcessRecord(context.Background(), rec, "test"); err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}
	findings := fx.listAll(t, testTenantA)
	if len(findings) != 1 {
		t.Fatalf("want finding in original tenant %q after caller-map mutation, got %d", testTenantA, len(findings))
	}
	if findings[0].Risk != shadow.FindingRiskLow {
		t.Fatalf("risk after caller-map mutation = %q, want low from copied fresh attach map", findings[0].Risk)
	}
	if strings.Contains(findings[0].EvidenceSummary, "mutated_category") {
		t.Fatalf("provider map mutation leaked into evidence summary: %q", findings[0].EvidenceSummary)
	}
}

func TestNetworkDetector_RiskClassification_StaleHeartbeat(t *testing.T) {
	// Workload's last heartbeat is older than the threshold => risk=high.
	staleTime := time.Date(2026, 5, 17, 15, 0, 0, 0, time.UTC) // 1h before clock
	fx := newFixture(t, network.Config{
		WorkloadTenantMap:       map[string]string{"stale-runner": testTenantA},
		KnownAttachWorkloadIDs:  map[string]time.Time{"stale-runner": staleTime},
		HeartbeatStaleThreshold: 5 * time.Minute,
	})
	rec := network.LogRecord{
		Timestamp:    fx.clock,
		Hostname:     "api.openai.com",
		WorkloadID:   "stale-runner",
		EndpointHash: "Hstale",
		Count:        1,
	}
	if err := fx.detector.ProcessRecord(context.Background(), rec, "test"); err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}
	findings := fx.listAll(t, testTenantA)
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	if findings[0].Risk != shadow.FindingRiskHigh {
		t.Errorf("stale-heartbeat: risk=%q, want high", findings[0].Risk)
	}
}

func TestNetworkDetector_RiskClassification_QuarantineTenant(t *testing.T) {
	// Unmapped workload falls to quarantine tenant => risk=high.
	fx := newFixture(t, network.Config{
		KnownAttachWorkloadIDs: map[string]time.Time{
			"orphan": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
	})
	rec := network.LogRecord{
		Timestamp:    fx.clock,
		Hostname:     "api.anthropic.com",
		WorkloadID:   "orphan",
		EndpointHash: "Horp",
		Count:        1,
	}
	if err := fx.detector.ProcessRecord(context.Background(), rec, "test"); err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}
	findings := fx.listAll(t, testQuarantineTen)
	if len(findings) != 1 {
		t.Fatalf("want 1 quarantine finding, got %d", len(findings))
	}
	if findings[0].Risk != shadow.FindingRiskHigh {
		t.Errorf("quarantine: risk=%q, want high", findings[0].Risk)
	}
	if findings[0].TenantSource != network.TenantSourceQuarantine {
		t.Errorf("quarantine: tenant_source=%q, want %q", findings[0].TenantSource, network.TenantSourceQuarantine)
	}
}

func TestNetworkDetector_TenantMapping_WorkloadOIDC(t *testing.T) {
	cases := []struct {
		name       string
		workload   string
		oidcSub    string
		wantTenant string
		wantSource string
	}{
		{"workload-identity wins", "wid-1", "repo:o/r:ref:main", "tenant-from-workload", network.TenantSourceWorkloadIdentity},
		{"oidc fallback when no workload map", "", "repo:o/r:ref:main", "tenant-from-oidc", network.TenantSourceOIDC},
		{"quarantine when neither", "", "", testQuarantineTen, network.TenantSourceQuarantine},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t, network.Config{
				WorkloadTenantMap: map[string]string{"wid-1": "tenant-from-workload"},
				OIDCTenantMap:     map[string]string{"repo:o/r:ref:main": "tenant-from-oidc"},
				KnownAttachWorkloadIDs: map[string]time.Time{
					"wid-1":  time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
					"orphan": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
				},
			})
			rec := network.LogRecord{
				Timestamp:    fx.clock,
				Hostname:     "api.anthropic.com",
				WorkloadID:   c.workload,
				OIDCSub:      c.oidcSub,
				EndpointHash: "Hx",
				Count:        1,
			}
			if err := fx.detector.ProcessRecord(context.Background(), rec, "test"); err != nil {
				t.Fatalf("ProcessRecord: %v", err)
			}
			findings := fx.listAll(t, c.wantTenant)
			if len(findings) != 1 {
				t.Fatalf("%s: want 1 finding in tenant %q, got %d", c.name, c.wantTenant, len(findings))
			}
			if findings[0].TenantSource != c.wantSource {
				t.Errorf("%s: tenant_source=%q, want %q", c.name, findings[0].TenantSource, c.wantSource)
			}
		})
	}
}

func TestNetworkDetector_Emit_TypedFields(t *testing.T) {
	fx := newFixture(t, network.Config{
		WorkloadTenantMap: map[string]string{"runner-typed": testTenantA},
		KnownAttachWorkloadIDs: map[string]time.Time{
			"runner-typed": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
	})
	rec := network.LogRecord{
		Timestamp:    fx.clock,
		Hostname:     "api.anthropic.com",
		WorkloadID:   "runner-typed",
		OIDCSub:      "repo:o/r:ref:main",
		EndpointHash: "Hfields",
		Count:        137,
	}
	if err := fx.detector.ProcessRecord(context.Background(), rec, "file"); err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}
	findings := fx.listAll(t, testTenantA)
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.SourceType != shadow.SourceTypeNetwork {
		t.Errorf("source_type=%q, want %q", f.SourceType, shadow.SourceTypeNetwork)
	}
	if !strings.HasPrefix(f.SourceID, "network:") {
		t.Errorf("source_id=%q, want prefix 'network:'", f.SourceID)
	}
	if f.EvidenceType != "network_direct_provider_traffic" {
		t.Errorf("evidence_type=%q, want network_direct_provider_traffic", f.EvidenceType)
	}
	if f.Hostname != "api.anthropic.com" {
		t.Errorf("hostname=%q, want api.anthropic.com", f.Hostname)
	}
	if !reflect.DeepEqual(f.SignalSet, []string{"direct_provider_traffic"}) {
		t.Errorf("signal_set=%v, want [direct_provider_traffic]", f.SignalSet)
	}
	if f.RetentionClass != shadow.ShadowRetentionDefault {
		t.Errorf("retention_class=%q, want %q", f.RetentionClass, shadow.ShadowRetentionDefault)
	}
	// EvidenceSummary must reference category + count_bucket; never the
	// raw count or any forbidden field.
	if !strings.Contains(f.EvidenceSummary, "anthropic_api") {
		t.Errorf("evidence_summary missing category: %q", f.EvidenceSummary)
	}
	if !strings.Contains(f.EvidenceSummary, "count_bucket=") {
		t.Errorf("evidence_summary missing count_bucket: %q", f.EvidenceSummary)
	}
	if strings.Contains(f.EvidenceSummary, "count=137") {
		t.Errorf("evidence_summary leaks raw count: %q", f.EvidenceSummary)
	}
}

func TestNetworkDetector_Observability(t *testing.T) {
	fx := newFixture(t, network.Config{
		PIIMode:           network.PIIModePseudonymize,
		WorkloadTenantMap: map[string]string{"obs-runner": testTenantA},
		KnownAttachWorkloadIDs: map[string]time.Time{
			"obs-runner": time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
		},
	})
	rec := network.LogRecord{
		Timestamp:    fx.clock,
		Hostname:     "api.anthropic.com",
		WorkloadID:   "obs-runner",
		EndpointHash: "Hobs",
		Count:        1,
	}
	if err := fx.detector.ProcessRecord(context.Background(), rec, "file"); err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}

	emits := fx.observer.emits()
	if len(emits) != 1 {
		t.Fatalf("want 1 RecordFindingEmit, got %d", len(emits))
	}
	if emits[0].Signal != "direct_provider_traffic" {
		t.Errorf("signal=%q, want direct_provider_traffic", emits[0].Signal)
	}
	if emits[0].IngestSource != "file" {
		t.Errorf("ingest_source=%q, want file", emits[0].IngestSource)
	}

	if got := fx.observer.pii(); got != string(network.PIIModePseudonymize) {
		t.Errorf("pii_mode_active=%q, want %q", got, network.PIIModePseudonymize)
	}

	audits := fx.observer.audits()
	if len(audits) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(audits))
	}
	if audits[0].Action != "shadow_agent.observed" {
		t.Errorf("audit action=%q, want shadow_agent.observed", audits[0].Action)
	}
	if audits[0].Extra["source_type"] != shadow.SourceTypeNetwork {
		t.Errorf("audit source_type=%q, want network", audits[0].Extra["source_type"])
	}
	if audits[0].Extra["hostname"] != "api.anthropic.com" {
		t.Errorf("audit hostname=%q, want api.anthropic.com", audits[0].Extra["hostname"])
	}

	logCalls := fx.observer.logCalls()
	if len(logCalls) == 0 {
		t.Errorf("expected at least one RecordLogRecord call for processed record")
	} else if logCalls[0].IngestSource != "file" || logCalls[0].Result != "emitted" {
		t.Errorf("log record call=%+v, want {file, emitted}", logCalls[0])
	}
}
