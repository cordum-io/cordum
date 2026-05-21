//go:build ignore

// EDGE-068b lint fixture: pure-argv exec.Command with no shell interpreter.
// The Phase 4 guard MUST NOT flag this — git is not a shell and there is no
// `-c` flag in the call.
package phase4pass

import (
	"context"
	"os/exec"
)

func runArgvOnly(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	return cmd.Run()
}
