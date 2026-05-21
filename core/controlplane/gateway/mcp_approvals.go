package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

// MCP per-tool approval storage and lifecycle.
//
// MCP approvals are NOT job-scoped, so they cannot live in the existing
// jobMetaKey-backed storage. They get their own Redis namespace
// (`mcp:approvals:<id>`) plus a per-tenant lookup index keyed by the
// (tenant, agent_id, tool_name, args_hash) tuple — the index is what the
// MCP server checks for a pre-approved call before enqueuing a fresh one.
//
// Atomicity: mutating transitions (consume, resolve, expire) use Redis
// WATCH/MULTI/EXEC CAS on the record key. Concurrent callers either win
// the CAS or retry with the post-write state; we never last-write-win a
// terminal state over a live resolution. Consume-once is enforced by the
// same CAS — two concurrent ClaimPreApproved calls produce at most one
// "claimed" winner; the loser sees ConsumedAt != 0 and re-enqueues.
//
// Index hygiene: terminal records (APPROVED+consumed, REJECTED, EXPIRED)
// are removed from the per-tuple index at commit time so the index
// stays bounded even under retry storms.

const (
	mcpApprovalKeyPrefix      = "mcp:approvals:"
	mcpApprovalIndexKeyPrefix = "mcp:approvals:idx:"
	mcpApprovalDefaultTTL     = 5 * time.Minute

	// mcpArgsMaxBytes caps the args blob we persist on an approval
	// record. Oversize payloads are replaced with a truncation marker
	// that points at the original hash — the approver still sees what
	// they are approving through the hash, and dashboard UX shows a
	// clear "payload too large" hint. Hash is computed BEFORE truncation
	// so consume-once semantics remain stable regardless of size.
	mcpArgsMaxBytes = 64 * 1024

	// mcpCASMaxAttempts bounds how many times WATCH/EXEC retries before
	// we surface an error. Five is plenty under realistic contention —
	// beyond that a hot key is likely a symptom, not the cause.
	mcpCASMaxAttempts = 5
)

// MCPApprovalRequest is the input to EnqueueMCPApproval. All fields are
// required (except Principal/ArgsJSON/TTL/Reason); the constructor
// validates them up front rather than letting downstream Redis writes
// fail with a partial record.
type MCPApprovalRequest struct {
	// Tenant is the org/tenant the calling agent belongs to. Drives both
	// the storage namespace and the audit-chain partition.
	Tenant string
	// AgentID is the display-facing MCP agent identifier (X-Agent-Id).
	// Persisted for audit clarity; the self-approval guard uses
	// Principal, not AgentID.
	AgentID string
	// Principal is the authenticated subject that initiated the call
	// (API-key principal, SSO subject). The self-approval guard
	// compares the approver's principal against this value.
	Principal string
	// ToolName is the MCP tool the call targets.
	ToolName string
	// ArgsHash is the SHA-256 (hex) of the canonical args JSON. Two
	// invocations with identical args share an approval; differing args
	// require separate approvals.
	ArgsHash string
	// ArgsJSON is the canonical-form args payload. Persisted so the
	// approver can read exactly what the agent intends to execute.
	// Truncated to mcpArgsMaxBytes with a placeholder when oversize.
	ArgsJSON json.RawMessage
	// Requester is the persisted requester identity — the authenticated
	// principal of the caller. This is what the self-approval guard
	// compares against on the resolver's composite identity.
	Requester string
	// TTL bounds how long a PENDING approval lives before the reaper
	// transitions it to EXPIRED. Zero means use mcpApprovalDefaultTTL.
	TTL time.Duration
	// Reason is an optional human-readable summary of why the call needs
	// approval (e.g. the matched approvalScope rule). Stored as the
	// trigger reason and never overwritten by a resolver's free-form
	// reason — see ResolutionReason on the record.
	Reason string
}

// MCPApprovalRecord is the persisted shape stored at mcp:approvals:<id>.
// It mirrors the lifecycle states of model.ApprovalStatus so the
// dashboard/CLI can render a uniform view.
//
// Reason vs ResolutionReason: Reason is the trigger reason set at
// enqueue and NEVER overwritten. ResolutionReason is the approver's
// free-form comment added by Resolve(). Keeping them separate preserves
// the audit trail: "why did this call need approval?" stays answerable
// even after a resolver types their note.
type MCPApprovalRecord struct {
	ID               string                 `json:"id"`
	Tenant           string                 `json:"tenant"`
	AgentID          string                 `json:"agent_id"`
	Principal        string                 `json:"principal,omitempty"`
	ToolName         string                 `json:"tool_name"`
	ArgsHash         string                 `json:"args_hash"`
	ArgsJSON         json.RawMessage        `json:"args,omitempty"`
	Requester        string                 `json:"requester,omitempty"`
	Reason           string                 `json:"reason,omitempty"`
	ResolutionReason string                 `json:"resolution_reason,omitempty"`
	Status           model.ApprovalStatus   `json:"status"`
	CreatedAt        int64                  `json:"created_at"`
	ExpiresAt        int64                  `json:"expires_at"`
	ResolvedAt       int64                  `json:"resolved_at,omitempty"`
	ResolvedBy       string                 `json:"resolved_by,omitempty"`
	Decision         model.ApprovalDecision `json:"decision,omitempty"`
	ConsumedAt       int64                  `json:"consumed_at,omitempty"`
}

// MCPApprovalStore is the per-server handle for MCP approval persistence.
type MCPApprovalStore struct {
	client    redis.UniversalClient
	auditHook MCPAuditHook
}

var ErrMCPApprovalAlreadyResolved = errors.New("mcp approval already resolved")

type MCPApprovalStateConflictError struct {
	ID     string
	Status model.ApprovalStatus
}

func (e *MCPApprovalStateConflictError) Error() string {
	return "mcp approval cannot resolve in its current state"
}

func (e *MCPApprovalStateConflictError) Is(target error) bool {
	return target == ErrMCPApprovalAlreadyResolved
}

// MCPAuditHook receives every MCP-approval lifecycle event as a fully
// populated audit.SIEMEvent. The gateway wires it to the audit chain +
// SIEM exporter; tests inject a capturing stub to assert emission.
//
// A nil hook is explicitly allowed (stdio/dev deploys without audit).
type MCPAuditHook func(audit.SIEMEvent)

// NewMCPApprovalStore wires the store. The audit hook is installed
// separately via WithAuditHook so stdio/dev deploys can run without one.
func NewMCPApprovalStore(client redis.UniversalClient) *MCPApprovalStore {
	return &MCPApprovalStore{client: client}
}

// WithAuditHook returns a copy of the store with the given audit hook
// installed. Chained after construction so tests don't have to care
// about the hook when they don't need it.
func (s *MCPApprovalStore) WithAuditHook(hook MCPAuditHook) *MCPApprovalStore {
	if s == nil {
		return nil
	}
	clone := *s
	clone.auditHook = hook
	return &clone
}

// emitAudit builds and emits a SIEMEvent describing the given lifecycle
// transition. The outcome string is the canonical audit verb — enqueued,
// approved, rejected, expired, consumed — and flows to the Extra map so
// SIEM correlation rules can filter by it without string-splitting the
// EventType.
func (s *MCPApprovalStore) emitAudit(rec *MCPApprovalRecord, outcome, resolver string, severity string) {
	if s == nil || s.auditHook == nil || rec == nil {
		return
	}
	if severity == "" {
		severity = audit.SeverityInfo
	}
	extra := map[string]string{
		"tool_name":   rec.ToolName,
		"args_hash":   rec.ArgsHash,
		"approval_id": rec.ID,
		"requester":   rec.Requester,
		"outcome":     outcome,
	}
	if resolver != "" {
		extra["resolver"] = resolver
	}
	if rec.Reason != "" {
		extra["reason"] = rec.Reason
		extra["trigger_reason"] = rec.Reason
	}
	if rec.ResolutionReason != "" {
		extra["resolution_reason"] = rec.ResolutionReason
	}
	if rec.Principal != "" {
		extra["principal"] = rec.Principal
	}
	// ev.Reason is the most salient reason for the transition. For
	// approve/reject that's the resolver's note; for enqueue/expire/
	// consume it's the trigger reason. SIEM correlation still sees both
	// via Extra; ev.Reason is just the single-line summary.
	headlineReason := rec.Reason
	if outcome == "approved" || outcome == "rejected" {
		if rec.ResolutionReason != "" {
			headlineReason = rec.ResolutionReason
		}
	}
	event := audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventMCPToolApproval,
		Severity:  severity,
		TenantID:  rec.Tenant,
		AgentID:   rec.AgentID,
		Action:    outcome,
		Decision:  string(rec.Decision),
		Reason:    headlineReason,
		Identity:  resolver,
		Extra:     extra,
	}
	s.auditHook(event)
}

// validateMCPApprovalRequest enforces the input contract so a malformed
// caller can't write a half-valid record into Redis. Returns a
// concatenated error mentioning every missing field — easier to debug
// than one-at-a-time field errors.
func validateMCPApprovalRequest(req *MCPApprovalRequest) error {
	if req == nil {
		return errors.New("mcp approval: request is nil")
	}
	var missing []string
	if strings.TrimSpace(req.Tenant) == "" {
		missing = append(missing, "tenant")
	}
	if strings.TrimSpace(req.AgentID) == "" {
		missing = append(missing, "agent_id")
	}
	if strings.TrimSpace(req.ToolName) == "" {
		missing = append(missing, "tool_name")
	}
	if strings.TrimSpace(req.ArgsHash) == "" {
		missing = append(missing, "args_hash")
	}
	if len(missing) > 0 {
		return fmt.Errorf("mcp approval: missing required field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// EnqueueMCPApproval creates a PENDING approval record, writes it to
// Redis, and registers it in the lookup index.
//
// Returns the created MCPApprovalRecord on success. The approval ID is
// crypto/rand-derived so callers can safely use it as a stable handle.
func (s *MCPApprovalStore) EnqueueMCPApproval(ctx context.Context, req *MCPApprovalRequest) (*MCPApprovalRecord, error) {
	if err := validateMCPApprovalRequest(req); err != nil {
		return nil, err
	}
	if s == nil || s.client == nil {
		return nil, errors.New("mcp approval: store is not initialised")
	}

	ttl := req.TTL
	if ttl <= 0 {
		ttl = mcpApprovalDefaultTTL
	}

	id, err := mcpApprovalID()
	if err != nil {
		return nil, fmt.Errorf("mcp approval: generate id: %w", err)
	}
	now := time.Now().UTC()

	// Size-bound the args payload so a hostile caller can't blow out
	// Redis memory. The hash was computed on the full payload so
	// consume-once semantics remain stable; operators inspecting a
	// truncated record still see the original hash for correlation.
	argsStored := req.ArgsJSON
	if len(argsStored) > mcpArgsMaxBytes {
		argsStored = json.RawMessage(fmt.Sprintf(
			`{"_truncated":true,"_original_bytes":%d,"_hash":%q}`,
			len(req.ArgsJSON), req.ArgsHash,
		))
	}

	rec := &MCPApprovalRecord{
		ID:        id,
		Tenant:    strings.TrimSpace(req.Tenant),
		AgentID:   strings.TrimSpace(req.AgentID),
		Principal: strings.TrimSpace(req.Principal),
		ToolName:  strings.TrimSpace(req.ToolName),
		ArgsHash:  strings.TrimSpace(req.ArgsHash),
		ArgsJSON:  argsStored,
		Requester: strings.TrimSpace(req.Requester),
		Reason:    strings.TrimSpace(req.Reason),
		Status:    model.ApprovalStatusPending,
		CreatedAt: now.UnixMicro(),
		ExpiresAt: now.Add(ttl).UnixMicro(),
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("mcp approval: marshal: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, mcpApprovalKey(rec.ID), raw, ttl+time.Minute)
	// Index goes into a per-tuple set so a single agent/tool/args trio
	// can have at most one outstanding PENDING approval. The set member
	// is the approval ID — pre-approval lookup walks members and picks
	// an APPROVED+unconsumed record via CAS.
	idxKey := mcpApprovalIndexKey(rec.Tenant, rec.AgentID, rec.ToolName, rec.ArgsHash)
	pipe.SAdd(ctx, idxKey, rec.ID)
	pipe.Expire(ctx, idxKey, ttl+time.Minute)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("mcp approval: write redis: %w", err)
	}

	s.emitAudit(rec, "enqueued", "", audit.SeverityMedium)
	return rec, nil
}

// Get returns the approval record by ID. Returns (nil, redis.Nil) when
// the key has expired or never existed; callers should treat that as
// "no such approval".
func (s *MCPApprovalStore) Get(ctx context.Context, id string) (*MCPApprovalRecord, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("mcp approval: store is not initialised")
	}
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("mcp approval: id required")
	}
	raw, err := s.client.Get(ctx, mcpApprovalKey(id)).Bytes()
	if err != nil {
		return nil, err
	}
	var rec MCPApprovalRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("mcp approval: unmarshal %s: %w", id, err)
	}
	return &rec, nil
}

// FindPreApproved returns an APPROVED, unconsumed approval matching the
// (tenant, agent_id, tool_name, args_hash) tuple, or (nil, nil) when no
// such record exists. It does NOT consume the approval.
//
// For production the gate uses ClaimPreApproved instead — it bundles
// find+consume into a single CAS so concurrent callers cannot both
// satisfy the consume-once contract. FindPreApproved is kept as a
// read-only utility for diagnostics/tests.
func (s *MCPApprovalStore) FindPreApproved(ctx context.Context, tenant, agentID, toolName, argsHash string) (*MCPApprovalRecord, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("mcp approval: store is not initialised")
	}
	idxKey := mcpApprovalIndexKey(tenant, agentID, toolName, argsHash)
	ids, err := s.client.SMembers(ctx, idxKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("mcp approval: index lookup: %w", err)
	}
	var best *MCPApprovalRecord
	for _, id := range ids {
		rec, err := s.Get(ctx, id)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				// stale index entry — clean up opportunistically
				_ = s.client.SRem(ctx, idxKey, id).Err()
				continue
			}
			return nil, err
		}
		if rec.Status != model.ApprovalStatusApproved || rec.ConsumedAt != 0 {
			continue
		}
		if best == nil || rec.CreatedAt < best.CreatedAt {
			best = rec
		}
	}
	return best, nil
}

// ClaimPreApproved atomically locates and consumes an APPROVED+unconsumed
// approval for the tuple. Returns the claimed record on success, (nil, nil)
// when no eligible record exists, or an error when Redis fails.
//
// Consume-once is enforced by WATCH/MULTI/EXEC on the record key: two
// racing callers both try to flip ConsumedAt=0 → now; the CAS loser
// retries, observes ConsumedAt != 0, moves on. At most one caller
// receives a non-nil record.
//
// On success the claimed record's ID is removed from the per-tuple
// index so the index stays bounded under retry storms and the next
// pre-approval lookup does not waste Redis GETs on terminal entries.
func (s *MCPApprovalStore) ClaimPreApproved(ctx context.Context, tenant, agentID, toolName, argsHash string) (*MCPApprovalRecord, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("mcp approval: store is not initialised")
	}
	idxKey := mcpApprovalIndexKey(tenant, agentID, toolName, argsHash)
	ids, err := s.client.SMembers(ctx, idxKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("mcp approval: index lookup: %w", err)
	}
	// Deterministic order so concurrent callers try the same candidate
	// first — the CAS then decides the winner consistently.
	sort.Strings(ids)
	for _, id := range ids {
		consumed, rec, err := s.consumeRecord(ctx, id)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				_ = s.client.SRem(ctx, idxKey, id).Err()
				continue
			}
			return nil, err
		}
		if consumed && rec != nil {
			// Terminal state — prune from index to bound its size.
			_ = s.client.SRem(ctx, idxKey, id).Err()
			return rec, nil
		}
		// Record exists but was not consumable (non-APPROVED or already
		// consumed). If it's in a terminal state, prune so subsequent
		// lookups skip it.
		if rec != nil && recordIsTerminal(rec) {
			_ = s.client.SRem(ctx, idxKey, id).Err()
		}
	}
	return nil, nil
}

// MarkConsumed is a backward-compat wrapper over consumeRecord. Kept so
// external callers and existing tests continue to work; production code
// should prefer ClaimPreApproved for atomic find-and-consume.
//
// Returns nil on success OR when the record was already consumed; any
// other condition (missing, non-APPROVED) returns an error.
func (s *MCPApprovalStore) MarkConsumed(ctx context.Context, id string) error {
	consumed, rec, err := s.consumeRecord(ctx, id)
	if err != nil {
		return err
	}
	if !consumed && rec != nil && rec.ConsumedAt != 0 {
		// Already consumed — idempotent success.
		return nil
	}
	if !consumed && rec != nil && rec.Status != model.ApprovalStatusApproved {
		return fmt.Errorf("mcp approval: cannot consume record in status %q", rec.Status)
	}
	return nil
}

// consumeRecord is the CAS primitive behind MarkConsumed and
// ClaimPreApproved. It transitions ConsumedAt 0 → now for an APPROVED
// record, preserving the record's existing TTL.
//
// Returns:
//   - consumed=true, rec=post-op record — when the caller won the CAS
//   - consumed=false, rec=observed record — when the record is not
//     eligible (non-APPROVED, already consumed, expired mid-flight)
//   - consumed=false, rec=nil, err — on Redis failure
func (s *MCPApprovalStore) consumeRecord(ctx context.Context, id string) (bool, *MCPApprovalRecord, error) {
	key := mcpApprovalKey(id)
	var (
		consumed bool
		final    *MCPApprovalRecord
	)
	txFn := func(tx *redis.Tx) error {
		raw, err := tx.Get(ctx, key).Bytes()
		if err != nil {
			return err
		}
		var rec MCPApprovalRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return fmt.Errorf("mcp approval: unmarshal: %w", err)
		}
		final = &rec
		if rec.Status != model.ApprovalStatusApproved || rec.ConsumedAt != 0 {
			return nil
		}
		next := rec
		next.ConsumedAt = time.Now().UTC().UnixMicro()
		newRaw, err := json.Marshal(&next)
		if err != nil {
			return fmt.Errorf("mcp approval: marshal: %w", err)
		}
		ttl, ttlErr := tx.PTTL(ctx, key).Result()
		if ttlErr != nil {
			return ttlErr
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, newRaw, preservedSetTTL(ttl))
			return nil
		})
		if err == nil {
			consumed = true
			final = &next
		}
		return err
	}
	// task-c7e419d8: Redis CAS retry loop extracted into redisutil.Retry.
	if err := redisutil.Retry(ctx, s.client, txFn,
		redisutil.WithKeys(key),
		redisutil.WithMaxAttempts(mcpCASMaxAttempts),
	); err != nil {
		if !errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
			return false, nil, err
		}
		// Budget exhausted on TxFailedErr — preserves pre-refactor
		// behaviour: the loop used to fall through with `consumed/final`
		// capturing the last pre-Watch snapshot; return whatever the
		// closure last set.
	}
	if consumed && final != nil {
		s.emitAudit(final, "consumed", "", audit.SeverityInfo)
	}
	return consumed, final, nil
}

// Resolve transitions a PENDING approval to APPROVED or REJECTED under
// CAS. A concurrent SweepExpired sees that the record is no longer
// PENDING and bails — the resolution wins; a concurrent second Resolve
// sees the new status and returns an error.
//
// resolverID is the principal who took the action. Reason is stored as
// ResolutionReason and NEVER overwrites the trigger Reason — the
// original context remains intact for the audit trail.
func (s *MCPApprovalStore) Resolve(ctx context.Context, id string, decision model.ApprovalDecision, resolverID, reason string) (*MCPApprovalRecord, error) {
	key := mcpApprovalKey(id)
	var (
		result     *MCPApprovalRecord
		resolveErr error
	)
	txFn := func(tx *redis.Tx) error {
		raw, err := tx.Get(ctx, key).Bytes()
		if err != nil {
			return err
		}
		var rec MCPApprovalRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return err
		}
		if rec.Status != model.ApprovalStatusPending {
			resolveErr = &MCPApprovalStateConflictError{ID: id, Status: rec.Status}
			result = &rec
			return nil
		}
		next := rec
		next.ResolvedAt = time.Now().UTC().UnixMicro()
		next.ResolvedBy = resolverID
		next.Decision = decision
		if reason != "" {
			next.ResolutionReason = reason
		}
		switch decision {
		case model.ApprovalDecisionApprove:
			next.Status = model.ApprovalStatusApproved
		case model.ApprovalDecisionReject:
			next.Status = model.ApprovalStatusRejected
		default:
			resolveErr = fmt.Errorf("mcp approval: unsupported decision %q", decision)
			return nil
		}
		newRaw, err := json.Marshal(&next)
		if err != nil {
			return err
		}
		ttl, ttlErr := tx.PTTL(ctx, key).Result()
		if ttlErr != nil {
			return ttlErr
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, newRaw, preservedSetTTL(ttl))
			return nil
		})
		if err == nil {
			result = &next
		}
		return err
	}
	// task-c7e419d8: Redis CAS retry loop extracted into redisutil.Retry.
	if err := redisutil.Retry(ctx, s.client, txFn,
		redisutil.WithKeys(key),
		redisutil.WithMaxAttempts(mcpCASMaxAttempts),
	); err != nil && !errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		return nil, err
	}
	if resolveErr != nil {
		return result, resolveErr
	}
	if result != nil {
		outcome := "approved"
		severity := audit.SeverityInfo
		if decision == model.ApprovalDecisionReject {
			outcome = "rejected"
			severity = audit.SeverityMedium
			// Rejected records are terminal — prune from the index so
			// FindPreApproved/ClaimPreApproved don't waste cycles on them.
			s.pruneIndexFor(ctx, result)
		}
		s.emitAudit(result, outcome, resolverID, severity)
	}
	return result, nil
}

// ListByStatus scans the MCP-approval namespace and returns records
// matching the given status filter. Empty filter returns every record.
// cursor is opaque — the caller round-trips it for pagination; the
// returned nextCursor is zero when the scan is complete.
//
// limit caps the records returned per call so the HTTP handler can
// bound response size. The Redis SCAN cursor is advanced until we have
// either limit matches or reach cursor=0.
func (s *MCPApprovalStore) ListByStatus(ctx context.Context, status string, cursor uint64, limit int64) ([]*MCPApprovalRecord, uint64, error) {
	if s == nil || s.client == nil {
		return nil, 0, errors.New("mcp approval: store is not initialised")
	}
	if limit <= 0 {
		limit = 50
	}
	wantStatus := strings.ToLower(strings.TrimSpace(status))
	out := make([]*MCPApprovalRecord, 0, limit)
	next := cursor
	for int64(len(out)) < limit {
		keys, c, err := s.client.Scan(ctx, next, mcpApprovalKeyPrefix+"*", limit).Result()
		if err != nil {
			return nil, 0, fmt.Errorf("mcp approval: list scan: %w", err)
		}
		for _, key := range keys {
			// Skip index keys — they share the same prefix.
			if strings.HasPrefix(key, mcpApprovalIndexKeyPrefix) {
				continue
			}
			id := strings.TrimPrefix(key, mcpApprovalKeyPrefix)
			rec, err := s.Get(ctx, id)
			if err != nil {
				continue
			}
			if wantStatus != "" && strings.ToLower(string(rec.Status)) != wantStatus {
				continue
			}
			out = append(out, rec)
			if int64(len(out)) >= limit {
				break
			}
		}
		next = c
		if next == 0 {
			break
		}
	}
	return out, next, nil
}

// SweepExpired transitions every still-PENDING approval whose
// ExpiresAt has passed to EXPIRED. Intended to be invoked from the
// existing reaper loop; safe to call concurrently with Resolve because
// each record is updated under CAS — if Resolve races ahead, sweep sees
// the record is no longer PENDING and leaves it alone.
//
// Returns the number of records transitioned.
func (s *MCPApprovalStore) SweepExpired(ctx context.Context, now time.Time) (int, error) {
	if s == nil || s.client == nil {
		return 0, nil
	}
	cursor := uint64(0)
	matched := 0
	deadline := now.UnixMicro()
	for {
		keys, next, err := s.client.Scan(ctx, cursor, mcpApprovalKeyPrefix+"*", 100).Result()
		if err != nil {
			return matched, fmt.Errorf("mcp approval: scan: %w", err)
		}
		for _, key := range keys {
			if strings.HasPrefix(key, mcpApprovalIndexKeyPrefix) {
				continue
			}
			id := strings.TrimPrefix(key, mcpApprovalKeyPrefix)
			expired, rec := s.expireOne(ctx, id, deadline)
			if expired && rec != nil {
				matched++
				s.pruneIndexFor(ctx, rec)
				s.emitAudit(rec, "expired", "", audit.SeverityMedium)
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return matched, nil
}

// expireOne CAS-transitions a single PENDING record to EXPIRED if its
// ExpiresAt has passed. Returns (true, rec) on transition, (false, nil)
// otherwise. Shared by SweepExpired; kept on the store so unit tests
// can exercise the CAS directly if needed.
func (s *MCPApprovalStore) expireOne(ctx context.Context, id string, deadline int64) (bool, *MCPApprovalRecord) {
	key := mcpApprovalKey(id)
	var (
		expired bool
		final   *MCPApprovalRecord
	)
	txFn := func(tx *redis.Tx) error {
		raw, err := tx.Get(ctx, key).Bytes()
		if err != nil {
			return err
		}
		var rec MCPApprovalRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return err
		}
		if rec.Status != model.ApprovalStatusPending {
			return nil
		}
		if rec.ExpiresAt > deadline {
			return nil
		}
		next := rec
		next.Status = model.ApprovalStatusExpired
		next.ResolvedAt = deadline
		next.Decision = model.ApprovalDecisionExpire
		newRaw, err := json.Marshal(&next)
		if err != nil {
			return err
		}
		ttl, ttlErr := tx.PTTL(ctx, key).Result()
		if ttlErr != nil {
			return ttlErr
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, newRaw, preservedSetTTL(ttl))
			return nil
		})
		if err == nil {
			expired = true
			final = &next
		}
		return err
	}
	// task-c7e419d8: Redis CAS retry loop extracted into redisutil.Retry.
	// SweepExpired is best-effort: any non-retryable error aborts without
	// propagating (the caller retries on the next sweep tick). Budget
	// exhaustion falls through with whatever state the closure last set.
	if err := redisutil.Retry(ctx, s.client, txFn,
		redisutil.WithKeys(key),
		redisutil.WithMaxAttempts(mcpCASMaxAttempts),
	); err != nil && !errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		return false, nil
	}
	return expired, final
}

// pruneIndexFor removes the approval's ID from its per-tuple index.
// Called after terminal transitions (reject, expire, consume) so the
// index stays proportional to PENDING+APPROVED work, not retry history.
func (s *MCPApprovalStore) pruneIndexFor(ctx context.Context, rec *MCPApprovalRecord) {
	if s == nil || s.client == nil || rec == nil {
		return
	}
	idxKey := mcpApprovalIndexKey(rec.Tenant, rec.AgentID, rec.ToolName, rec.ArgsHash)
	_ = s.client.SRem(ctx, idxKey, rec.ID).Err()
}

// preservedSetTTL maps a PTTL observation to the duration to pass to
// Set: positive → preserve, -1 (no TTL) → keep no-expiry, -2 (missing)
// → shouldn't happen after a successful Get but treat as no-expiry to
// avoid a silent TTL reset to 5 minutes. The prior implementation's
// `if ttl <= 0 { ttl = default }` was a latent bug: a key intentionally
// written without TTL would be given a 5-minute lifetime on every
// mutation.
func preservedSetTTL(ttl time.Duration) time.Duration {
	if ttl > 0 {
		return ttl
	}
	return 0
}

// recordIsTerminal reports whether the record's status is one from
// which no further meaningful transition is possible for pre-approval
// purposes. Consumed APPROVED records are terminal because their
// single-use has been spent.
func recordIsTerminal(rec *MCPApprovalRecord) bool {
	if rec == nil {
		return false
	}
	switch rec.Status {
	case model.ApprovalStatusRejected, model.ApprovalStatusExpired:
		return true
	case model.ApprovalStatusApproved:
		return rec.ConsumedAt != 0
	}
	return false
}

// mcpApprovalKey returns the canonical Redis key for an approval record.
func mcpApprovalKey(id string) string { return mcpApprovalKeyPrefix + id }

// mcpApprovalIndexKey returns the per-tuple lookup index key. All
// components are url-style colon-joined; Redis treats colons as
// arbitrary characters so we don't need to escape them.
func mcpApprovalIndexKey(tenant, agent, tool, argsHash string) string {
	return mcpApprovalIndexKeyPrefix + tenant + ":" + agent + ":" + tool + ":" + argsHash
}

// mcpApprovalID returns a 16-byte hex-encoded identifier (32 chars).
// crypto/rand because approval IDs end up in audit logs and the
// dashboard URL — guessable IDs would be a harvesting risk.
func mcpApprovalID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
