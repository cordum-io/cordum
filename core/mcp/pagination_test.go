package mcp

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizePageSize(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultPageSize},
		{-5, DefaultPageSize},
		{1, 1},
		{50, 50},
		{DefaultMaxPageSize, DefaultMaxPageSize},
		{DefaultMaxPageSize + 1, DefaultMaxPageSize},
		{10000, DefaultMaxPageSize},
	}
	for _, c := range cases {
		if got := NormalizePageSize(c.in); got != c.want {
			t.Errorf("NormalizePageSize(%d) = %d want %d", c.in, got, c.want)
		}
	}
}

func TestEncodeDecodeCursor_RoundTrip(t *testing.T) {
	in := CursorPayload{Offset: 42, LastID: "abc"}
	raw, err := EncodeCursor(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if raw == "" {
		t.Fatal("non-empty payload encoded to empty string")
	}
	out, err := DecodeCursor(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch: %+v -> %+v", in, out)
	}
}

func TestEncodeCursor_EmptyProducesEmpty(t *testing.T) {
	raw, err := EncodeCursor(CursorPayload{})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if raw != "" {
		t.Errorf("empty payload should encode to empty string, got %q", raw)
	}
}

func TestDecodeCursor_EmptyOK(t *testing.T) {
	p, err := DecodeCursor("")
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if p != (CursorPayload{}) {
		t.Errorf("empty cursor decoded to %+v want zero", p)
	}
}

func TestDecodeCursor_MalformedRejects(t *testing.T) {
	if _, err := DecodeCursor("!!!not-base64!!!"); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("want ErrInvalidCursor, got %v", err)
	}
	// Valid base64 but not JSON.
	if _, err := DecodeCursor(strings.TrimRight("bm90LWpzb24=", "=")); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("want ErrInvalidCursor on non-json, got %v", err)
	}
}

func TestPaginateSlice_FirstPage(t *testing.T) {
	items := makeItems(120)
	page, err := PaginateSlice(items, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 50 {
		t.Errorf("items = %d", len(page.Items))
	}
	if page.Total != 120 {
		t.Errorf("total = %d", page.Total)
	}
	if page.NextCursor == "" {
		t.Error("expected NextCursor on non-final page")
	}
}

func TestPaginateSlice_MiddlePage(t *testing.T) {
	items := makeItems(120)
	first, _ := PaginateSlice(items, "", 50)
	page, err := PaginateSlice(items, first.NextCursor, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 50 {
		t.Errorf("middle page items = %d", len(page.Items))
	}
	if page.Items[0]["id"] != "item-50" {
		t.Errorf("first id = %v", page.Items[0]["id"])
	}
}

func TestPaginateSlice_LastPage(t *testing.T) {
	items := makeItems(120)
	first, _ := PaginateSlice(items, "", 50)
	mid, _ := PaginateSlice(items, first.NextCursor, 50)
	last, err := PaginateSlice(items, mid.NextCursor, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Items) != 20 {
		t.Errorf("last page items = %d want 20", len(last.Items))
	}
	if last.NextCursor != "" {
		t.Errorf("last page should not carry NextCursor, got %q", last.NextCursor)
	}
}

func TestPaginateSlice_EmptyResult(t *testing.T) {
	page, err := PaginateSlice(nil, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Errorf("items = %d", len(page.Items))
	}
	if page.NextCursor != "" {
		t.Errorf("empty result should have no cursor, got %q", page.NextCursor)
	}
	if page.Total != 0 {
		t.Errorf("total = %d", page.Total)
	}
}

func TestPaginateSlice_OffsetBeyondLength(t *testing.T) {
	items := makeItems(10)
	cursor, _ := EncodeCursor(CursorPayload{Offset: 500})
	page, err := PaginateSlice(items, cursor, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Errorf("past-end page should be empty, got %d items", len(page.Items))
	}
}

func makeItems(n int) []map[string]any {
	items := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, map[string]any{"id": "item-" + intStr(i)})
	}
	return items
}

func intStr(i int) string {
	if i == 0 {
		return "0"
	}
	buf := []byte{}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
