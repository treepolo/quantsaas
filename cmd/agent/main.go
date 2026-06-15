package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	agentconfig "quantsaas/internal/agent/config"
	"quantsaas/internal/agent/exchange"
	agentws "quantsaas/internal/agent/ws"
)

func main() {
	cfg, err := agentconfig.Load("config.agent.yaml")
	if err != nil {
		panic(err)
	}

	exchangeClient, err := exchange.NewClient(cfg.Exchange)
	if err != nil {
		panic(err)
	}
	client := agentws.NewAgentClient(cfg, exchangeClient)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := client.Run(ctx); err != nil && err != context.Canceled {
		panic(err)
	}
}
