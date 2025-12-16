package main

import (
	"log"

	"github.com/yaront1111/coretex-os/core/controlplane/gateway"
	"github.com/yaront1111/coretex-os/core/infra/config"
)

func main() {
	log.Println("coretex api gateway starting...")
	cfg := config.Load()
	if err := gateway.Run(cfg); err != nil {
		log.Fatalf("api gateway error: %v", err)
	}
}
