//go:build ignore

// EDGE-068b lint fixture: PowerShell `-Command` form. MUST be flagged.
// Exercises the longer `"-[cC][oO][mM][mM][aA][nN][dD]"` flag regex.
package phase4fail

import (
	"context"
	"os/exec"
)

func runPowershellCommand(ctx context.Context, payload string) error {
	cmd := exec.CommandContext(ctx, "powershell", "-Command", payload)
	return cmd.Run()
}
