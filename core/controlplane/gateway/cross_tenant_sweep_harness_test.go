package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type crossTenantSweepTenant struct{ tenantID, apiKey, principalID string }

type crossTenantSweep struct {
	server           *server
	mux              *http.ServeMux
	handler          http.Handler
	bus              *stubBus
	tenantA, tenantB crossTenantSweepTenant
}

func newCrossTenantSweep(t *testing.T) *crossTenantSweep {
	t.Helper()
	s, bus, _ := newTestGateway(t)
	s.edgeStore = edgecore.NewRedisStoreFromClient(s.jobStore.Client())
	a := crossTenantSweepTenant{edgeRouteTenant, edgeRouteTestAPIKey, "principal-edge-a"}
	b := crossTenantSweepTenant{edgeRouteOtherTenant, edgeRouteOtherAPIKey, "principal-edge-b"}
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": fmt.Sprintf(`[{"key":%q,"tenant":%q,"role":"admin","principal_id":%q},{"key":%q,"tenant":%q,"role":"admin","principal_id":%q}]`,
			a.apiKey, a.tenantID, a.principalID, b.apiKey, b.tenantID, b.principalID),
	})
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	return &crossTenantSweep{s, mux,
		apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements))), bus, a, b}
}

func (sw *crossTenantSweep) as(t *testing.T, who crossTenantSweepTenant, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("X-API-Key", who.apiKey)
	req.Header.Set("X-Tenant-ID", who.tenantID)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	sw.handler.ServeHTTP(rr, req)
	return rr
}

func (sw *crossTenantSweep) publishMark() int {
	sw.bus.mu.Lock()
	defer sw.bus.mu.Unlock()
	return len(sw.bus.published)
}

func (sw *crossTenantSweep) publishedSince(mark int) []publishedMessage {
	sw.bus.mu.Lock()
	defer sw.bus.mu.Unlock()
	return append([]publishedMessage(nil), sw.bus.published[mark:]...)
}

func assertCrossTenantRefused(t *testing.T, rr *httptest.ResponseRecorder, label string, leaks ...string) {
	t.Helper()
	if rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound {
		t.Fatalf("%s: status=%d, want 403 or 404; body=%s", label, rr.Code, rr.Body.String())
	}
	assertCrossTenantNoLeaks(t, rr, label, leaks...)
}

func assertCrossTenantAbsent(t *testing.T, rr *httptest.ResponseRecorder, label string, leaks ...string) {
	t.Helper()
	if rr.Code < 200 || rr.Code >= 300 {
		t.Fatalf("%s: status=%d, want 2xx; body=%s", label, rr.Code, rr.Body.String())
	}
	assertCrossTenantNoLeaks(t, rr, label, leaks...)
}

func assertCrossTenantNoLeaks(t *testing.T, rr *httptest.ResponseRecorder, label string, leaks ...string) {
	t.Helper()
	for _, leak := range leaks {
		if leak == "" {
			t.Fatalf("%s: empty leak marker", label)
		}
		if strings.Contains(rr.Body.String(), leak) {
			t.Errorf("%s: response contains tenant-B data %q", label, leak)
		}
	}
}

type crossTenantCase struct {
	Route, Name string
	Run         func(t *testing.T, sw *crossTenantSweep)
}

// Registration happens only during init; tests read the registry without mutation.
var crossTenantFamilies = map[string][]crossTenantCase{}

func registerCrossTenantFamily(key string, cases ...crossTenantCase) {
	for _, c := range cases {
		for _, registered := range crossTenantFamilies {
			for _, existing := range registered {
				if c.Route == existing.Route {
					panic("duplicate cross-tenant route: " + c.Route)
				}
			}
		}
		if c.Route == "" || c.Name == "" || c.Run == nil {
			panic("invalid cross-tenant case in " + key)
		}
		crossTenantFamilies[key] = append(crossTenantFamilies[key], c)
	}
}

func runCrossTenantFamily(t *testing.T, key string) {
	t.Helper()
	if len(crossTenantFamilies[key]) == 0 {
		t.Fatalf("no cases registered for %s", key)
	}
	for _, c := range crossTenantFamilies[key] {
		t.Run(c.Name, func(t *testing.T) { c.Run(t, newCrossTenantSweep(t)) })
	}
}

func TestCrossTenantSweepHarnessTenantsAreBound(t *testing.T) {
	sw := newCrossTenantSweep(t)
	marker := "cross-tenant-harness-private-marker"
	// Lists expose metadata, not prompt text: use the marker in both fields.
	body := fmt.Sprintf(`{"prompt":%q,"topic":%q}`, marker, "job."+marker)
	rr := sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/jobs", body)
	assertCrossTenantAbsent(t, rr, "seed B")
	spoofed := sw.tenantA
	spoofed.tenantID = sw.tenantB.tenantID
	rr = sw.as(t, spoofed, http.MethodGet, "/api/v1/jobs", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("spoofed tenant header status=%d, want 403", rr.Code)
	}
	assertCrossTenantRefused(t, rr, "spoofed tenant header", marker)
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/jobs", ""), "A list", marker)
	rr = sw.as(t, sw.tenantB, http.MethodGet, "/api/v1/jobs", "")
	assertCrossTenantAbsent(t, rr, "B list")
	if !strings.Contains(rr.Body.String(), marker) {
		t.Fatalf("B positive control missing marker: %s", rr.Body.String())
	}
	mark := sw.publishMark()
	packet := &pb.BusPacket{TraceId: "harness-publish-marker"}
	if err := sw.bus.Publish("sweep.self-test", packet); err != nil {
		t.Fatal(err)
	}
	messages := sw.publishedSince(mark)
	if len(messages) != 1 || messages[0].subject != "sweep.self-test" || messages[0].packet != packet {
		t.Fatalf("publishedSince returned %v", messages)
	}
	messages[0].subject = "copy-only"
	if sw.publishedSince(mark)[0].subject != "sweep.self-test" {
		t.Fatal("publishedSince returned the bus slice rather than a copy")
	}
}
