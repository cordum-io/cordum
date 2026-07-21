package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// recordingReporter wraps the test reporter to capture every payload sent and
// optionally fail sends.
type recordingServer struct {
	server   *httptest.Server
	mu       sync.Mutex
	payloads []TelemetryPayload
	hits     int32
	failNext atomic.Bool
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rs := &recordingServer{}
	rs.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&rs.hits, 1)
		if rs.failNext.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var p TelemetryPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		rs.mu.Lock()
		rs.payloads = append(rs.payloads, p)
		rs.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(rs.server.Close)
	return rs
}

func (rs *recordingServer) reporter() *Reporter {
	r := NewReporter(rs.server.URL, rs.server.Client())
	r.baseBackoff = 1
	return r
}

func (rs *recordingServer) events() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]string, 0, len(rs.payloads))
	for _, p := range rs.payloads {
		out = append(out, p.Event)
	}
	return out
}

func (rs *recordingServer) countEvent(event string) int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	n := 0
	for _, p := range rs.payloads {
		if p.Event == event {
			n++
		}
	}
	return n
}

func (rs *recordingServer) lastEvent(event string) (TelemetryPayload, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for i := len(rs.payloads) - 1; i >= 0; i-- {
		if rs.payloads[i].Event == event {
			return rs.payloads[i], true
		}
	}
	return TelemetryPayload{}, false
}

// withReporter swaps in a recording reporter on an existing env's collector.
func (env *telemetryTestEnv) withReporter(r *Reporter) {
	env.collector.reporter = r
}

func TestInstallPingFiresExactlyOnce(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeLocalOnly)
	rs := newRecordingServer(t)
	env.withReporter(rs.reporter())
	ctx := context.Background()

	env.collector.maybeSendInstallPing(ctx)
	env.collector.maybeSendInstallPing(ctx)
	env.collector.maybeSendInstallPing(ctx)

	if got := rs.countEvent(EventInstall); got != 1 {
		t.Fatalf("install ping count = %d, want 1; events=%v", got, rs.events())
	}
	ok, err := env.collector.redisClient().Exists(ctx, installPingedKey).Result()
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if ok != 1 {
		t.Fatalf("install_pinged flag not set: %d", ok)
	}
}

func TestInstallPingMinimalPayload(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeAnonymous)
	rs := newRecordingServer(t)
	env.withReporter(rs.reporter())

	env.collector.maybeSendInstallPing(context.Background())

	p, ok := rs.lastEvent(EventInstall)
	if !ok {
		t.Fatalf("no install payload captured: %v", rs.events())
	}
	if p.Event != EventInstall {
		t.Fatalf("event = %q, want %q", p.Event, EventInstall)
	}
	if p.InstallID == "" {
		t.Fatal("install id empty")
	}
	if p.Tier != "team" {
		t.Fatalf("tier = %q, want team", p.Tier)
	}
	if p.OS == "" || p.Arch == "" {
		t.Fatalf("os/arch missing: os=%q arch=%q", p.OS, p.Arch)
	}
	// Minimal beacon must not carry usage/engagement/feature detail.
	if p.Usage.JobsLast24h != 0 || p.Usage.ActiveJobs != 0 || p.Workers.Registered != 0 {
		t.Fatalf("minimal beacon carried usage/workers: %+v %+v", p.Usage, p.Workers)
	}
	if len(p.FeaturesEnabled) != 0 {
		t.Fatalf("minimal beacon carried features: %+v", p.FeaturesEnabled)
	}
	if p.Engagement != (EngagementSignals{}) {
		t.Fatalf("minimal beacon carried engagement: %+v", p.Engagement)
	}
}

func TestInstallPingNotWhenOff(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeOff)
	rs := newRecordingServer(t)
	env.withReporter(rs.reporter())
	ctx := context.Background()

	env.collector.maybeSendInstallPing(ctx)

	if got := atomic.LoadInt32(&rs.hits); got != 0 {
		t.Fatalf("expected no send when off, got %d hits", got)
	}
	ok, _ := env.collector.redisClient().Exists(ctx, installPingedKey).Result()
	if ok != 0 {
		t.Fatalf("install_pinged flag set while off")
	}
}

func TestInstallPingFailedSendLeavesFlagUnset(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeLocalOnly)
	rs := newRecordingServer(t)
	rs.failNext.Store(true)
	env.withReporter(rs.reporter())
	ctx := context.Background()

	env.collector.maybeSendInstallPing(ctx)

	ok, _ := env.collector.redisClient().Exists(ctx, installPingedKey).Result()
	if ok != 0 {
		t.Fatalf("flag set despite failed send")
	}

	// Now allow sends to succeed; retry should fire and set the flag.
	rs.failNext.Store(false)
	env.collector.maybeSendInstallPing(ctx)
	if got := rs.countEvent(EventInstall); got != 1 {
		t.Fatalf("retry install ping count = %d, want 1", got)
	}
	ok, _ = env.collector.redisClient().Exists(ctx, installPingedKey).Result()
	if ok != 1 {
		t.Fatalf("flag not set after successful retry")
	}
}

func TestInstallPingConcurrentCollectorsSendOnce(t *testing.T) {
	// Two collectors sharing the same Redis must converge: at most one install
	// ping is sent (SetNX on the shared flag).
	env := newTelemetryTestEnv(t, ModeLocalOnly)
	rs := newRecordingServer(t)
	env.withReporter(rs.reporter())

	// Second collector on the same store/redis.
	c2 := NewCollector(CollectorOptions{
		Mode:              ModeLocalOnly,
		Store:             env.store,
		Reporter:          rs.reporter(),
		TierProvider:      func() string { return "team" },
		WorkerCredentials: env.workers,
		ConfigSvc:         env.configSvc,
		JobStore:          env.jobStore,
		WorkflowStore:     env.workflow,
		TenantID:          "default",
	})

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); env.collector.maybeSendInstallPing(ctx) }()
		go func() { defer wg.Done(); c2.maybeSendInstallPing(ctx) }()
	}
	wg.Wait()

	if got := rs.countEvent(EventInstall); got != 1 {
		t.Fatalf("concurrent install ping count = %d, want 1", got)
	}
}

func TestFirstUsePingRequiresUsedMarker(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeLocalOnly)
	rs := newRecordingServer(t)
	env.withReporter(rs.reporter())
	ctx := context.Background()

	// No used marker yet -> nothing fires.
	env.collector.maybeSendFirstUsePing(ctx)
	if got := rs.countEvent(EventFirstUse); got != 0 {
		t.Fatalf("first-use fired without used marker: %d", got)
	}

	// Set used marker -> fires once.
	if err := MarkUsed(ctx, env.collector.redisClient()); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	env.collector.maybeSendFirstUsePing(ctx)
	env.collector.maybeSendFirstUsePing(ctx)
	if got := rs.countEvent(EventFirstUse); got != 1 {
		t.Fatalf("first-use ping count = %d, want 1", got)
	}
}

func TestFirstUsePingFullerPayload(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeAnonymous)
	rs := newRecordingServer(t)
	env.withReporter(rs.reporter())
	ctx := context.Background()

	if err := MarkUsed(ctx, env.collector.redisClient()); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	env.collector.maybeSendFirstUsePing(ctx)

	p, ok := rs.lastEvent(EventFirstUse)
	if !ok {
		t.Fatalf("no first-use payload: %v", rs.events())
	}
	if p.Event != EventFirstUse {
		t.Fatalf("event = %q, want %q", p.Event, EventFirstUse)
	}
	if p.OS == "" || p.Arch == "" {
		t.Fatalf("os/arch missing")
	}
	if p.Workers.Registered != 1 || p.Workers.Connected != 1 {
		t.Fatalf("fuller beacon worker counts wrong: %+v", p.Workers)
	}
	if p.Usage.JobsLast24h < 2 {
		t.Fatalf("fuller beacon jobs_24h = %d, want >=2", p.Usage.JobsLast24h)
	}
	if p.Engagement.PacksInstalled != 1 {
		t.Fatalf("fuller beacon packs_installed = %d, want 1", p.Engagement.PacksInstalled)
	}
	// Fuller beacon still omits per-feature detail.
	if len(p.FeaturesEnabled) != 0 {
		t.Fatalf("fuller beacon carried features: %+v", p.FeaturesEnabled)
	}
}

func TestFirstUsePingNotWhenOff(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeOff)
	rs := newRecordingServer(t)
	env.withReporter(rs.reporter())
	ctx := context.Background()

	if err := MarkUsed(ctx, env.collector.redisClient()); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	env.collector.maybeSendFirstUsePing(ctx)
	if got := atomic.LoadInt32(&rs.hits); got != 0 {
		t.Fatalf("first-use fired while off: %d hits", got)
	}
	ok, _ := env.collector.redisClient().Exists(ctx, firstUsePingedKey).Result()
	if ok != 0 {
		t.Fatalf("firstuse_pinged flag set while off")
	}
}

func TestFirstUsePingFailedSendLeavesFlagUnset(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeLocalOnly)
	rs := newRecordingServer(t)
	rs.failNext.Store(true)
	env.withReporter(rs.reporter())
	ctx := context.Background()

	if err := MarkUsed(ctx, env.collector.redisClient()); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	env.collector.maybeSendFirstUsePing(ctx)
	ok, _ := env.collector.redisClient().Exists(ctx, firstUsePingedKey).Result()
	if ok != 0 {
		t.Fatalf("flag set despite failed send")
	}

	rs.failNext.Store(false)
	env.collector.maybeSendFirstUsePing(ctx)
	if got := rs.countEvent(EventFirstUse); got != 1 {
		t.Fatalf("retry first-use count = %d, want 1", got)
	}
	ok, _ = env.collector.redisClient().Exists(ctx, firstUsePingedKey).Result()
	if ok != 1 {
		t.Fatalf("flag not set after successful retry")
	}
}

func TestMarkUsedIdempotent(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeLocalOnly)
	client := env.collector.redisClient()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := MarkUsed(ctx, client); err != nil {
			t.Fatalf("mark used: %v", err)
		}
	}
	val, err := client.Get(ctx, UsedMarkerKey).Result()
	if err != nil {
		t.Fatalf("get used marker: %v", err)
	}
	if val != "1" {
		t.Fatalf("used marker = %q, want 1", val)
	}
}

func TestPeriodicReportEventLabel(t *testing.T) {
	// Ongoing anonymous reports must carry event=periodic.
	env := newTelemetryTestEnv(t, ModeAnonymous)
	rs := newRecordingServer(t)
	env.withReporter(rs.reporter())
	ctx := context.Background()

	if _, err := env.collector.CollectNow(ctx); err != nil {
		t.Fatalf("CollectNow: %v", err)
	}
	if err := env.collector.ReportNow(ctx); err != nil {
		t.Fatalf("ReportNow: %v", err)
	}
	if got := rs.countEvent(EventPeriodic); got != 1 {
		t.Fatalf("periodic event count = %d, want 1; events=%v", got, rs.events())
	}
}

func TestOngoingReportsOnlyWhenAnonymous(t *testing.T) {
	// local_only must NOT produce ongoing periodic reports.
	env := newTelemetryTestEnv(t, ModeLocalOnly)
	rs := newRecordingServer(t)
	env.withReporter(rs.reporter())
	ctx := context.Background()

	if _, err := env.collector.CollectNow(ctx); err != nil {
		t.Fatalf("CollectNow: %v", err)
	}
	// ReportNow is gated on ReportingEnabled (anonymous only) -> no-op here.
	if err := env.collector.ReportNow(ctx); err != nil {
		t.Fatalf("ReportNow: %v", err)
	}
	if got := rs.countEvent(EventPeriodic); got != 0 {
		t.Fatalf("local_only produced periodic reports: %d", got)
	}
}

func TestSetModeOffStopsPings(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeLocalOnly)
	rs := newRecordingServer(t)
	env.withReporter(rs.reporter())
	ctx := context.Background()

	env.collector.SetMode(ModeOff)
	if err := MarkUsed(ctx, env.collector.redisClient()); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	env.collector.maybeSendInstallPing(ctx)
	env.collector.maybeSendFirstUsePing(ctx)
	if got := atomic.LoadInt32(&rs.hits); got != 0 {
		t.Fatalf("pings fired after SetMode(off): %d", got)
	}

	// Re-enabling lets the unfired pings fire under their rules.
	env.collector.SetMode(ModeAnonymous)
	env.collector.maybeSendInstallPing(ctx)
	env.collector.maybeSendFirstUsePing(ctx)
	if rs.countEvent(EventInstall) != 1 || rs.countEvent(EventFirstUse) != 1 {
		t.Fatalf("pings did not fire after re-enable: events=%v", rs.events())
	}
}

func TestBeaconBuildersDefaultsAndContent(t *testing.T) {
	install := NewInstallBeacon("iid", ModeLocalOnly, "1.2.3", "team")
	if install.Event != EventInstall {
		t.Fatalf("install event = %q", install.Event)
	}
	if install.OS == "" || install.Arch == "" {
		t.Fatalf("install beacon missing platform")
	}
	if install.CollectedAt.IsZero() {
		t.Fatalf("install beacon missing collected_at")
	}

	first := NewFirstUseBeacon("iid", ModeAnonymous, "1.2.3", "team", 3, 2, 10, 4, 7)
	if first.Event != EventFirstUse {
		t.Fatalf("first event = %q", first.Event)
	}
	if first.Workers.Registered != 3 || first.Workers.Connected != 2 {
		t.Fatalf("first workers = %+v", first.Workers)
	}
	if first.Usage.JobsLast24h != 10 || first.Usage.WorkflowRunsLast24h != 4 {
		t.Fatalf("first usage = %+v", first.Usage)
	}
	if first.Engagement.PacksInstalled != 7 {
		t.Fatalf("first packs = %d", first.Engagement.PacksInstalled)
	}

	// Empty builder defaults Event to periodic (backward compat).
	def := NewPayloadBuilder().Build()
	if def.Event != EventPeriodic {
		t.Fatalf("default event = %q, want periodic", def.Event)
	}
}
