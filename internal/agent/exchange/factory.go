package exchange

import (
	"context"
	"fmt"
	"strings"

	agentconfig "quantsaas/internal/agent/config"
	"quantsaas/internal/protocol"
)

type Client interface {
	PlaceOrder(ctx context.Context, cmd protocol.TradeCommand) (protocol.Execution, error)
	GetBalances(ctx context.Context) ([]protocol.Balance, error)
}

func NewClient(cfg agentconfig.ExchangeConfig) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Name)) {
	case "", "binance", "binance_spot", "binance-spot":
		return NewBinanceClient(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported exchange %q", cfg.Name)
	}
}
