package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

const approvalCASMaxAttempts = 5

func edgeApprovalKey(approvalRef string) string {
	return "edge:approvals:" + strings.TrimSpace(approvalRef)
}

func edgeApprovalTenantIndexKey(tenantID string) string {
	return "edge:approvals:index:tenant:" + strings.TrimSpace(tenantID)
}

func edgeApprovalStatusIndexKey(tenantID string, status ApprovalStatus) string {
	return "edge:approvals:index:status:" + strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(string(status))
}

func edgeApprovalPrincipalIndexKey(tenantID, principalID string) string {
	return "edge:approvals:index:principal:" + strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(principalID)
}

func edgeApprovalPrincipalStatusIndexKey(tenantID, principalID string, status ApprovalStatus) string {
	return "edge:approvals:index:principal_status:" + strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(principalID) + ":" + strings.TrimSpace(string(status))
}

func edgeApprovalListIndexKey(tenantID, principalID string, status ApprovalStatus) string {
	switch {
	case strings.TrimSpace(principalID) != "" && status != "":
		return edgeApprovalPrincipalStatusIndexKey(tenantID, principalID, status)
	case strings.TrimSpace(principalID) != "":
		return edgeApprovalPrincipalIndexKey(tenantID, principalID)
	case status != "":
		return edgeApprovalStatusIndexKey(tenantID, status)
	default:
		return edgeApprovalTenantIndexKey(tenantID)
	}
}

func edgeApprovalTupleIndexKey(tenantID, sessionID, executionID, actionHash string) string {
	return "edge:approvals:index:tuple:" + strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(sessionID) + ":" + strings.TrimSpace(executionID) + ":" + strings.TrimSpace(actionHash)
}

func (s *RedisStore) EnqueueApproval(ctx context.Context, req EdgeApprovalRequest) (*EdgeApproval, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	req = normalizeApprovalRequest(req)
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if err := s.validateApprovalRequestParents(ctx, req); err != nil {
		return nil, err
	}

	tupleKey := edgeApprovalTupleIndexKey(req.TenantID, req.SessionID, req.ExecutionID, req.ActionHash)
	var result *EdgeApproval
	err := redisutil.Retry(ctx, s.client, func(tx *redis.Tx) error {
		if err := s.validateApprovalRequestParentsTx(ctx, tx, req); err != nil {
			return err
		}
		existing, err := s.firstLiveTupleApproval(ctx, tx, tupleKey, req)
		if err != nil {
			return err
		}
		if existing != nil {
			result = existing
			return nil
		}

		ref, err := GenerateApprovalRef()
		if err != nil {
			return err
		}
		now := s.now().UTC()
		expiresAt := req.ExpiresAt.UTC()
		if expiresAt.IsZero() {
			ttl := req.TTL
			if ttl <= 0 {
				ttl = defaultApprovalTTL
			}
			expiresAt = now.Add(ttl)
		}
		if expiresAt.Before(now) {
			return fmt.Errorf("expires_at must be >= created_at")
		}

		approval := EdgeApproval{
			ApprovalRef:    ref,
			TenantID:       req.TenantID,
			SessionID:      req.SessionID,
			ExecutionID:    req.ExecutionID,
			EventID:        req.EventID,
			PrincipalID:    req.PrincipalID,
			Requester:      req.Requester,
			Status:         ApprovalStatusPending,
			Reason:         req.Reason,
			RuleID:         req.RuleID,
			PolicySnapshot: req.PolicySnapshot,
			ActionHash:     req.ActionHash,
			InputHash:      req.InputHash,
			CreatedAt:      now,
			ExpiresAt:      &expiresAt,
			Labels:         cloneLabels(req.Labels),
			Metadata:       cloneMetadata(req.Metadata),
		}
		if err := approval.Validate(); err != nil {
			return fmt.Errorf("validate edge approval %s: %w", ref, err)
		}
		payload, err := json.Marshal(approval)
		if err != nil {
			return fmt.Errorf("marshal edge approval %s: %w", ref, err)
		}
		score := float64(approval.CreatedAt.UnixMicro())
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, edgeApprovalKey(ref), payload, 0)
			pipe.ZAdd(ctx, edgeApprovalTenantIndexKey(req.TenantID), redis.Z{Score: score, Member: ref})
			pipe.ZAdd(ctx, edgeApprovalStatusIndexKey(req.TenantID, ApprovalStatusPending), redis.Z{Score: score, Member: ref})
			pipe.ZAdd(ctx, edgeApprovalPrincipalIndexKey(req.TenantID, req.PrincipalID), redis.Z{Score: score, Member: ref})
			pipe.ZAdd(ctx, edgeApprovalPrincipalStatusIndexKey(req.TenantID, req.PrincipalID, ApprovalStatusPending), redis.Z{Score: score, Member: ref})
			pipe.SAdd(ctx, tupleKey, ref)
			return nil
		})
		if err == nil {
			result = &approval
		}
		return err
	}, redisutil.WithKeys(tupleKey, edgeSessionKey(req.SessionID), edgeExecutionKey(req.ExecutionID), edgeEventsKey(req.ExecutionID)), redisutil.WithMaxAttempts(approvalCASMaxAttempts))
	if err != nil && !errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		// EDGE-058 — emit the bounded fail-closed counter at the EnqueueApproval
		// boundary so the dashboard surfaces the abort regardless of which
		// inner validator (validateApprovalRequestParentsTx → loadEventFromTx)
		// surfaced ErrEventListTooLarge.
		if errors.Is(err, ErrEventListTooLarge) {
			s.recorder.RecordApprovalEnqueueAborted("event_list_too_large")
		}
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("%w: enqueue approval conflict", ErrApprovalConflict)
	}
	return result, nil
}

func (s *RedisStore) GetApproval(ctx context.Context, tenantID, approvalRef string) (*EdgeApproval, bool, error) {
	if err := s.ensureReady(); err != nil {
		return nil, false, err
	}
	tenantID = strings.TrimSpace(tenantID)
	approval, ok, err := s.loadApproval(ctx, approvalRef)
	if err != nil || !ok {
		return nil, ok, err
	}
	if approval.TenantID != tenantID {
		return nil, false, nil
	}
	return approval, true, nil
}

func (s *RedisStore) ListApprovals(ctx context.Context, query ListApprovalsQuery) (ApprovalPage, error) {
	if err := s.ensureReady(); err != nil {
		return ApprovalPage{}, err
	}
	tenantID := strings.TrimSpace(query.TenantID)
	if tenantID == "" {
		return ApprovalPage{}, fmt.Errorf("tenant_id is required")
	}
	principalID := strings.TrimSpace(query.PrincipalID)
	start, err := parseApprovalOffsetCursor(query.Cursor)
	if err != nil {
		return ApprovalPage{}, err
	}
	limit := normalizeStoreLimit(query.Limit)

	var refs []string
	if hasApprovalTupleQuery(query) {
		refs, err = s.client.SMembers(ctx, edgeApprovalTupleIndexKey(tenantID, query.SessionID, query.ExecutionID, query.ActionHash)).Result()
		if err != nil {
			return ApprovalPage{}, fmt.Errorf("list edge approval tuple index: %w", err)
		}
	} else {
		indexKey := edgeApprovalListIndexKey(tenantID, principalID, query.Status)
		refs, err = s.client.ZRevRange(ctx, indexKey, int64(start), int64(start+limit)).Result()
		if err != nil {
			return ApprovalPage{}, fmt.Errorf("list edge approvals index %s: %w", indexKey, err)
		}
		hasMore := len(refs) > limit
		if hasMore {
			refs = refs[:limit]
		}
		items := make([]EdgeApproval, 0, len(refs))
		for _, ref := range refs {
			approval, ok, err := s.loadApproval(ctx, ref)
			if err != nil {
				return ApprovalPage{}, err
			}
			if !ok || approval.TenantID != tenantID {
				continue
			}
			if query.Status != "" && approval.Status != query.Status {
				continue
			}
			if principalID != "" && approval.PrincipalID != principalID {
				continue
			}
			items = append(items, *approval)
		}
		sort.SliceStable(items, func(i, j int) bool {
			if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
				return items[i].CreatedAt.After(items[j].CreatedAt)
			}
			return items[i].ApprovalRef < items[j].ApprovalRef
		})
		page := ApprovalPage{Items: items}
		if hasMore {
			page.NextCursor = strconv.Itoa(start + limit)
		}
		return page, nil
	}

	items := make([]EdgeApproval, 0, len(refs))
	for _, ref := range refs {
		approval, ok, err := s.loadApproval(ctx, ref)
		if err != nil {
			return ApprovalPage{}, err
		}
		if !ok || approval.TenantID != tenantID {
			continue
		}
		if query.Status != "" && approval.Status != query.Status {
			continue
		}
		if principalID != "" && approval.PrincipalID != principalID {
			continue
		}
		if hasApprovalTupleQuery(query) && !approvalMatchesTuple(*approval, query.SessionID, query.ExecutionID, query.ActionHash) {
			continue
		}
		items = append(items, *approval)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ApprovalRef < items[j].ApprovalRef
	})
	return pageApprovals(items, start, limit), nil
}

func (s *RedisStore) ApproveApproval(ctx context.Context, req ApprovalResolution) (*EdgeApproval, error) {
	return s.resolveApproval(ctx, req, ApprovalDecisionApprove)
}

func (s *RedisStore) RejectApproval(ctx context.Context, req ApprovalResolution) (*EdgeApproval, error) {
	return s.resolveApproval(ctx, req, ApprovalDecisionReject)
}

func (s *RedisStore) ClaimApproval(ctx context.Context, req ApprovalClaimRequest) (*EdgeApproval, bool, error) {
	if err := s.ensureReady(); err != nil {
		return nil, false, err
	}
	req = normalizeApprovalClaimRequest(req)
	if err := req.Validate(); err != nil {
		return nil, false, err
	}

	key := edgeApprovalKey(req.ApprovalRef)
	var (
		result   *EdgeApproval
		claimed  bool
		claimErr error
	)
	err := redisutil.Retry(ctx, s.client, func(tx *redis.Tx) error {
		approval, err := loadApprovalFromTx(ctx, tx, req.ApprovalRef)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				claimErr = ErrNotFound
				return nil
			}
			return err
		}
		if approval.TenantID != req.TenantID {
			claimErr = ErrNotFound
			return nil
		}
		if approval.Status != ApprovalStatusApproved || approval.ConsumedAt != nil {
			result = nil
			claimed = false
			return nil
		}
		consumedAt := req.ConsumedAt.UTC()
		if consumedAt.IsZero() {
			consumedAt = s.now().UTC()
		}
		if approval.ExpiresAt != nil && consumedAt.After(*approval.ExpiresAt) {
			claimErr = fmt.Errorf("%w: approval expired", ErrApprovalConflict)
			return nil
		}
		if !approvalClaimMatches(approval, req) {
			claimErr = fmt.Errorf("%w: approval tuple mismatch", ErrApprovalConflict)
			return nil
		}
		if err := s.validateApprovalRecordActionableTx(ctx, tx, *approval); err != nil {
			claimErr = err
			return nil
		}

		next := *approval
		next.ConsumedAt = &consumedAt
		if err := next.Validate(); err != nil {
			return err
		}
		payload, err := json.Marshal(next)
		if err != nil {
			return err
		}
		ttl, err := tx.PTTL(ctx, key).Result()
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, payload, preservedApprovalSetTTL(ttl))
			pipe.SRem(ctx, edgeApprovalTupleIndexKey(next.TenantID, next.SessionID, next.ExecutionID, next.ActionHash), next.ApprovalRef)
			// EDGE-062 — consumed approvals are no longer actionable; remove
			// from the tenant index so list-without-filter returns only
			// the active set (Pending + Approved-not-yet-consumed).
			pipe.ZRem(ctx, edgeApprovalTenantIndexKey(next.TenantID), next.ApprovalRef)
			return nil
		})
		if err == nil {
			claimed = true
			result = &next
		}
		return err
	}, redisutil.WithKeys(key, edgeSessionKey(req.SessionID), edgeExecutionKey(req.ExecutionID), edgeEventsKey(req.ExecutionID)), redisutil.WithMaxAttempts(approvalCASMaxAttempts))
	if err != nil && !errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		return nil, false, err
	}
	if claimErr != nil {
		return nil, false, claimErr
	}
	return result, claimed, nil
}

func (s *RedisStore) ExpireApprovals(ctx context.Context, tenantID string, now time.Time) (int, error) {
	if err := s.ensureReady(); err != nil {
		return 0, err
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return 0, fmt.Errorf("tenant_id is required")
	}
	if now.IsZero() {
		now = s.now().UTC()
	} else {
		now = now.UTC()
	}
	refs, err := s.client.ZRange(ctx, edgeApprovalStatusIndexKey(tenantID, ApprovalStatusPending), 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("list pending edge approvals: %w", err)
	}
	expired := 0
	for _, ref := range refs {
		ok, err := s.expireApproval(ctx, tenantID, ref, now)
		if err != nil {
			return expired, err
		}
		if ok {
			expired++
		}
	}
	return expired, nil
}

func (s *RedisStore) resolveApproval(ctx context.Context, req ApprovalResolution, decision ApprovalDecision) (*EdgeApproval, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	req = normalizeApprovalResolution(req)
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if decision != ApprovalDecisionApprove && decision != ApprovalDecisionReject {
		return nil, fmt.Errorf("unsupported approval decision %q", decision)
	}

	key := edgeApprovalKey(req.ApprovalRef)
	pre, ok, err := s.GetApproval(ctx, req.TenantID, req.ApprovalRef)
	if err != nil {
		return nil, err
	}
	if !ok || pre == nil {
		return nil, ErrNotFound
	}
	var (
		result     *EdgeApproval
		resolveErr error
	)
	err = redisutil.Retry(ctx, s.client, func(tx *redis.Tx) error {
		approval, err := loadApprovalFromTx(ctx, tx, req.ApprovalRef)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				resolveErr = ErrNotFound
				return nil
			}
			return err
		}
		if approval.TenantID != req.TenantID {
			resolveErr = ErrNotFound
			return nil
		}
		if approval.Status != ApprovalStatusPending {
			resolveErr = fmt.Errorf("%w: approval is not pending", ErrApprovalConflict)
			return nil
		}
		resolvedAt := req.ResolvedAt.UTC()
		if resolvedAt.IsZero() {
			resolvedAt = s.now().UTC()
		}
		if approval.ExpiresAt != nil && resolvedAt.After(*approval.ExpiresAt) {
			resolveErr = fmt.Errorf("%w: approval expired", ErrApprovalConflict)
			return nil
		}
		if err := s.validateApprovalRecordActionableTx(ctx, tx, *approval); err != nil {
			resolveErr = err
			return nil
		}

		next := *approval
		next.ResolvedAt = &resolvedAt
		next.ResolverID = req.ResolverID
		next.ResolvedBy = req.ResolvedBy
		next.Decision = decision
		next.ResolutionReason = req.Reason
		if strings.TrimSpace(next.ResolutionReason) == "" {
			next.ResolutionReason = string(decision)
		}
		if decision == ApprovalDecisionApprove {
			next.Status = ApprovalStatusApproved
		} else {
			next.Status = ApprovalStatusRejected
		}
		if err := next.Validate(); err != nil {
			return err
		}
		payload, err := json.Marshal(next)
		if err != nil {
			return err
		}
		ttl, err := tx.PTTL(ctx, key).Result()
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, payload, preservedApprovalSetTTL(ttl))
			pipe.ZRem(ctx, edgeApprovalStatusIndexKey(next.TenantID, ApprovalStatusPending), next.ApprovalRef)
			pipe.ZAdd(ctx, edgeApprovalStatusIndexKey(next.TenantID, next.Status), redis.Z{Score: float64(next.CreatedAt.UnixMicro()), Member: next.ApprovalRef})
			pipe.ZRem(ctx, edgeApprovalPrincipalStatusIndexKey(next.TenantID, next.PrincipalID, ApprovalStatusPending), next.ApprovalRef)
			pipe.ZAdd(ctx, edgeApprovalPrincipalStatusIndexKey(next.TenantID, next.PrincipalID, next.Status), redis.Z{Score: float64(next.CreatedAt.UnixMicro()), Member: next.ApprovalRef})
			// EDGE-062 — terminal-state transitions remove from the tenant
			// index so list-without-filter returns only the active set.
			pipe.ZRem(ctx, edgeApprovalTenantIndexKey(next.TenantID), next.ApprovalRef)
			if next.Status == ApprovalStatusRejected {
				pipe.SRem(ctx, edgeApprovalTupleIndexKey(next.TenantID, next.SessionID, next.ExecutionID, next.ActionHash), next.ApprovalRef)
			}
			return nil
		})
		if err == nil {
			result = &next
		}
		return err
	}, redisutil.WithKeys(key, edgeSessionKey(pre.SessionID), edgeExecutionKey(pre.ExecutionID), edgeEventsKey(pre.ExecutionID)), redisutil.WithMaxAttempts(approvalCASMaxAttempts))
	if err != nil && !errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		return nil, err
	}
	if resolveErr != nil {
		return nil, resolveErr
	}
	if result == nil {
		return nil, fmt.Errorf("%w: resolve approval conflict", ErrApprovalConflict)
	}
	return result, nil
}

func (s *RedisStore) expireApproval(ctx context.Context, tenantID, approvalRef string, now time.Time) (bool, error) {
	key := edgeApprovalKey(approvalRef)
	var (
		expired   bool
		expireErr error
	)
	err := redisutil.Retry(ctx, s.client, func(tx *redis.Tx) error {
		approval, err := loadApprovalFromTx(ctx, tx, approvalRef)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return nil
			}
			return err
		}
		if approval.TenantID != tenantID || approval.Status != ApprovalStatusPending {
			return nil
		}
		if approval.ExpiresAt == nil || approval.ExpiresAt.After(now) {
			return nil
		}
		next := *approval
		resolvedAt := now.UTC()
		next.Status = ApprovalStatusExpired
		next.Decision = ApprovalDecisionExpire
		next.ResolverID = "system"
		next.ResolvedBy = "system"
		next.ResolutionReason = "approval expired"
		next.ResolvedAt = &resolvedAt
		if err := next.Validate(); err != nil {
			expireErr = err
			return nil
		}
		payload, err := json.Marshal(next)
		if err != nil {
			return err
		}
		ttl, err := tx.PTTL(ctx, key).Result()
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, payload, preservedApprovalSetTTL(ttl))
			pipe.ZRem(ctx, edgeApprovalStatusIndexKey(next.TenantID, ApprovalStatusPending), next.ApprovalRef)
			pipe.ZAdd(ctx, edgeApprovalStatusIndexKey(next.TenantID, ApprovalStatusExpired), redis.Z{Score: float64(next.CreatedAt.UnixMicro()), Member: next.ApprovalRef})
			pipe.ZRem(ctx, edgeApprovalPrincipalStatusIndexKey(next.TenantID, next.PrincipalID, ApprovalStatusPending), next.ApprovalRef)
			pipe.ZAdd(ctx, edgeApprovalPrincipalStatusIndexKey(next.TenantID, next.PrincipalID, ApprovalStatusExpired), redis.Z{Score: float64(next.CreatedAt.UnixMicro()), Member: next.ApprovalRef})
			// EDGE-062 — terminal-state transitions remove from the tenant
			// index so list-without-filter returns only the active set.
			pipe.ZRem(ctx, edgeApprovalTenantIndexKey(next.TenantID), next.ApprovalRef)
			pipe.SRem(ctx, edgeApprovalTupleIndexKey(next.TenantID, next.SessionID, next.ExecutionID, next.ActionHash), next.ApprovalRef)
			return nil
		})
		if err == nil {
			expired = true
		}
		return err
	}, redisutil.WithKeys(key), redisutil.WithMaxAttempts(approvalCASMaxAttempts))
	if err != nil && !errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		return false, err
	}
	if expireErr != nil {
		return false, expireErr
	}
	return expired, nil
}

func (s *RedisStore) firstLiveTupleApproval(ctx context.Context, tx *redis.Tx, tupleKey string, req EdgeApprovalRequest) (*EdgeApproval, error) {
	refs, err := tx.SMembers(ctx, tupleKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	sort.Strings(refs)
	for _, ref := range refs {
		approval, err := loadApprovalFromTx(ctx, tx, ref)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			return nil, err
		}
		if approval.TenantID != req.TenantID || approval.SessionID != req.SessionID || approval.ExecutionID != req.ExecutionID || approval.ActionHash != req.ActionHash {
			continue
		}
		switch approval.Status {
		case ApprovalStatusPending:
			return approval, nil
		case ApprovalStatusApproved:
			if approval.ConsumedAt == nil {
				return approval, nil
			}
		}
	}
	return nil, nil
}

func (s *RedisStore) validateApprovalRequestParents(ctx context.Context, req EdgeApprovalRequest) error {
	session, ok, err := s.GetSession(ctx, req.TenantID, req.SessionID)
	if err != nil {
		return err
	}
	if !ok || session == nil {
		return fmt.Errorf("%w: edge session %s", ErrNotFound, req.SessionID)
	}
	if isTerminalSessionStatus(session.Status) || session.EndedAt != nil {
		return fmt.Errorf("%w: edge session is not actionable", ErrApprovalConflict)
	}
	execution, ok, err := s.GetExecution(ctx, req.TenantID, req.ExecutionID)
	if err != nil {
		return err
	}
	if !ok || execution == nil {
		return fmt.Errorf("%w: agent execution %s", ErrNotFound, req.ExecutionID)
	}
	if execution.SessionID != req.SessionID {
		return fmt.Errorf("%w: execution does not belong to session", ErrApprovalConflict)
	}
	if isTerminalExecutionStatus(execution.Status) || execution.EndedAt != nil {
		return fmt.Errorf("%w: edge execution is not actionable", ErrApprovalConflict)
	}
	event, ok, err := s.findEvent(ctx, req.TenantID, req.SessionID, req.ExecutionID, req.EventID)
	if err != nil {
		return err
	}
	if !ok || event == nil {
		return fmt.Errorf("%w: edge approval event %s", ErrNotFound, req.EventID)
	}
	if err := validateApprovalEventBinding(req.PolicySnapshot, req.InputHash, event.PolicySnapshot, event.InputHash); err != nil {
		return err
	}
	return nil
}

func (s *RedisStore) validateApprovalRequestParentsTx(ctx context.Context, tx *redis.Tx, req EdgeApprovalRequest) error {
	candidate := EdgeApproval{
		TenantID:       req.TenantID,
		SessionID:      req.SessionID,
		ExecutionID:    req.ExecutionID,
		EventID:        req.EventID,
		PolicySnapshot: req.PolicySnapshot,
		InputHash:      req.InputHash,
	}
	return s.validateApprovalRecordActionableTx(ctx, tx, candidate)
}

func (s *RedisStore) validateApprovalRecordActionableTx(ctx context.Context, tx *redis.Tx, approval EdgeApproval) error {
	session, err := loadSessionFromTx(ctx, tx, approval.SessionID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return fmt.Errorf("%w: edge session %s", ErrNotFound, approval.SessionID)
		}
		return err
	}
	if session.TenantID != approval.TenantID {
		return fmt.Errorf("%w: edge session %s", ErrNotFound, approval.SessionID)
	}
	if isTerminalSessionStatus(session.Status) || session.EndedAt != nil {
		return fmt.Errorf("%w: edge session is not actionable", ErrApprovalConflict)
	}
	if strings.TrimSpace(session.PolicySnapshot) != "" && session.PolicySnapshot != approval.PolicySnapshot {
		return fmt.Errorf("%w: policy snapshot mismatch", ErrApprovalConflict)
	}

	execution, err := loadExecutionFromTx(ctx, tx, approval.ExecutionID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return fmt.Errorf("%w: agent execution %s", ErrNotFound, approval.ExecutionID)
		}
		return err
	}
	if execution.TenantID != approval.TenantID {
		return fmt.Errorf("%w: agent execution %s", ErrNotFound, approval.ExecutionID)
	}
	if execution.SessionID != approval.SessionID {
		return fmt.Errorf("%w: execution does not belong to session", ErrApprovalConflict)
	}
	if isTerminalExecutionStatus(execution.Status) || execution.EndedAt != nil {
		return fmt.Errorf("%w: edge execution is not actionable", ErrApprovalConflict)
	}
	if strings.TrimSpace(execution.PolicySnapshot) != "" && execution.PolicySnapshot != approval.PolicySnapshot {
		return fmt.Errorf("%w: policy snapshot mismatch", ErrApprovalConflict)
	}

	event, ok, err := loadEventFromTx(ctx, tx, approval)
	if err != nil {
		return err
	}
	if !ok || event == nil {
		return fmt.Errorf("%w: edge approval event %s is missing", ErrApprovalConflict, approval.EventID)
	}
	return validateApprovalEventBinding(approval.PolicySnapshot, approval.InputHash, event.PolicySnapshot, event.InputHash)
}

func (s *RedisStore) findEvent(ctx context.Context, tenantID, sessionID, executionID, eventID string) (*AgentActionEvent, bool, error) {
	cursor := ""
	for {
		page, err := s.listEventsForExecutionPage(ctx, ListEventsQuery{TenantID: tenantID, SessionID: sessionID}, executionID, cursor, maxStorePageLimit)
		if err != nil {
			return nil, false, err
		}
		for i := range page.Items {
			if page.Items[i].EventID == eventID && page.Items[i].TenantID == tenantID && page.Items[i].SessionID == sessionID && page.Items[i].ExecutionID == executionID {
				return &page.Items[i], true, nil
			}
		}
		if page.NextCursor == "" {
			return nil, false, nil
		}
		cursor = page.NextCursor
	}
}

func loadEventFromTx(ctx context.Context, tx *redis.Tx, approval EdgeApproval) (*AgentActionEvent, bool, error) {
	// EDGE-058 — bound the inline read so a runaway execution event list
	// cannot pin gateway memory or starve EXEC for healthy executions sharing
	// the same Redis connection. LLEN is O(1); a list above the cap aborts
	// before any large LRange allocation. Below the cap, LRange uses an
	// inclusive upper index so requesting `0, cap-1` reads at most `cap`
	// entries.
	eventsKey := edgeEventsKey(approval.ExecutionID)
	size, err := tx.LLen(ctx, eventsKey).Result()
	if err != nil {
		return nil, false, err
	}
	if size > maxEventsPerApprovalValidation {
		return nil, false, fmt.Errorf("%w: execution=%s events=%d cap=%d", ErrEventListTooLarge, approval.ExecutionID, size, maxEventsPerApprovalValidation)
	}
	rawEvents, err := tx.LRange(ctx, eventsKey, 0, maxEventsPerApprovalValidation-1).Result()
	if err != nil {
		return nil, false, err
	}
	for i, raw := range rawEvents {
		var event AgentActionEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, false, fmt.Errorf("unmarshal agent action event %s[%d]: %w", approval.ExecutionID, i, err)
		}
		if event.EventID == approval.EventID && event.TenantID == approval.TenantID && event.SessionID == approval.SessionID && event.ExecutionID == approval.ExecutionID {
			return &event, true, nil
		}
	}
	return nil, false, nil
}

func validateApprovalEventBinding(wantPolicy, wantInput, gotPolicy, gotInput string) error {
	if strings.TrimSpace(gotPolicy) != "" && strings.TrimSpace(wantPolicy) != strings.TrimSpace(gotPolicy) {
		return fmt.Errorf("%w: policy snapshot mismatch", ErrApprovalConflict)
	}
	if strings.TrimSpace(gotInput) != "" && strings.TrimSpace(wantInput) != strings.TrimSpace(gotInput) {
		return fmt.Errorf("%w: input hash mismatch", ErrApprovalConflict)
	}
	return nil
}

func (s *RedisStore) loadApproval(ctx context.Context, approvalRef string) (*EdgeApproval, bool, error) {
	approvalRef = strings.TrimSpace(approvalRef)
	if approvalRef == "" {
		return nil, false, nil
	}
	raw, err := s.client.Get(ctx, edgeApprovalKey(approvalRef)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get edge approval %s: %w", approvalRef, err)
	}
	var approval EdgeApproval
	if err := json.Unmarshal(raw, &approval); err != nil {
		return nil, false, fmt.Errorf("unmarshal edge approval %s: %w", approvalRef, err)
	}
	return &approval, true, nil
}

func loadApprovalFromTx(ctx context.Context, tx *redis.Tx, approvalRef string) (*EdgeApproval, error) {
	raw, err := tx.Get(ctx, edgeApprovalKey(approvalRef)).Bytes()
	if err != nil {
		return nil, err
	}
	var approval EdgeApproval
	if err := json.Unmarshal(raw, &approval); err != nil {
		return nil, fmt.Errorf("unmarshal edge approval %s: %w", approvalRef, err)
	}
	return &approval, nil
}

func loadSessionFromTx(ctx context.Context, tx *redis.Tx, sessionID string) (*EdgeSession, error) {
	raw, err := tx.Get(ctx, edgeSessionKey(sessionID)).Bytes()
	if err != nil {
		return nil, err
	}
	var session EdgeSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return nil, fmt.Errorf("unmarshal edge session %s: %w", sessionID, err)
	}
	return &session, nil
}

func loadExecutionFromTx(ctx context.Context, tx *redis.Tx, executionID string) (*AgentExecution, error) {
	raw, err := tx.Get(ctx, edgeExecutionKey(executionID)).Bytes()
	if err != nil {
		return nil, err
	}
	var execution AgentExecution
	if err := json.Unmarshal(raw, &execution); err != nil {
		return nil, fmt.Errorf("unmarshal agent execution %s: %w", executionID, err)
	}
	return &execution, nil
}

func normalizeApprovalRequest(req EdgeApprovalRequest) EdgeApprovalRequest {
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.ExecutionID = strings.TrimSpace(req.ExecutionID)
	req.EventID = strings.TrimSpace(req.EventID)
	req.PrincipalID = strings.TrimSpace(req.PrincipalID)
	req.Requester = strings.TrimSpace(req.Requester)
	req.Reason = strings.TrimSpace(req.Reason)
	req.RuleID = strings.TrimSpace(req.RuleID)
	req.PolicySnapshot = strings.TrimSpace(req.PolicySnapshot)
	req.ActionHash = strings.TrimSpace(req.ActionHash)
	req.InputHash = strings.TrimSpace(req.InputHash)
	if !req.ExpiresAt.IsZero() {
		req.ExpiresAt = req.ExpiresAt.UTC()
	}
	req.Labels = cloneLabels(req.Labels)
	req.Metadata = cloneMetadata(req.Metadata)
	return req
}

func normalizeApprovalResolution(req ApprovalResolution) ApprovalResolution {
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.ApprovalRef = strings.TrimSpace(req.ApprovalRef)
	req.ResolverID = strings.TrimSpace(req.ResolverID)
	req.ResolvedBy = strings.TrimSpace(req.ResolvedBy)
	req.Reason = strings.TrimSpace(req.Reason)
	if !req.ResolvedAt.IsZero() {
		req.ResolvedAt = req.ResolvedAt.UTC()
	}
	return req
}

func normalizeApprovalClaimRequest(req ApprovalClaimRequest) ApprovalClaimRequest {
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.ApprovalRef = strings.TrimSpace(req.ApprovalRef)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.ExecutionID = strings.TrimSpace(req.ExecutionID)
	req.EventID = strings.TrimSpace(req.EventID)
	req.ActionHash = strings.TrimSpace(req.ActionHash)
	req.InputHash = strings.TrimSpace(req.InputHash)
	req.PolicySnapshot = strings.TrimSpace(req.PolicySnapshot)
	if !req.ConsumedAt.IsZero() {
		req.ConsumedAt = req.ConsumedAt.UTC()
	}
	return req
}

func hasApprovalTupleQuery(query ListApprovalsQuery) bool {
	return strings.TrimSpace(query.SessionID) != "" &&
		strings.TrimSpace(query.ExecutionID) != "" &&
		strings.TrimSpace(query.ActionHash) != ""
}

func approvalMatchesTuple(approval EdgeApproval, sessionID, executionID, actionHash string) bool {
	return approval.SessionID == strings.TrimSpace(sessionID) &&
		approval.ExecutionID == strings.TrimSpace(executionID) &&
		approval.ActionHash == strings.TrimSpace(actionHash)
}

func approvalClaimMatches(approval *EdgeApproval, req ApprovalClaimRequest) bool {
	if approval == nil {
		return false
	}
	return approval.SessionID == req.SessionID &&
		approval.ExecutionID == req.ExecutionID &&
		approval.EventID == req.EventID &&
		approval.ActionHash == req.ActionHash &&
		approval.InputHash == req.InputHash &&
		approval.PolicySnapshot == req.PolicySnapshot
}

func pageApprovals(items []EdgeApproval, start, limit int) ApprovalPage {
	if start >= len(items) {
		return ApprovalPage{Items: []EdgeApproval{}}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := ApprovalPage{Items: append([]EdgeApproval(nil), items[start:end]...)}
	if end < len(items) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page
}

func cloneMetadata(in Metadata) Metadata {
	if len(in) == 0 {
		return nil
	}
	out := make(Metadata, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func preservedApprovalSetTTL(ttl time.Duration) time.Duration {
	if ttl > 0 {
		return ttl
	}
	return 0
}
