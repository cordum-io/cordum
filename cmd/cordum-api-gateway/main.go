package main

import (
	"log/slog"
	"os"

	"github.com/cordum/cordum/core/controlplane/gateway"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/policysign"
)

func main() {
	logging.Init("gateway")
	slog.Info("cordum api gateway starting...")
	buildinfo.Log("cordum-api-gateway")
	// Fail fast if the operator opted into enforce mode without a
	// signing key — we do not want to discover that at first bundle
	// save. The helper also emits the authoritative INFO log describing
	// the active mode + key_id for boot-time observability.
	if err := policysign.CheckGatewayBoot(); err != nil {
		slog.Error("api gateway: policy signing preflight failed", "error", err)
		os.Exit(1)
	}
	cfg := config.Load()
	entitlementResolver := licensing.NewEntitlementResolver()
	entitlementResolver.Init()
	if err := gateway.RunWithAuth(cfg, nil, entitlementResolver); err != nil {
		slog.Error("api gateway error", "error", err)
		os.Exit(1)
	}
}
