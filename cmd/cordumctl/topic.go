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

type topicListResponse struct {
	Items         []topicListItem `json:"items"`
	RegistryEmpty bool            `json:"registry_empty"`
}

type topicListItem struct {
	Name              string   `json:"name"`
	Pool              string   `json:"pool"`
	InputSchemaID     string   `json:"input_schema_id"`
	OutputSchemaID    string   `json:"output_schema_id"`
	PackID            string   `json:"pack_id"`
	Requires          []string `json:"requires"`
	RiskTags          []string `json:"risk_tags"`
	Status            string   `json:"status"`
	ActiveWorkerCount int      `json:"active_worker_count"`
}

func runTopicCmd(args []string) {
	if len(args) < 1 {
		topicUsage()
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "list":
		err = runTopicList(args[1:])
	case "create":
		err = runTopicCreate(args[1:])
	case "delete":
		err = runTopicDelete(args[1:])
	default:
		topicUsage()
		os.Exit(1)
	}
	if err != nil {
		fail(err.Error())
	}
}

func topicUsage() {
	fmt.Fprintln(os.Stderr, `Usage: cordumctl topic <command>

Commands:
  list                                            List registered topics
  create <name> --pool <pool>                     Register or update a topic
  delete <name>                                   Delete a topic

Flags for create:
  --pool <pool>                                   Worker pool name
  --input-schema <schema_id>                      Input schema ID
  --output-schema <schema_id>                     Output schema ID
  --pack-id <pack_id>                             Owning pack ID
  --requires cap1,cap2                            Capability requirements
  --risk-tags tag1,tag2                           Risk tags
  --status <active|deprecated|disabled>           Topic status`)
}

func runTopicList(args []string) error {
	fs := newFlagSet("topic list")
	fs.ParseArgs(args)

	client := restClientFromFlags(fs)
	resp, err := client.listTopics(context.Background())
	if err != nil {
		return err
	}

	if len(resp.Items) == 0 {
		fmt.Println("no topics registered")
		return nil
	}

	sort.Slice(resp.Items, func(i, j int) bool {
		return resp.Items[i].Name < resp.Items[j].Name
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tPOOL\tINPUT SCHEMA\tOUTPUT SCHEMA\tSTATUS\tACTIVE WORKERS")
	for _, item := range resp.Items {
		_, _ = fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%d\n",
			item.Name,
			valueOrDash(item.Pool),
			valueOrDash(item.InputSchemaID),
			valueOrDash(item.OutputSchemaID),
			valueOrDash(item.Status),
			item.ActiveWorkerCount,
		)
	}
	_ = w.Flush()
	return nil
}

func runTopicCreate(args []string) error {
	fs := newFlagSet("topic create")
	pool := fs.String("pool", "", "worker pool name")
	inputSchema := fs.String("input-schema", "", "input schema id")
	outputSchema := fs.String("output-schema", "", "output schema id")
	packID := fs.String("pack-id", "", "owning pack id")
	requires := fs.String("requires", "", "comma-separated capability requirements")
	riskTags := fs.String("risk-tags", "", "comma-separated risk tags")
	status := fs.String("status", "", "topic status")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("topic name required")
	}

	name := strings.TrimSpace(fs.Arg(0))
	if err := pools.ValidateTopicName(name); err != nil {
		return err
	}

	poolName := strings.TrimSpace(*pool)
	if poolName != "" {
		if err := pools.ValidatePoolName(poolName); err != nil {
			return err
		}
	}

	statusValue := strings.ToLower(strings.TrimSpace(*status))
	if poolName == "" && statusValue != "disabled" {
		return fmt.Errorf("pool required unless --status disabled")
	}

	req := topicRegistration{
		Name:           name,
		Pool:           poolName,
		InputSchemaID:  strings.TrimSpace(*inputSchema),
		OutputSchemaID: strings.TrimSpace(*outputSchema),
		PackID:         strings.TrimSpace(*packID),
		Requires:       splitComma(*requires),
		RiskTags:       splitComma(*riskTags),
		Status:         statusValue,
	}

	client := restClientFromFlags(fs)
	resp, err := client.createTopic(context.Background(), req)
	if err != nil {
		return err
	}

	fmt.Printf("Topic %q registered\n", resp.Name)
	return nil
}

func runTopicDelete(args []string) error {
	fs := newFlagSet("topic delete")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("topic name required")
	}

	name := strings.TrimSpace(fs.Arg(0))
	if err := pools.ValidateTopicName(name); err != nil {
		return err
	}

	client := restClientFromFlags(fs)
	if err := client.deleteTopic(context.Background(), name); err != nil {
		return err
	}

	fmt.Printf("Topic %q deleted\n", name)
	return nil
}

func valueOrDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
