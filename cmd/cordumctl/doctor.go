package main

// cordumctl doctor — install-verification subcommand.
//
// Runs a sequence of independent checks against a live Cordum deploy
// and reports results in human-readable or JSON form. Exit code is 0
// when every check passes, 1 when any check fails, 2 on usage error.
// Optional --strict promotes warns to fails.
//
// Architecture: direct-probe. cordumctl talks to each service's
// readyz / health endpoint + the gateway's /api/v1/status so doctor
// keeps working even when the gateway is down. No aggregated
// /api/v1/doctor endpoint — gating the diagnostic on the gateway
// would defeat the point.

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cordum/cordum/core/infra/buildinfo"
	sdk "github.com/cordum/cordum/sdk/client"
)

// doctorCLIVersion is the cordumctl build version used for version-skew
// comparisons. Package variable so tests can override it without poking
// the core/infra/buildinfo globals.
var doctorCLIVersion = buildinfo.Version

// checkState is the outcome of a single check.
type checkState string

const (
	stateOK   checkState = "ok"
	stateWarn checkState = "warn"
	stateFail checkState = "fail"
	stateSkip checkState = "skip"
)

// checkResult is the terminal state of one check, formatted for the
// human renderer + the JSON output.
type checkResult struct {
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	State  checkState `json:"state"`
	Detail string     `json:"detail,omitempty"`
	Fix    string     `json:"fix,omitempty"`
}

// doctorCheck bundles a probe with its metadata so the runner can
// present results in a stable order. Named with the `doctor` prefix
// because cordumctl already has a top-level `check(err)` helper.
type doctorCheck struct {
	id    string
	label string
	run   func(ctx context.Context, env *doctorEnv) checkResult
}

// doctorEnv carries the resolved flags + shared probe infrastructure
// so each check function reads a consistent view of the deploy.
type doctorEnv struct {
	gateway     string
	apiKey      string
	tenant      string
	caCert      string
	skipWorkers bool
	serviceURL  map[string]string
	httpClient  *http.Client
	status      *gatewayStatusResponse // lazily populated by the gateway_auth check
}

const (
	defaultDoctorPerCheckTimeout = 5 * time.Second
	defaultDoctorDeadline        = 30 * time.Second
)

// runDoctorCmd wires the `cordumctl doctor` subcommand.
func runDoctorCmd(args []string) {
	fs := newFlagSet("doctor")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON results for CI consumption")
	verbose := fs.Bool("verbose", false, "emit extra detail per check")
	strict := fs.Bool("strict", false, "treat warn as fail (exit non-zero on any non-ok)")
	timeoutSec := fs.Int("timeout", int(defaultDoctorDeadline/time.Second), "overall deadline in seconds")
	skipWorkers := fs.Bool("skip-workers", false, "do not warn when no workers are registered")
	fix := fs.Bool("fix", false, "interactive: prompt before running each check's suggested fix")
	serviceURL := fs.String("service-url", "", "override per-service URL (comma-separated KEY=URL pairs)")
	fs.ParseArgs(args)

	if *fix && *jsonOutput {
		fmt.Fprintln(os.Stderr, "doctor: --fix and --json are mutually exclusive")
		os.Exit(2)
	}

	env, err := buildDoctorEnv(fs, *skipWorkers, *serviceURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "doctor:", err)
		os.Exit(2)
	}

	deadline := time.Duration(*timeoutSec) * time.Second
	if deadline <= 0 {
		deadline = defaultDoctorDeadline
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	checks := defaultChecks()
	results := runDoctorChecks(ctx, env, checks)

	if *fix {
		results = runInteractiveFixes(os.Stdin, os.Stdout, ctx, env, checks, results, doctorShellRunner)
	}

	if *jsonOutput {
		emitJSON(results, *verbose)
	} else {
		emitHuman(os.Stdout, results, *verbose)
	}

	failures := countByState(results, stateFail)
	warnings := countByState(results, stateWarn)
	if failures > 0 || (*strict && warnings > 0) {
		os.Exit(1)
	}
}

// buildDoctorEnv resolves flags + constructs the shared HTTP client.
// Uses the SDK's TLS transport helper so --cacert + --insecure behave
// identically across subcommands.
func buildDoctorEnv(fs *flagSet, skipWorkers bool, serviceURLRaw string) (*doctorEnv, error) {
	tlsTransport, err := sdk.BuildTLSTransportErr(fs.tlsOptions())
	if err != nil {
		return nil, fmt.Errorf("tls configuration: %w", err)
	}
	gateway := strings.TrimSpace(*fs.gateway)
	if gateway == "" {
		gateway = defaultGateway
	}
	// BuildTLSTransportErr returns (nil, nil) when no TLS customization
	// is needed; assigning a typed-nil *http.Transport to http.Client's
	// interface-typed Transport field is a non-nil interface that panics
	// when Do() dispatches. Leave Transport unset so net/http falls
	// back to DefaultTransport.
	hc := &http.Client{Timeout: defaultDoctorPerCheckTimeout}
	if tlsTransport != nil {
		hc.Transport = tlsTransport
	}
	env := &doctorEnv{
		gateway:     strings.TrimRight(gateway, "/"),
		apiKey:      strings.TrimSpace(*fs.apiKey),
		tenant:      strings.TrimSpace(*fs.tenant),
		caCert:      strings.TrimSpace(*fs.cacert),
		skipWorkers: skipWorkers,
		serviceURL:  parseServiceURLOverrides(serviceURLRaw),
		httpClient:  hc,
	}
	return env, nil
}

// parseServiceURLOverrides splits "scheduler=http://...,mcp=http://..."
// into a map. Values without a KEY=VALUE shape are silently ignored so
// misformatted overrides don't take down the whole command.
func parseServiceURLOverrides(raw string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// serviceProbe describes one backend service's readyz endpoint. The
// URL can be overridden via --service-url KEY=URL so k8s + port-forward
// deploys can re-target each probe. Default URLs match service-local
// readyz endpoints documented by docker-compose.yml healthchecks; not all
// of those ports are published on the host in the default compose stack.
type serviceProbe struct {
	id               string // stable identifier — also the --service-url KEY and check id.
	label            string
	defaultURL       string
	fix              string // service-specific fix hint for fail/skip output.
	portMessage      string // short name used in the skip-when-port-not-exposed detail.
	optionalHostPort bool   // default compose keeps this service's readyz endpoint container-local.
}

// defaultServiceProbes enumerates the backend services doctor probes.
// Default URLs track the container-local readyz ports. optionalHostPort=true
// marks services whose default compose deployment does not publish that
// readyz port to the host; operators can still force strict probing with
// --service-url KEY=URL.
var defaultServiceProbes = []serviceProbe{
	{id: "scheduler", label: "Scheduler readyz", defaultURL: "http://127.0.0.1:9090/readyz", fix: "docker compose logs scheduler  (verify nats + redis reachable from scheduler)", portMessage: "scheduler host port 9090", optionalHostPort: true},
	{id: "safety-kernel", label: "Safety kernel readyz", defaultURL: "http://127.0.0.1:9095/readyz", fix: "docker compose logs safety-kernel  (kernel rejects gateway traffic until ready)", portMessage: "safety-kernel host port 9095", optionalHostPort: true},
	{id: "context-engine", label: "Context engine readyz", defaultURL: "http://127.0.0.1:9094/readyz", fix: "docker compose logs context-engine  (verify redis + grpc bootstrap)", portMessage: "context-engine host port 9094", optionalHostPort: true},
	{id: "workflow-engine", label: "Workflow engine readyz", defaultURL: "http://127.0.0.1:9093/readyz", fix: "docker compose logs workflow-engine  (verify NATS subscriptions came up)", portMessage: "workflow-engine host port 9093"},
	{id: "mcp", label: "MCP readyz", defaultURL: "http://127.0.0.1:8090/readyz", fix: "docker compose logs mcp  (check gateway_addr inside the mcp container)", portMessage: "mcp host port 8090", optionalHostPort: true},
	{id: "dashboard", label: "Dashboard reachable", defaultURL: "http://127.0.0.1:8082/", fix: "docker compose logs dashboard  (dashboard serves static build; re-pull image if missing)", portMessage: "dashboard host port 8082"},
}

// defaultChecks returns the shipped check sequence. Order is
// deliberate: connectivity first (gateway reachable + authenticated),
// then backend signals, then per-service readyz, then state checks
// (demo pack, policy bundle, TLS expiry). A fail early in the list
// frequently explains later skips.
func defaultChecks() []doctorCheck {
	checks := []doctorCheck{
		{id: "gateway_reachable", label: "Gateway reachable", run: checkGatewayReachable},
		{id: "gateway_auth", label: "Gateway authenticated", run: checkGatewayAuth},
		{id: "nats_connected", label: "NATS connected", run: checkNATSConnected},
		{id: "redis_ok", label: "Redis OK", run: checkRedisOK},
		{id: "workers_registered", label: "Workers registered", run: checkWorkersRegistered},
		{id: "build_info", label: "Build info available", run: checkBuildInfo},
	}
	for _, p := range defaultServiceProbes {
		checks = append(checks, doctorCheck{
			id:    "service_" + p.id,
			label: p.label,
			run:   makeServiceProbeCheck(p),
		})
	}
	checks = append(checks,
		doctorCheck{id: "demo_pack_installed", label: "Demo pack installed", run: checkDemoPackInstalled},
		doctorCheck{id: "policy_bundle_loaded", label: "Policy bundle loaded", run: checkPolicyBundleLoaded},
		doctorCheck{id: "version_skew", label: "Version skew (cordumctl vs gateway)", run: checkVersionSkew},
		doctorCheck{id: "tls_cert_expiry", label: "TLS cert expiry", run: checkTLSCertExpiry},
	)
	return checks
}

// makeServiceProbeCheck closes over a probe and returns the runner the
// framework expects. Factoring the body this way lets tests inject
// per-service fake URLs without duplicating the HTTP plumbing.
func makeServiceProbeCheck(p serviceProbe) func(ctx context.Context, env *doctorEnv) checkResult {
	return func(ctx context.Context, env *doctorEnv) checkResult {
		url := p.defaultURL
		overridden := false
		if override, ok := env.serviceURL[p.id]; ok && strings.TrimSpace(override) != "" {
			url = strings.TrimSpace(override)
			overridden = true
		}
		return probeServiceReadyz(ctx, env, p, url, overridden)
	}
}

// probeServiceReadyz issues a GET against the probe URL and maps the
// outcome to a checkResult. Connection errors fall back to stateSkip
// when the gateway's /api/v1/status reports the deploy is internally
// healthy — the common case in release compose where only the gateway
// port is published. Non-200 responses stay as fail with service-
// specific fix hints so operators can skip straight to `docker compose
// logs <service>`.
func probeServiceReadyz(ctx context.Context, env *doctorEnv, p serviceProbe, url string, overridden bool) checkResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return checkResult{State: stateFail, Detail: fmt.Sprintf("invalid probe URL %q: %v", url, err), Fix: p.fix}
	}
	resp, err := env.httpClient.Do(req)
	if err != nil {
		if p.optionalHostPort && !overridden && env.status != nil {
			return checkResult{
				State:  stateSkip,
				Detail: fmt.Sprintf("%s not exposed (expected in release compose); gateway /api/v1/status reports deploy up", p.portMessage),
			}
		}
		return checkResult{
			State:  stateFail,
			Detail: fmt.Sprintf("GET %s: %v", url, err),
			Fix:    p.fix,
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return checkResult{State: stateOK, Detail: fmt.Sprintf("GET %s %d", url, resp.StatusCode)}
	}
	if p.optionalHostPort && !overridden && env.status != nil {
		return checkResult{
			State:  stateSkip,
			Detail: fmt.Sprintf("%s returned %d or is owned by another local service; gateway /api/v1/status reports deploy up", p.portMessage, resp.StatusCode),
		}
	}
	return checkResult{
		State:  stateFail,
		Detail: fmt.Sprintf("GET %s returned %d", url, resp.StatusCode),
		Fix:    p.fix,
	}
}

func runDoctorChecks(ctx context.Context, env *doctorEnv, checks []doctorCheck) []checkResult {
	results := make([]checkResult, 0, len(checks))
	for _, c := range checks {
		if err := ctx.Err(); err != nil {
			results = append(results, checkResult{
				ID: c.id, Label: c.label, State: stateSkip,
				Detail: "overall deadline reached before this check ran",
			})
			continue
		}
		perCheckCtx, cancel := context.WithTimeout(ctx, defaultDoctorPerCheckTimeout)
		res := c.run(perCheckCtx, env)
		cancel()
		if res.ID == "" {
			res.ID = c.id
		}
		if res.Label == "" {
			res.Label = c.label
		}
		results = append(results, res)
	}
	return results
}

// ---------------------------------------------------------------------------
// Connectivity checks
// ---------------------------------------------------------------------------

func checkGatewayReachable(ctx context.Context, env *doctorEnv) checkResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.gateway+"/readyz", nil)
	if err != nil {
		return checkResult{State: stateFail, Detail: err.Error(), Fix: "ensure --gateway is a valid URL"}
	}
	resp, err := env.httpClient.Do(req)
	if err != nil {
		return checkResult{
			State:  stateFail,
			Detail: fmt.Sprintf("GET %s/readyz: %v", env.gateway, err),
			Fix:    "docker compose up -d api-gateway  (or check gateway_addr + TLS trust)",
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return checkResult{
			State:  stateFail,
			Detail: fmt.Sprintf("GET %s/readyz returned %d", env.gateway, resp.StatusCode),
			Fix:    "docker compose logs api-gateway",
		}
	}
	return checkResult{State: stateOK, Detail: env.gateway + "/readyz 200"}
}

func checkGatewayAuth(ctx context.Context, env *doctorEnv) checkResult {
	if env.apiKey == "" {
		return checkResult{
			State:  stateFail,
			Detail: "no API key configured",
			Fix:    "export CORDUM_API_KEY=<your-key>  (or --api-key flag; see .env)",
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.gateway+"/api/v1/status", nil)
	if err != nil {
		return checkResult{State: stateFail, Detail: err.Error()}
	}
	req.Header.Set("X-API-Key", env.apiKey)
	if env.tenant != "" {
		req.Header.Set("X-Tenant-ID", env.tenant)
	}
	resp, err := env.httpClient.Do(req)
	if err != nil {
		return checkResult{State: stateFail, Detail: err.Error(), Fix: "docker compose logs api-gateway"}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return checkResult{
			State:  stateFail,
			Detail: "401 Unauthorized from /api/v1/status",
			Fix:    "export CORDUM_API_KEY=<your-key>  (check .env for CORDUM_API_KEYS)",
		}
	}
	if resp.StatusCode != http.StatusOK {
		return checkResult{
			State:  stateFail,
			Detail: fmt.Sprintf("/api/v1/status returned %d", resp.StatusCode),
			Fix:    "docker compose logs api-gateway",
		}
	}
	var status gatewayStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return checkResult{State: stateFail, Detail: "decode /api/v1/status: " + err.Error()}
	}
	env.status = &status
	return checkResult{State: stateOK, Detail: "/api/v1/status 200 with build " + status.Build.Version}
}

// ---------------------------------------------------------------------------
// Backend-signal checks — depend on env.status populated by checkGatewayAuth
// ---------------------------------------------------------------------------

func checkNATSConnected(_ context.Context, env *doctorEnv) checkResult {
	if env.status == nil {
		return checkResult{State: stateSkip, Detail: "skipped — gateway auth did not populate /api/v1/status"}
	}
	if !env.status.NATS.Connected {
		return checkResult{
			State:  stateFail,
			Detail: "gateway reports NATS disconnected",
			Fix:    "docker compose logs nats  (verify NATS_TOKEN + nats service health)",
		}
	}
	return checkResult{State: stateOK, Detail: "NATS connected: " + env.status.NATS.Status}
}

func checkRedisOK(_ context.Context, env *doctorEnv) checkResult {
	if env.status == nil {
		return checkResult{State: stateSkip, Detail: "skipped — /api/v1/status unavailable"}
	}
	if !env.status.Redis.OK {
		return checkResult{
			State:  stateFail,
			Detail: "gateway reports Redis not OK",
			Fix:    "docker compose logs redis  (verify REDIS_PASSWORD + redis service)",
		}
	}
	return checkResult{State: stateOK, Detail: "Redis reachable"}
}

func checkWorkersRegistered(_ context.Context, env *doctorEnv) checkResult {
	if env.status == nil {
		return checkResult{State: stateSkip, Detail: "skipped — /api/v1/status unavailable"}
	}
	if env.skipWorkers {
		return checkResult{State: stateSkip, Detail: "--skip-workers set"}
	}
	if env.status.Workers.Count == 0 {
		return checkResult{
			State:  stateWarn,
			Detail: "no workers registered",
			Fix:    "start a worker  (cordumctl pack install ./demo/quickstart/pack, then run the worker binary)",
		}
	}
	return checkResult{State: stateOK, Detail: fmt.Sprintf("%d worker(s) registered", env.status.Workers.Count)}
}

func checkBuildInfo(_ context.Context, env *doctorEnv) checkResult {
	if env.status == nil {
		return checkResult{State: stateSkip, Detail: "skipped — /api/v1/status unavailable"}
	}
	v := strings.TrimSpace(env.status.Build.Version)
	if v == "" || v == "dev" {
		return checkResult{
			State:  stateWarn,
			Detail: fmt.Sprintf("build version %q — likely an unpinned image", v),
			Fix:    "docker compose pull && docker compose up -d  (or set CORDUM_VERSION)",
		}
	}
	return checkResult{State: stateOK, Detail: "build version " + v}
}

// ---------------------------------------------------------------------------
// Policy + pack state checks
// ---------------------------------------------------------------------------

// demoPackID is the canonical id for the quickstart demo pack shipped
// by task-26caa24e. Kept in sync with demo/quickstart/pack/pack.yaml so
// the check's fix hint references the right install command.
const demoPackID = "demo-quickstart"

func checkDemoPackInstalled(ctx context.Context, env *doctorEnv) checkResult {
	if env.apiKey == "" {
		return checkResult{State: stateSkip, Detail: "skipped — CORDUM_API_KEY unset"}
	}
	var body struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	status, err := doctorGetJSON(ctx, env, "/api/v1/packs", &body)
	if err != nil {
		return checkResult{
			State:  stateFail,
			Detail: fmt.Sprintf("GET /api/v1/packs: %v", err),
			Fix:    "docker compose logs api-gateway  (or verify packs endpoint is reachable)",
		}
	}
	if status != http.StatusOK {
		return checkResult{
			State:  stateFail,
			Detail: fmt.Sprintf("GET /api/v1/packs returned %d", status),
			Fix:    "docker compose logs api-gateway",
		}
	}
	for _, it := range body.Items {
		if it.ID == demoPackID {
			return checkResult{State: stateOK, Detail: "demo-quickstart pack installed"}
		}
	}
	return checkResult{
		State:  stateWarn,
		Detail: "demo-quickstart pack is not installed — ALLOW/DENY/APPROVE demo will not render",
		Fix:    "cordumctl pack install ./demo/quickstart/pack",
	}
}

// demoPolicyRulePrefix is the common prefix for every rule id shipped
// by demo/quickstart/pack/overlays/policy.fragment.yaml. Used to count
// partial/total coverage for the warn/fail messages.
const demoPolicyRulePrefix = "demo-quickstart-"

// demoPolicyRequiredRules is the canonical set of rule ids the
// quickstart demo needs to render the three verdict classes the epic
// rail demands ("Pre-seeded demo must show ALLOW, DENY, and APPROVE
// verdicts"). Source of truth: demo/quickstart/pack/overlays/policy.fragment.yaml.
// If ANY of these ids is missing from the live policy rules list, the
// demo cannot showcase the full ALLOW/DENY/APPROVE loop and doctor
// must warn rather than report ok. A partial demo policy is worse
// than a missing one because it looks healthy on first glance.
var demoPolicyRequiredRules = []string{
	"demo-quickstart-greet-allow",   // ALLOW verdict
	"demo-quickstart-delete-deny",   // DENY verdict
	"demo-quickstart-admin-approve", // APPROVE (require_approval) verdict
}

func checkPolicyBundleLoaded(ctx context.Context, env *doctorEnv) checkResult {
	if env.apiKey == "" {
		return checkResult{State: stateSkip, Detail: "skipped — CORDUM_API_KEY unset"}
	}
	// First establish that *some* bundle is loaded — a zero-bundle gateway
	// is a distinct failure mode from "bundle loaded but demo rules
	// missing" and deserves its own fix hint.
	var bundlesBody struct {
		Items []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
			Rules   int    `json:"rules_count"`
		} `json:"items"`
	}
	status, err := doctorGetJSON(ctx, env, "/api/v1/policy/bundles", &bundlesBody)
	if err != nil {
		return checkResult{
			State:  stateFail,
			Detail: fmt.Sprintf("GET /api/v1/policy/bundles: %v", err),
			Fix:    "docker compose logs api-gateway",
		}
	}
	if status != http.StatusOK {
		return checkResult{
			State:  stateFail,
			Detail: fmt.Sprintf("GET /api/v1/policy/bundles returned %d", status),
			Fix:    "docker compose logs api-gateway",
		}
	}
	if len(bundlesBody.Items) == 0 {
		return checkResult{
			State:  stateWarn,
			Detail: "no policy bundles loaded",
			Fix:    "cordumctl pack install ./demo/quickstart/pack  (seeds the demo policy bundle)",
		}
	}
	enabledBundles := 0
	for _, it := range bundlesBody.Items {
		if it.Enabled {
			enabledBundles++
		}
	}
	if enabledBundles == 0 {
		return checkResult{
			State:  stateWarn,
			Detail: fmt.Sprintf("%d policy bundle(s) present but none enabled — no rules in force", len(bundlesBody.Items)),
			Fix:    "cordumctl policy activate <bundle-id>  (see cordumctl policy list)",
		}
	}
	// At least one bundle is enabled. Now check specifically for the demo
	// rule ids via /api/v1/policy/rules (which parses bundle content into
	// rule items). This is the DoD-aligned signal — an unrelated bundle
	// being active is not the same as the demo policy being loaded.
	var rulesBody struct {
		Items []struct {
			ID         string `json:"id"`
			FragmentID string `json:"fragment_id"`
			Decision   string `json:"decision"`
		} `json:"items"`
	}
	rulesStatus, rulesErr := doctorGetJSON(ctx, env, "/api/v1/policy/rules", &rulesBody)
	if rulesErr != nil {
		// Transport error after bundles succeeded — don't downgrade the
		// whole check to fail, the operator already knows the gateway
		// answers. Warn with the specific failure.
		return checkResult{
			State:  stateWarn,
			Detail: fmt.Sprintf("%d enabled bundle(s) but GET /api/v1/policy/rules failed: %v", enabledBundles, rulesErr),
			Fix:    "docker compose logs api-gateway",
		}
	}
	if rulesStatus != http.StatusOK {
		return checkResult{
			State:  stateWarn,
			Detail: fmt.Sprintf("%d enabled bundle(s) but /api/v1/policy/rules returned %d", enabledBundles, rulesStatus),
			Fix:    "docker compose logs api-gateway",
		}
	}
	// Presence-test every rule id the ALLOW/DENY/APPROVE demo needs.
	// Partial coverage (e.g. only the ALLOW rule present) is a warn, not
	// an ok — the quickstart cannot demo the full verdict loop with a
	// subset of these rules. See demoPolicyRequiredRules for the source
	// of truth.
	present := make(map[string]bool, len(demoPolicyRequiredRules))
	prefixMatches := 0
	for _, rule := range rulesBody.Items {
		if strings.HasPrefix(rule.ID, demoPolicyRulePrefix) {
			prefixMatches++
		}
		for _, want := range demoPolicyRequiredRules {
			if rule.ID == want {
				present[want] = true
			}
		}
	}
	missing := make([]string, 0, len(demoPolicyRequiredRules))
	for _, want := range demoPolicyRequiredRules {
		if !present[want] {
			missing = append(missing, want)
		}
	}
	switch {
	case len(missing) == len(demoPolicyRequiredRules):
		// None of the expected rule ids present — demo policy is either
		// missing entirely or the pack has a different rule set.
		return checkResult{
			State:  stateWarn,
			Detail: fmt.Sprintf("%d enabled bundle(s) loaded but demo-quickstart policy rules absent — ALLOW/DENY/APPROVE verdicts won't demo", enabledBundles),
			Fix:    "cordumctl pack install ./demo/quickstart/pack  (merges demo-quickstart-* rules into the policy bundle)",
		}
	case len(missing) > 0:
		// Partial match — this is the false-green that QA caught. Promote
		// to warn so operators see the demo won't render all three verdicts.
		return checkResult{
			State: stateWarn,
			Detail: fmt.Sprintf(
				"%d enabled bundle(s) with partial demo policy — missing %d of %d required rule(s): %s. ALLOW/DENY/APPROVE demo will be incomplete.",
				enabledBundles,
				len(missing), len(demoPolicyRequiredRules),
				strings.Join(missing, ", "),
			),
			Fix: "cordumctl pack install ./demo/quickstart/pack  (reinstall overwrites the policy fragment so every demo-quickstart-* rule is merged)",
		}
	}
	return checkResult{
		State:  stateOK,
		Detail: fmt.Sprintf("%d enabled bundle(s) with full demo-quickstart ruleset (%d rules, %d prefix matches)", enabledBundles, len(demoPolicyRequiredRules), prefixMatches),
	}
}

// doctorGetJSON issues an authenticated GET and decodes the body.
// Returns (statusCode, error). Errors only surface transport failures;
// non-2xx responses return their status code with a nil error so each
// caller can map codes to actionable states.
func doctorGetJSON(ctx context.Context, env *doctorEnv, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.gateway+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-API-Key", env.apiKey)
	if env.tenant != "" {
		req.Header.Set("X-Tenant-ID", env.tenant)
	}
	resp, err := env.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode body: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// ---------------------------------------------------------------------------
// Version skew
// ---------------------------------------------------------------------------

// checkVersionSkew compares cordumctl's embedded build version with the
// Gateway's /api/v1/status Build.Version. Per-service /buildinfo HTTP
// endpoints are not currently exposed (core/infra/health/probes.go only
// returns dep-check state), so the honest check scopes to the one pair
// we CAN verify: the CLI the operator is running vs the gateway image
// that's live. This is the skew that bites operators most often — a
// stale cordumctl calling a newer gateway surfaces as silently missing
// flags or wrong CLI output.
//
// Thresholds: identical → ok. Minor mismatch (same major) → warn.
// Major mismatch → fail. "dev" / "unknown" on either side → skip (no
// semantic version to compare).
func checkVersionSkew(_ context.Context, env *doctorEnv) checkResult {
	if env.status == nil {
		return checkResult{State: stateSkip, Detail: "skipped — /api/v1/status unavailable"}
	}
	gw := strings.TrimSpace(env.status.Build.Version)
	cli := strings.TrimSpace(doctorCLIVersion)
	if gw == "" || cli == "" {
		return checkResult{State: stateSkip, Detail: "skipped — gateway or cordumctl version unreported"}
	}
	if gw == cli {
		return checkResult{State: stateOK, Detail: fmt.Sprintf("cordumctl %s matches gateway %s", cli, gw)}
	}
	if strings.EqualFold(gw, "dev") || strings.EqualFold(cli, "dev") ||
		strings.EqualFold(gw, "unknown") || strings.EqualFold(cli, "unknown") {
		return checkResult{
			State:  stateSkip,
			Detail: fmt.Sprintf("cordumctl=%s gateway=%s — skipping (dev/unknown version)", cli, gw),
		}
	}
	cliMajor, cliMinor, cliOK := splitMajorMinor(cli)
	gwMajor, gwMinor, gwOK := splitMajorMinor(gw)
	if !cliOK || !gwOK {
		return checkResult{
			State:  stateWarn,
			Detail: fmt.Sprintf("non-semver versions — cordumctl=%s gateway=%s", cli, gw),
			Fix:    "docker compose pull && docker compose up -d  (or update cordumctl to match)",
		}
	}
	if cliMajor != gwMajor {
		return checkResult{
			State:  stateFail,
			Detail: fmt.Sprintf("major version mismatch: cordumctl=%s gateway=%s", cli, gw),
			Fix:    "docker compose pull && docker compose up -d  (then re-install cordumctl from a matching release)",
		}
	}
	if cliMinor != gwMinor {
		return checkResult{
			State:  stateWarn,
			Detail: fmt.Sprintf("minor version skew: cordumctl=%s gateway=%s", cli, gw),
			Fix:    "docker compose pull && docker compose up -d",
		}
	}
	return checkResult{State: stateOK, Detail: fmt.Sprintf("cordumctl %s matches gateway %s (patch-level diff)", cli, gw)}
}

// splitMajorMinor parses a version string into (major, minor, ok).
// Accepts "1.2.3", "1.2.3-rc1", "1.2", "v1.2.3". Returns ok=false on
// empty input or non-numeric major/minor.
func splitMajorMinor(v string) (int, int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return 0, 0, false
	}
	// Strip pre-release / build metadata so "1.2.3-rc1" parses.
	for _, sep := range []string{"-", "+"} {
		if i := strings.Index(v, sep); i >= 0 {
			v = v[:i]
		}
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}

// ---------------------------------------------------------------------------
// TLS cert expiry
// ---------------------------------------------------------------------------

const defaultCACertPath = "./certs/ca/ca.crt"

func checkTLSCertExpiry(_ context.Context, env *doctorEnv) checkResult {
	path := env.caCert
	if path == "" {
		path = defaultCACertPath
	}
	data, err := os.ReadFile(path) // #nosec G304 — operator-selected path.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return checkResult{
				State:  stateSkip,
				Detail: fmt.Sprintf("%s not present — skipping (deploy may not use TLS)", path),
			}
		}
		return checkResult{State: stateFail, Detail: err.Error()}
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return checkResult{State: stateFail, Detail: "failed to decode PEM"}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return checkResult{State: stateFail, Detail: err.Error()}
	}
	remaining := time.Until(cert.NotAfter)
	switch {
	case remaining < 24*time.Hour:
		return checkResult{
			State:  stateFail,
			Detail: fmt.Sprintf("CA cert expires in %s (at %s)", humanDuration(remaining), cert.NotAfter.UTC().Format(time.RFC3339)),
			Fix:    "cordumctl generate-certs --force --days 365",
		}
	case remaining < 7*24*time.Hour:
		return checkResult{
			State:  stateWarn,
			Detail: fmt.Sprintf("CA cert expires in %s", humanDuration(remaining)),
			Fix:    "cordumctl generate-certs --force --days 365",
		}
	default:
		return checkResult{State: stateOK, Detail: fmt.Sprintf("CA cert valid for %s", humanDuration(remaining))}
	}
}

// humanDuration renders a duration in days/hours/minutes without
// importing a formatting library. Suitable for CLI output.
func humanDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}
	days := int(d.Hours() / 24)
	if days >= 2 {
		return fmt.Sprintf("%dd", days)
	}
	hours := int(d.Hours())
	if hours >= 2 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// ---------------------------------------------------------------------------
// Interactive --fix mode
// ---------------------------------------------------------------------------

// destructiveFixPatterns enumerates substrings in a suggested fix that
// warrant an extra confirmation before the fix runs. These aren't
// refusals — they surface the destructive step so the operator can
// back out instead of blindly pressing y.
var destructiveFixPatterns = []string{
	"--force",
	"down -v",
	"reset --hard",
	"rm -rf",
	"dropdb",
	"DELETE FROM",
}

// isDestructiveFix returns true when the suggested fix contains one of
// destructiveFixPatterns. Case-insensitive because shell command casing
// varies by platform.
func isDestructiveFix(fix string) bool {
	lower := strings.ToLower(fix)
	for _, p := range destructiveFixPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// shellRunner abstracts command execution so tests can supply a fake.
type shellRunner func(ctx context.Context, command string) (string, error)

// doctorShellRunner runs `command` through the platform shell and
// returns combined stdout+stderr. Windows uses cmd /c; everything else
// uses /bin/sh -c.
func doctorShellRunner(ctx context.Context, command string) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command) // no-shell-exec-lint: operator-confirmed doctor repair only
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command) // no-shell-exec-lint: operator-confirmed doctor repair only
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runInteractiveFixes walks the failing checks with a non-empty Fix,
// prompts the operator [y/N/a] per fix, optionally executes via the
// provided shellRunner, and re-runs the check to confirm the repair.
// Responses:
//
//	y → run the fix and re-check
//	N (default) → skip this fix, leave the check's fail intact
//	a → skip all remaining fixes
//
// Fixes containing destructive substrings add a second confirmation.
// No output on --json caller side — the --json + --fix combination is
// refused upstream. Reads prompts from stdin; writes to stdout.
func runInteractiveFixes(
	stdin io.Reader,
	stdout io.Writer,
	ctx context.Context,
	env *doctorEnv,
	checks []doctorCheck,
	initial []checkResult,
	run shellRunner,
) []checkResult {
	updated := append([]checkResult(nil), initial...)
	byID := map[string]doctorCheck{}
	for _, c := range checks {
		byID[c.id] = c
	}
	reader := bufio.NewReader(stdin)
	abort := false
	for i, r := range updated {
		if abort {
			break
		}
		if r.State != stateFail || strings.TrimSpace(r.Fix) == "" {
			continue
		}
		_, _ = fmt.Fprintf(stdout, "\n[FIX] %s (%s)\n", r.Label, r.ID)
		_, _ = fmt.Fprintf(stdout, "      detail: %s\n", r.Detail)
		_, _ = fmt.Fprintf(stdout, "      suggested: %s\n", r.Fix)
		if isDestructiveFix(r.Fix) {
			_, _ = fmt.Fprintf(stdout, "      WARNING: suggested fix contains a potentially destructive flag.\n")
		}
		_, _ = fmt.Fprint(stdout, "      run now? [y/N/a] ")
		resp, readErr := readPromptLine(reader)
		if readErr != nil {
			_, _ = fmt.Fprintln(stdout, "      (no input — skipping)")
			continue
		}
		choice := strings.ToLower(strings.TrimSpace(resp))
		switch choice {
		case "a":
			_, _ = fmt.Fprintln(stdout, "      aborting remaining fixes.")
			abort = true
			continue
		case "y", "yes":
			if isDestructiveFix(r.Fix) {
				_, _ = fmt.Fprint(stdout, "      destructive — confirm with 'yes' to proceed: ")
				confirm, cerr := readPromptLine(reader)
				if cerr != nil || strings.ToLower(strings.TrimSpace(confirm)) != "yes" {
					_, _ = fmt.Fprintln(stdout, "      confirmation declined — skipping.")
					continue
				}
			}
			_, _ = fmt.Fprintf(stdout, "      running: %s\n", r.Fix)
			out, runErr := run(ctx, r.Fix)
			if trimmed := strings.TrimSpace(out); trimmed != "" {
				_, _ = fmt.Fprintln(stdout, indentLines(trimmed, "        "))
			}
			if runErr != nil {
				_, _ = fmt.Fprintf(stdout, "      fix failed: %v\n", runErr)
				continue
			}
			if c, ok := byID[r.ID]; ok {
				perCtx, cancel := context.WithTimeout(ctx, defaultDoctorPerCheckTimeout)
				updated[i] = c.run(perCtx, env)
				cancel()
				if updated[i].ID == "" {
					updated[i].ID = c.id
				}
				if updated[i].Label == "" {
					updated[i].Label = c.label
				}
				_, _ = fmt.Fprintf(stdout, "      re-run: %s\n", updated[i].State)
			}
		default:
			// "", "n", "no" → default skip, leave result as-is.
		}
	}
	return updated
}

// readPromptLine reads one line from the reader. EOF becomes an empty
// line plus error so the caller can distinguish no-input from a bare
// newline.
func readPromptLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

// indentLines prepends indent to every line of s. Used to render the
// captured shell output below the prompt.
func indentLines(s, indent string) string {
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(indent)
		b.WriteString(line)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Output renderers
// ---------------------------------------------------------------------------

func countByState(results []checkResult, s checkState) int {
	n := 0
	for _, r := range results {
		if r.State == s {
			n++
		}
	}
	return n
}

func emitJSON(results []checkResult, verbose bool) {
	emitJSONTo(os.Stdout, results, verbose)
}

// emitJSONTo is the testable core of emitJSON. Callers that capture
// output (tests, --json mode) should call this with a bytes.Buffer.
func emitJSONTo(w io.Writer, results []checkResult, verbose bool) {
	_ = verbose // --verbose has no effect in JSON mode — the payload already includes every field.
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"checks":   results,
		"summary":  summaryCounts(results),
		"exitCode": computeExitCode(results, false),
	})
}

func summaryCounts(results []checkResult) map[string]int {
	out := map[string]int{"ok": 0, "warn": 0, "fail": 0, "skip": 0}
	for _, r := range results {
		out[string(r.State)]++
	}
	return out
}

func computeExitCode(results []checkResult, strict bool) int {
	failures := countByState(results, stateFail)
	warnings := countByState(results, stateWarn)
	if failures > 0 || (strict && warnings > 0) {
		return 1
	}
	return 0
}

func emitHuman(w *os.File, results []checkResult, verbose bool) {
	emitHumanTo(w, shouldUseColor(w), results, verbose)
}

// emitHumanTo is the testable core: colour decision factored out so
// tests can render against a bytes.Buffer without isatty detection.
func emitHumanTo(w io.Writer, useColor bool, results []checkResult, verbose bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CHECK\tSTATE\tDETAIL\tFIX")
	// Stable order so human output is diff-friendly across runs.
	ordered := append([]checkResult(nil), results...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, r := range ordered {
		state := string(r.State)
		if useColor {
			state = colorise(r.State) + state + "\x1b[0m"
		}
		detail := r.Detail
		if !verbose && len(detail) > 80 {
			detail = detail[:77] + "..."
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ID, state, detail, r.Fix)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(w, "\n%d checks: %d ok, %d warn, %d fail, %d skip\n",
		len(results),
		countByState(results, stateOK),
		countByState(results, stateWarn),
		countByState(results, stateFail),
		countByState(results, stateSkip),
	)
}

func shouldUseColor(w *os.File) bool {
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return false
	}
	fi, err := w.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func colorise(s checkState) string {
	switch s {
	case stateOK:
		return "\x1b[32m"
	case stateWarn:
		return "\x1b[33m"
	case stateFail:
		return "\x1b[31m"
	case stateSkip:
		return "\x1b[90m"
	}
	return ""
}
