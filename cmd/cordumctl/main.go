package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	sdk "github.com/cordum/cordum/sdk/client"
)

const defaultGateway = "http://localhost:8081"

func main() {
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
	case "job":
		runJobCmd(args)
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
		input := fs.String("input", "", "input json file")
		dryRun := fs.Bool("dry-run", false, "start in dry-run mode")
		idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
		fs.ParseArgs(args[1:])
		if fs.NArg() < 1 {
			fail("workflow id required")
		}
		payload := map[string]any{}
		if *input != "" {
			loadJSON(*input, &payload)
		}
		client := newClientFromFlags(fs)
		runID, err := client.StartRunWithOptions(context.Background(), fs.Arg(0), payload, sdk.RunOptions{
			DryRun:         *dryRun,
			IdempotencyKey: *idempotencyKey,
		})
		check(err)
		fmt.Println(runID)
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
	case "step":
		fs := newFlagSet("approval step")
		approve := fs.Bool("approve", false, "approve the step")
		reject := fs.Bool("reject", false, "reject the step")
		fs.ParseArgs(args[1:])
		if fs.NArg() < 3 {
			fail("usage: approval step <workflow_id> <run_id> <step_id>")
		}
		if *approve == *reject {
			fail("use --approve or --reject")
		}
		client := newClientFromFlags(fs)
		check(client.ApproveStep(context.Background(), fs.Arg(0), fs.Arg(1), fs.Arg(2), *approve))
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
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification (also: CORDUM_TLS_INSECURE=1)")
	cacert := fs.String("cacert", envOr("CORDUM_TLS_CA", ""), "CA certificate for TLS verification (also: CORDUM_TLS_CA)")
	return &flagSet{FlagSet: fs, gateway: gateway, apiKey: apiKey, tenant: tenant, insecure: insecure, cacert: cacert}
}

func (fs *flagSet) ParseArgs(args []string) {
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}
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
		strings.TrimSpace(os.Getenv("CORDUM_TLS_INSECURE")) == "1"
	return opts
}

func newClientFromFlags(fs *flagSet) *sdk.Client {
	client := sdk.NewWithTLS(
		strings.TrimRight(*fs.gateway, "/"),
		*fs.apiKey,
		fs.tlsOptions(),
	)
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
  cordumctl init <dir> [--force]
  cordumctl generate-certs [--dir ./certs] [--force] [--days 365]
  cordumctl dev [--file docker-compose.yml] [--build] [--detach]
  cordumctl up [--file docker-compose.yml] [--build] [--detach]
  cordumctl status
  cordumctl workflow create --file workflow.json
  cordumctl workflow delete <workflow_id>
  cordumctl run start <workflow_id> [--input input.json] [--dry-run]
  cordumctl run delete <run_id>
  cordumctl run timeline <run_id>
  cordumctl approval step <workflow_id> <run_id> <step_id> (--approve|--reject)
  cordumctl approval job <job_id> (--approve|--reject)
  cordumctl dlq retry <job_id>
  cordumctl job submit --topic job.example --prompt \"hello\" [--input input.json]
  cordumctl job status <job_id>
  cordumctl job logs <job_id>
  cordumctl pack install <path|url> [--upgrade] [--inactive] [--dry-run]
  cordumctl pack uninstall <pack_id> [--purge]
  cordumctl pack list
  cordumctl pack show <pack_id>
  cordumctl pack verify <pack_id>
  cordumctl pack create <pack_id> [--dir path] [--force]

Global flags:
  --gateway    Gateway base URL (default from CORDUM_GATEWAY)
  --api-key    API key (default from CORDUM_API_KEY)
  --cacert     CA certificate for TLS verification (also: CORDUM_TLS_CA)
  --insecure   Skip TLS certificate verification (also: CORDUM_TLS_INSECURE=1)
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
