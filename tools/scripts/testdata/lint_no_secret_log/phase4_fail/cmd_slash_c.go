//go:build ignore

// EDGE-068b lint fixture: Windows `cmd /C` shell spawn. MUST be flagged.
package phase4fail

import (
	"context"
	"os/exec"
)

func runCmdSlashC(ctx context.Context, payload string) error {
	cmd := exec.CommandContext(ctx, "cmd", "/C", payload)
	return cmd.Run()
}
