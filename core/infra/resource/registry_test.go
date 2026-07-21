package resource

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type stubResolver struct {
	id      string
	content []byte
	err     error
	calls   atomic.Int32
	after   func()
}

func (s *stubResolver) ID() string { return s.id }

func (s *stubResolver) resolve(_ context.Context, _ string, _ TrustedContext, _ string, _ uint64) ([]byte, error) {
	s.calls.Add(1)
	if s.after != nil {
		s.after()
	}
	return append([]byte(nil), s.content...), s.err
}

func validRef(now time.Time, body []byte) *agentv1.ResourceRef {
	digest := sha256.Sum256(body)
	return &agentv1.ResourceRef{
		ResolverId: "cache",
		Uri:        "redis://resources/blob:tenant-a:job-a:item",
		Sha256:     digest[:],
		MediaType:  "application/json",
		SizeBytes:  uint64(len(body)),
		ExpiresAt:  timestamppb.New(now.Add(time.Minute)),
		Purpose:    "job.input",
	}
}

func TestRegistryRejectsInvalidReferencesBeforeResolver(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"safe":true}`)
	tests := map[string]func(*agentv1.ResourceRef, *TrustedContext){
		"missing resolver": func(ref *agentv1.ResourceRef, _ *TrustedContext) { ref.ResolverId = "" },
		"missing uri":      func(ref *agentv1.ResourceRef, _ *TrustedContext) { ref.Uri = "" },
		"short digest":     func(ref *agentv1.ResourceRef, _ *TrustedContext) { ref.Sha256 = []byte("short") },
		"zero size":        func(ref *agentv1.ResourceRef, _ *TrustedContext) { ref.SizeBytes = 0 },
		"missing purpose":  func(ref *agentv1.ResourceRef, _ *TrustedContext) { ref.Purpose = "" },
		"missing expiry":   func(ref *agentv1.ResourceRef, _ *TrustedContext) { ref.ExpiresAt = nil },
		"invalid expiry": func(ref *agentv1.ResourceRef, _ *TrustedContext) {
			ref.ExpiresAt = &timestamppb.Timestamp{Seconds: 253402300800}
		},
		"noncanonical media": func(ref *agentv1.ResourceRef, _ *TrustedContext) {
			ref.MediaType = "Application/JSON"
		},
		"invalid tenant": func(_ *agentv1.ResourceRef, trusted *TrustedContext) { trusted.TenantID = "a:b" },
		"invalid job":    func(_ *agentv1.ResourceRef, trusted *TrustedContext) { trusted.JobID = "../job" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			backend := &stubResolver{id: "cache", content: body}
			registry, err := NewRegistry(func() time.Time { return now }, backend)
			if err != nil {
				t.Fatalf("NewRegistry: %v", err)
			}
			ref := validRef(now, body)
			trusted := TrustedContext{TenantID: "tenant-a", JobID: "job-a"}
			mutate(ref, &trusted)
			_, err = registry.Resolve(context.Background(), ref, trusted)
			if !errors.Is(err, ErrInvalidReference) {
				t.Fatalf("Resolve error = %v, want ErrInvalidReference", err)
			}
			if got := backend.calls.Load(); got != 0 {
				t.Fatalf("resolver calls = %d, want 0", got)
			}
		})
	}
}

func TestRegistryRejectsUnknownAndDuplicateResolvers(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	registry, err := NewRegistry(func() time.Time { return now }, &stubResolver{id: "cache"})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ref := validRef(now, []byte("x"))
	ref.ResolverId = "missing"
	_, err = registry.Resolve(context.Background(), ref, TrustedContext{TenantID: "t", JobID: "j"})
	if !errors.Is(err, ErrUnknownResolver) {
		t.Fatalf("Resolve error = %v, want ErrUnknownResolver", err)
	}
	_, err = NewRegistry(nil, &stubResolver{id: "cache"}, &stubResolver{id: "cache"})
	if !errors.Is(err, ErrInvalidResolverConfig) {
		t.Fatalf("duplicate error = %v, want ErrInvalidResolverConfig", err)
	}
}

func TestRegistryHasNoNetworkOrFileFallback(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	backend := &stubResolver{id: "cache", content: []byte("trusted")}
	registry, err := NewRegistry(func() time.Time { return now }, backend)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	for _, resolverID := range []string{"http", "https", "file"} {
		ref := validRef(now, []byte("trusted"))
		ref.ResolverId = resolverID
		ref.Uri = resolverID + "://attacker.invalid/resource"
		_, err = registry.Resolve(context.Background(), ref, TrustedContext{TenantID: "t", JobID: "j"})
		if !errors.Is(err, ErrUnknownResolver) {
			t.Fatalf("resolver %q error = %v, want ErrUnknownResolver", resolverID, err)
		}
	}
	if got := backend.calls.Load(); got != 0 {
		t.Fatalf("installed backend calls = %d, want 0 for unsupported DNS/file paths", got)
	}
}

func TestRegistryValidatesExpirySizeAndDigestAfterFetch(t *testing.T) {
	start := time.Unix(1_800_000_000, 0)
	body := []byte("trusted")
	for name, configure := range map[string]func(*agentv1.ResourceRef, *stubResolver, *time.Time){
		"expired before": func(ref *agentv1.ResourceRef, _ *stubResolver, _ *time.Time) {
			ref.ExpiresAt = timestamppb.New(start)
		},
		"expired during": func(ref *agentv1.ResourceRef, resolver *stubResolver, clock *time.Time) {
			resolver.after = func() { *clock = start.Add(2 * time.Minute) }
		},
		"size mismatch": func(_ *agentv1.ResourceRef, resolver *stubResolver, _ *time.Time) {
			resolver.content = []byte("different length")
		},
		"digest mismatch": func(_ *agentv1.ResourceRef, resolver *stubResolver, _ *time.Time) {
			resolver.content = []byte("truster")
		},
	} {
		t.Run(name, func(t *testing.T) {
			clock := start
			resolver := &stubResolver{id: "cache", content: body}
			ref := validRef(start, body)
			configure(ref, resolver, &clock)
			registry, err := NewRegistry(func() time.Time { return clock }, resolver)
			if err != nil {
				t.Fatalf("NewRegistry: %v", err)
			}
			_, err = registry.Resolve(context.Background(), ref, TrustedContext{TenantID: "t", JobID: "j"})
			want := map[string]error{"expired before": ErrExpired, "expired during": ErrExpired,
				"size mismatch": ErrSizeMismatch, "digest mismatch": ErrDigestMismatch}[name]
			if !errors.Is(err, want) {
				t.Fatalf("Resolve error = %v, want %v", err, want)
			}
		})
	}
}

func TestRegistryConcurrentResolve(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte("trusted")
	backend := &stubResolver{id: "cache", content: body}
	registry, err := NewRegistry(func() time.Time { return now }, backend)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ref := validRef(now, body)
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resolved, resolveErr := registry.Resolve(context.Background(), ref,
				TrustedContext{TenantID: "tenant-a", JobID: "job-a"})
			if resolveErr != nil || string(resolved.Content) != string(body) {
				t.Errorf("Resolve = %q, %v", resolved.Content, resolveErr)
			}
		}()
	}
	wg.Wait()
	if got := backend.calls.Load(); got != 64 {
		t.Fatalf("resolver calls = %d, want 64", got)
	}
}

func TestRegistryFailsClosedForNilRuntimeInputs(t *testing.T) {
	var registry *Registry
	ref := validRef(time.Now(), []byte("x"))
	trusted := TrustedContext{TenantID: "t", JobID: "j"}
	if _, err := registry.Resolve(context.Background(), ref, trusted); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil registry error = %v, want ErrUnavailable", err)
	}
	registry, err := NewRegistry(nil, &stubResolver{id: "cache", content: []byte("x")})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, err = registry.Resolve(nil, ref, trusted); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("nil context error = %v, want ErrInvalidReference", err)
	}
}

func TestRegistrySnapshotsReferenceBeforeFetch(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte("trusted")
	ref := validRef(now, body)
	backend := &stubResolver{id: "cache", content: body}
	backend.after = func() {
		ref.Sha256[0] ^= 0xff
		ref.SizeBytes++
		ref.ExpiresAt = timestamppb.New(now.Add(-time.Minute))
	}
	registry, err := NewRegistry(func() time.Time { return now }, backend)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	resolved, err := registry.Resolve(context.Background(), ref,
		TrustedContext{TenantID: "tenant-a", JobID: "job-a"})
	if err != nil || string(resolved.Content) != string(body) {
		t.Fatalf("Resolve = %q, %v", resolved.Content, err)
	}
}

func TestValidateTrustedContextUsesRegistryGrammar(t *testing.T) {
	for _, jobID := range []string{"job-a", "run:step@1", "run:loop[0]@12"} {
		if err := ValidateTrustedContext(TrustedContext{TenantID: "tenant-a", JobID: jobID}); err != nil {
			t.Fatalf("valid job ID %q: %v", jobID, err)
		}
	}
	for _, trusted := range []TrustedContext{
		{TenantID: "tenant:a", JobID: "job-a"},
		{TenantID: "tenant-a", JobID: "run/step"},
		{TenantID: "tenant-a", JobID: "run?step"},
		{TenantID: "tenant-a", JobID: "run..step"},
	} {
		if err := ValidateTrustedContext(trusted); !errors.Is(err, ErrInvalidReference) {
			t.Fatalf("invalid context %+v error = %v, want ErrInvalidReference", trusted, err)
		}
	}
}
