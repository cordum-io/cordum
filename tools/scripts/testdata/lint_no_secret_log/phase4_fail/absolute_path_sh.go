//go:build ignore

// EDGE-068b lint fixture: absolute path `/usr/bin/sh -c` shell spawn.
// MUST be flagged. The Phase 4 interpreter regex prefixes the interpreter
// name with `([^"]*[/\\])?` so absolute and Windows-style paths still
// trip the guard.
package phase4fail

import (
	"context"
	"os/exec"
)

func runAbsoluteShDashC(ctx context.Context, payload string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/sh", "-c", payload)
	return cmd.Run()
}
