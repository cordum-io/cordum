// Package shadow implements EDGE-140's opt-in local shadow-agent scanner.
// The scanner observes likely unmanaged Claude Code / Codex / Cursor MCP
// configs + known agent process names + known env-var names and emits
// redacted findings. It is observe-mode only: zero enforcement actions
// (see task rail #2 and TestScannerRefusesEnforcement in scanner_test.go).
//
// All public types and constants live in this file so consumers can depend
// on them without pulling in the scanner runtime. Implementation of the
// detection pipeline lives in scanner.go; pure-fn redaction helpers live
// in redaction.go.
package shadow

import (
	"errors"
	"time"
)

// EvidenceType identifies how the finding was observed.
const (
	// EvidenceConfigFile — finding sourced from a known-path config file
	// (Claude Code / Codex / Cursor) under the user's home directory.
	EvidenceConfigFile = "config_file"
	// EvidenceProcessName — finding sourced from a running process whose
	// executable name matches a known agent.
	EvidenceProcessName = "process_name"
	// EvidenceEnvironmentVar — finding sourced from a process-wide
	// environment variable matching a known agent secret-name pattern.
	// The VALUE of the env var is NEVER captured; only the name + the
	// fact-of-presence are recorded.
	EvidenceEnvironmentVar = "environment_var"
)

// Status describes the post-detection state of a finding.
const (
	// StatusObserved — finding is a legitimate shadow observation that
	// downstream surfaces should display to operators.
	StatusObserved = "observed"
	// StatusUnreadable — detection target exists but the scanner could
	// not read it (permission denied, broken symlink, etc.). Operators
	// see this as a hint rather than an alert.
	StatusUnreadable = "unreadable"
	// StatusManagedSkip — detection target is derived from a Cordum
	// managed-settings deployment (carries the enterprise-policy-mode
	// invariant) and is NOT flagged as shadow. DoD #4 'managed config
	// not flagged'.
	StatusManagedSkip = "managed_skip"
	// StatusPartial — detection target exceeded the per-file byte cap
	// and was only partially scanned. Summary is best-effort.
	StatusPartial = "partial"
)

// Risk categorises the observation severity. Shadow detection never emits
// 'critical' because enforcement is out of scope (task rail #2); the most
// severe shadow observation that can ship from this package is 'high'.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// Finding is the single-observation record emitted by the scanner. Every
// field is intended for safe persistence and SIEM ingestion; no value here
// carries raw secrets, full developer paths, or prompt content.
type Finding struct {
	TenantID              string    `json:"tenant_id"`
	PrincipalID           string    `json:"principal_id"`
	Hostname              string    `json:"hostname"`
	Product               string    `json:"product"`                 // "claude-code", "codex", "cursor", ...
	EvidenceType          string    `json:"evidence_type"`           // one of Evidence* constants
	RedactedPath          string    `json:"redacted_path"`           // home-prefix replaced with "~"; never absolute developer path
	RedactedConfigSummary string    `json:"redacted_config_summary"` // bounded ≤2048 bytes; no raw secrets, no prompt content
	Risk                  string    `json:"risk"`                    // one of Risk* constants
	RemediationHint       string    `json:"remediation_hint"`
	Status                string    `json:"status"` // one of Status* constants
	ObservedAt            time.Time `json:"observed_at"`
}

// ProcessInfo is the minimal per-process record consumed by the scanner's
// process-name detector. Real implementations are produced by
// github.com/shirou/gopsutil/v3/process; tests inject a mock via
// WithProcessLister.
type ProcessInfo struct {
	Name string
	PID  int32
}

// ErrOptInRequired is returned by Scanner.Scan when the caller has not
// explicitly opted in via WithOptIn() or CORDUM_EDGE_SHADOW_SCAN_ENABLED.
// Task rail #1 'opt-in observe/warn only by default' depends on this
// fail-closed default.
var ErrOptInRequired = errors.New("shadow: opt-in required (set CORDUM_EDGE_SHADOW_SCAN_ENABLED=true or pass --enable-shadow-scan / WithOptIn())")
