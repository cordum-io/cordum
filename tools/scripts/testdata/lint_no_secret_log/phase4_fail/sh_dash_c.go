//go:build ignore

// EDGE-068b lint fixture: bare `sh -c` shell spawn. MUST be flagged.
package phase4fail

import (
	"context"
	"os/exec"
)

func runShDashC(ctx context.Context, payload string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", payload)
	return cmd.Run()
}
