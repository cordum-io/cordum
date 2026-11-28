package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/replay"
)

// defaultProductionClaimLease is the bus default AckWait: JetStream redelivers
// a delivery that never settled once it passes.
const defaultProductionClaimLease = 10 * time.Minute

// claimVerified admits a verified packet through a replay store that keeps
// admission apart from completion. The accepted delivery owns the message
// until it settles or its lease ends. A delivery that meets a live lease is
// retried when that lease would end; one that meets a settled message is a
// duplicate.
func (b *ProductionRawBoundary) claimVerified(
	ctx context.Context,
	subject string,
	session AuthenticatedProductionSession,
	raw []byte,
	claims *replay.RedisReplayStore,
) bus.RawAdmissionResult {
	verified, replayExpiry, err := b.verify(ctx, subject, session, raw)
	if err != nil {
		return productionRawAdmissionError(err)
	}
	if !verified.IsVerified() {
		return bus.RawAdmissionResult{Disposition: bus.RawAdmissionRetry}
	}
	digest := verified.SignedBodyDigest()
	claim, err := claims.Claim(verified.Tenant(), verified.Subject(), verified.Sender(),
		verified.MessageID(), digest[:], replayExpiry, b.claimLease())
	if err != nil {
		return productionRawAdmissionError(replayAdmissionError(err))
	}
	switch claim.State {
	case replay.ClaimAcquired:
		result := acceptedProductionResult(verified, session)
		result.Settle = func(outcome bus.RawAdmissionOutcome) {
			if err := claims.Settle(claim, settleOutcome(outcome)); err != nil {
				slog.Warn("scheduler: replay settle failed", "subject", subject, "err", err)
			}
		}
		return result
	case replay.ClaimCompleted:
		return bus.RawAdmissionResult{Disposition: bus.RawAdmissionDuplicate}
	default:
		return bus.RawAdmissionResult{Disposition: bus.RawAdmissionRetry, RetryAfter: claim.RetryAfter}
	}
}

func (b *ProductionRawBoundary) claimLease() time.Duration {
	if b.ClaimLease > 0 {
		return b.ClaimLease
	}
	return defaultProductionClaimLease
}

// settleOutcome maps how a delivery ended to its replay record. Anything but
// success or a terminal failure releases the message for another delivery.
func settleOutcome(outcome bus.RawAdmissionOutcome) replay.SettleOutcome {
	switch outcome {
	case bus.RawAdmissionSucceeded:
		return replay.SettleSucceeded
	case bus.RawAdmissionFailed:
		return replay.SettleFailed
	default:
		return replay.SettleReleased
	}
}

// replayAdmissionError keeps a digest conflict and reports every other replay
// store failure as unavailable, so the delivery is retried.
func replayAdmissionError(err error) error {
	if errors.Is(err, capsdk.ErrReplayConflict) {
		return capsdk.ErrReplayConflict
	}
	return capsdk.ErrReplayStoreUnavailable
}
