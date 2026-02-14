package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
)

// ensureSSRFProtection makes sure the private IP check is active for these
// security tests, even if another test set the bypass.
func ensureSSRFProtection(t *testing.T) {
	t.Helper()
	old := skipPrivateIPCheck.Load()
	skipPrivateIPCheck.Store(false)
	t.Cleanup(func() { skipPrivateIPCheck.Store(old) })
}

func withLookupHostIPs(t *testing.T, fn func(context.Context, string) ([]net.IP, error)) {
	t.Helper()
	old := lookupHostIPs
	lookupHostIPs = fn
	t.Cleanup(func() { lookupHostIPs = old })
}

func TestIsPrivateIP(t *testing.T) {
	ensureSSRFProtection(t)

	privateHosts := []string{
		"127.0.0.1",
		"127.0.0.2",
		"10.0.0.1",
		"10.255.255.255",
		"172.16.0.1",
		"172.31.255.255",
		"192.168.1.1",
		"192.168.0.0",
		"169.254.169.254", // AWS metadata
		"169.254.1.1",
		"::1",
		"fe80::1",
		"fc00::1",
		"fd12:3456::1",
		"localhost",
		"LOCALHOST",
		"metadata.google.internal",
	}

	for _, host := range privateHosts {
		if !isPrivateIP(host) {
			t.Errorf("isPrivateIP(%q) = false, want true", host)
		}
	}
}

func TestIsPrivateIP_PublicAddresses(t *testing.T) {
	ensureSSRFProtection(t)
	withLookupHostIPs(t, func(ctx context.Context, host string) ([]net.IP, error) {
		switch host {
		case "github.com", "example.com":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		default:
			return nil, errors.New("unhandled host")
		}
	})

	publicHosts := []string{
		"8.8.8.8",
		"142.250.80.14",
		"1.1.1.1",
		"203.0.113.1",
		"2607:f8b0:4004:800::200e", // public IPv6
		"github.com",
		"example.com",
		"",
	}

	for _, host := range publicHosts {
		if isPrivateIP(host) {
			t.Errorf("isPrivateIP(%q) = true, want false", host)
		}
	}
}

func TestIsPrivateIP_ResolvesHostnamePrivate(t *testing.T) {
	ensureSSRFProtection(t)
	withLookupHostIPs(t, func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "internal.test" {
			return []net.IP{net.ParseIP("10.0.0.1")}, nil
		}
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	})

	if !isPrivateIP("internal.test") {
		t.Errorf("isPrivateIP(%q) = false, want true", "internal.test")
	}
}

func TestIsPrivateIP_UnresolvableFailsClosed(t *testing.T) {
	ensureSSRFProtection(t)
	withLookupHostIPs(t, func(ctx context.Context, host string) ([]net.IP, error) {
		return nil, errors.New("no such host")
	})

	if !isPrivateIP("does-not-resolve.test") {
		t.Errorf("isPrivateIP(%q) = false, want true", "does-not-resolve.test")
	}
}

func TestIsPrivateIP_EdgeCases(t *testing.T) {
	ensureSSRFProtection(t)

	tests := []struct {
		host    string
		private bool
	}{
		{"172.15.255.255", false}, // just below 172.16.0.0/12
		{"172.16.0.0", true},
		{"172.32.0.0", false}, // just above 172.16.0.0/12
		{"11.0.0.0", false},   // just above 10.0.0.0/8
		{"9.255.255.255", false},
		{"192.167.255.255", false}, // just below 192.168.0.0/16
		{"192.169.0.0", false},     // just above 192.168.0.0/16
		{"  127.0.0.1  ", true},    // whitespace trimmed
	}

	for _, tt := range tests {
		got := isPrivateIP(tt.host)
		if got != tt.private {
			t.Errorf("isPrivateIP(%q) = %v, want %v", tt.host, got, tt.private)
		}
	}
}

func TestValidateMarketplaceURL_RejectsPrivateIPs(t *testing.T) {
	ensureSSRFProtection(t)
	withLookupHostIPs(t, func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "internal.test" {
			return []net.IP{net.ParseIP("10.0.0.2")}, nil
		}
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	})

	// Even when host is in the allowlist, private IPs must be rejected.
	allowedHosts := map[string]struct{}{
		"127.0.0.1":       {},
		"10.0.0.1":        {},
		"192.168.1.1":     {},
		"169.254.169.254": {},
		"localhost":       {},
		"internal.test":   {},
	}

	urls := []string{
		"https://127.0.0.1/pack.tar.gz",
		"https://10.0.0.1/pack.tar.gz",
		"https://192.168.1.1/pack.tar.gz",
		"https://169.254.169.254/latest/meta-data",
		"https://localhost/pack.tar.gz",
		"https://internal.test/pack.tar.gz",
	}

	for _, u := range urls {
		_, err := validateMarketplaceURL(u, allowedHosts)
		if err == nil {
			t.Errorf("validateMarketplaceURL(%q) = nil error, want rejection", u)
		} else if err.Error() != "invalid pack url" {
			t.Errorf("validateMarketplaceURL(%q) error = %q, want 'invalid pack url'", u, err.Error())
		}
	}
}

func TestValidateMarketplaceURL_AcceptsPublicHosts(t *testing.T) {
	ensureSSRFProtection(t)
	withLookupHostIPs(t, func(ctx context.Context, host string) ([]net.IP, error) {
		switch host {
		case "github.com":
			return []net.IP{net.ParseIP("140.82.114.4")}, nil
		default:
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		}
	})

	allowedHosts := map[string]struct{}{
		"github.com": {},
		"8.8.8.8":    {},
	}

	urls := []string{
		"https://github.com/cordum/packs/releases/download/v1/pack.tar.gz",
		"https://8.8.8.8/pack.tar.gz",
	}

	for _, u := range urls {
		parsed, err := validateMarketplaceURL(u, allowedHosts)
		if err != nil {
			t.Errorf("validateMarketplaceURL(%q) = error %v, want success", u, err)
		}
		if parsed == nil {
			t.Errorf("validateMarketplaceURL(%q) returned nil parsed URL", u)
		}
	}
}

func TestValidateMarketplaceURL_RejectsNonAllowedHost(t *testing.T) {
	ensureSSRFProtection(t)
	withLookupHostIPs(t, func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	})

	allowedHosts := map[string]struct{}{
		"github.com": {},
	}

	_, err := validateMarketplaceURL("https://evil.com/pack.tar.gz", allowedHosts)
	if err == nil {
		t.Error("validateMarketplaceURL should reject hosts not in allowlist")
	}
}

func TestFetchMarketplaceCatalog_EnforcesAllowlist(t *testing.T) {
	ensureSSRFProtection(t)
	withLookupHostIPs(t, func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	})

	allowedHosts := map[string]struct{}{
		"other.example": {},
	}
	_, err := fetchMarketplaceCatalog(context.Background(), "https://catalog.example/catalog.json", allowedHosts)
	if err == nil {
		t.Fatal("expected allowlist rejection for catalog host")
	}
}

func TestLoadMarketplaceEntriesRespectsTimeout(t *testing.T) {
	s, _, _ := newTestGateway(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"updated_at":"","packs":[]}`))
	}))
	t.Cleanup(srv.Close)

	doc := &configsvc.Document{
		Scope:   configsvc.Scope(packCatalogScope),
		ScopeID: packCatalogID,
		Data: map[string]any{
			"catalogs": []map[string]any{
				{
					"id":      "test",
					"title":   "Test",
					"url":     srv.URL,
					"enabled": true,
				},
			},
		},
	}
	if err := s.configSvc.Set(context.Background(), doc); err != nil {
		t.Fatalf("set catalog config: %v", err)
	}

	origTimeout := marketplaceCatalogFetchTimeout
	marketplaceCatalogFetchTimeout = 10 * time.Millisecond
	t.Cleanup(func() { marketplaceCatalogFetchTimeout = origTimeout })

	statuses, entries, err := s.loadMarketplaceEntries(context.Background())
	if err != nil {
		t.Fatalf("loadMarketplaceEntries: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Error == "" {
		t.Fatalf("expected catalog fetch failure status, got %#v", statuses)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries on timeout, got %d", len(entries))
	}
}

func TestMarketplaceHTTPClient_RedirectBlocksPrivateIP(t *testing.T) {
	ensureSSRFProtection(t)
	withLookupHostIPs(t, func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "private.example" {
			return []net.IP{net.ParseIP("10.0.0.5")}, nil
		}
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	})

	allowedHosts := map[string]struct{}{
		"private.example": {},
	}
	client := marketplaceHTTPClient(allowedHosts, "")
	reqURL, _ := url.Parse("https://private.example/redirect")
	err := client.CheckRedirect(&http.Request{URL: reqURL}, []*http.Request{})
	if err == nil {
		t.Fatalf("expected redirect to private address to be blocked")
	}
}

func TestMarketplaceHTTPClient_RedirectAllowsSameHost(t *testing.T) {
	ensureSSRFProtection(t)
	withLookupHostIPs(t, func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	})

	allowedHosts := map[string]struct{}{
		"public.example": {},
	}
	client := marketplaceHTTPClient(allowedHosts, "public.example")
	reqURL, _ := url.Parse("https://public.example/redirect")
	err := client.CheckRedirect(&http.Request{URL: reqURL}, []*http.Request{})
	if err != nil {
		t.Fatalf("expected same-host redirect to be allowed, got %v", err)
	}
}

func TestMarketplaceCacheIsolation(t *testing.T) {
	now := time.Now().UTC()
	s := &server{
		marketplaceCache: marketplaceCache{
			Response: marketplaceResponse{
				Catalogs: []marketplaceCatalogStatus{
					{ID: "cat-1", Title: "Catalog One", URL: "https://example.com/catalog.json", Enabled: true},
				},
				Items: []marketplacePackItem{
					{
						ID:           "pack-1",
						Version:      "1.0.0",
						Title:        "Pack One",
						Capabilities: []string{"cap-1"},
						Requires:     []string{"req-1"},
						RiskTags:     []string{"risk-1"},
					},
				},
			},
			FetchedAt: now,
		},
	}

	first, err := s.marketplaceSnapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("marketplaceSnapshot() error = %v", err)
	}

	first.Catalogs[0].Title = "mutated"
	first.Items[0].Capabilities[0] = "mutated"
	first.Items[0].Requires[0] = "mutated"
	first.Items[0].RiskTags[0] = "mutated"
	first.Items = append(first.Items, marketplacePackItem{ID: "pack-2"})
	first.Catalogs = append(first.Catalogs, marketplaceCatalogStatus{ID: "cat-2", URL: "https://example.com/other.json"})

	second, err := s.marketplaceSnapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("marketplaceSnapshot() second call error = %v", err)
	}

	if len(second.Catalogs) != 1 {
		t.Fatalf("second response catalogs length = %d, want 1", len(second.Catalogs))
	}
	if second.Catalogs[0].Title != "Catalog One" {
		t.Fatalf("second response catalog title = %q, want %q", second.Catalogs[0].Title, "Catalog One")
	}
	if len(second.Items) != 1 {
		t.Fatalf("second response items length = %d, want 1", len(second.Items))
	}
	if second.Items[0].Capabilities[0] != "cap-1" {
		t.Fatalf("second response capabilities[0] = %q, want %q", second.Items[0].Capabilities[0], "cap-1")
	}
	if second.Items[0].Requires[0] != "req-1" {
		t.Fatalf("second response requires[0] = %q, want %q", second.Items[0].Requires[0], "req-1")
	}
	if second.Items[0].RiskTags[0] != "risk-1" {
		t.Fatalf("second response risk_tags[0] = %q, want %q", second.Items[0].RiskTags[0], "risk-1")
	}

	cache := s.marketplaceCache.Response
	if cache.Catalogs[0].Title != "Catalog One" {
		t.Fatalf("cached catalog title = %q, want %q", cache.Catalogs[0].Title, "Catalog One")
	}
	if cache.Items[0].Capabilities[0] != "cap-1" {
		t.Fatalf("cached capabilities[0] = %q, want %q", cache.Items[0].Capabilities[0], "cap-1")
	}
	if cache.Items[0].Requires[0] != "req-1" {
		t.Fatalf("cached requires[0] = %q, want %q", cache.Items[0].Requires[0], "req-1")
	}
	if cache.Items[0].RiskTags[0] != "risk-1" {
		t.Fatalf("cached risk_tags[0] = %q, want %q", cache.Items[0].RiskTags[0], "risk-1")
	}
}

func TestMarketplaceCacheConcurrent(t *testing.T) {
	now := time.Now().UTC()
	s := &server{
		marketplaceCache: marketplaceCache{
			Response: marketplaceResponse{
				Catalogs: []marketplaceCatalogStatus{
					{ID: "cat-1", Title: "Catalog One", URL: "https://example.com/catalog.json", Enabled: true},
				},
				Items: []marketplacePackItem{
					{
						ID:           "pack-1",
						Version:      "1.0.0",
						Title:        "Pack One",
						Capabilities: []string{"cap-1"},
						Requires:     []string{"req-1"},
						RiskTags:     []string{"risk-1"},
					},
				},
			},
			FetchedAt: now,
		},
	}

	const workers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := s.marketplaceSnapshot(context.Background(), false)
			if err != nil {
				errCh <- err
				return
			}
			if len(resp.Items) == 0 || len(resp.Catalogs) == 0 {
				errCh <- errors.New("unexpected empty marketplace response")
				return
			}
			resp.Items[0].Capabilities[0] = "mutated"
			resp.Items[0].Requires[0] = "mutated"
			resp.Items[0].RiskTags[0] = "mutated"
			resp.Catalogs[0].Title = "mutated"
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent snapshot error = %v", err)
		}
	}

	cache := s.marketplaceCache.Response
	if cache.Catalogs[0].Title != "Catalog One" {
		t.Fatalf("cached catalog title = %q, want %q", cache.Catalogs[0].Title, "Catalog One")
	}
	if cache.Items[0].Capabilities[0] != "cap-1" {
		t.Fatalf("cached capabilities[0] = %q, want %q", cache.Items[0].Capabilities[0], "cap-1")
	}
	if cache.Items[0].Requires[0] != "req-1" {
		t.Fatalf("cached requires[0] = %q, want %q", cache.Items[0].Requires[0], "req-1")
	}
	if cache.Items[0].RiskTags[0] != "risk-1" {
		t.Fatalf("cached risk_tags[0] = %q, want %q", cache.Items[0].RiskTags[0], "risk-1")
	}
}
