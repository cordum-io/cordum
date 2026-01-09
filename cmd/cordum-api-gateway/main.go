package main

import (
	"log"

	"github.com/cordum/cordum/core/controlplane/gateway"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/config"
)

func main() {
	log.Println("cordum api gateway starting...")
	buildinfo.Log("cordum-api-gateway")
	cfg := config.Load()
	if err := gateway.Run(cfg); err != nil {
		log.Fatalf("api gateway error: %v", err)
	}
}
