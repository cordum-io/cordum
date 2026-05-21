// Package engine is the in-memory state layer of the conformance
// gateway simulator. It holds every resource the fixtures operate on
// (agents, jobs, workflows, policies, audit events) plus the scenario
// script layer that programs deterministic failure modes keyed off the
// X-Conformance-Script request header.
//
// The engine has NO wall-clock dependencies: every id is allocated
// from an incrementing counter and every timestamp is a monotonic
// offset from a fixed origin. That keeps the entire simulator
// deterministic from one run to the next so the conformance harnesses
// can detect regressions via byte-equal diffs once the fixture's
// wildcard tokens are masked.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Conformance scripts wired through the X-Conformance-Script header.
// A fixture opts-in to one of these per-request to coax the simulator
// into a specific failure mode without needing a scenario manifest
// file on disk.
const (
	ScriptRateLimitOnce       = "rate-limit-once"
	ScriptServer500Once       = "server-500-once"
	ScriptServer500ThreeTimes = "server-500-three-times"
	ScriptServer500OneShot    = "server-500-one-shot"
)

// Origin is the fixed timestamp the engine reports as "now" for every
// resource it creates, plus/minus a small monotonic offset so order-
// dependent fixtures still see distinct timestamps. Fixtures mask
// timestamps with the $timestamp$ wildcard so the exact value never
// matters for grading — stability matters for byte-equal diffs.
var Origin = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// Agent captures the subset of the real AgentIdentity record the
// conformance fixtures care about.
type Agent struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Owner               string            `json:"owner"`
	RiskTier            string            `json:"risk_tier"`
	Description         string            `json:"description,omitempty"`
	Status              string            `json:"status"`
	AllowedServers      []string          `json:"allowed_servers,omitempty"`
	AllowedTools        []string          `json:"allowed_tools,omitempty"`
	AllowedResources    []string          `json:"allowed_resources,omitempty"`
	Entitlements        []string          `json:"entitlements,omitempty"`
	AllowedTopics       []string          `json:"allowed_topics,omitempty"`
	AllowedPools        []string          `json:"allowed_pools,omitempty"`
	DataClassifications []string          `json:"data_classifications,omitempty"`
	Labels              map[string]string `json:"labels,omitempty"`
	CreatedAt           string            `json:"created_at"`
	UpdatedAt           string            `json:"updated_at"`
}

// Job is the submitted-job state the fixtures track.
type Job struct {
	ID        string            `json:"id"`
	JobID     string            `json:"job_id,omitempty"`
	Topic     string            `json:"topic"`
	Prompt    string            `json:"prompt,omitempty"`
	Priority  string            `json:"priority,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Status    string            `json:"status"`
	TraceID   string            `json:"trace_id,omitempty"`
	UpdatedAt string            `json:"updatedAt"`
}

// Workflow CRUD + run state. Steps is `any` so the simulator accepts
// both the map-keyed form (`{"approve": {...}}`) and the array-keyed
// form (`[{"id":"s1",...},...]`) — fixtures declare whichever shape
// matches their scenario.
type Workflow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Steps     any    `json:"steps"`
	CreatedAt string `json:"created_at"`
}

type WorkflowRun struct {
	RunID      string `json:"run_id"`
	WorkflowID string `json:"workflow_id"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
}

// PolicyBundle pair mirrors the gateway's bundle surface.
type PolicyBundle struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     int    `json:"version"`
	Digest      string `json:"digest"`
	UpdatedAt   string `json:"updated_at"`
	PublishedAt string `json:"published_at,omitempty"`
}

// AuditEvent is paginated in `audit/list_paginated.json`.
type AuditEvent struct {
	ID        string         `json:"id"`
	EventType string         `json:"event_type"`
	Timestamp string         `json:"timestamp"`
	Actor     string         `json:"actor"`
	Resource  string         `json:"resource"`
	Details   map[string]any `json:"details,omitempty"`
}

// Session represents a login/bearer-token pair minted by POST /auth/login.
// Both `token` and `session_token` are emitted so fixtures + SDKs that
// expect either form stay compatible.
type Session struct {
	Token        string `json:"token"`
	SessionToken string `json:"session_token"`
	UserID       string `json:"user_id"`
	Principal    string `json:"principal"`
	Tenant       string `json:"tenant"`
	ExpiresAt    string `json:"expires_at"`
}

// Engine holds every piece of state the simulator needs to serve the
// 20 conformance fixtures. One Engine per sim process — concurrent
// requests are safe via the internal mu.
type Engine struct {
	mu sync.Mutex

	// Monotonic counter feeding opaque ids. Starts at 1 so that zero
	// remains a clear "unset" sentinel on the wire.
	seq int64

	Agents       map[string]*Agent
	Jobs         map[string]*Job
	Workflows    map[string]*Workflow
	WorkflowRuns map[string]*WorkflowRun

	// Policy bundles: the simulator seeds one "default" bundle on
	// construction so the read-only policies.list fixture has
	// something to return without a prior publish step.
	PolicyBundles map[string]*PolicyBundle
	PolicyVersion int // monotonic across publish/rollback

	// Audit events: seeded with ≥15 entries so the paginated list
	// fixture sees at least two cursor pages.
	AuditEvents []*AuditEvent

	// Sessions keyed by bearer token string.
	Sessions map[string]*Session

	// scriptRuns tracks per-(script, path) burn-down counters. A
	// fixture that asks for "server-500-three-times" on getJob trips
	// that counter up to 3 times, then falls through.
	scriptRuns map[string]int

	// idempotencyKeys records the last response for an
	// Idempotency-Key scoped to a request path. When the same key
	// repeats, the simulator replays the original response so the
	// idempotency fixtures can prove the SDK carried the header
	// across retries without causing a duplicate-submit.
	idempotencyKeys map[string]int
}

// New returns an engine seeded with the invariants the fixture library
// assumes are in place at t=0.
func New() *Engine {
	e := &Engine{
		Agents:          map[string]*Agent{},
		Jobs:            map[string]*Job{},
		Workflows:       map[string]*Workflow{},
		WorkflowRuns:    map[string]*WorkflowRun{},
		PolicyBundles:   map[string]*PolicyBundle{},
		Sessions:        map[string]*Session{},
		scriptRuns:      map[string]int{},
		idempotencyKeys: map[string]int{},
	}
	// Seeded "default" bundle for the read-only policies fixture.
	e.PolicyVersion = 1
	e.PolicyBundles["default"] = &PolicyBundle{
		ID:        "default",
		Name:      "default",
		Version:   e.PolicyVersion,
		Digest:    fakeDigest("default", e.PolicyVersion),
		UpdatedAt: e.Timestamp(0),
	}
	// Seed 18 audit events so audit.list_paginated (page size 10)
	// consistently has >1 page.
	for i := 0; i < 18; i++ {
		e.AuditEvents = append(e.AuditEvents, &AuditEvent{
			ID:        fmt.Sprintf("audit-seed-%04d", i),
			EventType: "system.startup",
			Timestamp: e.Timestamp(int64(i)),
			Actor:     "system",
			Resource:  "gateway",
		})
	}
	return e
}

// Mu exposes the engine-wide mutex so handlers in sibling packages
// can atomically read-or-mutate the maps the engine holds. Using a
// single mutex across every resource map is fine for conformance
// volumes (≤100 req/fixture); per-resource locks would be premature.
func (e *Engine) Mu() *sync.Mutex { return &e.mu }

// NextID allocates a monotonically increasing opaque id with a human-
// readable prefix. Fixtures mask ids with $any$ so the exact shape is
// for debuggability, not correctness.
func (e *Engine) NextID(prefix string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	return fmt.Sprintf("%s-%04d", prefix, e.seq)
}

// Timestamp emits an ISO-8601 string at Origin+offsetSeconds so
// fixtures see monotonically increasing-but-deterministic timestamps.
func (e *Engine) Timestamp(offsetSeconds int64) string {
	return Origin.Add(time.Duration(offsetSeconds) * time.Second).Format(time.RFC3339)
}

// ShouldFire consumes one budget for the (script, key) tuple. Returns
// true the caller should activate the script's behavior for this
// request, false when the budget for this key is exhausted. Keys are
// conventionally "method path" (e.g. "GET /api/v1/jobs/abc").
//
// Rules:
//
//	rate-limit-once        → one 429
//	server-500-once        → one 500
//	server-500-one-shot    → one 500
//	server-500-three-times → three 500s
func (e *Engine) ShouldFire(script, key string) bool {
	if script == "" {
		return false
	}
	limit := budgetFor(script)
	if limit == 0 {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Per-key-per-script tracking keeps different fixtures in the
	// same run independent. Tests re-seed the engine per fixture so
	// cross-contamination is impossible at the suite level; this
	// tracker exists for the in-process tests.
	bucket := script + "|" + key
	used := e.scriptRuns[bucket]
	if used >= limit {
		return false
	}
	e.scriptRuns[bucket]++
	return true
}

// budgetFor turns a script name into its per-key firing budget.
// Unknown scripts return 0 so the handler falls through to normal
// behavior — defense in depth against typos in fixture headers.
func budgetFor(script string) int {
	switch script {
	case ScriptRateLimitOnce, ScriptServer500Once, ScriptServer500OneShot:
		return 1
	case ScriptServer500ThreeTimes:
		return 3
	}
	return 0
}

// SeenIdempotencyKey returns true on the second+ call with the same
// (key, path) pair — the caller SHOULD replay the prior response.
// First call registers the pair and returns false. Returns false for
// the empty key so non-idempotent POSTs aren't affected.
func (e *Engine) SeenIdempotencyKey(key, path string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	bucket := key + "|" + path
	if _, seen := e.idempotencyKeys[bucket]; seen {
		return true
	}
	e.idempotencyKeys[bucket] = 1
	return false
}

// ListAgents returns the engine's agents in a deterministic order
// (ascending id) so paginated fixtures see stable ordering.
func (e *Engine) ListAgents() []*Agent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Agent, 0, len(e.Agents))
	for _, a := range e.Agents {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListJobs returns jobs in ascending id order.
func (e *Engine) ListJobs() []*Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Job, 0, len(e.Jobs))
	for _, j := range e.Jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListWorkflows returns workflows in ascending id order.
func (e *Engine) ListWorkflows() []*Workflow {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Workflow, 0, len(e.Workflows))
	for _, w := range e.Workflows {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PageAudit returns up to limit events starting at the event whose id
// immediately follows the cursor. An empty cursor starts at the head.
// Returns (page, nextCursor). nextCursor is empty on the final page.
func (e *Engine) PageAudit(cursor string, limit int) ([]*AuditEvent, string) {
	if limit <= 0 {
		limit = 10
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	start := 0
	if cursor != "" {
		idx, err := strconv.Atoi(cursor)
		if err == nil && idx > 0 && idx < len(e.AuditEvents) {
			start = idx
		}
	}
	end := start + limit
	if end > len(e.AuditEvents) {
		end = len(e.AuditEvents)
	}
	page := e.AuditEvents[start:end]
	next := ""
	if end < len(e.AuditEvents) {
		next = strconv.Itoa(end)
	}
	return page, next
}

// PublishBundle creates/updates a bundle and bumps the global version.
func (e *Engine) PublishBundle(id, name string) *PolicyBundle {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.PolicyVersion++
	ts := e.Timestamp(int64(e.PolicyVersion))
	bundle := &PolicyBundle{
		ID:          id,
		Name:        name,
		Version:     e.PolicyVersion,
		Digest:      fakeDigest(id, e.PolicyVersion),
		UpdatedAt:   ts,
		PublishedAt: ts,
	}
	e.PolicyBundles[id] = bundle
	return bundle
}

// RollbackBundle regresses the bundle to an older version. The
// simulator fakes the rollback by bumping the global version one more
// time (rollbacks produce a fresh monotonic version per the fixture).
func (e *Engine) RollbackBundle(id string, targetVersion int) (*PolicyBundle, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.PolicyBundles[id]
	if !ok {
		return nil, false
	}
	e.PolicyVersion++
	ts := e.Timestamp(int64(e.PolicyVersion))
	b.Version = e.PolicyVersion
	b.Digest = fakeDigest(id+":rollback", e.PolicyVersion)
	b.UpdatedAt = ts
	b.PublishedAt = ts
	_ = targetVersion
	return b, true
}

// fakeDigest returns a deterministic pseudo-SHA prefix so bundle
// responses don't carry wall-clock state. Real gateway uses a real
// SHA-256 over the canonical bundle bytes; the simulator short-circuits.
func fakeDigest(name string, version int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s@%d", name, version)))
	return "sha256:" + hex.EncodeToString(sum[:8])
}
