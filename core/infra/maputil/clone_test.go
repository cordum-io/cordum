package maputil

import "testing"

func TestCloneStringMap_Nil(t *testing.T) {
	got := CloneStringMap(nil)
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestCloneStringMap_Empty(t *testing.T) {
	got := CloneStringMap(map[string]string{})
	if got == nil {
		t.Fatal("expected non-nil empty map")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestCloneStringMap_Values(t *testing.T) {
	orig := map[string]string{"a": "1", "b": "2"}
	got := CloneStringMap(orig)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got["a"] != "1" || got["b"] != "2" {
		t.Fatalf("unexpected values: %v", got)
	}
	// Mutating clone must not affect original.
	got["a"] = "changed"
	if orig["a"] != "1" {
		t.Fatal("mutation leaked to original")
	}
}

func TestCloneAnyMap_Nil(t *testing.T) {
	got := CloneAnyMap(nil)
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestCloneAnyMap_Empty(t *testing.T) {
	got := CloneAnyMap(map[string]any{})
	if got == nil {
		t.Fatal("expected non-nil empty map")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestCloneAnyMap_Values(t *testing.T) {
	orig := map[string]any{"x": 42, "y": "hello"}
	got := CloneAnyMap(orig)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got["x"] != 42 || got["y"] != "hello" {
		t.Fatalf("unexpected values: %v", got)
	}
	// Mutating clone must not affect original.
	got["x"] = 99
	if orig["x"] != 42 {
		t.Fatal("mutation leaked to original")
	}
}

func TestCloneAnyMap_ShallowCopy(t *testing.T) {
	inner := map[string]any{"nested": true}
	orig := map[string]any{"inner": inner}
	got := CloneAnyMap(orig)
	// Shallow copy: inner reference is shared.
	gotInner, ok := got["inner"].(map[string]any)
	if !ok {
		t.Fatal("expected inner map")
	}
	if gotInner["nested"] != true {
		t.Fatal("expected nested value")
	}
	// Verify it is indeed the same reference (shallow).
	inner["nested"] = false
	if gotInner["nested"] != false {
		t.Fatal("expected shallow copy to share inner reference")
	}
}

func TestDeepCloneAnyMap_Nil(t *testing.T) {
	got := DeepCloneAnyMap(nil)
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestDeepCloneAnyMap_NestedMapIsolated(t *testing.T) {
	inner := map[string]any{"nested": true}
	orig := map[string]any{"inner": inner}
	got := DeepCloneAnyMap(orig)

	gotInner, ok := got["inner"].(map[string]any)
	if !ok {
		t.Fatal("expected inner map")
	}
	if gotInner["nested"] != true {
		t.Fatal("expected nested value preserved")
	}

	// Deep copy: mutating clone's inner must NOT affect original.
	gotInner["nested"] = false
	if inner["nested"] != true {
		t.Fatal("deep clone mutation leaked to original inner map")
	}
}

func TestDeepCloneAnyMap_PreservesIntType(t *testing.T) {
	orig := map[string]any{"count": 42, "rate": 3.14, "name": "test"}
	got := DeepCloneAnyMap(orig)

	// int must stay int, not become float64 (unlike JSON round-trip).
	if v, ok := got["count"].(int); !ok || v != 42 {
		t.Fatalf("expected int 42, got %T %v", got["count"], got["count"])
	}
	if v, ok := got["rate"].(float64); !ok || v != 3.14 {
		t.Fatalf("expected float64 3.14, got %T %v", got["rate"], got["rate"])
	}
}

func TestDeepCloneAnyMap_StringSliceIsolated(t *testing.T) {
	orig := map[string]any{"tags": []string{"a", "b"}}
	got := DeepCloneAnyMap(orig)

	gotSlice, ok := got["tags"].([]string)
	if !ok || len(gotSlice) != 2 {
		t.Fatalf("expected []string of len 2, got %T %v", got["tags"], got["tags"])
	}
	gotSlice[0] = "changed"
	if orig["tags"].([]string)[0] != "a" {
		t.Fatal("[]string mutation leaked to original")
	}
}

func TestDeepCloneAnyMap_MapAnyAnyIsolated(t *testing.T) {
	inner := map[any]any{"nested": true}
	orig := map[string]any{"yaml": inner}
	got := DeepCloneAnyMap(orig)

	gotInner, ok := got["yaml"].(map[any]any)
	if !ok {
		t.Fatalf("expected map[any]any, got %T", got["yaml"])
	}
	gotInner["nested"] = false
	if inner["nested"] != true {
		t.Fatal("map[any]any mutation leaked to original")
	}
}

func TestDeepCloneAnyMap_MapSliceIsolated(t *testing.T) {
	orig := map[string]any{"items": []map[string]any{{"k": 1}}}
	got := DeepCloneAnyMap(orig)

	gotSlice, ok := got["items"].([]map[string]any)
	if !ok || len(gotSlice) != 1 {
		t.Fatalf("expected []map[string]any len 1, got %T %v", got["items"], got["items"])
	}
	gotSlice[0]["k"] = 999
	if orig["items"].([]map[string]any)[0]["k"] != 1 {
		t.Fatal("[]map[string]any mutation leaked to original")
	}
}

func TestDeepCloneAnyMap_SliceCloned(t *testing.T) {
	orig := map[string]any{
		"tags": []any{"a", "b", map[string]any{"nested": true}},
	}
	got := DeepCloneAnyMap(orig)

	gotSlice, ok := got["tags"].([]any)
	if !ok || len(gotSlice) != 3 {
		t.Fatalf("expected 3-element slice, got %v", got["tags"])
	}

	// Mutate nested map in clone's slice.
	if nested, ok := gotSlice[2].(map[string]any); ok {
		nested["nested"] = false
	}
	// Original's nested map must be unaffected.
	origSlice := orig["tags"].([]any)
	origNested := origSlice[2].(map[string]any)
	if origNested["nested"] != true {
		t.Fatal("deep clone slice mutation leaked to original")
	}
}
