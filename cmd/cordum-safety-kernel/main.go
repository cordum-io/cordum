package main

import (
	"log/slog"
	"os"

	"github.com/cordum/cordum/core/controlplane/safetykernel"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/policysign"
)

func main() {
	logging.Init("safety-kernel")
	slog.Info("cordum safety kernel starting...")
	buildinfo.Log("cordum-safety-kernel")
	// Enforce mode requires at least one trusted public key — otherwise
	// every bundle would be rejected at load time. Refuse to start so
	// the operator notices the misconfiguration immediately.
	if err := policysign.CheckKernelBoot(); err != nil {
		slog.Error("safety kernel: policy signing preflight failed", "error", err)
		os.Exit(1)
	}
	cfg := config.Load()
	entitlementResolver := licensing.NewEntitlementResolver()
	entitlementResolver.Init()
	if err := safetykernel.RunWithEntitlements(cfg, entitlementResolver); err != nil {
		slog.Error("safety-kernel error", "error", err)
		os.Exit(1)
	}
}
