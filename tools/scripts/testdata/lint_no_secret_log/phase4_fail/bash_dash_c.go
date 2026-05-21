//go:build ignore

// EDGE-068b lint fixture: `bash -c` shell spawn. MUST be flagged.
package phase4fail

import (
	"context"
	"os/exec"
)

func runBashDashC(ctx context.Context, payload string) error {
	cmd := exec.CommandContext(ctx, "bash", "-c", payload)
	return cmd.Run()
}
