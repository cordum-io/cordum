package main

import (
	"log"

	"github.com/yaront1111/coretex-os/core/controlplane/workflowengine"
	"github.com/yaront1111/coretex-os/core/infra/config"
)

func main() {
	log.Println("coretex workflow engine starting...")
	cfg := config.Load()
	if err := workflowengine.Run(cfg); err != nil {
		log.Fatalf("workflow engine error: %v", err)
	}
}
