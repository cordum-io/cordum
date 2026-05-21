package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestMCPUpstreamRegistryLazyInitConcurrent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"` + mcpUpstreamTestAPIKey + `","tenant":"tenant-a","role":"admin","principal_id":"mcp-admin"}]`,
	})
	s.mcpUpstreamRegistry = nil

	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	handler := apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))

	const callers = 40
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/mcp/upstreams", nil)
			addMCPUpstreamAuth(req)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				errs <- fmt.Errorf("caller %d status=%d body=%s", i, rec.Code, rec.Body.String())
				return
			}
			var got mcpUpstreamListResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				errs <- fmt.Errorf("caller %d unmarshal: %w body=%s", i, err, rec.Body.String())
				return
			}
			if len(got.Items) != 0 {
				errs <- fmt.Errorf("caller %d got %d upstreams, want empty list", i, len(got.Items))
				return
			}
			errs <- nil
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.mcpUpstreamRegistry == nil {
		t.Fatal("mcp upstream registry remained nil after concurrent lazy init")
	}
}
