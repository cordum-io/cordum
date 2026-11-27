package schema

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
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

	claim := func(id, tenant string) error {
		t.Helper()
		return reg.RegisterOwned(ctx, id, tenant, body)
	}

	t.Run("claim", func(t *testing.T) {
		if err := claim("owned/a", "tenant-a"); err != nil {
			t.Fatal(err)
		}
		if got, err := reg.Owner(ctx, "owned/a"); err != nil || got != "tenant-a" {
			t.Fatalf("Owner = %q, %v; want tenant-a", got, err)
		}
	})

	t.Run("second claim keeps first owner", func(t *testing.T) {
		if err := claim("owned/a", "tenant-b"); !errors.Is(err, ErrIDNotAvailable) {
			t.Fatalf("second claim = %v, want ErrIDNotAvailable", err)
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

	t.Run("delete lets another tenant claim", func(t *testing.T) {
		if err := claim("owned/r", "tenant-a"); err != nil {
			t.Fatal(err)
		}
		if err := reg.DeleteOwned(ctx, "owned/r", "tenant-a"); err != nil {
			t.Fatal(err)
		}
		if err := claim("owned/r", "tenant-b"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("delete clears the owner", func(t *testing.T) {
		if err := claim("owned/d", "tenant-a"); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := reg.Delete(ctx, "owned/d"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if mr.Exists(ownerKey("owned/d")) {
			t.Fatalf("owner key left after delete")
		}
	})

	t.Run("schema id owners leaves other owners intact", func(t *testing.T) {
		if err := reg.Register(ctx, "owners", body); err != nil {
			t.Fatalf("register owners: %v", err)
		}
		if err := reg.Delete(ctx, "owners"); err != nil {
			t.Fatalf("delete owners: %v", err)
		}
		if got, err := reg.Owner(ctx, "owned/a"); err != nil || got != "tenant-a" {
			t.Fatalf("owner = %q, %v after id owners, want tenant-a", got, err)
		}
	})
}

func TestRegistryOwnedDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, owner, tenant string
		doc                 bool
		register, read, del error
	}{
		{"own", "a", "a", true, nil, nil, nil},
		{"foreign", "b", "a", true, ErrIDNotAvailable, redis.Nil, redis.Nil},
		{"platform", "", "a", true, ErrIDNotAvailable, nil, ErrIDNotAvailable},
		{"absent", "", "a", false, nil, redis.Nil, redis.Nil},
		{"own-no-document", "a", "a", false, nil, redis.Nil, nil},
		{"foreign-no-document", "b", "a", false, ErrIDNotAvailable, redis.Nil, redis.Nil},
		{"empty-tenant-absent", "", "", false, ErrIDNotAvailable, redis.Nil, redis.Nil},
		{"empty-tenant-platform", "", "", true, ErrIDNotAvailable, nil, ErrIDNotAvailable},
		{"empty-tenant-owned", "a", "", true, ErrIDNotAvailable, redis.Nil, redis.Nil},
	} {
		for method, want := range map[string]error{"register": tc.register, "read": tc.read, "delete": tc.del} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				reg, mr := ownedRegistry(t, tc.owner, tc.doc)
				before := mr.Dump()
				data, err := ownedCall(reg, method, tc.tenant)
				if !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
				if err != nil || method == "read" {
					if mr.Dump() != before {
						t.Fatal("read or refused request changed stored state")
					}
				}
				if method == "read" && ((err != nil && len(data) != 0) || (err == nil && string(data) != "document")) {
					t.Fatalf("read bytes = %q, error = %v", data, err)
				}
				if err == nil && method != "read" {
					ownedResult(t, reg, mr, method, tc.tenant)
				}
			})
		}
	}
}

func ownedRegistry(t *testing.T, owner string, doc bool) (*Registry, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	reg, err := NewRegistry("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	if doc {
		if err := reg.Register(t.Context(), "id", []byte("document")); err != nil {
			t.Fatal(err)
		}
	}
	if owner != "" {
		if err := mr.Set(ownerKey("id"), owner); err != nil {
			t.Fatal(err)
		}
	}
	return reg, mr
}

func ownedCall(reg *Registry, method, tenant string) ([]byte, error) {
	ctx := context.Background()
	switch method {
	case "register":
		return nil, reg.RegisterOwned(ctx, "id", tenant, []byte("new-document"))
	case "delete":
		return nil, reg.DeleteOwned(ctx, "id", tenant)
	default:
		return reg.GetOwned(ctx, "id", tenant)
	}
}

func ownedResult(t *testing.T, reg *Registry, mr *miniredis.Miniredis, method, tenant string) {
	t.Helper()
	ids, err := reg.List(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if method == "delete" {
		if mr.Exists(schemaKey("id")) || mr.Exists(ownerKey("id")) || len(ids) != 0 {
			t.Fatalf("delete left state: %s", mr.Dump())
		}
		return
	}
	doc, err := reg.Get(t.Context(), "id")
	owner, ownerErr := reg.Owner(t.Context(), "id")
	if err != nil || ownerErr != nil || string(doc) != "new-document" || owner != tenant || !reflect.DeepEqual(ids, []string{"id"}) {
		t.Fatalf("registered state: %s, errors: %v %v", mr.Dump(), err, ownerErr)
	}
}

type ownedReadHook struct{ after func() }

func (h *ownedReadHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *ownedReadHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *ownedReadHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if cmd.Name() == "get" && cmd.Args()[1] == ownerKey("id") && h.after != nil {
			after := h.after
			h.after = nil
			after()
		}
		return err
	}
}

func TestRegistryOwnedOwnerChange(t *testing.T) {
	for _, method := range []string{"register", "delete"} {
		for _, initial := range []string{"a", ""} {
			t.Run(method+"/owner="+initial, func(t *testing.T) {
				reg, mr := ownedRegistry(t, initial, true)
				var winner string
				hook := &ownedReadHook{after: func() {
					if err := mr.Set(ownerKey("id"), "b"); err != nil {
						t.Fatal(err)
					}
					winner = mr.Dump()
				}}
				reg.client.AddHook(hook)
				_, err := ownedCall(reg, method, "a")
				want := ErrIDNotAvailable
				if method == "delete" {
					want = redis.Nil
				}
				if !errors.Is(err, want) || winner == "" || mr.Dump() != winner {
					t.Fatalf("owner-checked %s: err=%v; state=%s, want=%s", method, err, mr.Dump(), winner)
				}
			})
		}
	}
}

func TestRegistryOwnerKeySlot(t *testing.T) {
	if schemaTestSlot("foo") != 12182 {
		t.Fatal("CRC16 check vector")
	}
	for _, id := range []string{"id", "a/b", "owners", "org:document", "spaces allowed"} {
		if ownerKey(id) != "cordum:schema:owner:{schema:"+id+"}" || schemaTestSlot(ownerKey(id)) != schemaTestSlot(schemaKey(id)) {
			t.Fatalf("document/owner slot mismatch for %q", id)
		}
	}
}

func TestRegistryOwnerReadErrors(t *testing.T) {
	for _, kind := range []string{"owner", "document", "owners-pipeline"} {
		t.Run(kind, func(t *testing.T) {
			reg, mr := ownedRegistry(t, "", false)
			key := ownerKey("id")
			if kind == "document" {
				key = schemaKey("id")
			}
			if err := reg.client.HSet(t.Context(), key, "field", "value").Err(); err != nil {
				t.Fatal(err)
			}
			before := mr.Dump()
			if kind == "owners-pipeline" {
				owners, err := reg.Owners(t.Context(), []string{"absent", "id"})
				if err == nil || errors.Is(err, redis.Nil) || owners != nil {
					t.Fatalf("Owners = %q, %v", owners, err)
				}
			} else {
				data, err := reg.GetOwned(t.Context(), "id", "a")
				if err == nil || errors.Is(err, redis.Nil) || len(data) != 0 {
					t.Fatalf("GetOwned = %q, %v", data, err)
				}
			}
			if mr.Dump() != before {
				t.Fatal("read error changed stored state")
			}
		})
	}
}

func schemaTestSlot(key string) int {
	if start := strings.IndexByte(key, '{'); start >= 0 {
		if end := strings.IndexByte(key[start+1:], '}'); end > 0 {
			key = key[start+1 : start+1+end]
		}
	}
	var crc uint16
	for i := 0; i < len(key); i++ {
		crc ^= uint16(key[i]) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return int(crc) & 16383
}
