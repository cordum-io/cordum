package gateway

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/google/uuid"
)

// schemaTenantSeed is one subtest's own ids: sB is registered by tenant B with
// a private marker, and sP is written straight to the registry, as pack
// install writes it, so no tenant owns it.
type schemaTenantSeed struct{ sB, sP, marker string }

type schemaTenantState struct {
	sB, sP string
	ids    []string
	owners map[string]string
}

func TestSchemaRegistryTenantBound(t *testing.T) {
	sw := newCrossTenantSweep(t)
	a, b := sw.tenantA, sw.tenantB

	t.Run("list", func(t *testing.T) {
		seed := schemaTenantSeedIDs(t, sw)
		rr := sw.as(t, a, http.MethodGet, "/api/v1/schemas", "")
		schemaTenantWant(t, rr, "A list", http.StatusOK)
		schemaTenantNoMarker(t, rr, "A list", seed.sB)
		if !strings.Contains(rr.Body.String(), seed.sP) {
			t.Fatalf("A list lacks the platform schema %s: %s", seed.sP, rr.Body.String())
		}
		rr = sw.as(t, b, http.MethodGet, "/api/v1/schemas", "")
		schemaTenantWant(t, rr, "B list", http.StatusOK)
		if !strings.Contains(rr.Body.String(), seed.sB) {
			t.Fatalf("B list lacks its own schema %s: %s", seed.sB, rr.Body.String())
		}
	})

	t.Run("get", func(t *testing.T) {
		seed := schemaTenantSeedIDs(t, sw)
		rr := sw.as(t, a, http.MethodGet, "/api/v1/schemas/"+seed.sB, "")
		schemaTenantNoMarker(t, rr, "A get B", seed.marker)
		absent := sw.as(t, a, http.MethodGet, "/api/v1/schemas/"+schemaTenantAbsentID(), "")
		schemaTenantSameAsAbsent(t, rr, absent, "A get B", http.StatusNotFound)
		schemaTenantWant(t, sw.as(t, a, http.MethodGet, "/api/v1/schemas/"+seed.sP, ""), "A get platform", http.StatusOK)
		rr = sw.as(t, b, http.MethodGet, "/api/v1/schemas/"+seed.sB, "")
		schemaTenantWant(t, rr, "B get", http.StatusOK)
		if !strings.Contains(rr.Body.String(), seed.marker) {
			t.Fatalf("B get lacks its marker: %s", rr.Body.String())
		}
	})

	t.Run("register", func(t *testing.T) {
		seed := schemaTenantSeedIDs(t, sw)
		before, mark := schemaTenantSnapshot(t, sw, seed), sw.publishMark()
		for _, id := range []string{seed.sB, seed.sP} {
			rr := sw.as(t, a, http.MethodPost, "/api/v1/schemas", schemaTenantBody(id, "schemaTenant-a-overwrite"))
			schemaTenantRefused(t, rr, "A register over "+id)
		}
		schemaTenantUnchanged(t, sw, seed, before, mark)
		rr := sw.as(t, b, http.MethodPost, "/api/v1/schemas", schemaTenantBody(seed.sB, seed.marker))
		schemaTenantWant(t, rr, "B register over its own", http.StatusNoContent)
	})

	t.Run("delete", func(t *testing.T) {
		seed := schemaTenantSeedIDs(t, sw)
		before, mark := schemaTenantSnapshot(t, sw, seed), sw.publishMark()
		rr := sw.as(t, a, http.MethodDelete, "/api/v1/schemas/"+seed.sB, "")
		absent := sw.as(t, a, http.MethodDelete, "/api/v1/schemas/"+schemaTenantAbsentID(), "")
		schemaTenantSameAsAbsent(t, rr, absent, "A delete B", http.StatusNotFound)
		schemaTenantRefused(t, sw.as(t, a, http.MethodDelete, "/api/v1/schemas/"+seed.sP, ""), "A delete platform")
		schemaTenantUnchanged(t, sw, seed, before, mark)
		schemaTenantWant(t, sw.as(t, b, http.MethodDelete, "/api/v1/schemas/"+seed.sB, ""), "B delete its own", http.StatusNoContent)
		rr = sw.as(t, a, http.MethodPost, "/api/v1/schemas", schemaTenantBody(seed.sB, "schemaTenant-a-new"))
		schemaTenantWant(t, rr, "A register the freed id", http.StatusNoContent)
	})

	t.Run("new-id", func(t *testing.T) {
		sA, marker := "schemaTenant-a-"+uuid.NewString(), "schemaTenant-a-private-"+uuid.NewString()
		schemaTenantWant(t, sw.as(t, a, http.MethodPost, "/api/v1/schemas", schemaTenantBody(sA, marker)), "A register new", http.StatusNoContent)
		rr := sw.as(t, b, http.MethodGet, "/api/v1/schemas/"+sA, "")
		schemaTenantNoMarker(t, rr, "B get A", marker)
		schemaTenantWant(t, rr, "B get A", http.StatusNotFound)
		rr = sw.as(t, b, http.MethodPost, "/api/v1/schemas", schemaTenantBody(sA, "schemaTenant-b-overwrite"))
		schemaTenantRefused(t, rr, "B register over A")
	})

	t.Run("cross-tenant", func(t *testing.T) {
		seed := schemaTenantSeedIDs(t, sw)
		admin := &auth.AuthContext{Tenant: a.tenantID, Role: "admin", PrincipalID: "principal-schema-cross", AllowCrossTenant: true}
		rr := schemaTenantDirect(sw.server.handleListSchemas, admin, http.MethodGet, "/api/v1/schemas", "", "")
		schemaTenantWant(t, rr, "cross-tenant list", http.StatusOK)
		if !strings.Contains(rr.Body.String(), seed.sB) {
			t.Fatalf("cross-tenant list lacks %s: %s", seed.sB, rr.Body.String())
		}
		rr = schemaTenantDirect(sw.server.handleGetSchema, admin, http.MethodGet, "/api/v1/schemas/"+seed.sB, seed.sB, "")
		schemaTenantWant(t, rr, "cross-tenant get", http.StatusOK)
		if !strings.Contains(rr.Body.String(), seed.marker) {
			t.Fatalf("cross-tenant get lacks the marker: %s", rr.Body.String())
		}
		sX := "schemaTenant-x-" + uuid.NewString()
		rr = schemaTenantDirect(sw.server.handleRegisterSchema, admin, http.MethodPost, "/api/v1/schemas", "", schemaTenantBody(sX, "schemaTenant-x"))
		schemaTenantWant(t, rr, "cross-tenant register", http.StatusNoContent)
		schemaTenantWant(t, sw.as(t, a, http.MethodGet, "/api/v1/schemas/"+sX, ""), "A get cross-tenant schema", http.StatusOK)
	})
}

func schemaTenantSeedIDs(t *testing.T, sw *crossTenantSweep) schemaTenantSeed {
	t.Helper()
	suffix := uuid.NewString()
	seed := schemaTenantSeed{sB: "schemaTenant-b-" + suffix, sP: "schemaTenant-p-" + suffix, marker: "schemaTenant-private-" + suffix}
	rr := sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/schemas", schemaTenantBody(seed.sB, seed.marker))
	schemaTenantWant(t, rr, "seed B schema", http.StatusNoContent)
	if err := sw.server.schemaRegistry.Register(context.Background(), seed.sP, []byte(`{"type":"object"}`)); err != nil {
		t.Fatalf("seed platform schema: %v", err)
	}
	return seed
}

func schemaTenantBody(id, marker string) string {
	return fmt.Sprintf(`{"id":%q,"schema":{"type":"object","description":%q}}`, id, marker)
}

func schemaTenantAbsentID() string { return "schemaTenant-absent-" + uuid.NewString() }

func schemaTenantWant(t *testing.T, rr *httptest.ResponseRecorder, label string, code int) {
	t.Helper()
	if rr.Code != code {
		t.Fatalf("%s: status=%d, want %d; body=%s", label, rr.Code, code, rr.Body.String())
	}
}

func schemaTenantRefused(t *testing.T, rr *httptest.ResponseRecorder, label string) {
	t.Helper()
	schemaTenantWant(t, rr, label, http.StatusForbidden)
	if !strings.Contains(rr.Body.String(), "schema id not available") {
		t.Fatalf("%s: body=%s, want the fixed refusal message", label, rr.Body.String())
	}
}

func schemaTenantNoMarker(t *testing.T, rr *httptest.ResponseRecorder, label, marker string) {
	t.Helper()
	if strings.Contains(rr.Body.String(), marker) {
		t.Errorf("%s: response carries tenant-B data %q", label, marker)
	}
}

func schemaTenantSameAsAbsent(t *testing.T, rr, absent *httptest.ResponseRecorder, label string, code int) {
	t.Helper()
	if rr.Code != code || absent.Code != code || !bytes.Equal(rr.Body.Bytes(), absent.Body.Bytes()) {
		t.Fatalf("%s: status=%d body=%q; absent id status=%d body=%q; want both %d with equal bodies",
			label, rr.Code, rr.Body.String(), absent.Code, absent.Body.String(), code)
	}
}

func schemaTenantSnapshot(t *testing.T, sw *crossTenantSweep, seed schemaTenantSeed) schemaTenantState {
	t.Helper()
	ctx := context.Background()
	reg := sw.server.schemaRegistry
	sB, err := reg.Get(ctx, seed.sB)
	if err != nil {
		t.Fatalf("read B schema: %v", err)
	}
	sP, err := reg.Get(ctx, seed.sP)
	if err != nil {
		t.Fatalf("read platform schema: %v", err)
	}
	ids, err := reg.List(ctx, 1000)
	if err != nil {
		t.Fatalf("list schemas: %v", err)
	}
	client := sw.server.jobStore.Client()
	keys, err := client.Keys(ctx, "cordum:schema:owner:*").Result()
	if err != nil {
		t.Fatalf("read schema owners: %v", err)
	}
	owners := make(map[string]string, len(keys))
	for _, key := range keys {
		owner, err := client.Get(ctx, key).Result()
		if err != nil {
			t.Fatalf("read schema owner %s: %v", key, err)
		}
		owners[key] = owner
	}
	return schemaTenantState{sB: string(sB), sP: string(sP), ids: ids, owners: owners}
}

func schemaTenantUnchanged(t *testing.T, sw *crossTenantSweep, seed schemaTenantSeed, before schemaTenantState, mark int) {
	t.Helper()
	if after := schemaTenantSnapshot(t, sw, seed); !reflect.DeepEqual(after, before) {
		t.Fatalf("refused writes changed state:\nbefore=%+v\nafter=%+v", before, after)
	}
	if published := sw.publishedSince(mark); len(published) != 0 {
		t.Fatalf("refused writes published %d messages", len(published))
	}
}

func schemaTenantDirect(h http.HandlerFunc, ac *auth.AuthContext, method, path, id, body string) *httptest.ResponseRecorder {
	req := withAuth(httptest.NewRequest(method, path, strings.NewReader(body)), ac)
	if id != "" {
		req.SetPathValue("id", id)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}
