package safetykernel

// The policy identity (the snapshot string) changes whenever the effective
// policy does. Two bundles with unchanged contents but swapped install times
// change which rule or default takes effect, so a reload of them must yield a
// new identity, and nothing keyed on the old one, such as a cached decision,
// may keep answering.

import (
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	installedFirst  = "2026-01-01T00:00:00Z"
	installedSecond = "2026-01-02T00:00:00Z"
)

// installOrderBundles returns the allow and deny bundles stamped with the
// given install times.
func installOrderBundles(allow, deny, allowAt, denyAt string) map[string]any {
	return map[string]any{
		"install-order/allow": map[string]any{"content": allow, "installed_at": allowAt},
		"install-order/deny":  map[string]any{"content": deny, "installed_at": denyAt},
	}
}

func TestPolicyIdentityFollowsInstallOrder(t *testing.T) {
	for _, tc := range []struct {
		name            string
		allow, deny     string
		allowAt, denyAt string // the allow bundle takes effect first
	}{
		// The later-installed bundle wins a rule id both bundles define.
		{"shared rule id", pseContent("allow"), pseContent("deny"), installedSecond, installedFirst},
		// The earliest-installed bundle's default_decision wins.
		{"default decision", "default_tenant: default\ndefault_decision: allow\n",
			"default_tenant: default\ndefault_decision: deny\n", installedFirst, installedSecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pseEnv(t, false)
			swapped := installOrderBundles(tc.allow, tc.deny, tc.denyAt, tc.allowAt)

			// A kernel loading the swapped bundles from scratch denies.
			fresh := newPSEStore(t)
			fresh.publish(t, swapped)
			if err := fresh.reload(); err != nil {
				t.Fatalf("fresh reload: %v", err)
			}
			if got := fresh.identity(t).decision; got != pb.DecisionType_DECISION_TYPE_DENY {
				t.Fatalf("fresh kernel on swapped bundles = %s, want DENY", got)
			}

			h := newPSEStore(t)
			h.srv.cacheTTL = time.Minute
			h.publish(t, installOrderBundles(tc.allow, tc.deny, tc.allowAt, tc.denyAt))
			if err := h.reload(); err != nil {
				t.Fatalf("reload: %v", err)
			}
			before := h.identity(t) // answered and cached
			if before.decision != pb.DecisionType_DECISION_TYPE_ALLOW {
				t.Fatalf("decision before the swap = %s, want ALLOW", before.decision)
			}

			h.publish(t, swapped)
			if err := h.reload(); err != nil {
				t.Fatalf("reload after the swap: %v", err)
			}
			after := h.identity(t)
			if after.snapshot == before.snapshot {
				t.Errorf("policy identity unchanged after the effective policy changed: %q", after.snapshot)
			}
			if after.decision != pb.DecisionType_DECISION_TYPE_DENY {
				t.Errorf("decision after the swap = %s, want DENY as a fresh kernel answers", after.decision)
			}
		})
	}
}
