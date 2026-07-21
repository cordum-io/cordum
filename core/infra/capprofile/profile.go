// Package capprofile resolves the single explicit CAP runtime profile switch
// shared by the scheduler, gateway, and workflow binaries (task-a13f83fa,
// step 12).
//
// CAP-PRODUCTION is an orthogonal opt-in profile, not a conformance tier. A
// binary runs under it only when the operator sets CORDUM_CAP_PROFILE
// explicitly AND every mandatory dependency reports ready. Both halves are
// required: the profile alone selects intent, Readiness proves capability.
//
// The parser deliberately fails startup on an unrecognised token rather than
// falling back to compat. A silent downgrade is the dangerous failure mode
// here — an operator who writes "prod" would otherwise run an unprotected
// control plane while believing production enforcement is active.
package capprofile

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// EnvVar is the only supported way to select the CAP runtime profile.
const EnvVar = "CORDUM_CAP_PROFILE"

// CapabilityProduction is the handshake capability key asserting that this
// component enforces the CAP-PRODUCTION profile. It is advertised only after
// an authenticated handshake and full dependency initialization.
const CapabilityProduction = "cap_production"

var (
	// ErrUnknownProfile reports an unrecognised CORDUM_CAP_PROFILE value.
	ErrUnknownProfile = errors.New("capprofile: unknown CAP profile")
	// ErrProductionDependencyMissing reports that CAP-PRODUCTION was selected
	// while at least one mandatory dependency is not initialized.
	ErrProductionDependencyMissing = errors.New("capprofile: CAP-PRODUCTION dependency not initialized")
)

// Profile is the explicit CAP runtime profile for a process.
type Profile int

const (
	// Compat is the default: legacy unsigned packets, string pointers, and
	// operator-configured fail-open modes remain reachable. It MUST NOT
	// advertise CAP-PRODUCTION.
	Compat Profile = iota
	// Production selects CAP-PRODUCTION enforcement.
	Production
)

func (p Profile) String() string {
	if p == Production {
		return "production"
	}
	return "compat"
}

// IsProduction reports whether the operator selected CAP-PRODUCTION. It says
// nothing about whether the dependencies needed to enforce it are ready.
func (p Profile) IsProduction() bool { return p == Production }

// Parse resolves an explicit profile token. An empty or whitespace-only value
// means "not configured" and yields Compat. Any other unrecognised value is an
// error so the process refuses to start instead of downgrading silently.
func Parse(raw string) (Profile, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return Compat, nil
	case "compat":
		return Compat, nil
	case "production":
		return Production, nil
	default:
		return Compat, fmt.Errorf(
			"%w: %s=%q (supported: %q, %q)",
			ErrUnknownProfile, EnvVar, raw, Compat.String(), Production.String(),
		)
	}
}

// FromEnv resolves the profile from the process environment.
func FromEnv() (Profile, error) { return Parse(os.Getenv(EnvVar)) }

// Readiness records whether each mandatory CAP-PRODUCTION dependency has been
// initialized. Its zero value is "nothing ready", so a caller that forgets to
// populate it can never accidentally advertise production.
type Readiness struct {
	// AuthenticatedTransportReady: the established broker connection uses
	// verified TLS plus broker credentials or a client certificate.
	AuthenticatedTransportReady bool
	// HandshakeEnforced: the authenticated worker handshake is in enforcing
	// mode (not observe/permissive).
	HandshakeEnforced bool
	// RawAdmissionInstalled: the exact-wire admission hook is installed and
	// frozen on the bus before any subscription exists.
	RawAdmissionInstalled bool
	// ReplayStoreReady: the atomic replay store backing first/duplicate/
	// conflict decisions is reachable.
	ReplayStoreReady bool
	// TrustStoreReady: a local sender/tenant-scoped signing-key trust store is
	// configured; packet-provided keys are never trusted.
	TrustStoreReady bool
	// SessionResolverReady: authenticated transport/session authority can be
	// derived without consulting packet payload.
	SessionResolverReady bool
	// OutboundSignerReady: this component can sign the packets it publishes
	// against the actual destination subject.
	OutboundSignerReady bool
	// ResourceAllowlistReady: an operator-installed resolver allowlist is
	// loaded; generic URI fetch has no fallback.
	ResourceAllowlistReady bool
	// SafetyKernelReady: the Safety Kernel dependency is configured.
	SafetyKernelReady bool
	// OutputSafetyEnabled: output safety evaluation is active.
	OutputSafetyEnabled bool
	// FailClosedModes: no input/output/compensation fail-open mode is active.
	FailClosedModes bool
}

// missing lists the dependency names that are not ready, in a stable order so
// startup diagnostics are deterministic.
func (r Readiness) missing() []string {
	checks := []struct {
		name  string
		ready bool
	}{
		{"authenticated_transport", r.AuthenticatedTransportReady},
		{"handshake_enforced", r.HandshakeEnforced},
		{"raw_admission_installed", r.RawAdmissionInstalled},
		{"replay_store", r.ReplayStoreReady},
		{"trust_store", r.TrustStoreReady},
		{"session_resolver", r.SessionResolverReady},
		{"outbound_signer", r.OutboundSignerReady},
		{"resource_allowlist", r.ResourceAllowlistReady},
		{"safety_kernel", r.SafetyKernelReady},
		{"output_safety", r.OutputSafetyEnabled},
		{"fail_closed_modes", r.FailClosedModes},
	}
	var out []string
	for _, c := range checks {
		if !c.ready {
			out = append(out, c.name)
		}
	}
	return out
}

// Ready reports whether every mandatory dependency is initialized.
func (r Readiness) Ready() bool { return len(r.missing()) == 0 }

// Validate returns an error naming every dependency that is not initialized.
// It reports all of them at once so an operator fixes one deployment rather
// than restarting into the next missing dependency.
func (r Readiness) Validate() error {
	missing := r.missing()
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrProductionDependencyMissing, strings.Join(missing, ", "))
}

// AdvertiseProduction reports whether this component may claim CAP-PRODUCTION
// conformance on the wire. Both the explicit operator profile and full
// dependency readiness are required.
func (p Profile) AdvertiseProduction(r Readiness) bool {
	return p.IsProduction() && r.Ready()
}

// Capabilities returns a copy of base with the CAP-PRODUCTION capability key
// set only when this component may advertise it. The caller's map is never
// mutated, so a readiness check that fails cannot leave a stale production
// claim behind in a reused literal.
func (p Profile) Capabilities(base map[string]bool, r Readiness) map[string]bool {
	caps := make(map[string]bool, len(base)+1)
	for k, v := range base {
		caps[k] = v
	}
	if p.AdvertiseProduction(r) {
		caps[CapabilityProduction] = true
	}
	return caps
}
