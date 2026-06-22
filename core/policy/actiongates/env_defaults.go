package actiongates

import (
	"log/slog"
	"os"
	"strings"

	"github.com/cordum/cordum/core/infra/env"
)

const (
	// envMCPTaintFailClosedDestructive gates whether a destructive MCP call whose
	// session-taint lookup errored is held fail-closed. Default ON in production
	// profiles, OFF in dev/demo; an explicit value overrides the profile default.
	envMCPTaintFailClosedDestructive = "CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE"
	// envMCPTaintFailClosedApprover reports whether an approver is wired to act on
	// a REQUIRE_HUMAN hold. When unset/false the fail-closed no-clean-confirmation
	// outcome is a hard DENY instead of an unactionable hold. Default false.
	envMCPTaintFailClosedApprover = "CORDUM_MCP_TAINT_FAILCLOSED_APPROVER"
)

// ResolveFailClosedDestructiveOnTaintLookupError resolves the destructive-path
// taint fail-closed default with profile-aware defaulting and explicit-override
// precedence:
//
//   - unset / empty            => env.IsProduction() (ON in production, OFF in dev/demo)
//   - explicit {1,true,t,yes,y,on}  => true
//   - explicit {0,false,f,no,n,off} => false (an explicit value wins, even in production)
//   - unrecognized non-empty   => env.IsProduction(), with a warning
//
// This is the single source of truth for the default. Every pipeline-construction
// site (the gateway HTTP path and the safety-kernel gRPC path) calls it so the two
// enforcement surfaces cannot diverge into a split-brain (one fail-closing while the
// other allows the same destructive call). os.LookupEnv is used so an explicit
// "false" in a production profile is distinguishable from unset.
func ResolveFailClosedDestructiveOnTaintLookupError() bool {
	raw, ok := os.LookupEnv(envMCPTaintFailClosedDestructive)
	if !ok || strings.TrimSpace(raw) == "" {
		return env.IsProduction()
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		prod := env.IsProduction()
		slog.Warn("unrecognized CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE value; using profile default",
			"value", raw,
			"production_default", prod,
		)
		return prod
	}
}

// ApproverConfiguredForTaintFailClosed reports whether an approver is configured
// to act on a REQUIRE_HUMAN hold (CORDUM_MCP_TAINT_FAILCLOSED_APPROVER). Default
// false => the fail-closed no-clean-confirmation outcome is a hard DENY rather
// than parking a destructive action in an unactionable hold.
func ApproverConfiguredForTaintFailClosed() bool {
	return env.Bool(envMCPTaintFailClosedApprover)
}
