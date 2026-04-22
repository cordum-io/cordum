package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	sdk "github.com/cordum/cordum/sdk/client"
)

// runAuditCmd dispatches `cordumctl audit <subcommand>`.
func runAuditCmd(args []string) {
	if len(args) < 1 {
		fail("usage: cordumctl audit <verify> [options]")
	}
	switch args[0] {
	case "verify":
		runAuditVerify(args[1:])
	default:
		fail(fmt.Sprintf("unknown audit subcommand %q", args[0]))
	}
}

// runAuditVerify implements `cordumctl audit verify <tenant>`.
//
// Exit codes (so CI can decide based on integrity without grepping):
//
//	0  status=ok (chain intact, no gaps or only retention-trimmed prefix)
//	2  status=compromised (tampering detected)
//	3  status=partial (boundary issue — verify reported inconclusively)
//	1  fatal error (network, permissions, usage)
func runAuditVerify(args []string) {
	fs := newFlagSet("audit verify")
	since := fs.Int64("since", 0, "unix milliseconds lower bound (inclusive)")
	until := fs.Int64("until", 0, "unix milliseconds upper bound (inclusive)")
	limit := fs.Int64("limit", 0, "max events to read (default 10000, max 100000)")
	jsonOut := fs.Bool("json", false, "emit the raw JSON response (for CI)")
	fs.ParseArgs(args)

	tenant := strings.TrimSpace(fs.Arg(0))
	// Tenant is optional on the CLI — an empty string defaults the
	// request to the client's configured tenant. This matches the REST
	// surface: the gateway resolves tenant from header + query.

	client := newClientFromFlags(fs)
	result, err := client.VerifyAuditChain(context.Background(), tenant, sdk.AuditVerifyOptions{
		SinceMs: *since,
		UntilMs: *until,
		Limit:   *limit,
	})
	check(err)

	if *jsonOut {
		printJSON(result)
	} else {
		renderAuditVerifyTable(os.Stdout, result, tenant)
	}

	switch result.Status {
	case "ok":
		return
	case "partial":
		os.Exit(3)
	case "compromised":
		os.Exit(2)
	default:
		// Unknown status — treat as fatal so an out-of-date CLI does
		// not silently pass CI when the gateway reports something we
		// don't understand.
		os.Exit(1)
	}
}

// renderAuditVerifyTable prints a compact human-readable summary of a
// verify result. ASCII-only so Windows / MSYS consoles render.
func renderAuditVerifyTable(out io.Writer, r *sdk.AuditVerifyResult, tenant string) {
	if tenant == "" {
		tenant = "(default)"
	}
	_, _ = fmt.Fprintf(out, "Audit chain verification — tenant %s\n", tenant)
	_, _ = fmt.Fprintf(out, "  status:                 %s\n", r.Status)
	_, _ = fmt.Fprintf(out, "  events checked:         %d\n", r.TotalEvents)
	_, _ = fmt.Fprintf(out, "  events verified:        %d\n", r.VerifiedEvents)
	if r.FirstSeq > 0 || r.LastSeq > 0 {
		_, _ = fmt.Fprintf(out, "  seq range observed:     %d..%d\n", r.FirstSeq, r.LastSeq)
	}
	_, _ = fmt.Fprintf(out, "  retention boundary:     seq %d\n", r.RetentionBoundarySeq)
	if r.RetentionWindowHours > 0 {
		_, _ = fmt.Fprintf(out, "  retention window:       %.1f hours\n", r.RetentionWindowHours)
	}

	if len(r.Gaps) == 0 {
		_, _ = fmt.Fprintln(out, "  gaps:                   none")
		return
	}

	// Split by classification so "retention vs tampering" is obvious
	// without having to count rows by hand.
	var trimmed, missing, mismatched, outOfOrder []sdk.AuditVerifyGap
	for _, g := range r.Gaps {
		switch g.Type {
		case "retention_trimmed":
			trimmed = append(trimmed, g)
		case "missing":
			missing = append(missing, g)
		case "hash_mismatch":
			mismatched = append(mismatched, g)
		case "out_of_order":
			outOfOrder = append(outOfOrder, g)
		default:
			missing = append(missing, g)
		}
	}
	_, _ = fmt.Fprintf(out, "  gaps:                   %d total\n", len(r.Gaps))
	if len(trimmed) > 0 {
		_, _ = fmt.Fprintf(out, "    retention_trimmed:    %d  %s\n", len(trimmed), formatGapSeqs(trimmed))
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(out, "    missing (tampering):  %d  %s\n", len(missing), formatGapSeqs(missing))
	}
	if len(mismatched) > 0 {
		_, _ = fmt.Fprintf(out, "    hash_mismatch:        %d  %s\n", len(mismatched), formatGapSeqs(mismatched))
	}
	if len(outOfOrder) > 0 {
		_, _ = fmt.Fprintf(out, "    out_of_order:         %d  %s\n", len(outOfOrder), formatGapSeqs(outOfOrder))
	}
}

// formatGapSeqs renders up to 10 seqs as a compact list, then "...N more"
// if the slice is longer. Keeps the CLI table from exploding on a
// long retention-trimmed prefix.
func formatGapSeqs(gaps []sdk.AuditVerifyGap) string {
	const show = 10
	var parts []string
	for i, g := range gaps {
		if i >= show {
			break
		}
		parts = append(parts, fmt.Sprintf("%d", g.AtSeq))
	}
	s := "[" + strings.Join(parts, ", ") + "]"
	if len(gaps) > show {
		s += fmt.Sprintf(" (+%d more)", len(gaps)-show)
	}
	return s
}
