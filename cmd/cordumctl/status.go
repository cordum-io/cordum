package main

import "context"

func runStatusCmd(args []string) {
	fs := newFlagSet("status")
	fs.ParseArgs(args)
	client := newClient(*fs.gateway, *fs.apiKey, *fs.tenant)
	status, err := client.GetStatus(context.Background())
	check(err)
	printJSON(status)
}
