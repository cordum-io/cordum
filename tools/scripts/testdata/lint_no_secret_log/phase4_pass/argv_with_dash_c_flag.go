//go:build ignore

// EDGE-068b lint fixture: exec.Command using `-c` as a non-shell flag.
// The Phase 4 guard demands a recognised shell interpreter (sh/bash/cmd/
// powershell/pwsh) BEFORE the `-c|/c|-Command` flag check. Here the first
// argv is "go" — the go-test compile flag `-c` must NOT trip the guard.
package phase4pass

import (
	"context"
	"os/exec"
)

func runGoTestCompile(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "./...")
	return cmd.Run()
}
