package shadow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxConfigFileBytes caps how much of a candidate config file the scanner
// will read into memory. Legitimate Claude / Codex / Cursor MCP configs
// are well under 50 KiB; the 1 MiB ceiling protects the scan path from
// OOM on a maliciously oversized file and produces a `partial` finding
// rather than crashing.
const maxConfigFileBytes int64 = 1 << 20

// Scanner is the EDGE-140 P3 shadow-agent observer. Construct via
// NewScanner and apply functional options. The zero value is unusable —
// always go through NewScanner so logger / nowFn / process-list defaults
// are populated.
type Scanner struct {
	optIn         bool
	logger        *slog.Logger
	nowFn         func() time.Time
	tenantID      string
	principalID   string
	hostname      string
	home          string
	processLister func() ([]ProcessInfo, error)
	envLookup     func(string) string
}

// Option configures a Scanner at construction time.
type Option func(*Scanner)

// NewScanner returns a Scanner ready for Scan(). Defaults: opt-in false,
// no-op logger, time.Now, os.Getenv-backed env lookup, and a
// gopsutil-backed default process lister. Tests inject seams via
// WithProcessLister / WithEnvLookup / WithHomeDir.
func NewScanner(opts ...Option) *Scanner {
	s := &Scanner{
		logger:        slog.New(slog.NewTextHandler(discardWriter{}, nil)),
		nowFn:         time.Now,
		envLookup:     getEnv,
		processLister: defaultProcessLister,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Option setters live next to NewScanner so the contract is in one place.
func WithOptIn() Option                    { return func(s *Scanner) { s.optIn = true } }
func WithLogger(l *slog.Logger) Option     { return func(s *Scanner) { s.logger = l } }
func WithNowFn(fn func() time.Time) Option { return func(s *Scanner) { s.nowFn = fn } }
func WithTenant(id string) Option          { return func(s *Scanner) { s.tenantID = id } }
func WithPrincipal(id string) Option       { return func(s *Scanner) { s.principalID = id } }
func WithHostname(name string) Option      { return func(s *Scanner) { s.hostname = name } }
func WithHomeDir(path string) Option       { return func(s *Scanner) { s.home = path } }
func WithProcessLister(fn func() ([]ProcessInfo, error)) Option {
	return func(s *Scanner) { s.processLister = fn }
}
func WithEnvLookup(fn func(string) string) Option {
	return func(s *Scanner) { s.envLookup = fn }
}

// clientSpec lists every known MCP-client config-file path the scanner
// probes. Path is relative to $HOME and ToSlash-normalised; the runtime
// converts to host separators via filepath.FromSlash.
type clientSpec struct {
	product string
	relPath string
}

var clientSpecs = []clientSpec{
	{"claude-code", ".claude/settings.json"},
	{"codex", ".codex/config.toml"},
	{"cursor", ".cursor/mcp.json"},
}

// knownEnvVar associates a developer-credential env-var name with the
// agent product it most likely indicates. The scanner never reads the
// value of any matched variable — only the fact-of-presence is recorded.
type knownEnvVar struct {
	name    string
	product string
}

var knownEnvVars = []knownEnvVar{
	{"ANTHROPIC_API_KEY", "claude-code"},
	{"OPENAI_API_KEY", "codex"},
	{"CURSOR_API_KEY", "cursor"},
}

// processMatch maps a case-insensitive substring of a process executable
// name to the agent product it indicates. The longest / most-specific
// pattern wins so "claude-code" matches before bare "claude".
type processMatch struct {
	contains string
	product  string
}

var processMatches = []processMatch{
	{"claude-code", "claude-code"},
	{"claude", "claude-code"},
	{"codex", "codex"},
	{"cursor", "cursor"},
}

// Scan walks the three detection sources and emits redacted findings.
// Fail-closes on opt-in: ErrOptInRequired when neither WithOptIn() nor
// CORDUM_EDGE_SHADOW_SCAN_ENABLED is set.
func (s *Scanner) Scan(ctx context.Context) ([]Finding, error) {
	if s == nil {
		return nil, ErrOptInRequired
	}
	if !s.isOptedIn() {
		return nil, ErrOptInRequired
	}

	var findings []Finding
	findings = append(findings, s.scanConfigFiles(ctx)...)
	findings = append(findings, s.scanProcesses(ctx)...)
	findings = append(findings, s.scanEnvVars(ctx)...)
	return findings, nil
}

func (s *Scanner) isOptedIn() bool {
	if s.optIn {
		return true
	}
	if s.envLookup == nil {
		return false
	}
	switch s.envLookup("CORDUM_EDGE_SHADOW_SCAN_ENABLED") {
	case "true", "1", "yes":
		return true
	}
	return false
}

func (s *Scanner) resolveHome() string {
	if s.home != "" {
		return s.home
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// scanConfigFiles iterates the known client specs and emits one finding
// per existing config file. Missing files are silently skipped. Symlinks
// and unreadable files emit unreadable findings rather than crashing.
func (s *Scanner) scanConfigFiles(ctx context.Context) []Finding {
	home := s.resolveHome()
	if home == "" {
		return nil
	}
	var out []Finding
	for _, spec := range clientSpecs {
		if ctx.Err() != nil {
			return out
		}
		p := filepath.Join(home, filepath.FromSlash(spec.relPath))
		f := s.scanOneConfig(spec, p)
		if f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// scanOneConfig handles a single config-file probe. Returns nil when the
// file is absent (not a finding); returns a populated Finding otherwise.
func (s *Scanner) scanOneConfig(spec clientSpec, path string) *Finding {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return s.newFinding(spec.product, EvidenceConfigFile, path,
			"", "verify file permissions allow read access",
			StatusUnreadable, RiskLow)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// Symlinks are a privacy / TOCTOU risk; refuse to follow.
		return s.newFinding(spec.product, EvidenceConfigFile, path,
			"symlink skipped — refusing to follow",
			"replace the symlink with a regular file before re-running the scan",
			StatusUnreadable, RiskLow)
	}

	fh, err := os.Open(path) // #nosec G304 -- path is from a fixed allow-list under $HOME
	if err != nil {
		return s.newFinding(spec.product, EvidenceConfigFile, path,
			"", "verify file permissions / privilege allow read access",
			StatusUnreadable, RiskLow)
	}
	defer func() { _ = fh.Close() }()

	content, readErr := io.ReadAll(io.LimitReader(fh, maxConfigFileBytes))
	if readErr != nil {
		return s.newFinding(spec.product, EvidenceConfigFile, path,
			"",
			"read error encountered during scan; rerun after resolving filesystem issue",
			StatusUnreadable, RiskLow)
	}
	partial := int64(len(content)) >= maxConfigFileBytes

	summary := RedactConfigSummary(content)

	if isManagedConfig(content) {
		return s.newFinding(spec.product, EvidenceConfigFile, path,
			summary,
			"config carries the Cordum managed-settings invariant; no operator action required",
			StatusManagedSkip, RiskLow)
	}

	status := StatusObserved
	risk := RiskMedium
	if partial {
		status = StatusPartial
		risk = RiskLow
	}
	return s.newFinding(spec.product, EvidenceConfigFile, path,
		summary,
		"consider managing this client via `cordumctl edge managed-settings export` to centralise control",
		status, risk)
}

// isManagedConfig recognises configs that originated from a Cordum
// managed-settings deployment. The signature is the explicit
// enterprise-strict policy mode marker — EDGE-150 bakes this invariant
// into every managed-derived configuration so the shadow scanner can
// safely skip it (DoD #4 'managed config not flagged').
func isManagedConfig(content []byte) bool {
	if !contains(content, "CORDUM_EDGE_MANAGED_POLICY_MODE") {
		return false
	}
	return contains(content, "enterprise-strict")
}

func contains(b []byte, needle string) bool {
	return strings.Contains(string(b), needle)
}

// scanProcesses lists processes via the configured lister and emits one
// finding per name match. PID is recorded as part of the redacted-path
// string so operators can investigate, but no other process metadata
// (cmdline / environ / open-files) is captured.
func (s *Scanner) scanProcesses(ctx context.Context) []Finding {
	if s.processLister == nil {
		return nil
	}
	procs, err := s.processLister()
	if err != nil {
		s.logger.WarnContext(ctx, "shadow scanner: process enumeration failed", "err", err.Error())
		return nil
	}
	seen := map[string]bool{}
	var out []Finding
	for _, p := range procs {
		if ctx.Err() != nil {
			return out
		}
		product, ok := matchProcessName(p.Name)
		if !ok {
			continue
		}
		// De-duplicate by product so a host running 50 cursor processes
		// emits one finding, not fifty.
		if seen[product] {
			continue
		}
		seen[product] = true
		out = append(out, *s.newFinding(product, EvidenceProcessName,
			fmt.Sprintf("process:%s:%d", p.Name, p.PID),
			"",
			"verify the running agent is under cordum management",
			StatusObserved, RiskMedium))
	}
	return out
}

func matchProcessName(name string) (string, bool) {
	lower := strings.ToLower(strings.TrimSuffix(name, ".exe"))
	for _, m := range processMatches {
		if strings.Contains(lower, m.contains) {
			return m.product, true
		}
	}
	if strings.HasPrefix(lower, "mcp-") {
		return "mcp-server", true
	}
	return "", false
}

// scanEnvVars emits one finding per recognised credential env-var that is
// set in the calling process's environment. The VALUE of the env var is
// never read or recorded — only the fact-of-presence and the variable
// name (which is itself non-secret).
func (s *Scanner) scanEnvVars(ctx context.Context) []Finding {
	if s.envLookup == nil {
		return nil
	}
	var out []Finding
	for _, ev := range knownEnvVars {
		if ctx.Err() != nil {
			return out
		}
		if s.envLookup(ev.name) == "" {
			continue
		}
		out = append(out, *s.newFinding(ev.product, EvidenceEnvironmentVar,
			"env:"+ev.name,
			"",
			"prefer cordum-agentd keychain for credential provisioning",
			StatusObserved, RiskLow))
	}
	return out
}

// newFinding builds a Finding with the scanner's attribution stamped in.
// All callers route through this constructor so the audit envelope stays
// uniform.
func (s *Scanner) newFinding(product, evidence, rawPath, summary, hint, status, risk string) *Finding {
	f := Finding{
		TenantID:              s.tenantID,
		PrincipalID:           s.principalID,
		Hostname:              s.hostname,
		Product:               product,
		EvidenceType:          evidence,
		RedactedConfigSummary: summary,
		Risk:                  risk,
		RemediationHint:       hint,
		Status:                status,
		ObservedAt:            s.nowFn(),
	}
	// Process / env paths are not filesystem paths — they're synthetic
	// `process:name:pid` and `env:NAME` shapes that we record verbatim
	// without RedactPath canonicalisation.
	if evidence == EvidenceConfigFile {
		f.RedactedPath = RedactPath(rawPath)
	} else {
		f.RedactedPath = rawPath
	}
	return &f
}
