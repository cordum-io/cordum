package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

// VerifyStatus is the high-level outcome of a chain verification walk.
type VerifyStatus string

const (
	// VerifyStatusOK means every event in the requested range verified.
	VerifyStatusOK VerifyStatus = "ok"
	// VerifyStatusCompromised means at least one event failed hash
	// recomputation, its PrevHash did not match its predecessor, or the
	// seq numbers skipped a value that falls after the retention boundary.
	VerifyStatusCompromised VerifyStatus = "compromised"
	// VerifyStatusPartial means nothing failed verification but the walk
	// is incomplete: the retention sweeper deleted events in range (every
	// gap is retention_trimmed), or the window's predecessor failed its own
	// hash check so the first event could not be linked.
	VerifyStatusPartial VerifyStatus = "partial"
)

// VerifyGapType distinguishes how the chain broke at a given seq.
type VerifyGapType string

const (
	// GapTypeMissing is a seq absent from the stream where its
	// predecessor was present. Strong tamper signal after retention.
	GapTypeMissing VerifyGapType = "missing"
	// GapTypeOutOfOrder is a seq observed before its predecessor.
	GapTypeOutOfOrder VerifyGapType = "out_of_order"
	// GapTypeHashMismatch is a seq whose EventHash does not match the
	// canonical recomputation, or whose PrevHash does not link to the
	// previous event. Strongest tamper signal.
	GapTypeHashMismatch VerifyGapType = "hash_mismatch"
	// GapTypeRetentionTrimmed is a seq the retention sweeper deleted: at or
	// below the retention anchor. Expected, not tampering.
	GapTypeRetentionTrimmed VerifyGapType = "retention_trimmed"
	// GapTypeHMACMismatch is a seq whose HMAC-SHA256 tag does not match
	// the recomputed value. Indicates the event was modified or forged
	// by a process without the signing key. Only flagged when an HMAC
	// key is supplied via VerifyOptions.
	GapTypeHMACMismatch VerifyGapType = "hmac_mismatch"
)

// VerifyGap is a single gap in the chain walk. AtSeq is the position the
// gap was observed at; for missing events it's the seq that SHOULD have
// been present; for hash mismatches it's the seq whose hash was wrong.
type VerifyGap struct {
	AtSeq int64         `json:"at_seq"`
	Type  VerifyGapType `json:"type"`
}

// VerifyResult is the response payload for /api/v1/audit/verify. It
// intentionally excludes raw event content — the endpoint is for
// integrity reporting, not audit retrieval.
//
// retention_boundary_seq is the lowest seq that should still be present:
// the retention anchor's seq + 1 once the sweeper has trimmed the chain.
// Gaps strictly below it are retention_trimmed (expected log expiry); gaps at
// or above are missing (suspected tampering). Without an anchor nothing was
// trimmed, so every absent seq is missing and the field echoes
// VerifyOptions.RetentionBoundarySeq (callers pass the lowest seq in the
// stream).
//
// retention_window_hours is the effective retention window (see
// RetentionWindow; omitted when unlimited) so a caller consuming the JSON
// has everything needed to reason about whether the oldest present entry
// is within policy. The field is a float64 so fractional-hour windows
// (test fixtures) render cleanly.
type VerifyResult struct {
	Status               VerifyStatus `json:"status"`
	TotalEvents          int          `json:"total_events"`
	VerifiedEvents       int          `json:"verified_events"`
	Gaps                 []VerifyGap  `json:"gaps"`
	RetentionBoundarySeq int64        `json:"retention_boundary_seq"`
	RetentionWindowHours float64      `json:"retention_window_hours,omitempty"`
	FirstSeq             int64        `json:"first_seq,omitempty"`
	LastSeq              int64        `json:"last_seq,omitempty"`
	// HMACVerified counts events whose HMAC-SHA256 tag was recomputed
	// and matched. Zero when no HMACKey was supplied in VerifyOptions.
	HMACVerified int `json:"hmac_verified,omitempty"`
	// HMACSkipped counts events that carry no HMAC tag (pre-HMAC
	// events). These are not treated as failures — backward compat.
	HMACSkipped int `json:"hmac_skipped,omitempty"`
	// HMACSeen is true when at least one event in the scanned range
	// carried an HMAC tag. Used by the verify handler to detect chains
	// that need HMAC verification even when the current process has no
	// key configured (fail-closed safety net).
	HMACSeen bool `json:"hmac_seen,omitempty"`
}

// maxGapRun caps how many gap entries the walker emits for one run of
// absent seqs (the retention_trimmed prefix, or missing seqs between two
// events). Beyond this, a trimmed prefix is summarised by
// RetentionBoundarySeq and a missing run by its first entries, so the
// response payload stays bounded however many events are gone.
const maxGapRun = 1000

// VerifyOptions narrows a verification walk.
type VerifyOptions struct {
	// SinceMs / UntilMs bound the Redis Stream ID range. Zero means no
	// bound. XADD-generated IDs embed a millisecond timestamp, so the
	// filter is native to the stream.
	SinceMs int64
	UntilMs int64
	// Limit caps the number of stream entries read. Zero means use the
	// default (10000). The caller is expected to enforce the ceiling
	// before constructing options.
	Limit int64
	// RetentionBoundarySeq (optional) is reported as
	// VerifyResult.RetentionBoundarySeq when the chain has no retention
	// anchor. It does not change how gaps are classified.
	RetentionBoundarySeq int64
	// RetentionAnchorKey is the tenant's retention anchor key
	// (Chainer.AnchorKey), read in the same snapshot as the range (on Redis
	// Cluster just before and after it, see verifyChainCluster). Only
	// seqs up to the anchor count as retention_trimmed: the oldest retained
	// event must be anchor+1 and link to the anchor's hash, or be the
	// genesis event when there is no anchor. Leaving it empty therefore
	// makes a trimmed chain verify as compromised. With HMACKey set the
	// anchor must carry a valid MAC. A pending anchor (a Redis Cluster trim
	// not yet confirmed) counts as the anchor it replaces.
	RetentionAnchorKey string
	// HMACKey (optional) enables HMAC-SHA256 verification. When non-nil,
	// every event that carries an HMAC tag is verified against this key.
	// Events without an HMAC (pre-HMAC era) are counted as HMACSkipped
	// rather than failed, so mixed chains are gracefully handled during
	// key rotation or initial HMAC rollout.
	HMACKey []byte
}

// DefaultVerifyLimit / MaxVerifyLimit bound how much of the chain a single
// verify call will read. Exposed so the HTTP handler can apply them
// consistently and so tests can refer to the same numbers.
const (
	DefaultVerifyLimit = int64(10_000)
	MaxVerifyLimit     = int64(100_000)
)

// VerifyChain walks a tenant's audit chain stream and reports integrity.
// The caller supplies the Redis client and stream key so this helper can
// be invoked from the HTTP handler (with the gateway's shared client) or
// from tests (with miniredis).
//
// Never returns raw event contents; the result is a compact integrity
// report suitable for audit dashboards and CI exit codes.
func VerifyChain(ctx context.Context, client redis.UniversalClient, streamKey string, opts VerifyOptions) (*VerifyResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = DefaultVerifyLimit
	}
	if opts.Limit > MaxVerifyLimit {
		opts.Limit = MaxVerifyLimit
	}

	minID := "-"
	if opts.SinceMs > 0 {
		minID = strconv.FormatInt(opts.SinceMs, 10) + "-0"
	}
	maxID := "+"
	if opts.UntilMs > 0 {
		// XRANGE is inclusive; append the maximum sequence part so
		// callers filtering by whole-ms boundaries include every entry
		// produced in the final millisecond.
		maxID = strconv.FormatInt(opts.UntilMs, 10) + "-18446744073709551615"
	}
	if redisutil.IsCluster(client) {
		return verifyChainCluster(ctx, client, streamKey, opts, minID, maxID)
	}

	// Read the range, the window's predecessor and the retention anchor in
	// one MULTI/EXEC: a retention trim landing between separate reads would
	// look like missing or reordered events. The predecessor of the first
	// in-range entry is the newest entry before minID.
	pipe := client.TxPipeline()
	rangeCmd := pipe.XRangeN(ctx, streamKey, minID, maxID, opts.Limit)
	var predCmd *redis.XMessageSliceCmd
	if opts.SinceMs > 0 {
		predCmd = pipe.XRevRangeN(ctx, streamKey, xRevBefore(minID), "-", 1)
	}
	var anchorCmd *redis.StringCmd
	if opts.RetentionAnchorKey != "" {
		anchorCmd = pipe.Get(ctx, opts.RetentionAnchorKey)
	}
	// redis.Nil only means "no anchor yet" (GET of a missing key).
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read chain %s: %w", streamKey, err)
	}
	rawAnchor := ""
	if anchorCmd != nil {
		rawAnchor = anchorCmd.Val()
	}
	var predecessors []redis.XMessage
	if predCmd != nil {
		predecessors = predCmd.Val()
	}
	return walkChain(rangeCmd.Val(), predecessors, rawAnchor, opts)
}

// walkChain verifies the entries read for a window (predecessors holds the
// newest entry before a SinceMs window, if any) against the retention anchor
// value read with them.
func walkChain(entries, predecessors []redis.XMessage, rawAnchor string, opts VerifyOptions) (*VerifyResult, error) {
	anchor, err := parseRetentionAnchor(rawAnchor)
	if err != nil {
		return nil, fmt.Errorf("parse retention anchor: %w", err)
	}
	// link is what the oldest retained event links to: the anchor itself,
	// or for a pending (unconfirmed) cluster trim the anchor it replaces.
	link := anchor.linked()

	result := &VerifyResult{
		Status:               VerifyStatusOK,
		Gaps:                 []VerifyGap{},
		RetentionBoundarySeq: opts.RetentionBoundarySeq,
		HMACSeen:             anchor.MAC != "",
	}
	if link.Seq > 0 {
		result.RetentionBoundarySeq = link.Seq + 1
	}

	if len(entries) == 0 {
		// No events in range. Not compromised — just empty. Status
		// remains OK. Callers can disambiguate by checking total_events.
		// Exception: the sweeper never trims the chain head, so an
		// unbounded walk of a trimmed chain that finds nothing means the
		// stream itself was deleted.
		if anchor.Seq > 0 && opts.SinceMs == 0 && opts.UntilMs == 0 {
			result.Status = VerifyStatusCompromised
			result.Gaps = append(result.Gaps, VerifyGap{AtSeq: link.Seq + 1, Type: GapTypeMissing})
		}
		return result, nil
	}

	var (
		prevHash string
		prevSeq  int64
		hasPrev  bool
	)
	// firstIsOldest: the first walked entry is the oldest one retained in
	// the stream, so its predecessor is the retention anchor (or genesis).
	firstIsOldest := opts.SinceMs == 0
	// Cross-window linkage bootstrap: when the caller asked for a
	// mid-chain slice (SinceMs > 0), read the single entry immediately
	// preceding the first in-range event so the first linkage check
	// isn't a free pass. Without this, an attacker mutating the
	// PrevHash of the first event in the window goes undetected.
	if opts.SinceMs > 0 {
		if len(predecessors) > 0 {
			if payload, ok := predecessors[0].Values[chainStreamFieldEvent].(string); ok {
				var pred SIEMEvent
				if err := json.Unmarshal([]byte(payload), &pred); err == nil {
					// We trust the stored EventHash only after recomputing —
					// otherwise a compromised bootstrap event would silently
					// authenticate a compromised first-in-range event.
					if ok, err := VerifyEventHash(&pred); err == nil && ok {
						prevHash = pred.EventHash
						prevSeq = pred.Seq
						hasPrev = true
					} else {
						// Predecessor hash is itself broken. Down-grade
						// the overall result to Partial so operators see
						// that the window isn't fully attested.
						result.Status = VerifyStatusPartial
					}
				}
			}
		} else {
			// No retained entry precedes the window.
			firstIsOldest = true
		}
	}
	if firstIsOldest {
		// Only the sweeper, which holds the HMAC key, may move the anchor;
		// one without a valid MAC explains nothing.
		if anchor.Seq > 0 && len(opts.HMACKey) > 0 && !anchor.validMAC(opts.RetentionAnchorKey, opts.HMACKey) {
			result.Status = VerifyStatusCompromised
			result.Gaps = append(result.Gaps, VerifyGap{AtSeq: anchor.Seq, Type: GapTypeHMACMismatch})
			result.RetentionBoundarySeq = opts.RetentionBoundarySeq
			anchor, link = retentionAnchor{}, retentionAnchor{}
		}
		// Link the oldest retained event to the newest trimmed one, or to
		// genesis (seq 0, empty PrevHash) when there is no anchor: only the
		// sweeper deletes chain events and it records each trim in the
		// anchor, so a prefix absent without an anchor was deleted. A seq
		// other than link+1, or a PrevHash other than the link hash, is
		// reported by the ordinary gap/linkage checks below.
		prevSeq, prevHash, hasPrev = link.Seq, link.Hash, true
		// A window that starts at or before the newest trimmed event lost
		// events to retention.
		if opts.SinceMs > 0 && link.Seq > 0 && link.streamMs() >= opts.SinceMs {
			result.Gaps = append(result.Gaps, VerifyGap{AtSeq: link.Seq, Type: GapTypeRetentionTrimmed})
		}
	}
	for i, entry := range entries {
		payload, ok := entry.Values[chainStreamFieldEvent].(string)
		if !ok {
			result.Status = VerifyStatusCompromised
			result.Gaps = append(result.Gaps, VerifyGap{AtSeq: prevSeq + 1, Type: GapTypeHashMismatch})
			continue
		}
		var ev SIEMEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			result.Status = VerifyStatusCompromised
			result.Gaps = append(result.Gaps, VerifyGap{AtSeq: prevSeq + 1, Type: GapTypeHashMismatch})
			continue
		}
		result.TotalEvents++

		if i == 0 {
			result.FirstSeq = ev.Seq
			// Pre-walk retention prefix: seqs up to the anchor were
			// deleted by the sweeper. Emit them as retention_trimmed gaps
			// up to a cap so long-running systems don't produce unbounded
			// payloads; absent seqs above the anchor are reported missing
			// by the linkage check.
			if opts.SinceMs == 0 {
				for trimmed := int64(1); trimmed <= min(ev.Seq-1, link.Seq, maxGapRun); trimmed++ {
					result.Gaps = append(result.Gaps, VerifyGap{AtSeq: trimmed, Type: GapTypeRetentionTrimmed})
				}
			}
		}
		result.LastSeq = ev.Seq

		// Missing / out-of-order detection: every step must be prev+1.
		// Within the walk, every observed event has seq >= boundary
		// (since boundary = oldest present seq), so any gap between
		// consecutive in-walk events is strictly > boundary and
		// therefore real tampering, not retention expiry.
		if hasPrev {
			switch {
			case ev.Seq < prevSeq:
				result.Status = VerifyStatusCompromised
				result.Gaps = append(result.Gaps, VerifyGap{AtSeq: ev.Seq, Type: GapTypeOutOfOrder})
			case ev.Seq > prevSeq+1:
				result.Status = VerifyStatusCompromised
				for missing := prevSeq + 1; missing < min(ev.Seq, prevSeq+1+maxGapRun); missing++ {
					result.Gaps = append(result.Gaps, VerifyGap{AtSeq: missing, Type: GapTypeMissing})
				}
			}
		}

		// Hash recomputation. A pending anchor also names the event its
		// unconfirmed trim ends at; the stream's event at that seq must be it.
		ok, err := VerifyEventHash(&ev)
		if err != nil || !ok || (anchor.Pending && ev.Seq == anchor.Seq && ev.EventHash != anchor.Hash) {
			result.Status = VerifyStatusCompromised
			result.Gaps = append(result.Gaps, VerifyGap{AtSeq: ev.Seq, Type: GapTypeHashMismatch})
		} else if hasPrev && ev.PrevHash != prevHash {
			// Linkage check. Only meaningful when we have a prior
			// in-range event to compare to. Cross-window verification
			// (range starts mid-chain) cannot check this for the first
			// event without an extra Redis read — out of scope here.
			result.Status = VerifyStatusCompromised
			result.Gaps = append(result.Gaps, VerifyGap{AtSeq: ev.Seq, Type: GapTypeHashMismatch})
		} else {
			result.VerifiedEvents++
		}

		// HMAC verification (when key supplied and event carries a tag).
		if ev.HMAC != "" {
			result.HMACSeen = true
		}
		if len(opts.HMACKey) > 0 {
			if ev.HMAC == "" {
				result.HMACSkipped++
			} else {
				ok, err := VerifyEventHMAC(&ev, opts.HMACKey)
				if err != nil || !ok {
					result.Status = VerifyStatusCompromised
					result.Gaps = append(result.Gaps, VerifyGap{AtSeq: ev.Seq, Type: GapTypeHMACMismatch})
				} else {
					result.HMACVerified++
				}
			}
		}

		prevHash = ev.EventHash
		prevSeq = ev.Seq
		hasPrev = true
	}

	// Promote status from OK to Partial when every gap is retention-trimmed.
	if result.Status == VerifyStatusOK && len(result.Gaps) > 0 {
		result.Status = VerifyStatusPartial
	}
	return result, nil
}

// verifyClusterAttempts bounds how often a Redis Cluster verify re-reads.
const verifyClusterAttempts = 5

// errAnchorKeptMoving: retention trims kept moving the anchor while a Redis
// Cluster verify read the chain.
var errAnchorKeptMoving = errors.New("retention anchor kept changing while the chain was read; retry the verify")

// verifyChainCluster is VerifyChain on Redis Cluster, where the retention
// anchor and the stream hash to different slots and cannot be read in one
// MULTI. It reads the anchor, then the stream (one MULTI on the stream's
// slot), then the anchor again, and accepts a verdict that holds against
// either read. A compromised verdict is re-read, with backoff, when the
// anchor moved in between, or when the stream is already trimmed past a
// pending anchor, which a sweep confirms moments later in normal operation
// (see trimBatchCluster). Out of attempts, a verdict that kept failing
// against a moving anchor is an error rather than a verdict.
func verifyChainCluster(ctx context.Context, client redis.UniversalClient, streamKey string, opts VerifyOptions, minID, maxID string) (*VerifyResult, error) {
	var res *VerifyResult
	for attempt := 0; attempt < verifyClusterAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(10 * time.Millisecond << (attempt - 1)):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		before, err := getRetentionAnchor(ctx, client, opts.RetentionAnchorKey)
		if err != nil {
			return nil, err
		}
		pipe := client.TxPipeline()
		rangeCmd := pipe.XRangeN(ctx, streamKey, minID, maxID, opts.Limit)
		var predCmd *redis.XMessageSliceCmd
		if opts.SinceMs > 0 {
			predCmd = pipe.XRevRangeN(ctx, streamKey, xRevBefore(minID), "-", 1)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, fmt.Errorf("read chain %s: %w", streamKey, err)
		}
		var predecessors []redis.XMessage
		if predCmd != nil {
			predecessors = predCmd.Val()
		}
		after, err := getRetentionAnchor(ctx, client, opts.RetentionAnchorKey)
		if err != nil {
			return nil, err
		}
		if res, err = walkChain(rangeCmd.Val(), predecessors, before, opts); err != nil {
			return nil, err
		}
		if res.Status != VerifyStatusCompromised {
			return res, nil
		}
		if after != before {
			if moved, err := walkChain(rangeCmd.Val(), predecessors, after, opts); err == nil && moved.Status != VerifyStatusCompromised {
				return moved, nil
			}
			res = nil
			continue
		}
		if a, _ := parseRetentionAnchor(before); !a.Pending || res.FirstSeq != a.Seq+1 {
			return res, nil
		}
	}
	if res == nil {
		return nil, errAnchorKeptMoving
	}
	return res, nil
}

// getRetentionAnchor reads an anchor value; "" when key is empty or unset.
func getRetentionAnchor(ctx context.Context, client redis.UniversalClient, key string) (string, error) {
	if key == "" {
		return "", nil
	}
	raw, err := client.Get(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("read retention anchor: %w", err)
	}
	return raw, nil
}

// xRevBefore returns a Redis Stream ID usable as the XREVRANGE start
// argument that is strictly less than the given ID. Stream IDs have
// the form "<ms>-<seq>". Subtracting one from the seq (wrapping to
// the previous millisecond when seq == 0) gives the ID just before.
// On malformed input we fall back to "+" so the caller's XREVRANGE
// scans from the tail — safer than returning a bogus ID that could
// silently match the wrong entry.
func xRevBefore(id string) string {
	idx := strings.IndexByte(id, '-')
	if idx <= 0 || idx == len(id)-1 {
		return "+"
	}
	msPart := id[:idx]
	seqPart := id[idx+1:]
	ms, err := strconv.ParseInt(msPart, 10, 64)
	if err != nil {
		return "+"
	}
	seq, err := strconv.ParseInt(seqPart, 10, 64)
	if err != nil {
		return "+"
	}
	if seq > 0 {
		return strconv.FormatInt(ms, 10) + "-" + strconv.FormatInt(seq-1, 10)
	}
	if ms > 0 {
		return strconv.FormatInt(ms-1, 10) + "-18446744073709551615"
	}
	return "+"
}
