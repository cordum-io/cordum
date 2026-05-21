package gateway

import (
	"context"
	"errors"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/store"
)

// TestEnforceJobBackpressure_FailClosedOnConfigError asserts that when the
// config service returns an error from Effective(), enforceJobBackpressure
// returns a non-nil error (fail-closed) instead of silently allowing the
// request through. A misconfigured config service must not disable
// backpressure enforcement.
//
// Regression for audit finding #8 (helpers.go:789-820).
func TestEnforceJobBackpressure_FailClosedOnConfigError(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	// jobStore must be present — the early-return at the top of
	// enforceJobBackpressure short-circuits when jobStore is nil. Use the same
	// miniredis so we exercise the configSvc.Effective branch specifically.
	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer func() { _ = jobStore.Close() }()

	// A closed configsvc returns an error from every Effective() call.
	cfg, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	_ = cfg.Close()

	s := &server{configSvc: cfg, jobStore: jobStore}
	gotErr := s.enforceJobBackpressure(context.Background(), "tenant-x", "team-y")
	if gotErr == nil {
		t.Fatalf("enforceJobBackpressure must fail-closed when configSvc errors; got nil")
	}
}

// TestEnforceJobBackpressure_NilConfigAllowsWithWarning asserts that when
// configSvc is nil, the function returns nil (does not fail-closed) — the
// architect's directive is "log warning when configSvc is nil; fail-closed
// only when configSvc returns an error". This pins the nil-configSvc path so
// later refactors don't accidentally tighten it.
//
// Regression companion for audit finding #8.
func TestEnforceJobBackpressure_NilConfigAllowsWithWarning(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()
	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer func() { _ = jobStore.Close() }()

	s := &server{configSvc: nil, jobStore: jobStore}
	if err := s.enforceJobBackpressure(context.Background(), "tenant-x", "team-y"); err != nil {
		t.Fatalf("nil configSvc must allow with warning, got err=%v", err)
	}
}

// TestEnforceMemoryID_FailClosedOnMalformedContext asserts that when the
// effective config contains a "context" key whose value cannot be parsed as a
// ContextConfig (e.g. wrong type, malformed JSON shape), enforceMemoryID
// returns a 503 instead of silently allowing the memory_id through.
//
// Regression for audit finding #9 (helpers.go:898-926).
func TestEnforceMemoryID_FailClosedOnMalformedContext(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	cfg, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	if err := cfg.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("ensure default: %v", err)
	}
	// Write a system-default config whose "context" key is a malformed value
	// that JSON-marshals OK but fails Unmarshal into ContextConfig.
	// ContextConfig has fields like AllowedMemoryIDs/DeniedMemoryIDs []string.
	// Putting a string (not a struct) into "context" makes ParseEffectiveContextMap
	// return ok=false on Unmarshal.
	doc, err := cfg.Get(context.Background(), configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	doc.Data["context"] = "this-is-not-an-object"
	if err := cfg.Set(context.Background(), doc); err != nil {
		t.Fatalf("set: %v", err)
	}

	s := &server{configSvc: cfg}
	gotErr := s.enforceMemoryID(context.Background(), "tenant-x", "", "", "", "mem-id-a")
	if gotErr == nil {
		t.Fatalf("enforceMemoryID must fail-closed on malformed context, got nil")
	}
	var memErr memoryPolicyError
	if !errors.As(gotErr, &memErr) {
		t.Fatalf("expected memoryPolicyError, got %T %v", gotErr, gotErr)
	}
	if memErr.status != 503 {
		t.Fatalf("expected 503 on malformed context config, got %d", memErr.status)
	}
}

// TestEnforceMemoryID_AbsentContextStillAllows asserts that when the effective
// config has NO "context" key at all (legitimate absence, not a malformed
// value), enforceMemoryID still returns nil (allow). Only present-but-malformed
// "context" should fail-closed.
//
// Regression companion for audit finding #9.
func TestEnforceMemoryID_AbsentContextStillAllows(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	cfg, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	if err := cfg.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("ensure default: %v", err)
	}
	// EnsureDefault writes a minimal config with no "context" section.

	s := &server{configSvc: cfg}
	if err := s.enforceMemoryID(context.Background(), "tenant-x", "", "", "", "mem-id-a"); err != nil {
		t.Fatalf("absent context key must still allow, got %v", err)
	}
}
