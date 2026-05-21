// manifest-cli — releases-pipeline helper that hashes a set of files and
// emits a cosign-compatible SHA256SUMS-style manifest to stdout. Used by
// .github/workflows/release.yml after the cross-compilation matrix and
// before `gpg --detach-sign`. Reads file paths from argv when provided,
// else from stdin (one per line) so it composes cleanly with `find`.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/cordum/cordum/tools/sign"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out io.Writer) error {
	paths := append([]string(nil), args...)
	if len(paths) == 0 {
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			if line := sc.Text(); line != "" {
				paths = append(paths, line)
			}
		}
		if err := sc.Err(); err != nil {
			return fmt.Errorf("manifest-cli: read stdin: %w", err)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("manifest-cli: no input paths (provide args or pipe stdin)")
	}
	body, err := sign.BuildManifest(paths)
	if err != nil {
		return err
	}
	if _, err := out.Write(body); err != nil {
		return fmt.Errorf("manifest-cli: write stdout: %w", err)
	}
	return nil
}
