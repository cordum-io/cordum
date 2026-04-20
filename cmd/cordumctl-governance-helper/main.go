package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		helperUsage()
		os.Exit(1)
	}
	runGovernanceCmd(os.Args[1:])
}

func usage() {
	helperUsage()
}

func helperUsage() {
	fmt.Print(`cordumctl-governance-helper

Usage:
  cordumctl-governance-helper backfill-decisions [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--dry-run] [--redis-url redis://localhost:6379]
  cordumctl-governance-helper tail [--redis-url redis://localhost:6379] [--nats-url nats://localhost:4222]
`)
}

func envOr(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func printJSON(value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	check(err)
	fmt.Println(string(data))
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
