package main

import (
	"log"

	"github.com/cordum/cordum/core/workflow"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/config"
)

func main() {
	log.Println("cordum workflow engine starting...")
	buildinfo.Log("cordum-workflow-engine")
	cfg := config.Load()
	if err := workflow.Run(cfg); err != nil {
		log.Fatalf("workflow engine error: %v", err)
	}
}
