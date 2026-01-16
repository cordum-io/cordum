package main

import "context"

func runStatusCmd(args []string) {
	fs := newFlagSet("status")
	fs.ParseArgs(args)
	client := newClient(*fs.gateway, *fs.apiKey)
	status, err := client.GetStatus(context.Background())
	check(err)
	printJSON(status)
}
