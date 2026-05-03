package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cordum/cordum/core/infra/logging"
	sdk "github.com/cordum/cordum/sdk/client"
)

const defaultGateway = "http://localhost:8081"

func main() {
	logging.Init("cordumctl")
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "init":
		runInitCmd(args)
	case "generate-certs":
		runGenerateCertsCmd(args)
	case "dev":
		runDevCmd(args)
	case "up":
		runUpCmd(args)
	case "status":
		runStatusCmd(args)
	case "license":
		runLicenseCmd(args)
	case "workflow":
		runWorkflowCmd(args)
	case "run":
		runRunCmd(args)
	case "approval":
		runApprovalCmd(args)
	case "dlq":
		runDLQCmd(args)
	case "pack":
		runPackCmd(args)
	case "pool":
		runPoolCmd(args)
	case "topic":
		runTopicCmd(args)
	case "worker":
		runWorkerCmd(args)
	case "job":
		runJobCmd(args)
	case "demo":
		runDemoCmd(args)
	case "mcp":
		runMCPCmd(args)
	case "policy":
		runPolicyCmd(args)
	case "audit":
		runAuditCmd(args)
	case "evals":
		runEvalsCmd(args)
	case "auth":
		runAuthCmd(args)
	case "doctor":
		runDoctorCmd(args)
	case "delegation":
		runDelegationCmd(args)
	case "governance":
		runGovernanceCmd(args)
	case "edge":
		os.Exit(runEdgeCmd(args))
	default:
		usage()
		os.Exit(1)
	}
}

func runWorkflowCmd(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	switch args[0] {
	case "create":
		fs := newFlagSet("workflow create")
		file := fs.String("file", "", "workflow json file")
		fs.ParseArgs(args[1:])
		client := newClientFromFlags(fs)
		if *file == "" {
			fail("workflow file required")
		}
		var req sdk.CreateWorkflowRequest
		loadJSON(*file, &req)
		id, err := client.CreateWorkflow(context.Background(), &req)
		check(err)
		fmt.Println(id)
	case "delete":
		fs := newFlagSet("workflow delete")
		fs.ParseArgs(args[1:])
		if fs.NArg() < 1 {
			fail("workflow id required")
		}
		client := newClientFromFlags(fs)
		check(client.DeleteWorkflow(context.Background(), fs.Arg(0)))
	default:
		usage()
		os.Exit(1)
	}
}

func runRunCmd(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	switch args[0] {
	case "start":
		fs := newFlagSet("run start")
		input := fs.String("input", "", "input json (inline, file path, or - for stdin)")
		dryRun := fs.Bool("dry-run", false, "start in dry-run mode")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		debug := fs.Bool("debug", false, "print debug info to stderr")
		fs.ParseArgs(args[1:])
		if *debug {
			fmt.Fprintf(os.Stderr, "[debug] os.Args: %q\n", os.Args)
			fmt.Fprintf(os.Stderr, "[debug] sub-args (after 'run start'): %q\n", args[1:])
			fmt.Fprintf(os.Stderr, "[debug] reordered+parsed positional: %q\n", fs.Args())
			fmt.Fprintf(os.Stderr, "[debug] input flag value: %q\n", *input)
		}
		if fs.NArg() < 1 {
			fail("workflow id required")
		}
		payload := map[string]any{}
		if *input != "" {
			parsed, err := parseJSONArg(*input)
			check(err)
			if m, ok := parsed.(map[string]any); ok {
				payload = m
			} else if parsed != nil {
				fail("input must be a JSON object")
			}
		}
		if *debug {
			debugJSON, _ := json.Marshal(payload)
			fmt.Fprintf(os.Stderr, "[debug] payload to send: %s\n", debugJSON)
		}
		client := newClientFromFlags(fs)
		runID, err := client.StartRunWithOptions(context.Background(), fs.Arg(0), payload, sdk.RunOptions{
			DryRun:         *dryRun,
			IdempotencyKey: *idempotencyKey,
		})
		check(err)
		fmt.Println(runID)
	case "get":
		fs := newFlagSet("run get")
		fs.ParseArgs(args[1:])
		if fs.NArg() < 1 {
			fail("run id required")
		}
		client := newClientFromFlags(fs)
		run, err := client.GetRun(context.Background(), fs.Arg(0))
		check(err)
		printJSON(run)
	case "delete":
		fs := newFlagSet("run delete")
		fs.ParseArgs(args[1:])
		if fs.NArg() < 1 {
			fail("run id required")
		}
		client := newClientFromFlags(fs)
		check(client.DeleteRun(context.Background(), fs.Arg(0)))
	case "timeline":
		fs := newFlagSet("run timeline")
		fs.ParseArgs(args[1:])
		if fs.NArg() < 1 {
			fail("run id required")
		}
		client := newClientFromFlags(fs)
		events, err := client.GetRunTimeline(context.Background(), fs.Arg(0))
		check(err)
		printJSON(events)
	default:
		usage()
		os.Exit(1)
	}
}

func runApprovalCmd(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	switch args[0] {
	case "job":
		fs := newFlagSet("approval job")
		approve := fs.Bool("approve", false, "approve the job")
		reject := fs.Bool("reject", false, "reject the job")
		fs.ParseArgs(args[1:])
		if fs.NArg() < 1 {
			fail("usage: approval job <job_id>")
		}
		if *approve == *reject {
			fail("use --approve or --reject")
		}
		client := newClientFromFlags(fs)
		check(client.ApproveJob(context.Background(), fs.Arg(0), *approve))
	case "repair":
		fs := newFlagSet("approval repair")
		apply := fs.Bool("apply", false, "apply the repair plan (default: dry-run)")
		note := fs.String("note", "", "operator note stored with the repair")
		fs.ParseArgs(args[1:])
		if fs.NArg() < 1 {
			fail("usage: approval repair <job_id>")
		}
		client := newClientFromFlags(fs)
		resp, err := client.RepairApproval(context.Background(), fs.Arg(0), *apply, *note)
		check(err)
		printJSON(resp)
	default:
		usage()
		os.Exit(1)
	}
}

func runDLQCmd(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	switch args[0] {
	case "retry":
		fs := newFlagSet("dlq retry")
		fs.ParseArgs(args[1:])
		if fs.NArg() < 1 {
			fail("usage: dlq retry <job_id>")
		}
		client := newClientFromFlags(fs)
		check(client.RetryDLQ(context.Background(), fs.Arg(0)))
	default:
		usage()
		os.Exit(1)
	}
}

type flagSet struct {
	*flag.FlagSet
	gateway  *string
	apiKey   *string
	tenant   *string
	insecure *bool
	cacert   *string
}

func newFlagSet(name string) *flagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	gateway := fs.String("gateway", envOr("CORDUM_GATEWAY", defaultGateway), "gateway base url")
	apiKey := fs.String("api-key", envOr("CORDUM_API_KEY", ""), "api key")
	tenant := fs.String("tenant", envOr("CORDUM_TENANT_ID", "default"), "tenant id")
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification (also: CORDUM_TLS_INSECURE=1|true)")
	cacert := fs.String("cacert", envOr("CORDUM_TLS_CA", ""), "CA certificate for TLS verification (also: CORDUM_TLS_CA)")
	return &flagSet{FlagSet: fs, gateway: gateway, apiKey: apiKey, tenant: tenant, insecure: insecure, cacert: cacert}
}

func (fs *flagSet) ParseArgs(args []string) {
	reordered := reorderArgs(fs.FlagSet, args)
	if err := fs.Parse(reordered); err != nil {
		fail(err.Error())
	}
}

// reorderArgs rearranges args so that flag arguments (and their values) appear
// before positional arguments. Go's flag.FlagSet.Parse stops at the first
// non-flag argument, so without reordering, flags placed after a positional
// argument (e.g. "cordumctl run start <wf-id> --input '{...}'") are silently
// ignored. This function inspects the FlagSet to distinguish boolean flags
// (which don't consume a following value) from value flags (which do).
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			// Everything after bare "--" is positional.
			positional = append(positional, args[i:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		// Extract flag name: strip leading dashes, split on '='.
		name := strings.TrimLeft(arg, "-")
		hasEq := strings.Contains(name, "=")
		if hasEq {
			name = name[:strings.Index(name, "=")]
		}
		flags = append(flags, arg)
		// If this is a value flag without inline "=", consume the next arg.
		if !hasEq {
			f := fs.Lookup(name)
			if f != nil {
				isBool := false
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok {
					isBool = bf.IsBoolFlag()
				}
				if !isBool && i+1 < len(args) {
					i++
					flags = append(flags, args[i])
				}
			}
		}
	}
	return append(flags, positional...)
}

// tlsOptions resolves TLS config from CLI flags (priority) then env vars.
func (fs *flagSet) tlsOptions() sdk.TLSOptions {
	var opts sdk.TLSOptions
	switch {
	case fs.cacert != nil && *fs.cacert != "":
		opts.CACertPath = *fs.cacert
	default:
		opts.CACertPath = strings.TrimSpace(os.Getenv("CORDUM_TLS_CA"))
	}
	opts.InsecureSkipVerify = (fs.insecure != nil && *fs.insecure) ||
		parseBoolEnv("CORDUM_TLS_INSECURE")
	return opts
}

// parseBoolEnv returns true when the named env var is set to "1" or
// case-insensitive "true". Matches the SDK runtime's parseBoolEnv semantics.
func parseBoolEnv(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	return v == "1" || strings.EqualFold(v, "true")
}

func newClientFromFlags(fs *flagSet) *sdk.Client {
	client, err := sdk.NewWithTLSErr(
		strings.TrimRight(*fs.gateway, "/"),
		*fs.apiKey,
		fs.tlsOptions(),
	)
	if err != nil {
		fail(fmt.Sprintf("tls configuration error: %v", err))
	}
	if t := strings.TrimSpace(*fs.tenant); t != "" {
		client.TenantID = t
	}
	return client
}

func loadJSON(path string, out any) {
	// #nosec G304 -- CLI explicitly reads local files provided by the operator.
	data, err := os.ReadFile(path)
	check(err)
	if err := json.Unmarshal(data, out); err != nil {
		fail(fmt.Sprintf("invalid json: %v", err))
	}
}

func printJSON(value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	check(err)
	fmt.Println(string(data))
}

func usage() {
	fmt.Print(`cordumctl - Cordum platform CLI

Usage:
  cordumctl init <dir> [--force] [--framework langchain|crewai|autogen]
  cordumctl generate-certs [--dir ./certs] [--force] [--days 365]
  cordumctl demo run <demo_id> [--timeout 30] [--json]
  cordumctl mcp pending [--status pending] [--json]
  cordumctl mcp approve <approval_id> [--reason text]
  cordumctl mcp reject <approval_id> [--reason text]
  cordumctl mcp tools list [--agent-id X] [--json]
  cordumctl dev [--file docker-compose.yml] [--build] [--detach]
  cordumctl up [--file docker-compose.yml] [--build] [--detach]
  cordumctl status [--json]
  cordumctl license install <path>
  cordumctl license info [--json]
  cordumctl workflow create --file workflow.json
  cordumctl workflow delete <workflow_id>
  cordumctl run start <workflow_id> [--input input.json|'{...}'|-] [--dry-run] [--debug]
  cordumctl run get <run_id>
  cordumctl run delete <run_id>
  cordumctl run timeline <run_id>
  cordumctl approval job <job_id> (--approve|--reject)
  cordumctl approval repair <job_id> [--apply] [--note "text"]
  cordumctl dlq retry <job_id>
  cordumctl job submit --topic job.example --prompt \"hello\" [--input input.json]
  cordumctl job status <job_id>
  cordumctl job logs <job_id>
  cordumctl pool list
  cordumctl pool get <name>
  cordumctl pool create <name> [--description text] [--requires cap1,cap2]
  cordumctl pool update <name> [--description text] [--requires cap1,cap2]
  cordumctl pool delete <name> [--force]
  cordumctl pool drain <name> [--timeout 300]
  cordumctl pool topic add <pool> <topic>
  cordumctl pool topic remove <pool> <topic>
  cordumctl topic list
  cordumctl topic create <name> --pool <pool> [--input-schema id] [--output-schema id] [--pack-id id]
  cordumctl topic delete <name>
  cordumctl worker credential list
  cordumctl worker credential create --worker-id <worker_id> [--allowed-pools pool1,pool2] [--allowed-topics topic1,topic2]
  cordumctl worker credential revoke --worker-id <worker_id>
  cordumctl pack install <path|url> [--upgrade] [--inactive] [--dry-run]
  cordumctl pack uninstall <pack_id> [--purge]
  cordumctl pack list
  cordumctl pack show <pack_id>
  cordumctl pack verify <pack_id>
  cordumctl pack create <pack_id> [--dir path] [--force]
  cordumctl policy sign --in <path> [--out <path>.sig] [--key-env NAME] [--key-id ID]
  cordumctl policy verify --in <path> [--sig <path>] [--public-key-env NAME]
  cordumctl audit verify [tenant] [--since ms] [--until ms] [--limit N] [--json]
  cordumctl evals extract --name <dataset> [--dry-run]
  cordumctl evals run --dataset <id> [--use-current] [--wait]
  cordumctl auth <subcommand> [...]
  cordumctl doctor [--json] [--verbose] [--strict] [--timeout SEC] [--skip-workers] [--fix]
  cordumctl delegation <subcommand> [...]
  cordumctl governance <subcommand> [...]
  cordumctl edge init [--cwd path] [--force] [--gateway URL] [--tenant id] [--principal id] [--api-key-env NAME] [--no-wrapper] [--non-interactive]
  cordumctl edge claude [--policy-mode enforce] [--dry-run|--no-launch] [--settings-output path|-] [--claude-path path] [-- --claude-args]
  cordumctl edge doctor [--json] [--policy-mode enforce] [--settings-path path] [--claude-path path]

Global flags:
  --gateway    Gateway base URL (default from CORDUM_GATEWAY)
  --api-key    API key (default from CORDUM_API_KEY)
  --tenant     Tenant ID (default from CORDUM_TENANT_ID)
  --cacert     CA certificate for TLS verification (also: CORDUM_TLS_CA)
  --insecure   Skip TLS certificate verification (also: CORDUM_TLS_INSECURE=1|true)
`)
}

func envOr(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
