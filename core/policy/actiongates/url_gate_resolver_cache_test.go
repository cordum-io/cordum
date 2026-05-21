package actiongates

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestURLGate_ResolverCache_BoundedEviction(t *testing.T) {
	resolver := &fakeHostResolver{
		resolve: map[string][]string{
			"one.nip.io":   {"203.0.113.1"},
			"two.nip.io":   {"203.0.113.2"},
			"three.nip.io": {"203.0.113.3"},
		},
	}
	gate := NewURLGate(URLGateOptions{
		Resolver:         resolver,
		ResolverTTL:      time.Hour,
		ResolverCacheMax: 2,
	})
	before := testutil.ToFloat64(urlGateResolverCacheEvictionsTotal)

	evaluateURL(t, gate, "http://one.nip.io/", pb.DecisionType_DECISION_TYPE_ALLOW, "")
	evaluateURL(t, gate, "http://two.nip.io/", pb.DecisionType_DECISION_TYPE_ALLOW, "")
	evaluateURL(t, gate, "http://ONE.NIP.IO:8080/path", pb.DecisionType_DECISION_TYPE_ALLOW, "")
	if got := resolver.callsFor("one.nip.io"); got != 1 {
		t.Fatalf("normalized mixed-case host:port resolver calls = %d, want 1", got)
	}

	evaluateURL(t, gate, "http://three.nip.io/", pb.DecisionType_DECISION_TYPE_ALLOW, "")
	evaluateURL(t, gate, "http://two.nip.io/again", pb.DecisionType_DECISION_TYPE_ALLOW, "")
	if got := resolver.callsFor("two.nip.io"); got != 2 {
		t.Fatalf("oldest evicted host resolver calls = %d, want 2", got)
	}

	if got := testutil.ToFloat64(urlGateResolverCacheEvictionsTotal) - before; got != 2 {
		t.Fatalf("eviction metric delta = %.0f, want 2", got)
	}
}

func TestURLGate_DNSRebinding_FailsClosedOnResolverError(t *testing.T) {
	resolver := &fakeHostResolver{
		err: map[string]error{"flaky.nip.io": errResolverUnavailable},
	}
	gate := NewURLGate(URLGateOptions{Resolver: resolver})

	dec := evaluateURL(t, gate, "http://flaky.nip.io/", pb.DecisionType_DECISION_TYPE_DENY, CodeResolverError)
	if !strings.Contains(dec.SubReason, "dns_rebind:resolver_error") {
		t.Fatalf("subReason = %q, want dns_rebind:resolver_error", dec.SubReason)
	}
}

func TestURLGate_DNSRebinding_FailsClosedOnInvalidResolverAnswer(t *testing.T) {
	resolver := &fakeHostResolver{
		resolve: map[string][]string{
			"empty.nip.io":     {},
			"malformed.nip.io": {"not-an-ip"},
		},
	}
	gate := NewURLGate(URLGateOptions{Resolver: resolver})

	for _, raw := range []string{"http://empty.nip.io/", "http://malformed.nip.io/"} {
		dec := evaluateURL(t, gate, raw, pb.DecisionType_DECISION_TYPE_DENY, CodeResolverError)
		if !strings.Contains(dec.SubReason, "dns_rebind:resolver_error") {
			t.Fatalf("subReason for %q = %q, want dns_rebind:resolver_error", raw, dec.SubReason)
		}
	}
}

func TestURLGate_DNSRebinding_ResolverErrorsNotCached(t *testing.T) {
	const host = "retry.nip.io"
	resolver := &fakeHostResolver{
		resolve:       map[string][]string{host: {"203.0.113.44"}},
		orderedErrors: map[string][]error{host: {errResolverUnavailable}},
	}
	gate := NewURLGate(URLGateOptions{Resolver: resolver, ResolverTTL: time.Hour})

	evaluateURL(t, gate, "http://"+host+"/first", pb.DecisionType_DECISION_TYPE_DENY, CodeResolverError)
	evaluateURL(t, gate, "http://"+host+"/second", pb.DecisionType_DECISION_TYPE_ALLOW, "")
	if got := resolver.callsFor(host); got != 2 {
		t.Fatalf("resolver error calls = %d, want 2 so errors are not cached", got)
	}
}

func TestURLGate_DNSResolution_ResolverErrorsNotCached(t *testing.T) {
	const host = "retry.example"
	resolver := &fakeHostResolver{
		resolve:       map[string][]string{host: {"93.184.216.34"}},
		orderedErrors: map[string][]error{host: {errResolverUnavailable}},
	}
	gate := NewURLGate(URLGateOptions{Resolver: resolver, ResolverTTL: time.Hour})

	dec := evaluateURL(t, gate, "http://"+host+"/first", pb.DecisionType_DECISION_TYPE_DENY, CodeResolverError)
	if !strings.Contains(dec.SubReason, "dns_resolution:resolver_error") {
		t.Fatalf("subReason = %q, want dns_resolution:resolver_error", dec.SubReason)
	}
	evaluateURL(t, gate, "http://"+host+"/second", pb.DecisionType_DECISION_TYPE_ALLOW, "")
	if got := resolver.callsFor(host); got != 2 {
		t.Fatalf("ordinary resolver error calls = %d, want 2 so errors are not cached", got)
	}
}

func TestURLGate_ResolverCache_TTLExpiryReResolves(t *testing.T) {
	const host = "ttl.nip.io"
	resolver := &fakeHostResolver{
		orderedResponses: map[string][][]string{
			host: {{"203.0.113.55"}, {"10.0.0.7"}},
		},
	}
	gate := NewURLGate(URLGateOptions{
		Resolver:         resolver,
		ResolverTTL:      10 * time.Millisecond,
		ResolverCacheMax: 2,
	})

	evaluateURL(t, gate, "http://"+host+"/first", pb.DecisionType_DECISION_TYPE_ALLOW, "")
	time.Sleep(25 * time.Millisecond)
	evaluateURL(t, gate, "http://"+host+"/second", pb.DecisionType_DECISION_TYPE_DENY, CodeAccessDenied)
	if got := resolver.callsFor(host); got != 2 {
		t.Fatalf("ttl-expired resolver calls = %d, want 2", got)
	}
}

func TestURLGate_ResolverCache_TTLExpiryReResolvesOrdinaryHost(t *testing.T) {
	const host = "ttl.example"
	resolver := &fakeHostResolver{
		orderedResponses: map[string][][]string{
			host: {{"93.184.216.34"}, {"10.0.0.7"}},
		},
	}
	gate := NewURLGate(URLGateOptions{
		Resolver:         resolver,
		ResolverTTL:      10 * time.Millisecond,
		ResolverCacheMax: 2,
	})

	evaluateURL(t, gate, "http://"+host+"/first", pb.DecisionType_DECISION_TYPE_ALLOW, "")
	time.Sleep(25 * time.Millisecond)
	dec := evaluateURL(t, gate, "http://"+host+"/second", pb.DecisionType_DECISION_TYPE_DENY, CodeAccessDenied)
	if !strings.Contains(dec.SubReason, "dns_resolution:rfc1918") {
		t.Fatalf("subReason after TTL private re-resolve = %q, want dns_resolution:rfc1918", dec.SubReason)
	}
	if got := resolver.callsFor(host); got != 2 {
		t.Fatalf("ordinary ttl-expired resolver calls = %d, want 2", got)
	}
}

func TestURLGate_ResolverCache_ClonesIPs(t *testing.T) {
	cache := newURLGateResolverCache(2)
	original := net.ParseIP("203.0.113.77")
	cache.put("clone.nip.io", []net.IP{original}, time.Now().Add(time.Hour))
	original[0] ^= 0xff

	got, ok := cache.get("clone.nip.io", time.Now())
	if !ok || got[0].String() != "203.0.113.77" {
		t.Fatalf("cached IP after put mutation = %v ok=%v, want 203.0.113.77", got, ok)
	}
	got[0][0] ^= 0xff
	again, ok := cache.get("clone.nip.io", time.Now())
	if !ok || again[0].String() != "203.0.113.77" {
		t.Fatalf("cached IP after get mutation = %v ok=%v, want 203.0.113.77", again, ok)
	}
}

func TestURLGate_ResolverCache_SingleflightCoalescesConcurrentLookups(t *testing.T) {
	const host = "coalesce.nip.io"
	started := make(chan string, 1)
	release := make(chan struct{})
	resolver := &fakeHostResolver{
		resolve:          map[string][]string{host: {"203.0.113.40"}},
		started:          started,
		waitBeforeReturn: release,
	}
	gate := NewURLGate(URLGateOptions{
		Resolver:         resolver,
		ResolverTTL:      time.Hour,
		ResolverCacheMax: 8,
	})

	const workers = 16
	begin := make(chan struct{})
	results := make(chan ActionGateDecision, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-begin
			results <- evalURL(gate, "http://"+host+"/")
		}()
	}
	close(begin)
	waitForLookupStart(t, started)
	time.Sleep(25 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)

	for dec := range results {
		if dec.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
			t.Fatalf("decision = %v code=%q subReason=%q, want allow", dec.Decision, dec.Code, dec.SubReason)
		}
	}
	if got := resolver.callsFor(host); got != 1 {
		t.Fatalf("underlying resolver calls = %d, want 1", got)
	}
}

func evaluateURL(t *testing.T, gate *URLGate, raw string, wantDecision pb.DecisionType, wantCode string) ActionGateDecision {
	t.Helper()
	dec := evalURL(gate, raw)
	if dec.Decision != wantDecision {
		t.Fatalf("decision for %q = %v, want %v (code=%q subReason=%q)", raw, dec.Decision, wantDecision, dec.Code, dec.SubReason)
	}
	if wantCode != "" && dec.Code != wantCode {
		t.Fatalf("code for %q = %q, want %q", raw, dec.Code, wantCode)
	}
	return dec
}

func evalURL(gate *URLGate, raw string) ActionGateDecision {
	return gate.Evaluate(context.Background(), &config.PolicyInput{
		Action: &config.ActionDescriptor{
			Kind:      config.ActionKindURL,
			Verb:      config.ActionVerbRead,
			TargetURL: raw,
		},
	})
}

func waitForLookupStart(t *testing.T, started <-chan string) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resolver lookup to start")
	}
}
