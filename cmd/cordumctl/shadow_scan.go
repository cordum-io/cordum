package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cordum/cordum/core/edge/shadow"
)

// shadowScanFactory is the construction seam runShadowScanCmd uses to
// build the scanner. Production wires it to shadow.NewScanner; tests
// inject a wrapper that adds WithHomeDir / WithProcessLister overrides
// pointing at fixtures.
type shadowScanFactory func(opts ...shadow.Option) *shadow.Scanner

// runShadowScanCmd is the public entry point for `cordumctl shadow scan`.
// It defers to runShadowScanCmdWith so tests can inject a factory.
func runShadowScanCmd(args []string, stdout, stderr io.Writer) int {
	return runShadowScanCmdWith(args, stdout, stderr, shadow.NewScanner)
}

// runShadowScanCmdWith is the test-seam'd implementation of
// `cordumctl shadow scan`. Exit codes:
//
//	0  scan disabled (default), or scan succeeded (any number of findings)
//	2  flag parse error, scanner runtime error, or output-file write error
func runShadowScanCmdWith(args []string, stdout, stderr io.Writer, factory shadowScanFactory) int {
	fs := flag.NewFlagSet("shadow scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	enable := fs.Bool("enable-shadow-scan", false, "opt in to active scanning (default: disabled)")
	output := fs.String("output", "", "write JSONL findings to this file (default: stdout)")
	tenant := fs.String("tenant", "", "override tenant attribution (default: empty)")
	principal := fs.String("principal", "", "override principal attribution (default: empty)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Fast path: explicit opt-out before constructing anything. Matches the
	// scanner's own opt-in gate so users get a consistent message whether
	// they invoke via CLI or library.
	envEnabled := os.Getenv("CORDUM_EDGE_SHADOW_SCAN_ENABLED")
	if !*enable && envEnabled != "true" && envEnabled != "1" && envEnabled != "yes" {
		_, _ = fmt.Fprintln(stdout, "shadow scan disabled by default; use --enable-shadow-scan to opt in")
		return 0
	}

	opts := []shadow.Option{
		shadow.WithTenant(*tenant),
		shadow.WithPrincipal(*principal),
	}
	if *enable {
		opts = append(opts, shadow.WithOptIn())
	}
	if hn, err := os.Hostname(); err == nil && hn != "" {
		opts = append(opts, shadow.WithHostname(hn))
	}

	scanner := factory(opts...)
	findings, err := scanner.Scan(context.Background())
	if err != nil {
		// ErrOptInRequired here is unexpected (the env / flag gate above
		// should have short-circuited) but kept for defence-in-depth.
		if errors.Is(err, shadow.ErrOptInRequired) {
			_, _ = fmt.Fprintln(stdout, "shadow scan disabled by default; use --enable-shadow-scan to opt in")
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "shadow scan: %s\n", err)
		return 2
	}

	sink := stdout
	if *output != "" {
		// O_EXCL not used here — multiple scans on the same host should be
		// permitted to overwrite the prior JSONL output (the file is a
		// transient artefact, not a long-lived audit log). Mode 0600 still
		// applies to keep findings off other users' eyes.
		f, openErr := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if openErr != nil {
			_, _ = fmt.Fprintf(stderr, "shadow scan: open output: %s\n", openErr)
			return 2
		}
		defer func() { _ = f.Close() }()
		sink = f
	}

	enc := json.NewEncoder(sink)
	for _, finding := range findings {
		if encErr := enc.Encode(finding); encErr != nil {
			_, _ = fmt.Fprintf(stderr, "shadow scan: encode finding: %s\n", encErr)
			return 2
		}
	}
	return 0
}
