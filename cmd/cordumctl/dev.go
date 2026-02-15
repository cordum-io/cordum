package main

import (
	"flag"
	"fmt"
	"path/filepath"
)

func runDevCmd(args []string) {
	fs := flag.NewFlagSet("dev", flag.ExitOnError)
	file := fs.String("file", "docker-compose.yml", "compose file path")
	build := fs.Bool("build", true, "build images before starting")
	detach := fs.Bool("detach", false, "run in background")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}

	ensureDevCerts(filepath.Dir(*file))

	if err := runCompose(*file, *build, *detach); err != nil {
		fail(err.Error())
	}

	fmt.Println("Cordum stack started (dev mode).")
}
