package main

import (
	"log"

	"github.com/yaront1111/coretex-os/core/controlplane/gateway"
	"github.com/yaront1111/coretex-os/core/infra/buildinfo"
	"github.com/yaront1111/coretex-os/core/infra/config"
)

func main() {
	log.Println("coretex api gateway starting...")
	buildinfo.Log("coretex-api-gateway")
	cfg := config.Load()
	if err := gateway.Run(cfg); err != nil {
		log.Fatalf("api gateway error: %v", err)
	}
}
