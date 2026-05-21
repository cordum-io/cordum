//go:build ignore

// EDGE-068b lint fixture: Windows `cmd.exe /c` shell spawn. MUST be flagged.
// Uses lowercase `/c` to exercise the case-insensitive flag regex
// `"\/[cC]"` in lint_no_secret_log.sh.
package phase4fail

import (
	"context"
	"os/exec"
)

func runCmdExeSlashLowerC(ctx context.Context, payload string) error {
	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", payload)
	return cmd.Run()
}
