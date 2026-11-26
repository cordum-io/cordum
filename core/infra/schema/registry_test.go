package schema

import (
	"context"
	"encoding/json"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
)

func TestRegistryValidate(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewRegistry("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	defer func() { _ = reg.Close() }()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"count": map[string]any{"type": "integer"},
		},
		"required": []any{"name"},
	}
	raw, _ := json.Marshal(schema)
	if err := reg.Register(context.Background(), "test", raw); err != nil {
		t.Fatalf("register: %v", err)
	}

	okPayload := map[string]any{"name": "alpha", "count": 3}
	if err := reg.ValidateID(context.Background(), "test", okPayload); err != nil {
		t.Fatalf("expected validation ok: %v", err)
	}

	badPayload := map[string]any{"count": 3}
	if err := reg.ValidateID(context.Background(), "test", badPayload); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestValidateMap(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"enabled": map[string]any{"type": "boolean"},
		},
		"required": []any{"enabled"},
	}
	if err := ValidateMap(schema, map[string]any{"enabled": true}); err != nil {
		t.Fatalf("expected schema validation ok: %v", err)
	}
	if err := ValidateMap(schema, map[string]any{"enabled": "nope"}); err == nil {
		t.Fatalf("expected schema validation error")
	}
}

func TestRegistryURL_RegisterGetDelete(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewRegistry("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	defer func() { _ = reg.Close() }()

	ctx := context.Background()
	url := "https://cordum.io/schemas/test/item.json"
	body := []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)

	// Register
	if err := reg.RegisterURL(ctx, url, body); err != nil {
		t.Fatalf("register url: %v", err)
	}

	// Get
	got, err := reg.GetByURL(ctx, url)
	if err != nil {
		t.Fatalf("get by url: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("expected %s, got %s", body, got)
	}

	// Delete
	if err := reg.DeleteURL(ctx, url); err != nil {
		t.Fatalf("delete url: %v", err)
	}

	// Confirm gone
	if _, err := reg.GetByURL(ctx, url); err == nil {
		t.Fatalf("expected error after delete")
	}
}

func TestRegistryValidateID_CrossRef(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewRegistry("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	defer func() { _ = reg.Close() }()

	ctx := context.Background()

	// Schema A: defines an Item type, has a $id URL
	schemaA := map[string]any{
		"$id":  "https://cordum.io/schemas/test/item.json",
		"type": "object",
		"properties": map[string]any{
			"sku": map[string]any{"type": "string"},
			"qty": map[string]any{"type": "integer"},
		},
		"required": []any{"sku", "qty"},
	}
	rawA, _ := json.Marshal(schemaA)
	if err := reg.Register(ctx, "test/item", rawA); err != nil {
		t.Fatalf("register schema A: %v", err)
	}
	if err := reg.RegisterURL(ctx, "https://cordum.io/schemas/test/item.json", rawA); err != nil {
		t.Fatalf("register schema A url: %v", err)
	}

	// Schema B: references Schema A via $ref
	schemaB := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"orderId": map[string]any{"type": "string"},
			"item":    map[string]any{"$ref": "https://cordum.io/schemas/test/item.json"},
		},
		"required": []any{"orderId", "item"},
	}
	rawB, _ := json.Marshal(schemaB)
	if err := reg.Register(ctx, "test/order", rawB); err != nil {
		t.Fatalf("register schema B: %v", err)
	}

	// Valid payload
	valid := map[string]any{
		"orderId": "ORD-001",
		"item":    map[string]any{"sku": "WIDGET-42", "qty": float64(5)},
	}
	if err := reg.ValidateID(ctx, "test/order", valid); err != nil {
		t.Fatalf("expected valid: %v", err)
	}

	// Invalid: item missing required qty
	invalid := map[string]any{
		"orderId": "ORD-002",
		"item":    map[string]any{"sku": "WIDGET-99"},
	}
	if err := reg.ValidateID(ctx, "test/order", invalid); err == nil {
		t.Fatalf("expected validation error for missing qty")
	}
}

func TestRegistryOwners(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewRegistry("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	defer func() { _ = reg.Close() }()
	ctx := context.Background()
	body := []byte(`{"type":"object"}`)

	claim := func(id, tenant string) string {
		t.Helper()
		owner, err := reg.ClaimOwner(ctx, id, tenant)
		if err != nil {
			t.Fatalf("claim %s as %s: %v", id, tenant, err)
		}
		return owner
	}

	t.Run("claim", func(t *testing.T) {
		if got := claim("owned/a", "tenant-a"); got != "tenant-a" {
			t.Fatalf("claim owner = %q, want tenant-a", got)
		}
		if got, err := reg.Owner(ctx, "owned/a"); err != nil || got != "tenant-a" {
			t.Fatalf("Owner = %q, %v; want tenant-a", got, err)
		}
	})

	t.Run("second claim keeps first owner", func(t *testing.T) {
		if got := claim("owned/a", "tenant-b"); got != "tenant-a" {
			t.Fatalf("second claim owner = %q, want tenant-a", got)
		}
	})

	t.Run("owners over owned and platform ids", func(t *testing.T) {
		got, err := reg.Owners(ctx, []string{"platform/p", "owned/a"})
		if err != nil {
			t.Fatalf("Owners: %v", err)
		}
		if len(got) != 2 || got[0] != "" || got[1] != "tenant-a" {
			t.Fatalf("Owners = %q, want [\"\" tenant-a]", got)
		}
		if owner, err := reg.Owner(ctx, "platform/p"); err != nil || owner != "" {
			t.Fatalf("Owner(platform) = %q, %v; want empty", owner, err)
		}
	})

	t.Run("release lets another tenant claim", func(t *testing.T) {
		claim("owned/r", "tenant-a")
		if err := reg.ReleaseOwner(ctx, "owned/r"); err != nil {
			t.Fatalf("release: %v", err)
		}
		if got := claim("owned/r", "tenant-b"); got != "tenant-b" {
			t.Fatalf("claim after release = %q, want tenant-b", got)
		}
	})

	t.Run("delete clears the owner", func(t *testing.T) {
		if err := reg.Register(ctx, "owned/d", body); err != nil {
			t.Fatalf("register: %v", err)
		}
		claim("owned/d", "tenant-a")
		if err := reg.Delete(ctx, "owned/d"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if mr.HGet(schemaOwnersKey, "owned/d") != "" {
			t.Fatalf("owner field left after delete")
		}
	})

	t.Run("schema id owners leaves the owner hash intact", func(t *testing.T) {
		if err := reg.Register(ctx, "owners", body); err != nil {
			t.Fatalf("register owners: %v", err)
		}
		if err := reg.Delete(ctx, "owners"); err != nil {
			t.Fatalf("delete owners: %v", err)
		}
		if got := mr.HGet(schemaOwnersKey, "owned/a"); got != "tenant-a" {
			t.Fatalf("owner hash field = %q after id owners, want tenant-a", got)
		}
	})
}
