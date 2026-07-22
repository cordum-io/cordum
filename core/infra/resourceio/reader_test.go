package resourceio

import (
	"context"
	"errors"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/resource"
)

type stubResolver struct {
	resolved resource.ResolvedResource
	err      error
	calls    int
}

func (s *stubResolver) Resolve(
	_ context.Context,
	_ *agentv1.ResourceRef,
	_ resource.TrustedContext,
) (resource.ResolvedResource, error) {
	s.calls++
	return s.resolved, s.err
}

func TestReaderRequiresStructuredReferenceByDefault(t *testing.T) {
	reader := Reader{}
	legacyCalled := false
	_, err := reader.Read(context.Background(), ReadRequest{
		LegacyPointer: "redis://ctx:job-a",
		Trusted:       resource.TrustedContext{TenantID: "tenant-a", JobID: "job-a"},
		LoadLegacy: func(context.Context, string) ([]byte, error) {
			legacyCalled = true
			return []byte("legacy"), nil
		},
	})
	if !errors.Is(err, ErrStructuredRequired) {
		t.Fatalf("Read error = %v, want ErrStructuredRequired", err)
	}
	if legacyCalled {
		t.Fatal("legacy loader called while compatibility was disabled")
	}
}

func TestReaderResolvesStructuredReference(t *testing.T) {
	resolver := &stubResolver{resolved: resource.ResolvedResource{
		Content: []byte(`{"safe":true}`), MediaType: "application/json",
	}}
	reader := Reader{Resolver: resolver}
	got, err := reader.Read(context.Background(), ReadRequest{
		Reference: &agentv1.ResourceRef{ResolverId: "cache"},
		Trusted:   resource.TrustedContext{TenantID: "tenant-a", JobID: "job-a"},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Content) != `{"safe":true}` || got.MediaType != "application/json" {
		t.Fatalf("Read = %#v", got)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
}

func TestReaderLegacyCompatibilityIsExplicitAndObservable(t *testing.T) {
	var events []LegacyUse
	reader := Reader{Compatibility: LegacyCompatibility{
		Enabled: true,
		Observe: func(event LegacyUse) { events = append(events, event) },
	}}
	got, err := reader.Read(context.Background(), ReadRequest{
		LegacyPointer: "redis://ctx:job-a",
		Trusted:       resource.TrustedContext{TenantID: "tenant-a", JobID: "job-a"},
		Component:     "scheduler.input",
		LoadLegacy: func(_ context.Context, pointer string) ([]byte, error) {
			if pointer != "redis://ctx:job-a" {
				t.Fatalf("pointer = %q", pointer)
			}
			return []byte("legacy"), nil
		},
	})
	if err != nil || string(got.Content) != "legacy" {
		t.Fatalf("Read = %q, %v", got.Content, err)
	}
	if len(events) != 1 || events[0].Component != "scheduler.input" || events[0].TenantID != "tenant-a" {
		t.Fatalf("events = %#v", events)
	}
}

func TestReaderDualFieldsMustResolveToIdenticalBytes(t *testing.T) {
	for name, legacy := range map[string]string{"equal": "trusted", "different": "tampered"} {
		t.Run(name, func(t *testing.T) {
			resolver := &stubResolver{resolved: resource.ResolvedResource{Content: []byte("trusted")}}
			reader := Reader{Resolver: resolver, Compatibility: LegacyCompatibility{Enabled: true}}
			got, err := reader.Read(context.Background(), ReadRequest{
				Reference:     &agentv1.ResourceRef{ResolverId: "cache"},
				LegacyPointer: "redis://res:job-a",
				Trusted:       resource.TrustedContext{TenantID: "tenant-a", JobID: "job-a"},
				LoadLegacy: func(context.Context, string) ([]byte, error) {
					return []byte(legacy), nil
				},
			})
			if name == "different" {
				if !errors.Is(err, ErrDualContentMismatch) {
					t.Fatalf("Read error = %v, want ErrDualContentMismatch", err)
				}
				return
			}
			if err != nil || string(got.Content) != "trusted" {
				t.Fatalf("Read = %q, %v", got.Content, err)
			}
		})
	}
}

func TestReaderRejectsInvalidTrustedContextBeforeLegacyRead(t *testing.T) {
	reader := Reader{Compatibility: LegacyCompatibility{Enabled: true}}
	called := false
	_, err := reader.Read(context.Background(), ReadRequest{
		LegacyPointer: "redis://ctx:job-a",
		Trusted:       resource.TrustedContext{TenantID: "tenant:a", JobID: "job-a"},
		LoadLegacy: func(context.Context, string) ([]byte, error) {
			called = true
			return nil, nil
		},
	})
	if !errors.Is(err, ErrInvalidTrustedContext) {
		t.Fatalf("Read error = %v, want ErrInvalidTrustedContext", err)
	}
	if called {
		t.Fatal("legacy loader called with invalid trusted context")
	}
}

func TestReaderPropagatesResolverAndLegacyFailures(t *testing.T) {
	resolverErr := errors.New("resolver unavailable")
	reader := Reader{Resolver: &stubResolver{err: resolverErr}}
	_, err := reader.Read(context.Background(), ReadRequest{
		Reference: &agentv1.ResourceRef{ResolverId: "cache"},
		Trusted:   resource.TrustedContext{TenantID: "tenant-a", JobID: "job-a"},
	})
	if !errors.Is(err, resolverErr) {
		t.Fatalf("resolver error = %v", err)
	}

	legacyErr := errors.New("legacy unavailable")
	reader = Reader{Compatibility: LegacyCompatibility{Enabled: true}}
	_, err = reader.Read(context.Background(), ReadRequest{
		LegacyPointer: "redis://ctx:job-a",
		Trusted:       resource.TrustedContext{TenantID: "tenant-a", JobID: "job-a"},
		LoadLegacy:    func(context.Context, string) ([]byte, error) { return nil, legacyErr },
	})
	if !errors.Is(err, legacyErr) {
		t.Fatalf("legacy error = %v", err)
	}
}

func TestReaderRejectsTypedNilResolver(t *testing.T) {
	var registry *resource.Registry
	reader := Reader{Resolver: registry}
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Read panicked for typed-nil resolver: %v", recovered)
		}
	}()
	_, err := reader.Read(context.Background(), ReadRequest{
		Reference: &agentv1.ResourceRef{ResolverId: "cache"},
		Trusted:   resource.TrustedContext{TenantID: "tenant-a", JobID: "job-a"},
	})
	if !errors.Is(err, ErrResolverUnavailable) {
		t.Fatalf("Read error = %v, want ErrResolverUnavailable", err)
	}
}
