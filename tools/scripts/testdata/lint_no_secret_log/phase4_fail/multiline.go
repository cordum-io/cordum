//go:build ignore

// EDGE-068b lint fixture: shell spawn split across multiple source lines.
// MUST be flagged. This case exercises the awk multi-line paren tracker
// in lint_no_secret_log.sh (lines 130-200): the function name appears on
// line 14, the interpreter on line 15, the flag on line 16, and the
// closing paren on line 17.
package phase4fail

import (
	"context"
	"os/exec"
)

func runMultiline(ctx context.Context, payload string) error {
	cmd := exec.CommandContext(
		ctx,
		"sh",
		"-c",
		payload,
	)
	return cmd.Run()
}
