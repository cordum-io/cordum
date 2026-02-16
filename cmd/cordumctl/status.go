package main

import "context"

func runStatusCmd(args []string) {
	fs := newFlagSet("status")
	fs.ParseArgs(args)
	client := newClientFromFlags(fs)
	status, err := client.GetStatus(context.Background())
	check(err)
	printJSON(status)
}
