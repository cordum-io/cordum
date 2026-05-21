package main

import (
	"fmt"
	"os"
)

// runShadowCmd dispatches `cordumctl shadow <subcommand>`. The
// dispatch table is intentionally small to keep the surface auditable:
//
//	scan       — observe-only local scanner (EDGE-140).
//	remediate  — offline advisory remediation generator (EDGE-142).
func runShadowCmd(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: cordumctl shadow <scan|remediate>")
		return 2
	}
	switch args[0] {
	case "scan":
		return runShadowScanCmd(args[1:], os.Stdout, os.Stderr)
	case "remediate":
		return runShadowRemediateCmd(args[1:], os.Stdin, os.Stdout, os.Stderr)
	default:
		fmt.Fprintf(os.Stderr, "unknown shadow subcommand %q\n", args[0])
		return 2
	}
}
