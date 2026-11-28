package safetykernel

import (
	"strings"
	"testing"
)

func TestPolicyIdentityCoversEffectiveSemantics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*testing.T, *pseStore, map[string]any)
		changed bool
	}{
		{"install order", func(_ *testing.T, _ *pseStore, b map[string]any) {
			b["install-order/allow"].(map[string]any)["installed_at"] = installedSecond
			b["install-order/deny"].(map[string]any)["installed_at"] = installedFirst
		}, true},
		{"winning default", func(_ *testing.T, _ *pseStore, b map[string]any) {
			b["install-order/allow"].(map[string]any)["content"] = strings.ReplaceAll(pseContent("allow"), "default_decision: deny", "default_decision: allow")
		}, true},
		{"nonsemantic edits", func(_ *testing.T, _ *pseStore, b map[string]any) {
			b["install-order/allow"].(map[string]any)["installed_at"] = "2026-02-01T00:00:00Z"
			b["install-order/deny"].(map[string]any)["installed_at"] = "2026-02-02T00:00:00Z"
			b["install-order/deny"].(map[string]any)["content"] = "# unchanged policy\n" + pseContent("deny")
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pseEnv(t, false)
			h := newPSEStore(t)
			bundles := installOrderBundles(pseContent("allow"), pseContent("deny"), installedFirst, installedSecond)
			h.publish(t, bundles)
			if err := h.reload(); err != nil {
				t.Fatal(err)
			}
			before := h.identity(t)
			tc.mutate(t, h, bundles)
			h.publish(t, bundles)
			if err := h.reload(); err != nil {
				t.Fatal(err)
			}
			after := h.identity(t)
			if changed := before.snapshot != after.snapshot; changed != tc.changed {
				t.Fatalf("identity changed = %t, want %t: %q -> %q", changed, tc.changed, before.snapshot, after.snapshot)
			}
		})
	}
}
