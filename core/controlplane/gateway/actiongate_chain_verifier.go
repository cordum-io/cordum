package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/policy/actiongates"
	"github.com/redis/go-redis/v9"
)

var errAuditChainVerifierUnavailable = errors.New("audit chain verifier unavailable")

const (
	// approvalEvidenceScanMaxEvents is the shared guardrail for approval chain
	// verification plus exact approval-evidence lookup. It intentionally
	// exceeds audit's default one-page verify limit so busy tenants do not
	// false-deny when the approval event sits just past the first 10k entries,
	// while still bounding Redis stream work per verification.
	approvalEvidenceScanMaxEvents = audit.MaxVerifyLimit
	approvalEvidenceScanPageSize  = audit.DefaultVerifyLimit
)

type auditChainApprovalVerifier struct {
	client  redis.UniversalClient
	chainer *audit.Chainer
	now     func() time.Time
}

func newAuditChainApprovalVerifier(client redis.UniversalClient, chainer *audit.Chainer) *auditChainApprovalVerifier {
	return &auditChainApprovalVerifier{
		client:  client,
		chainer: chainer,
		now:     time.Now,
	}
}

func (v *auditChainApprovalVerifier) VerifyForApproval(
	ctx context.Context,
	tenant string,
	approval *edgecore.EdgeApproval,
) (actiongates.ChainVerifyOutcome, error) {
	if v == nil || v.client == nil || v.chainer == nil {
		return actiongates.ChainVerifyOutcome{}, errAuditChainVerifierUnavailable
	}
	if tenant == "" || approval == nil {
		return actiongates.ChainVerifyOutcome{}, fmt.Errorf("%w: missing tenant or approval", errAuditChainVerifierUnavailable)
	}
	streamKey := v.chainer.StreamKey(tenant)
	boundary, err := readRetentionBoundary(ctx, v.client, streamKey)
	if err != nil {
		return actiongates.ChainVerifyOutcome{}, fmt.Errorf("read retention boundary: %w", err)
	}
	opts := auditVerifyOptionsForApproval(approval, v.now())
	opts.RetentionBoundarySeq = boundary
	if v.chainer.HMACEnabled() {
		opts.HMACKey = v.chainer.HMACKeyForVerify()
	}
	opts.Limit = approvalEvidenceScanCap(opts)
	result, err := auditVerifyChainFn(ctx, v.client, streamKey, opts)
	if err != nil {
		return actiongates.ChainVerifyOutcome{}, err
	}
	outcome := chainOutcomeFromVerifyResult(result, len(opts.HMACKey) > 0)
	found, err := approvalEvidenceExists(ctx, v.client, streamKey, opts, approval)
	if err != nil {
		return actiongates.ChainVerifyOutcome{}, err
	}
	if !found {
		outcome.HasEvidenceGap = true
		if outcome.Status != actiongates.ChainStatusCompromised {
			outcome.Detail = "approval_evidence_missing:" + approvalRefDetailPrefix(approval.ApprovalRef)
		}
	}
	return outcome, nil
}

func auditVerifyOptionsForApproval(approval *edgecore.EdgeApproval, now time.Time) audit.VerifyOptions {
	end := maxApprovalBound(now.UTC(), approval.ResolvedAt, approval.ConsumedAt)
	start := approval.CreatedAt.UTC()
	if start.IsZero() || end.Sub(start) > maxVerifySinceUntilSpread {
		start = end.Add(-maxVerifySinceUntilSpread)
	}
	if end.Before(start) {
		end = start
	}
	return audit.VerifyOptions{
		SinceMs: unixMilliNonNegative(start),
		UntilMs: unixMilliNonNegative(end),
	}
}

func maxApprovalBound(now time.Time, bounds ...*time.Time) time.Time {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	max := now.UTC()
	for _, bound := range bounds {
		if bound == nil || bound.IsZero() {
			continue
		}
		if candidate := bound.UTC(); candidate.After(max) {
			max = candidate
		}
	}
	return max
}

func unixMilliNonNegative(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	ms := t.UTC().UnixNano() / int64(time.Millisecond)
	if ms < 0 {
		return 0
	}
	return ms
}

func chainOutcomeFromVerifyResult(result *audit.VerifyResult, hmacChecked bool) actiongates.ChainVerifyOutcome {
	if result == nil {
		return actiongates.ChainVerifyOutcome{Status: actiongates.ChainStatusCompromised, Detail: "nil_result"}
	}
	status := chainStatusFromAudit(normalizeVerifyStatus(result))
	if result.HMACSeen && !hmacChecked {
		status = actiongates.ChainStatusCompromised
	}
	return actiongates.ChainVerifyOutcome{
		Status:         status,
		HasEvidenceGap: verifyResultHasEvidenceGap(result),
		Detail:         verifyResultDetail(result),
	}
}

func normalizeVerifyStatus(result *audit.VerifyResult) audit.VerifyStatus {
	if result.Status == audit.VerifyStatusPartial && result.FirstSeq == 1 && len(result.Gaps) == 0 {
		return audit.VerifyStatusOK
	}
	return result.Status
}

func chainStatusFromAudit(status audit.VerifyStatus) actiongates.ChainStatus {
	switch status {
	case audit.VerifyStatusOK:
		return actiongates.ChainStatusOK
	case audit.VerifyStatusPartial:
		return actiongates.ChainStatusPartial
	case audit.VerifyStatusCompromised:
		return actiongates.ChainStatusCompromised
	default:
		return actiongates.ChainStatusCompromised
	}
}

func verifyResultHasEvidenceGap(result *audit.VerifyResult) bool {
	if result.TotalEvents == 0 {
		return true
	}
	for _, gap := range result.Gaps {
		if gap.Type == audit.GapTypeMissing || gap.Type == audit.GapTypeOutOfOrder {
			return true
		}
	}
	return false
}

func verifyResultDetail(result *audit.VerifyResult) string {
	if result.TotalEvents == 0 {
		return "no_events"
	}
	if len(result.Gaps) == 0 {
		return "events=" + strconv.Itoa(result.TotalEvents)
	}
	gap := result.Gaps[0]
	return "gap=" + string(gap.Type) + ":seq=" + strconv.FormatInt(gap.AtSeq, 10)
}

func approvalEvidenceExists(
	ctx context.Context,
	client redis.UniversalClient,
	streamKey string,
	opts audit.VerifyOptions,
	approval *edgecore.EdgeApproval,
) (bool, error) {
	if approval == nil || strings.TrimSpace(approval.ApprovalRef) == "" || strings.TrimSpace(approval.ActionHash) == "" {
		return false, nil
	}
	maxEvents := approvalEvidenceScanCap(opts)
	cursor := streamMinID(opts)
	maxID := streamMaxID(opts)
	var scanned int64
	for scanned < maxEvents {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("scan approval evidence: %w", err)
		}
		pageSize := approvalEvidenceScanPageSize
		if remaining := maxEvents - scanned; remaining < pageSize {
			pageSize = remaining
		}
		entries, err := client.XRangeN(ctx, streamKey, cursor, maxID, pageSize).Result()
		if err != nil {
			return false, fmt.Errorf("scan approval evidence: %w", err)
		}
		if len(entries) == 0 {
			return false, nil
		}
		for _, entry := range entries {
			scanned++
			if approvalEvidenceEntryMatches(entry, approval) {
				return true, nil
			}
			if scanned >= maxEvents {
				return false, nil
			}
		}
		cursor = "(" + entries[len(entries)-1].ID
	}
	return false, nil
}

func approvalEvidenceEntryMatches(entry redis.XMessage, approval *edgecore.EdgeApproval) bool {
	payload, ok := entry.Values["event"].(string)
	if !ok || payload == "" {
		return false
	}
	var event audit.SIEMEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return false
	}
	if !approvalEvidenceEventType(event.EventType) || strings.TrimSpace(event.TenantID) != strings.TrimSpace(approval.TenantID) {
		return false
	}
	if !approvalEvidenceApprovedDecision(event.Decision) {
		return false
	}
	return strings.TrimSpace(event.Extra["approval_ref"]) == strings.TrimSpace(approval.ApprovalRef) &&
		strings.TrimSpace(event.Extra["action_hash"]) == strings.TrimSpace(approval.ActionHash)
}

func approvalEvidenceEventType(eventType string) bool {
	return strings.TrimSpace(eventType) == audit.EventEdgeApprovalResolved
}

func approvalEvidenceApprovedDecision(decision string) bool {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "approved", "approve":
		return true
	default:
		return false
	}
}

func streamMinID(opts audit.VerifyOptions) string {
	if opts.SinceMs <= 0 {
		return "-"
	}
	return strconv.FormatInt(opts.SinceMs, 10) + "-0"
}

func streamMaxID(opts audit.VerifyOptions) string {
	if opts.UntilMs <= 0 {
		return "+"
	}
	return strconv.FormatInt(opts.UntilMs, 10) + "-18446744073709551615"
}

func approvalEvidenceScanCap(opts audit.VerifyOptions) int64 {
	if opts.Limit > 0 {
		return verifyScanLimit(opts)
	}
	return approvalEvidenceScanMaxEvents
}

func verifyScanLimit(opts audit.VerifyOptions) int64 {
	if opts.Limit <= 0 {
		return audit.DefaultVerifyLimit
	}
	if opts.Limit > audit.MaxVerifyLimit {
		return audit.MaxVerifyLimit
	}
	return opts.Limit
}

func approvalRefDetailPrefix(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "missing_ref"
	}
	if len(ref) > 16 {
		return ref[:16]
	}
	return ref
}
