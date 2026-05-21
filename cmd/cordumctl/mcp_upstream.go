package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type mcpUpstreamAPI interface {
	ValidateMCPUpstream(context.Context, mcpUpstreamRequest) (mcpUpstreamValidationResult, error)
	CreateMCPUpstream(context.Context, mcpUpstreamRequest) (mcpUpstreamRecord, error)
	ListMCPUpstreams(context.Context) ([]mcpUpstreamRecord, error)
	GetMCPUpstream(context.Context, string) (mcpUpstreamRecord, error)
	DisableMCPUpstream(context.Context, string) (mcpUpstreamRecord, error)
	EnableMCPUpstream(context.Context, string) (mcpUpstreamRecord, error)
}

type mcpUpstreamRequest struct {
	Name          string            `json:"name"`
	Transport     string            `json:"transport"`
	Endpoint      string            `json:"endpoint,omitempty"`
	Command       []string          `json:"command,omitempty"`
	TenantID      string            `json:"tenant_id,omitempty"`
	AuthSecretRef string            `json:"auth_secret_ref,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Risk          string            `json:"risk,omitempty"`
	Enabled       bool              `json:"enabled"`
}

type mcpUpstreamRecord struct {
	Name          string            `json:"name"`
	Transport     string            `json:"transport"`
	Endpoint      string            `json:"endpoint,omitempty"`
	Command       []string          `json:"command,omitempty"`
	TenantID      string            `json:"tenant_id"`
	AuthSecretRef string            `json:"auth_secret_ref,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Risk          string            `json:"risk"`
	Enabled       bool              `json:"enabled"`
	CreatedAt     string            `json:"created_at,omitempty"`
	UpdatedAt     string            `json:"updated_at,omitempty"`
}

type mcpUpstreamValidationResult struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

type labelFlags []string

func (l *labelFlags) String() string { return strings.Join(*l, ",") }
func (l *labelFlags) Set(value string) error {
	*l = append(*l, value)
	return nil
}

func runMCPUpstreamCmd(args []string, stdout, stderr io.Writer, client mcpUpstreamAPI) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: cordumctl mcp upstream <add|validate|list|get|disable|enable> [options]")
		return 2
	}
	switch args[0] {
	case "add", "create":
		return runMCPUpstreamAdd(args[1:], stdout, stderr, client)
	case "validate":
		return runMCPUpstreamAdd(append(args[1:], "--validate-only"), stdout, stderr, client)
	case "list":
		return runMCPUpstreamList(args[1:], stdout, stderr, client)
	case "get":
		return runMCPUpstreamGet(args[1:], stdout, stderr, client)
	case "disable":
		return runMCPUpstreamSetEnabled(args[1:], stdout, stderr, client, false)
	case "enable":
		return runMCPUpstreamSetEnabled(args[1:], stdout, stderr, client, true)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown mcp upstream subcommand %q\n", args[0])
		return 2
	}
}

func runMCPUpstreamAdd(args []string, stdout, stderr io.Writer, api mcpUpstreamAPI) int {
	fs := newFlagSet("mcp upstream add")
	name := fs.String("name", "", "upstream server name")
	transport := fs.String("transport", "http", "transport: http, sse, or stdio")
	endpoint := fs.String("endpoint", "", "remote MCP endpoint URL for http/sse transports")
	authSecretRef := fs.String("auth-secret-ref", "", "secret:// reference for upstream authentication")
	risk := fs.String("risk", "medium", "risk metadata: low, medium, high, or critical")
	enabled := fs.Bool("enabled", true, "enable the upstream after creation")
	validateOnly := fs.Bool("validate-only", false, "validate without creating the upstream")
	var labels labelFlags
	var command labelFlags
	fs.Var(&labels, "label", "label key=value; repeat for multiple labels")
	fs.Var(&command, "command", "stdio command argv element; repeat for each argv element")
	fs.ParseArgs(args)
	if strings.TrimSpace(*name) == "" {
		_, _ = fmt.Fprintln(stderr, "missing required --name")
		return 2
	}
	api = mcpUpstreamClientOrDefault(api, fs)
	req, err := buildMCPUpstreamRequest(fs, *name, *transport, *endpoint, *authSecretRef, *risk, *enabled, command, labels)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err.Error())
		return 2
	}
	if *validateOnly {
		return printMCPUpstreamValidation(stdout, stderr, api, req)
	}
	record, err := api.CreateMCPUpstream(context.Background(), req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "create failed: %v\n", err)
		return 1
	}
	return writeMCPUpstreamJSON(stdout, stderr, record)
}

func runMCPUpstreamList(args []string, stdout, stderr io.Writer, api mcpUpstreamAPI) int {
	fs := newFlagSet("mcp upstream list")
	jsonOut := fs.Bool("json", false, "print raw JSON rather than the table")
	fs.ParseArgs(args)
	api = mcpUpstreamClientOrDefault(api, fs)
	records, err := api.ListMCPUpstreams(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "list failed: %v\n", err)
		return 1
	}
	if *jsonOut {
		return writeMCPUpstreamJSON(stdout, stderr, records)
	}
	renderMCPUpstreamList(stdout, records)
	return 0
}

func runMCPUpstreamGet(args []string, stdout, stderr io.Writer, api mcpUpstreamAPI) int {
	fs := newFlagSet("mcp upstream get")
	fs.ParseArgs(args)
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: cordumctl mcp upstream get <name>")
		return 2
	}
	api = mcpUpstreamClientOrDefault(api, fs)
	record, err := api.GetMCPUpstream(context.Background(), fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "get failed: %v\n", err)
		return 1
	}
	return writeMCPUpstreamJSON(stdout, stderr, record)
}

func runMCPUpstreamSetEnabled(args []string, stdout, stderr io.Writer, api mcpUpstreamAPI, enabled bool) int {
	verb := "disable"
	if enabled {
		verb = "enable"
	}
	fs := newFlagSet("mcp upstream " + verb)
	fs.ParseArgs(args)
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintf(stderr, "usage: cordumctl mcp upstream %s <name>\n", verb)
		return 2
	}
	api = mcpUpstreamClientOrDefault(api, fs)
	call := api.DisableMCPUpstream
	if enabled {
		call = api.EnableMCPUpstream
	}
	record, err := call(context.Background(), fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s failed: %v\n", verb, err)
		return 1
	}
	return writeMCPUpstreamJSON(stdout, stderr, record)
}

func buildMCPUpstreamRequest(fs *flagSet, name, transport, endpoint, authSecretRef, risk string, enabled bool, command, labels labelFlags) (mcpUpstreamRequest, error) {
	parsedLabels, err := parseMCPUpstreamLabels(labels)
	if err != nil {
		return mcpUpstreamRequest{}, err
	}
	return mcpUpstreamRequest{
		Name:          strings.TrimSpace(name),
		Transport:     strings.TrimSpace(transport),
		Endpoint:      strings.TrimSpace(endpoint),
		Command:       trimMCPUpstreamStrings(command),
		TenantID:      strings.TrimSpace(*fs.tenant),
		AuthSecretRef: strings.TrimSpace(authSecretRef),
		Labels:        parsedLabels,
		Risk:          strings.TrimSpace(risk),
		Enabled:       enabled,
	}, nil
}

func parseMCPUpstreamLabels(raw []string) (map[string]string, error) {
	labels := make(map[string]string, len(raw))
	for _, item := range raw {
		key, value, ok := strings.Cut(item, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid --label %q: expected key=value", item)
		}
		labels[key] = strings.TrimSpace(value)
	}
	if len(labels) == 0 {
		return nil, nil
	}
	return labels, nil
}

func trimMCPUpstreamStrings(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func printMCPUpstreamValidation(stdout, stderr io.Writer, api mcpUpstreamAPI, req mcpUpstreamRequest) int {
	result, err := api.ValidateMCPUpstream(context.Background(), req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate failed: %v\n", err)
		return 1
	}
	if !result.Valid {
		_, _ = fmt.Fprintf(stderr, "invalid: %s\n", result.Reason)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "valid")
	return 0
}

func renderMCPUpstreamList(out io.Writer, records []mcpUpstreamRecord) {
	if len(records) == 0 {
		_, _ = fmt.Fprintln(out, "No MCP upstreams registered.")
		return
	}
	_, _ = fmt.Fprintln(out, "NAME\tTENANT\tTRANSPORT\tRISK\tENABLED")
	for _, record := range records {
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%t\n", record.Name, record.TenantID, record.Transport, record.Risk, record.Enabled)
	}
}

func writeMCPUpstreamJSON[T mcpUpstreamRecord | []mcpUpstreamRecord](stdout, stderr io.Writer, value T) int {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "encode output: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, string(data))
	return 0
}
