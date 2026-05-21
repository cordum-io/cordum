//go:build ignore

// EDGE-068b lint fixture: absolute `/bin/sh -c` shell spawn. MUST be flagged.
// Exercises the `[^"]*[/\\]` path-prefix branch of the interpreter regex.
package phase4fail

import (
	"context"
	"os/exec"
)

func runBinShDashC(ctx context.Context, payload string) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", payload)
	return cmd.Run()
}
