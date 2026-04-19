package mcp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Pagination envelope for read-only list tools (task-466b6a6a).
//
// Every list tool accepts an opaque cursor string and a page_size.
// PageSize is bounded; oversized requests are clamped to DefaultMaxPageSize
// so a misbehaving client can't pull the whole store in one call.
// Cursor is base64-encoded JSON so the server can encode whatever
// continuation state it needs (offset, last-id, Redis SCAN cursor…)
// without the client having to understand the shape. The client's only
// contract is "pass back exactly what you received" to resume.

const (
	// DefaultPageSize is the fallback when the caller omits page_size.
	DefaultPageSize = 50
	// DefaultMaxPageSize is the ceiling applied after caller input.
	DefaultMaxPageSize = 500
)

// ErrInvalidCursor is returned by DecodeCursor when the input is not
// base64 or does not decode to the expected JSON shape.
var ErrInvalidCursor = errors.New("mcp: invalid pagination cursor")

// NormalizePageSize applies the default/cap so every caller sees the
// same bounds. Zero or negative → DefaultPageSize. Above the cap →
// DefaultMaxPageSize. Otherwise returned unchanged.
func NormalizePageSize(requested int) int {
	if requested <= 0 {
		return DefaultPageSize
	}
	if requested > DefaultMaxPageSize {
		return DefaultMaxPageSize
	}
	return requested
}

// CursorPayload is the structured shape every bridge encodes inside an
// opaque cursor. Additive fields are ignored on decode so bridges can
// evolve without breaking clients mid-page.
type CursorPayload struct {
	Offset    int    `json:"offset,omitempty"`
	LastID    string `json:"last_id,omitempty"`
	ScanAfter string `json:"scan_after,omitempty"`
}

// EncodeCursor serialises a CursorPayload to a base64 opaque string.
// Empty payloads produce "" (not "e30=") so callers can skip emitting
// next_cursor entirely on the final page.
func EncodeCursor(p CursorPayload) (string, error) {
	if p == (CursorPayload{}) {
		return "", nil
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodeCursor parses a base64 cursor produced by EncodeCursor. An
// empty input returns a zero CursorPayload with no error so callers can
// treat "" as "first page". Malformed inputs return ErrInvalidCursor.
func DecodeCursor(raw string) (CursorPayload, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CursorPayload{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		// Tolerate std padding too — some clients do not use the
		// URL-safe alphabet.
		data, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return CursorPayload{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
	}
	var p CursorPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return CursorPayload{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	return p, nil
}

// PaginateSlice is a helper for in-memory bridges that already have the
// full slice materialised. Returns the requested page plus a cursor
// pointing at the next offset. On the last page NextCursor is empty.
func PaginateSlice(items []map[string]any, cursor string, pageSize int) (*ListPage, error) {
	pageSize = NormalizePageSize(pageSize)
	p, err := DecodeCursor(cursor)
	if err != nil {
		return nil, err
	}
	offset := p.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	out := &ListPage{
		Items: append([]map[string]any(nil), items[offset:end]...),
		Total: len(items),
	}
	if end < len(items) {
		next, err := EncodeCursor(CursorPayload{Offset: end})
		if err != nil {
			return nil, err
		}
		out.NextCursor = next
	}
	return out, nil
}
