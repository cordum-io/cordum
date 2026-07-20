package capprofile

import (
	"errors"
	"strings"
	"testing"
)

func TestParseDefaultsToCompatOnlyWhenUnset(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t\n"} {
		got, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v, want nil", raw, err)
		}
		if got != Compat {
			t.Fatalf("Parse(%q) = %v, want Compat", raw, got)
		}
	}
}

func TestParseAcceptsExplicitTokens(t *testing.T) {
	cases := map[string]Profile{
		"compat":       Compat,
		"  compat  ":   Compat,
		"COMPAT":       Compat,
		"production":   Production,
		"Production":   Production,
		"  PRODUCTION": Production,
	}
	for raw, want := range cases {
		got, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v, want nil", raw, err)
		}
		if got != want {
			t.Fatalf("Parse(%q) = %v, want %v", raw, got, want)
		}
	}
}

// An unknown token MUST fail startup. Silently mapping it to Compat is the
// downgrade attack this parser exists to prevent: an operator who typos
// "prod" would otherwise run unprotected while believing production is on.
func TestParseRejectsUnknownTokenInsteadOfDowngrading(t *testing.T) {
	for _, raw := range []string{"prod", "production ", "produktion", "1", "true", "strict", "compat,production"} {
		if raw == "production " {
			continue // trimmed form is valid; covered above
		}
		got, err := Parse(raw)
		if !errors.Is(err, ErrUnknownProfile) {
			t.Fatalf("Parse(%q) error = %v, want ErrUnknownProfile", raw, err)
		}
		if got != Compat {
			t.Fatalf("Parse(%q) = %v on error, want zero-value Compat", raw, got)
		}
		if !strings.Contains(err.Error(), EnvVar) {
			t.Fatalf("Parse(%q) error %q should name %s for operator diagnosis", raw, err.Error(), EnvVar)
		}
	}
}

func TestFromEnvReadsProcessEnvironment(t *testing.T) {
	t.Setenv(EnvVar, "production")
	got, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v, want nil", err)
	}
	if !got.IsProduction() {
		t.Fatalf("FromEnv() = %v, want production", got)
	}

	t.Setenv(EnvVar, "nonsense")
	if _, err := FromEnv(); !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("FromEnv() error = %v, want ErrUnknownProfile", err)
	}
}

func TestCompatProfileIsNotProduction(t *testing.T) {
	if Compat.IsProduction() {
		t.Fatal("Compat.IsProduction() = true, want false")
	}
	if Compat.String() != "compat" || Production.String() != "production" {
		t.Fatalf("String() = %q/%q, want compat/production", Compat.String(), Production.String())
	}
}

func fullReadiness() Readiness {
	return Readiness{
		HandshakeEnforced:      true,
		RawAdmissionInstalled:  true,
		ReplayStoreReady:       true,
		TrustStoreReady:        true,
		SessionResolverReady:   true,
		OutboundSignerReady:    true,
		ResourceAllowlistReady: true,
		SafetyKernelReady:      true,
		OutputSafetyEnabled:    true,
		FailClosedModes:        true,
	}
}

func TestReadinessValidateAcceptsFullyInitializedDependencies(t *testing.T) {
	if err := fullReadiness().Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// Every mandatory dependency must independently block advertisement. A
// zero-value Readiness must never be treated as ready.
func TestReadinessValidateRejectsEachMissingDependency(t *testing.T) {
	clear := map[string]func(*Readiness){
		"handshake_enforced":      func(r *Readiness) { r.HandshakeEnforced = false },
		"raw_admission_installed": func(r *Readiness) { r.RawAdmissionInstalled = false },
		"replay_store":            func(r *Readiness) { r.ReplayStoreReady = false },
		"trust_store":             func(r *Readiness) { r.TrustStoreReady = false },
		"session_resolver":        func(r *Readiness) { r.SessionResolverReady = false },
		"outbound_signer":         func(r *Readiness) { r.OutboundSignerReady = false },
		"resource_allowlist":      func(r *Readiness) { r.ResourceAllowlistReady = false },
		"safety_kernel":           func(r *Readiness) { r.SafetyKernelReady = false },
		"output_safety":           func(r *Readiness) { r.OutputSafetyEnabled = false },
		"fail_closed_modes":       func(r *Readiness) { r.FailClosedModes = false },
	}
	for name, mutate := range clear {
		r := fullReadiness()
		mutate(&r)
		err := r.Validate()
		if !errors.Is(err, ErrProductionDependencyMissing) {
			t.Fatalf("Validate() with %s cleared = %v, want ErrProductionDependencyMissing", name, err)
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("Validate() error %q must name the missing dependency %q", err.Error(), name)
		}
	}

	var zero Readiness
	if err := zero.Validate(); !errors.Is(err, ErrProductionDependencyMissing) {
		t.Fatalf("zero Readiness Validate() = %v, want ErrProductionDependencyMissing", err)
	}
}

func TestReadinessValidateReportsEveryMissingDependency(t *testing.T) {
	var zero Readiness
	err := zero.Validate()
	if err == nil {
		t.Fatal("zero Readiness Validate() = nil, want error")
	}
	for _, name := range []string{"handshake_enforced", "replay_store", "safety_kernel", "fail_closed_modes"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("aggregated error %q missing %q", err.Error(), name)
		}
	}
}

// Advertisement is the externally visible claim "this component enforces
// CAP-PRODUCTION". It must be false in compat mode even when every dependency
// happens to be initialized, and false in production until they all are.
func TestAdvertiseProductionRequiresProfileAndReadiness(t *testing.T) {
	if Compat.AdvertiseProduction(fullReadiness()) {
		t.Fatal("compat profile advertised CAP-PRODUCTION")
	}
	var zero Readiness
	if Production.AdvertiseProduction(zero) {
		t.Fatal("production profile advertised CAP-PRODUCTION with no dependencies ready")
	}
	if !Production.AdvertiseProduction(fullReadiness()) {
		t.Fatal("production profile with full readiness did not advertise CAP-PRODUCTION")
	}
}

func TestCapabilitiesStampsProductionKeyOnlyWhenAdvertising(t *testing.T) {
	base := map[string]bool{"routing": true}

	compatCaps := Compat.Capabilities(base, fullReadiness())
	if compatCaps[CapabilityProduction] {
		t.Fatalf("compat capabilities advertised %s", CapabilityProduction)
	}
	if !compatCaps["routing"] {
		t.Fatal("Capabilities dropped caller capability")
	}

	prodCaps := Production.Capabilities(base, fullReadiness())
	if !prodCaps[CapabilityProduction] {
		t.Fatalf("production capabilities missing %s", CapabilityProduction)
	}

	notReady := Production.Capabilities(base, Readiness{})
	if notReady[CapabilityProduction] {
		t.Fatalf("unready production capabilities advertised %s", CapabilityProduction)
	}

	// The caller's map must never be mutated: callers reuse literals and a
	// leaked production key would outlive a failed readiness check.
	if _, ok := base[CapabilityProduction]; ok {
		t.Fatal("Capabilities mutated the caller's map")
	}
	if len(base) != 1 {
		t.Fatalf("Capabilities mutated caller map length = %d, want 1", len(base))
	}
}

func TestCapabilitiesHandlesNilBase(t *testing.T) {
	caps := Production.Capabilities(nil, fullReadiness())
	if !caps[CapabilityProduction] {
		t.Fatalf("nil base lost %s", CapabilityProduction)
	}
	if len(caps) != 1 {
		t.Fatalf("nil base produced %d capabilities, want 1", len(caps))
	}
}
