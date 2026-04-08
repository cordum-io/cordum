package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/cordum/cordum/core/controlplane/gateway/pools"
)

type workerCredentialListResponse struct {
	Items []workerCredentialRecord `json:"items"`
}

type workerCredentialCreateRequest struct {
	WorkerID      string   `json:"worker_id"`
	AllowedPools  []string `json:"allowed_pools"`
	AllowedTopics []string `json:"allowed_topics"`
}

type workerCredentialRecord struct {
	WorkerID      string   `json:"worker_id"`
	AllowedPools  []string `json:"allowed_pools"`
	AllowedTopics []string `json:"allowed_topics"`
	PackID        string   `json:"pack_id"`
	CreatedBy     string   `json:"created_by"`
	CreatedAt     string   `json:"created_at"`
	RevokedAt     string   `json:"revoked_at"`
}

type issuedWorkerCredential struct {
	workerCredentialRecord
	Token string `json:"token"`
}

func runWorkerCmd(args []string) {
	if len(args) < 1 {
		workerUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "credential":
		runWorkerCredentialCmd(args[1:])
	default:
		workerUsage()
		os.Exit(1)
	}
}

func runWorkerCredentialCmd(args []string) {
	if len(args) < 1 {
		workerCredentialUsage()
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "list":
		err = runWorkerCredentialList(args[1:])
	case "create":
		err = runWorkerCredentialCreate(args[1:])
	case "revoke":
		err = runWorkerCredentialRevoke(args[1:])
	default:
		workerCredentialUsage()
		os.Exit(1)
	}
	if err != nil {
		fail(err.Error())
	}
}

func workerUsage() {
	fmt.Fprintln(os.Stderr, `Usage: cordumctl worker <command>

Commands:
  credential                                     Manage worker credentials`)
}

func workerCredentialUsage() {
	fmt.Fprintln(os.Stderr, `Usage: cordumctl worker credential <command>

Commands:
  list                                           List worker credentials
  create --worker-id <worker_id>                 Create or rotate a worker credential
  revoke --worker-id <worker_id>                 Revoke a worker credential

Flags for create:
  --worker-id <worker_id>                        Worker identity to provision
  --allowed-pools pool1,pool2                    Allowed worker pools
  --allowed-topics topic1,topic2                 Allowed job topics`)
}

func runWorkerCredentialList(args []string) error {
	fs := newFlagSet("worker credential list")
	fs.ParseArgs(args)

	client := restClientFromFlags(fs)
	resp, err := client.listCredentials(context.Background())
	if err != nil {
		return err
	}

	if len(resp.Items) == 0 {
		fmt.Println("no worker credentials provisioned")
		return nil
	}

	sort.Slice(resp.Items, func(i, j int) bool {
		return resp.Items[i].WorkerID < resp.Items[j].WorkerID
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "WORKER ID\tSTATUS\tPOOLS\tTOPICS\tPACK ID\tCREATED BY\tCREATED AT")
	for _, item := range resp.Items {
		_, _ = fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.WorkerID,
			workerCredentialStatus(item),
			sliceOrDash(item.AllowedPools),
			sliceOrDash(item.AllowedTopics),
			valueOrDash(item.PackID),
			valueOrDash(item.CreatedBy),
			valueOrDash(item.CreatedAt),
		)
	}
	_ = w.Flush()
	return nil
}

func runWorkerCredentialCreate(args []string) error {
	fs := newFlagSet("worker credential create")
	workerIDFlag := fs.String("worker-id", "", "worker identity to provision")
	allowedPools := fs.String("allowed-pools", "", "comma-separated allowed pools")
	allowedTopics := fs.String("allowed-topics", "", "comma-separated allowed topics")
	fs.ParseArgs(args)

	workerID, err := workerIDFromArgs(fs, workerIDFlag)
	if err != nil {
		return err
	}

	poolsList := splitComma(*allowedPools)
	for _, poolName := range poolsList {
		if err := pools.ValidatePoolName(poolName); err != nil {
			return err
		}
	}

	topicsList := splitComma(*allowedTopics)
	for _, topic := range topicsList {
		if err := pools.ValidateTopicName(topic); err != nil {
			return err
		}
	}

	client := restClientFromFlags(fs)
	resp, err := client.createCredential(context.Background(), workerCredentialCreateRequest{
		WorkerID:      workerID,
		AllowedPools:  poolsList,
		AllowedTopics: topicsList,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(resp.Token) == "" {
		return fmt.Errorf("worker credential created but token was empty")
	}

	fmt.Fprintf(os.Stderr, "WARNING: token for worker %q is shown only once. Store it securely now.\n", resp.WorkerID)
	fmt.Println(resp.Token)
	return nil
}

func runWorkerCredentialRevoke(args []string) error {
	fs := newFlagSet("worker credential revoke")
	workerIDFlag := fs.String("worker-id", "", "worker identity to revoke")
	fs.ParseArgs(args)

	workerID, err := workerIDFromArgs(fs, workerIDFlag)
	if err != nil {
		return err
	}

	client := restClientFromFlags(fs)
	if err := client.revokeCredential(context.Background(), workerID); err != nil {
		return err
	}

	fmt.Printf("Worker credential %q revoked\n", workerID)
	return nil
}

func workerIDFromArgs(fs *flagSet, workerIDFlag *string) (string, error) {
	workerID := strings.TrimSpace(*workerIDFlag)
	if workerID == "" && fs.NArg() > 0 {
		workerID = strings.TrimSpace(fs.Arg(0))
	}
	if err := validateWorkerCredentialID(workerID); err != nil {
		return "", err
	}
	if fs.NArg() > 1 {
		return "", fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args()[1:], " "))
	}
	if fs.NArg() == 1 && strings.TrimSpace(*workerIDFlag) != "" {
		return "", fmt.Errorf("worker id provided both as --worker-id and positional argument")
	}
	return workerID, nil
}

func validateWorkerCredentialID(workerID string) error {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return fmt.Errorf("worker id required")
	}
	if strings.ContainsAny(workerID, " \t\r\n") {
		return fmt.Errorf("worker id must not contain whitespace")
	}
	return nil
}

func workerCredentialStatus(item workerCredentialRecord) string {
	if strings.TrimSpace(item.RevokedAt) != "" {
		return "revoked"
	}
	return "active"
}

func sliceOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ",")
}
