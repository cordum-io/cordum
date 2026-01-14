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
	case "up":
		runUpCmd(args)
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
		client := newClient(*fs.gateway, *fs.apiKey)
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
		client := newClient(*fs.gateway, *fs.apiKey)
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
		client := newClient(*fs.gateway, *fs.apiKey)
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
		client := newClient(*fs.gateway, *fs.apiKey)
		check(client.DeleteRun(context.Background(), fs.Arg(0)))
	case "timeline":
		fs := newFlagSet("run timeline")
		fs.ParseArgs(args[1:])
		if fs.NArg() < 1 {
			fail("run id required")
		}
		client := newClient(*fs.gateway, *fs.apiKey)
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
		client := newClient(*fs.gateway, *fs.apiKey)
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
		client := newClient(*fs.gateway, *fs.apiKey)
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
		client := newClient(*fs.gateway, *fs.apiKey)
		check(client.RetryDLQ(context.Background(), fs.Arg(0)))
	default:
		usage()
		os.Exit(1)
	}
}

type flagSet struct {
	*flag.FlagSet
	gateway *string
	apiKey  *string
}

func newFlagSet(name string) *flagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	gateway := fs.String("gateway", envOr("CORDUM_GATEWAY", defaultGateway), "gateway base url")
	apiKey := fs.String("api-key", envOr("CORDUM_API_KEY", ""), "api key")
	return &flagSet{FlagSet: fs, gateway: gateway, apiKey: apiKey}
}

func (fs *flagSet) ParseArgs(args []string) {
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}
}

func newClient(gateway, apiKey string) *sdk.Client {
	return sdk.New(strings.TrimRight(gateway, "/"), apiKey)
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
  cordumctl up [--file docker-compose.yml] [--build] [--detach]
  cordumctl workflow create --file workflow.json
  cordumctl workflow delete <workflow_id>
  cordumctl run start <workflow_id> [--input input.json] [--dry-run]
  cordumctl run delete <run_id>
  cordumctl run timeline <run_id>
  cordumctl approval step <workflow_id> <run_id> <step_id> (--approve|--reject)
  cordumctl approval job <job_id> (--approve|--reject)
  cordumctl dlq retry <job_id>
  cordumctl pack install <path|url> [--upgrade] [--inactive] [--dry-run]
  cordumctl pack uninstall <pack_id> [--purge]
  cordumctl pack list
  cordumctl pack show <pack_id>
  cordumctl pack verify <pack_id>

Global flags:
  --gateway   Gateway base URL (default from CORDUM_GATEWAY)
  --api-key   API key (default from CORDUM_API_KEY)
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
