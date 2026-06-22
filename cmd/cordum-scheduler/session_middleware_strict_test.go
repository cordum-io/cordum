package main

import (
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
)

// RED coverage for task-948d913b: a typo'd CORDUM_SDK_HANDSHAKE must refuse to
// boot, never silently degrade to admit. Pre-fix this false-passed: the lenient
// parser mapped "enforse" -> warn -> (no key material) -> (nil,nil) admit, so
// the operator who meant "enforce" booted with the gate fully disabled.
func TestBuildSessionTokenMiddleware_RejectsTypoedMode(t *testing.T) {
	for _, typo := range []string{"enforse", "enforce-mode", "true", "1"} {
		t.Run(typo, func(t *testing.T) {
			clearSigningKeyEnv(t)
			t.Setenv(scheduler.EnvHandshakeMode, typo)
			mw, err := buildSessionTokenMiddleware(nil)
			if err == nil {
				t.Fatalf("typo'd mode %q must return a non-nil error (refuse to boot)", typo)
			}
			if mw != nil {
				t.Fatalf("a rejected boot must not return a middleware; got %v", mw)
			}
		})
	}
}
