package main

import (
	"log/slog"
	"os"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/workflow"
)

func main() {
	logging.Init("workflow-engine")
	slog.Info("cordum workflow engine starting...")
	buildinfo.Log("cordum-workflow-engine")
	cfg := config.Load()
	entitlementResolver := licensing.NewEntitlementResolver()
	entitlementResolver.Init()

	// Constructed here (not in core/workflow) because core/workflow cannot
	// import core/controlplane/scheduler without an import cycle through
	// core/telemetry (which imports core/workflow for run metrics).
	var outputSafety model.OutputSafetyChecker
	if cfg.OutputPolicyEnabled {
		client, err := scheduler.NewOutputSafetyClientWithRedis(cfg.SafetyKernelAddr, cfg.RedisURL)
		if err != nil {
			slog.Error("failed to connect output policy client", "error", err)
			os.Exit(1)
		}
		defer func() { _ = client.Close() }()
		outputSafety = client
	}

	if err := workflow.RunWithEntitlements(cfg, entitlementResolver, outputSafety); err != nil {
		slog.Error("workflow engine error", "error", err)
		os.Exit(1)
	}
}
