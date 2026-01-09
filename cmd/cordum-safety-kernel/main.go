package main

import (
	"log"

	"github.com/cordum/cordum/core/controlplane/safetykernel"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/config"
)

func main() {
	log.Println("cordum safety kernel starting...")
	buildinfo.Log("cordum-safety-kernel")
	cfg := config.Load()
	if err := safetykernel.Run(cfg); err != nil {
		log.Fatalf("safety-kernel error: %v", err)
	}
}
