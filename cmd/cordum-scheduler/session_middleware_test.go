package main

// Policy coverage for the session-token wiring (task-5c18f890). The gate
// must be opt-in (back-compat admit when unconfigured) yet fail CLOSED when
// an operator turns on enforcement without the key material to back it.

import (
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/policysign"
)

func clearSigningKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv(policysign.EnvSigningKey, "")
	t.Setenv(policysign.EnvSigningKeyPath, "")
	t.Setenv(policysign.EnvDevSigningSeed, "")
}

func TestBuildSessionTokenMiddleware_BackCompatAndFailClosed(t *testing.T) {
	t.Run("unset disables the gate (back-compat admit)", func(t *testing.T) {
		t.Setenv(scheduler.EnvHandshakeMode, "")
		mw, err := buildSessionTokenMiddleware(nil)
		if err != nil || mw != nil {
			t.Fatalf("unset must return (nil,nil); got mw=%v err=%v", mw, err)
		}
	})

	t.Run("off disables the gate (admit)", func(t *testing.T) {
		t.Setenv(scheduler.EnvHandshakeMode, "off")
		mw, err := buildSessionTokenMiddleware(nil)
		if err != nil || mw != nil {
			t.Fatalf("off must return (nil,nil); got mw=%v err=%v", mw, err)
		}
	})

	t.Run("enforce without key material fails closed", func(t *testing.T) {
		clearSigningKeyEnv(t)
		t.Setenv(scheduler.EnvHandshakeMode, "enforce")
		mw, err := buildSessionTokenMiddleware(nil)
		if err == nil {
			t.Fatal("enforce without key material must fail closed (non-nil error)")
		}
		if mw != nil {
			t.Fatalf("a fail-closed result must not return a middleware; got %v", mw)
		}
	})

	t.Run("warn without key material degrades to admit", func(t *testing.T) {
		clearSigningKeyEnv(t)
		t.Setenv(scheduler.EnvHandshakeMode, "warn")
		mw, err := buildSessionTokenMiddleware(nil)
		if err != nil || mw != nil {
			t.Fatalf("warn without key must degrade to (nil,nil); got mw=%v err=%v", mw, err)
		}
	})
}
