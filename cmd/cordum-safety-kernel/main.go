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
	// Fail fast if strict=enforce and the trust store is empty: with no
	// keys to verify against, every bundle will be refused — better to
	// surface that as a clear boot error than to silently drop policy.
	if err := policysign.CheckKernelBoot(); err != nil {
		slog.Error("safety-kernel refused to start", "error", err)
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
