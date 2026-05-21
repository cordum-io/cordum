//go:build ignore

// EDGE-068b lint fixture: inline `// no-shell-exec-lint` marker on a single
// shell spawn with no runtime branch. Demonstrates the minimum marker shape
// the convention accepts. MUST pass.
package phase4exception

import (
	"context"
	"os/exec"
)

func runAuditedShellSpawn(ctx context.Context, command string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command) // no-shell-exec-lint: audited single-tenant migration helper
	return cmd.Run()
}
