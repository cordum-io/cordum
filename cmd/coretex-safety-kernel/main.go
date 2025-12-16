package main

import (
	"log"

	"github.com/yaront1111/coretex-os/core/controlplane/safetykernel"
	"github.com/yaront1111/coretex-os/core/infra/config"
)

func main() {
	log.Println("coretex safety kernel starting...")
	cfg := config.Load()
	if err := safetykernel.Run(cfg); err != nil {
		log.Fatalf("safety-kernel error: %v", err)
	}
}
