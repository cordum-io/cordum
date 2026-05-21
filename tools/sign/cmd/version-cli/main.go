// version-cli — releases-pipeline helper that compares two release tags
// using the same semver comparator as the install path
// (tools/sign.SemverCompare). It is used by .github/workflows/release.yml's
// `version-monotonicity` job to refuse a tag push whose semver is not
// strictly greater than the most recently published tag, preventing an
// accidental sibling-release downgrade at the moment a tag is created.
//
// Usage:
//
//	version-cli compare <a> <b>             # prints -1 / 0 / 1
//	version-cli monotonic-or-fail <new> <prior>
//	    exit 0 when new > prior, exit 1 otherwise.
//
// EDGE-151-DOWNGRADE.
package main

import (
	"fmt"
	"os"

	"github.com/cordum/cordum/tools/sign"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout *os.File) error {
	if len(args) < 1 {
		return fmt.Errorf("version-cli: missing subcommand (compare|monotonic-or-fail)")
	}
	switch args[0] {
	case "compare":
		if len(args) != 3 {
			return fmt.Errorf("version-cli: compare requires two version arguments")
		}
		_, _ = fmt.Fprintln(stdout, sign.SemverCompare(args[1], args[2]))
		return nil
	case "monotonic-or-fail":
		if len(args) != 3 {
			return fmt.Errorf("version-cli: monotonic-or-fail requires <new> <prior>")
		}
		newer, prior := args[1], args[2]
		if _, _, _, _, ok := sign.ParseSemver(newer); !ok {
			return fmt.Errorf("version-cli: invalid new tag %q", newer)
		}
		if prior == "" || prior == "v0.0.0" {
			// First-tag corner case: anything monotonic is fine.
			_, _ = fmt.Fprintf(stdout, "ok: %s is the first release\n", newer)
			return nil
		}
		if _, _, _, _, ok := sign.ParseSemver(prior); !ok {
			return fmt.Errorf("version-cli: invalid prior tag %q", prior)
		}
		if sign.SemverCompare(newer, prior) <= 0 {
			return fmt.Errorf("release tag %s is not strictly greater than prior %s", newer, prior)
		}
		_, _ = fmt.Fprintf(stdout, "ok: %s > %s\n", newer, prior)
		return nil
	default:
		return fmt.Errorf("version-cli: unknown subcommand %q", args[0])
	}
}
