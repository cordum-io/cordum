//go:build ignore

// EDGE-068b lint fixture: mirror of cmd/cordumctl/doctor.go:878-883.
// Both shell variants carry `// no-shell-exec-lint: <reason>` markers so
// the Phase 4 guard MUST allow them through. If the marker bypass at
// lint_no_secret_log.sh:158 regresses, this fixture flips to FAIL and
// catches it.
package phase4exception

import (
	"context"
	"os/exec"
	"runtime"
)

func runOperatorRepair(ctx context.Context, command string) ([]byte, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command) // no-shell-exec-lint: operator-confirmed doctor repair only
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command) // no-shell-exec-lint: operator-confirmed doctor repair only
	}
	return cmd.CombinedOutput()
}
