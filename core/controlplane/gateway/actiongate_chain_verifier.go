package gateway

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/policy/actiongates"
	"github.com/redis/go-redis/v9"
)

var errAuditChainVerifierUnavailable = errors.New("audit chain verifier unavailable")

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
	result, err := auditVerifyChainFn(ctx, v.client, streamKey, opts)
	if err != nil {
		return actiongates.ChainVerifyOutcome{}, err
	}
	return chainOutcomeFromVerifyResult(result, len(opts.HMACKey) > 0), nil
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
