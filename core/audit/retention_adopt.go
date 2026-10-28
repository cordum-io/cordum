package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrAdoptRefused is returned when AdoptRetentionAnchor does not write the
// anchor; the message says why.
var ErrAdoptRefused = errors.New("retention anchor not adopted")

// AdoptAnchorRequest asks AdoptRetentionAnchor to accept a chain's missing
// prefix as trimmed.
type AdoptAnchorRequest struct {
	// ExpectedOldestSeq is the seq of the oldest retained event the caller
	// saw (verify's first_seq). Adoption is refused when the chain starts
	// anywhere else.
	ExpectedOldestSeq int64
	// ReplaceInvalid allows replacing an anchor that does not anchor the
	// chain. A valid anchor is never replaced.
	ReplaceInvalid bool
	// Actor is who adopts, recorded in the chain event.
	Actor string
}

// AdoptAnchorResult is the adopted anchor and what it replaced.
type AdoptAnchorResult struct {
	Tenant          string `json:"tenant"`
	AdoptedSeq      int64  `json:"adopted_seq"`
	AdoptedHash     string `json:"adopted_hash"`
	AdoptedStreamID string `json:"adopted_stream_id"`
	// PreviousAnchor is the replaced anchor value, "" when there was none.
	PreviousAnchor string `json:"previous_anchor"`
	// PreviousAnchorState is none, malformed, hmac_mismatch or unlinked.
	PreviousAnchorState string `json:"previous_anchor_state"`
	// EventSeq is the seq of the chain event recording the adoption.
	EventSeq int64 `json:"event_seq"`
}

// AdoptRetentionAnchor writes the retention anchor a chain trimmed outside
// the retention sweeper (by hand, or by max-length eviction) lacks, so
// verify reads the missing prefix as retention_trimmed rather than missing.
// The anchor is the oldest retained event's predecessor: seq oldest-1, the
// oldest event's prev_hash, and the stream ID just below its entry, signed
// when the chain has an HMAC key.
//
// It is an explicit operator action, never automatic: the caller names the
// oldest retained seq it expects, and an existing anchor is replaced only
// when it is invalid and the caller asks to. The adoption is appended to the
// chain (actor, adopted seq, previous anchor and its state) before the
// anchor is written, so none goes unrecorded; a write that then fails
// leaves that event behind for an adoption that did not happen.
func (c *Chainer) AdoptRetentionAnchor(ctx context.Context, tenant string, req AdoptAnchorRequest) (*AdoptAnchorResult, error) {
	if req.ExpectedOldestSeq < 2 {
		return nil, fmt.Errorf("%w: expected oldest seq must be at least 2; a chain that starts at seq 1 is not trimmed", ErrAdoptRefused)
	}
	if strings.TrimSpace(req.Actor) == "" {
		return nil, errors.New("adopt retention anchor: actor required")
	}
	anchorKey := c.AnchorKey(tenant)
	// Read the anchor before the stream. The sweeper moves the anchor before
	// (Redis Cluster) or with (single node) every trim, so a trim after this
	// read fails the swap below and one before it shows in the stream read.
	raw, err := getRetentionAnchor(ctx, c.client, anchorKey)
	if err != nil {
		return nil, err
	}
	entries, err := c.client.XRangeN(ctx, c.StreamKey(tenant), "-", "+", 1).Result()
	if err != nil {
		return nil, fmt.Errorf("read chain: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: the chain is empty", ErrAdoptRefused)
	}
	var oldest SIEMEvent
	payload, _ := entries[0].Values[chainStreamFieldEvent].(string)
	if err := json.Unmarshal([]byte(payload), &oldest); err != nil {
		return nil, fmt.Errorf("%w: the oldest chain entry %s is malformed", ErrAdoptRefused, entries[0].ID)
	}
	if oldest.Seq != req.ExpectedOldestSeq {
		return nil, fmt.Errorf("%w: the oldest retained event is seq %d, not %d", ErrAdoptRefused, oldest.Seq, req.ExpectedOldestSeq)
	}
	adopted := retentionAnchor{Seq: oldest.Seq - 1, Hash: oldest.PrevHash, StreamID: xRevBefore(entries[0].ID)}
	if !isHexHash(adopted.Hash) || adopted.StreamID == "+" {
		return nil, fmt.Errorf("%w: the oldest chain entry %s has no usable prev_hash or stream ID", ErrAdoptRefused, entries[0].ID)
	}

	state := "none"
	if raw != "" {
		if state = c.anchorProblem(anchorKey, raw, oldest); state == "" {
			return nil, fmt.Errorf("%w: the retention anchor already links the oldest retained event", ErrAdoptRefused)
		}
		if state == "pending" {
			// The sweep that wrote it may still trim the stream under it.
			return nil, fmt.Errorf("%w: a retention trim is in progress; the next sweep finishes it", ErrAdoptRefused)
		}
		if !req.ReplaceInvalid {
			return nil, fmt.Errorf("%w: the retention anchor is invalid (%s); ask to replace it", ErrAdoptRefused, state)
		}
	}

	event := &SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: EventRetentionAnchorAdopted,
		Severity:  SeverityHigh,
		TenantID:  tenant,
		Action:    "retention_anchor_adopt",
		Identity:  req.Actor,
		Extra: map[string]string{
			"adopted_seq":           strconv.FormatInt(adopted.Seq, 10),
			"adopted_stream_id":     adopted.StreamID,
			"previous_anchor":       raw,
			"previous_anchor_state": state,
		},
	}
	if err := c.Append(ctx, event); err != nil {
		return nil, fmt.Errorf("record the adoption: %w", err)
	}
	if err := swapAnchor(ctx, c.client, anchorKey, raw, c.signAnchor(anchorKey, adopted)); err != nil {
		if errors.Is(err, errAnchorMoved) {
			return nil, fmt.Errorf("%w: the retention anchor changed meanwhile (a retention sweep?); verify again and retry", ErrAdoptRefused)
		}
		return nil, err
	}
	return &AdoptAnchorResult{
		Tenant:              tenant,
		AdoptedSeq:          adopted.Seq,
		AdoptedHash:         adopted.Hash,
		AdoptedStreamID:     adopted.StreamID,
		PreviousAnchor:      raw,
		PreviousAnchorState: state,
		EventSeq:            event.Seq,
	}, nil
}

// anchorProblem says why the anchor value raw does not anchor a chain whose
// oldest retained event is oldest, as verify would see it: malformed,
// hmac_mismatch, unlinked, or pending (a cluster trim not yet confirmed).
// It returns "" for an anchor that does.
func (c *Chainer) anchorProblem(anchorKey, raw string, oldest SIEMEvent) string {
	a, err := parseRetentionAnchor(raw)
	switch {
	case err != nil:
		return "malformed"
	case len(c.hmacKey) > 0 && !a.validMAC(anchorKey, c.hmacKey):
		return "hmac_mismatch"
	case a.Pending:
		return "pending"
	case oldest.Seq != a.Seq+1 || oldest.PrevHash != a.Hash:
		return "unlinked"
	}
	return ""
}
