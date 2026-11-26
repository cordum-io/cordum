package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/cordum/cordum/tools/perfharness"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := command(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func command(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: perfharness run|validate")
	}
	switch args[0] {
	case "validate":
		if len(args) != 2 {
			return fmt.Errorf("usage: perfharness validate <results.json>")
		}
		file, err := os.Open(filepath.Clean(args[1]))
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(file, 4<<20+1))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		_, err = perfharness.ParseResults(data)
		return err
	case "run":
		flags := flag.NewFlagSet("run", flag.ContinueOnError)
		workload := flags.String("workload", "", "workload YAML file")
		profile := flags.String("profile", "", "profile name")
		out := flags.String("out", "", "results JSON file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *workload == "" || *profile == "" || *out == "" || flags.NArg() != 0 {
			return fmt.Errorf("run requires --workload, --profile and --out")
		}
		data, err := perfharness.Run(ctx, *workload, *profile)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Clean(*out), data, 0600)
	default:
		return fmt.Errorf("unknown command; use run or validate")
	}
}
