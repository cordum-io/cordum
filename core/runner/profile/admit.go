package profile

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Admission records what a run was admitted against, for the evidence chain.
type Admission struct {
	Profile       Profile      `json:"profile"`
	ProfileDigest string       `json:"profile_digest"`
	Required      []Capability `json:"required"`
	Probe         ProbeResult  `json:"probe"`
	AdmittedAt    time.Time    `json:"admitted_at"`
}

// MissingCapability is a required capability the probe did not report available.
// Status is empty when the probe result has no entry for it.
type MissingCapability struct {
	Capability Capability `json:"capability"`
	Status     Status     `json:"status"`
	Detail     string     `json:"detail"`
}

// RefusalError is the only way a start fails: there is no weaker-run fallback.
type RefusalError struct {
	Missing []MissingCapability
	Err     error
	Probe   ProbeResult
}

func (e *RefusalError) Error() string {
	reasons := make([]string, 0, len(e.Missing)+1)
	for _, m := range e.Missing {
		reasons = append(reasons, fmt.Sprintf("%s %s (%s)", m.Capability, cmp.Or(m.Status, "absent"), m.Detail))
	}
	if e.Err != nil {
		reasons = append(reasons, e.Err.Error())
	}
	return "run refused: " + strings.Join(reasons, "; ")
}

func (e *RefusalError) Unwrap() error { return e.Err }

// Admit probes the host on every call and admits p only when every capability
// in p.Requirements() is available. Anything else returns a *RefusalError and
// a zero Admission.
func Admit(ctx context.Context, p Profile, pr Prober) (Admission, error) {
	if p.Network.Mode != NetworkNone && p.Network.Mode != NetworkAllowlist {
		// Requirements drops net.default_deny for any mode but allowlist (I02).
		return Admission{}, &RefusalError{Err: fmt.Errorf("unsupported network mode %q", p.Network.Mode)}
	}
	required := p.Requirements()
	res, err := pr.Probe(ctx, p)
	var missing []MissingCapability
	for _, c := range required {
		r, ok := res.Capabilities[c]
		if r.Status == StatusAvailable {
			continue
		}
		if !ok {
			r.Detail = "absent from probe result"
		}
		missing = append(missing, MissingCapability{Capability: c, Status: r.Status, Detail: r.Detail})
	}
	if err != nil || len(missing) > 0 {
		return Admission{}, &RefusalError{Missing: missing, Err: err, Probe: res}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return Admission{}, &RefusalError{Err: fmt.Errorf("profile digest: %w", err), Probe: res}
	}
	sum := sha256.Sum256(b)
	return Admission{Profile: p, ProfileDigest: hex.EncodeToString(sum[:]), Required: required, Probe: res, AdmittedAt: time.Now().UTC()}, nil
}
