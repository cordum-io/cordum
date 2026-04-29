package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func runGovernanceCmd(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	cmd, err := governanceHelperCommand(args)
	if err != nil {
		fail(err.Error())
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fail(err.Error())
	}
}

func governanceHelperCommand(args []string) (*exec.Cmd, error) {
	helpers := governanceHelperCandidates()
	for _, helper := range helpers {
		if _, err := os.Stat(helper); err == nil {
			return exec.Command(helper, args...), nil // #nosec G204 -- helper is resolved from cordumctl's install directory.
		}
	}
	if _, err := exec.LookPath("go"); err == nil {
		return exec.Command("go", append([]string{"run", "./cmd/cordumctl-governance-helper"}, args...)...), nil // #nosec G204 -- fixed go subcommand runs the checked-in helper.
	}
	return nil, fmt.Errorf("governance helper not found; build cordumctl-governance-helper alongside cordumctl or run from the repo with Go installed")
}

func governanceHelperCandidates() []string {
	exePath, err := os.Executable()
	if err != nil {
		return nil
	}
	exeDir := filepath.Dir(exePath)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	return []string{
		filepath.Join(exeDir, "cordumctl-governance-helper"+suffix),
		filepath.Join(exeDir, "cordumctl-governance"+suffix),
	}
}
