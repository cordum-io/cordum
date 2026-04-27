package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

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
	// VerifyStatusPartial means the range contained only events older
	// than the retention boundary — any "gaps" are trimmed events, not
	// tampering.
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
	// GapTypeRetentionTrimmed is a seq absent because the retention
	// window has expired it — expected, not tampering.
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
// retention_boundary_seq is the lowest seq currently present in the
// stream. Gaps at seqs strictly below it are retention_trimmed (expected
// log expiry); gaps at or above are missing (suspected tampering). The
// boundary is reported even for intact chains so operators always have
// a concrete number to reason about.
//
// retention_window_hours mirrors CORDUM_AUDIT_RETENTION_HOURS so a
// caller consuming the JSON has everything needed to reason about
// whether the oldest present entry is within policy. The field is a
// float64 so fractional-hour windows (test fixtures) render cleanly.
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

// maxRetentionTrimmedGaps caps how many retention_trimmed gap entries
// the walker emits for a pre-first_seq prefix. Beyond this, the prefix
// is summarised by RetentionBoundarySeq alone so the response payload
// stays bounded on long-running systems.
const maxRetentionTrimmedGaps = 1000

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
	// RetentionBoundarySeq (optional) is the lowest seq the operator
	// expects to still be present. Gaps strictly below this value are
	// classified as retention_trimmed rather than tampering. Zero means
	// no boundary known — every gap is potential tampering.
	RetentionBoundarySeq int64
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

	entries, err := client.XRangeN(ctx, streamKey, minID, maxID, opts.Limit).Result()
	if err != nil {
		return nil, fmt.Errorf("xrange %s: %w", streamKey, err)
	}

	result := &VerifyResult{
		Status:               VerifyStatusOK,
		Gaps:                 []VerifyGap{},
		RetentionBoundarySeq: opts.RetentionBoundarySeq,
	}

	if len(entries) == 0 {
		// No events in range. Not compromised — just empty. Status
		// remains OK. Callers can disambiguate by checking total_events.
		return result, nil
	}

	var (
		prevHash string
		prevSeq  int64
		hasPrev  bool
	)
	// Cross-window linkage bootstrap: when the caller asked for a
	// mid-chain slice (SinceMs > 0), read the single entry immediately
	// preceding the first in-range event so the first linkage check
	// isn't a free pass. Without this, an attacker mutating the
	// PrevHash of the first event in the window goes undetected.
	if opts.SinceMs > 0 && len(entries) > 0 {
		firstID := entries[0].ID
		predecessors, perr := client.XRevRangeN(ctx, streamKey, xRevBefore(firstID), "-", 1).Result()
		if perr == nil && len(predecessors) > 0 {
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
		} else if perr == nil && len(predecessors) == 0 {
			// No predecessor inside retention. Status is Partial because
			// the first in-range PrevHash cannot be linkage-checked.
			result.Status = VerifyStatusPartial
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
			// Pre-walk retention prefix. When the caller did not supply
			// a since filter and the first seq we observe is > 1, seqs
			// 1..first_seq-1 are absent from the stream — retention
			// trimmed (or pre-chain legacy events). Emit them as
			// retention_trimmed gaps up to a cap so long-running systems
			// don't produce unbounded payloads.
			if opts.SinceMs == 0 && ev.Seq > 1 {
				emit := ev.Seq - 1
				if emit > maxRetentionTrimmedGaps {
					emit = maxRetentionTrimmedGaps
				}
				for missing := int64(1); missing <= emit; missing++ {
					result.Gaps = append(result.Gaps, VerifyGap{AtSeq: missing, Type: GapTypeRetentionTrimmed})
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
				for missing := prevSeq + 1; missing < ev.Seq; missing++ {
					result.Gaps = append(result.Gaps, VerifyGap{AtSeq: missing, Type: GapTypeMissing})
				}
			}
		}

		// Hash recomputation.
		ok, err := VerifyEventHash(&ev)
		if err != nil || !ok {
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
