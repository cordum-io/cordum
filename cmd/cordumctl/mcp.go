package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	sdk "github.com/cordum/cordum/sdk/client"
)

// runMCPCmd dispatches `cordumctl mcp <subcommand>`.
func runMCPCmd(args []string) {
	if len(args) < 1 {
		fail("usage: cordumctl mcp <pending|approve|reject|tools|keygen|upstream|preview|attach|rollback> [options]")
	}
	switch args[0] {
	case "pending":
		runMCPPending(args[1:])
	case "approve":
		runMCPResolve(args[1:], "approve")
	case "reject":
		runMCPResolve(args[1:], "reject")
	case "tools":
		runMCPTools(args[1:])
	case "keygen":
		runMCPKeygen(args[1:])
	case "upstream":
		if code := runMCPUpstreamCmd(args[1:], os.Stdout, os.Stderr, nil); code != 0 {
			os.Exit(code)
		}
	case "preview", "attach", "rollback":
		// EDGE-104: cordumctl mcp <preview|attach|rollback> --client X
		// dispatches the attach lifecycle. The verb stays in args so
		// runMCPAttachCmd can branch on it without re-parsing.
		if code := runMCPAttachCmd(args, os.Stdout, os.Stderr); code != 0 {
			os.Exit(code)
		}
	default:
		fail(fmt.Sprintf("unknown mcp subcommand %q", args[0]))
	}
}

// runMCPTools handles `cordumctl mcp tools <list>`.
func runMCPTools(args []string) {
	if len(args) < 1 {
		fail("usage: cordumctl mcp tools list [--agent-id X] [--json]")
	}
	switch args[0] {
	case "list":
		runMCPToolsList(args[1:])
	default:
		fail(fmt.Sprintf("unknown mcp tools subcommand %q", args[0]))
	}
}

func runMCPToolsList(args []string) {
	fs := newFlagSet("mcp tools list")
	agentID := fs.String("agent-id", "", "agent identity id to scope the listing (empty = full admin catalogue)")
	jsonOut := fs.Bool("json", false, "print raw JSON rather than the table")
	filter := fs.String("filter", "", "substring (case-insensitive) against tool name + description")
	fs.ParseArgs(args)

	client := newClientFromFlags(fs)
	list, err := client.ListMCPTools(context.Background(), *agentID)
	check(err)

	if f := strings.ToLower(strings.TrimSpace(*filter)); f != "" {
		list = filterMCPToolList(list, f)
	}

	if *jsonOut {
		printJSON(list)
		return
	}
	renderMCPToolList(os.Stdout, list)
}

// filterMCPToolList drops tools whose name+description does not
// contain the given substring (case-insensitive). Returns a new list
// so the caller still sees unmodified list metadata (AgentID, Note).
func filterMCPToolList(list *sdk.MCPToolList, needle string) *sdk.MCPToolList {
	if list == nil {
		return list
	}
	needle = strings.ToLower(strings.TrimSpace(needle))
	out := *list
	out.Tools = out.Tools[:0]
	if needle == "" {
		out.Tools = append(out.Tools, list.Tools...)
		return &out
	}
	for _, t := range list.Tools {
		combined := strings.ToLower(t.Name + " " + t.Description)
		if strings.Contains(combined, needle) {
			out.Tools = append(out.Tools, t)
		}
	}
	return &out
}

// renderMCPToolList prints a fixed-width table. ASCII only so
// MSYS/Windows consoles render it correctly.
func renderMCPToolList(out io.Writer, list *sdk.MCPToolList) {
	if list == nil || len(list.Tools) == 0 {
		if list != nil && list.Note != "" {
			_, _ = fmt.Fprintln(out, "note:", list.Note)
		}
		_, _ = fmt.Fprintln(out, "No MCP tools visible.")
		return
	}
	header := "  Full registry"
	if list.Filtered {
		header = fmt.Sprintf("  Filtered for agent %s", list.AgentID)
	}
	_, _ = fmt.Fprintln(out, header)
	if list.Note != "" {
		_, _ = fmt.Fprintln(out, "  note:", list.Note)
	}

	const (
		nameW = 32
		tierW = 10
		clsW  = 20
		apvW  = 9
	)
	hr := "  +" +
		strings.Repeat("-", nameW+2) + "+" +
		strings.Repeat("-", tierW+2) + "+" +
		strings.Repeat("-", clsW+2) + "+" +
		strings.Repeat("-", apvW+2) + "+"
	_, _ = fmt.Fprintln(out, hr)
	_, _ = fmt.Fprintf(out, "  | %-*s | %-*s | %-*s | %-*s |\n",
		nameW, "Name", tierW, "Tier", clsW, "Classifications", apvW, "Approval")
	_, _ = fmt.Fprintln(out, hr)
	for _, t := range list.Tools {
		tier := t.RiskTier
		if tier == "" {
			tier = "high"
		}
		apv := "-"
		if t.RequiresApproval {
			apv = "required"
		}
		_, _ = fmt.Fprintf(out, "  | %-*s | %-*s | %-*s | %-*s |\n",
			nameW, truncateASCII(t.Name, nameW),
			tierW, truncateASCII(tier, tierW),
			clsW, truncateASCII(strings.Join(t.DataClassifications, ","), clsW),
			apvW, truncateASCII(apv, apvW))
	}
	_, _ = fmt.Fprintln(out, hr)
	_, _ = fmt.Fprintf(out, "  %d tool(s)\n", len(list.Tools))
}

func runMCPPending(args []string) {
	fs := newFlagSet("mcp pending")
	status := fs.String("status", "pending", "approval status filter (pending|approved|rejected|expired)")
	jsonOut := fs.Bool("json", false, "print raw JSON rather than the table")
	// task-2d989055: bulk-review filters so operators can scope the
	// terminal view to mutating calls (the high-privilege cohort) or
	// to a specific tool / time window during incident response.
	mutating := fs.Bool("mutating", false, "show only mutating tool approvals (cordum_create_workflow, cordum_install_pack, etc.)")
	toolName := fs.String("tool-name", "", "exact tool name filter (e.g. 'cordum_install_pack')")
	since := fs.String("since", "", "RFC3339 timestamp; hide approvals created before this time")
	fs.ParseArgs(args)

	client := newClientFromFlags(fs)
	items, err := client.ListMCPApprovals(context.Background(), *status)
	check(err)

	items = filterMCPPending(items, filterMCPPendingOpts{
		mutatingOnly: *mutating,
		toolName:     strings.TrimSpace(*toolName),
		since:        strings.TrimSpace(*since),
	})

	if *jsonOut {
		printJSON(items)
		return
	}
	renderMCPApprovalList(os.Stdout, items)
}

// filterMCPPendingOpts groups the optional predicates applied to the
// /mcp/approvals response so the filter function stays pure (no flag
// globals) and is easy to unit-test.
type filterMCPPendingOpts struct {
	mutatingOnly bool
	toolName     string
	since        string
}

// filterMCPPending returns a new slice containing only approvals
// matching all active predicates. Missing predicates pass.
func filterMCPPending(items []sdk.MCPApproval, opts filterMCPPendingOpts) []sdk.MCPApproval {
	if !opts.mutatingOnly && opts.toolName == "" && opts.since == "" {
		return items
	}
	// Parse --since once, up front. An invalid timestamp is a usage
	// error, not a silent drop-all.
	var sinceMs int64
	if opts.since != "" {
		t, err := parseSinceTimestamp(opts.since)
		if err != nil {
			fail(fmt.Sprintf("invalid --since timestamp: %v", err))
		}
		sinceMs = t
	}
	out := make([]sdk.MCPApproval, 0, len(items))
	for _, it := range items {
		if opts.mutatingOnly && !isMutatingMCPTool(it.ToolName) {
			continue
		}
		if opts.toolName != "" && it.ToolName != opts.toolName {
			continue
		}
		// CreatedAt on the gateway record is unix microseconds (see
		// mcp_approvals.go:289 UnixMicro). Convert the ms-based
		// threshold to match.
		if sinceMs > 0 && it.CreatedAt > 0 && (it.CreatedAt/1000) < sinceMs {
			continue
		}
		out = append(out, it)
	}
	return out
}

// isMutatingMCPTool returns whether the given tool name belongs to
// the mutating-tool family shipped by task-2d989055. Kept as a small
// explicit list so adding a new mutating tool requires an explicit
// update here (and in the list) rather than depending on a naming
// convention that could drift.
func isMutatingMCPTool(name string) bool {
	switch name {
	case "cordum_create_workflow",
		"cordum_install_pack",
		"cordum_uninstall_pack",
		"cordum_register_agent",
		"cordum_update_policy_bundle",
		"cordum_revoke_worker_session",
		"cordum_set_agent_scope":
		return true
	}
	return false
}

// parseSinceTimestamp accepts RFC3339 or a bare unix-ms integer so
// script-driven callers don't have to format timestamps.
func parseSinceTimestamp(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	// Try unix-ms first (common in audit events).
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
		return n, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, fmt.Errorf("expected RFC3339 or unix-ms, got %q: %w", raw, err)
	}
	return t.UnixMilli(), nil
}

func runMCPResolve(args []string, verb string) {
	fs := newFlagSet("mcp " + verb)
	reason := fs.String("reason", "", "human-readable justification")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		fail(fmt.Sprintf("usage: cordumctl mcp %s <approval_id> [--reason text]", verb))
	}
	id := strings.TrimSpace(fs.Arg(0))

	client := newClientFromFlags(fs)
	var (
		rec *sdk.MCPApproval
		err error
	)
	if verb == "approve" {
		rec, err = client.ApproveMCP(context.Background(), id, *reason)
	} else {
		rec, err = client.RejectMCP(context.Background(), id, *reason)
	}
	if err != nil {
		// Friendly handling of the self-approval guard so CI scripts can
		// detect it via a clean exit-code + stderr message without
		// parsing the JSON body.
		if isMCPSelfApproval(err) {
			fmt.Fprintln(os.Stderr, "error: self-approval not permitted (you cannot resolve your own MCP call)")
			os.Exit(3)
		}
		if isMCPNotFound(err) {
			fmt.Fprintln(os.Stderr, "error: approval not found")
			os.Exit(4)
		}
		fail(err.Error())
	}
	printJSON(rec)
}

// renderMCPApprovalList prints a deterministic fixed-width table. ASCII
// only so MSYS/Windows consoles render correctly.
func renderMCPApprovalList(out io.Writer, items []sdk.MCPApproval) {
	if len(items) == 0 {
		_, _ = fmt.Fprintln(out, "No MCP approvals match the current filter.")
		return
	}
	// Approval IDs are 32 hex chars. Widen the column so operators can
	// copy them straight from the table without truncation — the `...`
	// variant was a recurring UX tripwire flagged by ops.
	const (
		idW     = 34
		toolW   = 24
		agentW  = 18
		statusW = 10
		hashW   = 10
	)
	hr := "  +" +
		strings.Repeat("-", idW+2) + "+" +
		strings.Repeat("-", toolW+2) + "+" +
		strings.Repeat("-", agentW+2) + "+" +
		strings.Repeat("-", statusW+2) + "+" +
		strings.Repeat("-", hashW+2) + "+"
	_, _ = fmt.Fprintln(out, hr)
	_, _ = fmt.Fprintf(out, "  | %-*s | %-*s | %-*s | %-*s | %-*s |\n",
		idW, "Approval ID", toolW, "Tool", agentW, "Agent", statusW, "Status", hashW, "Args hash")
	_, _ = fmt.Fprintln(out, hr)
	for _, it := range items {
		_, _ = fmt.Fprintf(out, "  | %-*s | %-*s | %-*s | %-*s | %-*s |\n",
			idW, truncateASCII(it.ID, idW),
			toolW, truncateASCII(it.ToolName, toolW),
			agentW, truncateASCII(it.AgentID, agentW),
			statusW, truncateASCII(it.Status, statusW),
			hashW, truncateASCII(shortHex(it.ArgsHash, 8), hashW))
	}
	_, _ = fmt.Fprintln(out, hr)
}

func truncateASCII(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func shortHex(s string, prefix int) string {
	if len(s) <= prefix {
		return s
	}
	return s[:prefix]
}

// isMCPSelfApproval and isMCPNotFound sniff the server's error body for
// the well-known codes surfaced by the gateway handlers. The SDK
// returns errors as a wrapped fmt.Errorf, so string containment is
// sufficient — we already control both sides of the wire.
func isMCPSelfApproval(err error) bool {
	return err != nil && strings.Contains(err.Error(), "self_approval_denied")
}
func isMCPNotFound(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "approval_not_found") ||
		strings.Contains(err.Error(), "no such mcp approval"))
}

// guard silences a possible future lint about the errors import — the
// file currently uses stdlib errors indirectly via fmt.Errorf in the
// SDK and the .Is helpers above, so importing `errors` directly is
// optional. Kept as a no-op sentinel.
var _ = errors.New
