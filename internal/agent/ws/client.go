package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	agentconfig "quantsaas/internal/agent/config"
	"quantsaas/internal/protocol"

	"github.com/gorilla/websocket"
)

type Exchange interface {
	PlaceOrder(ctx context.Context, cmd protocol.TradeCommand) (protocol.Execution, error)
	GetBalances(ctx context.Context) ([]protocol.Balance, error)
}

type AgentClient struct {
	cfg        agentconfig.AgentConfig
	exchange   Exchange
	httpClient *http.Client
	writeMu    sync.Mutex
}

func NewAgentClient(cfg agentconfig.AgentConfig, exchange Exchange) *AgentClient {
	return &AgentClient{
		cfg:        cfg,
		exchange:   exchange,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *AgentClient) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.connectOnce(ctx); err != nil {
			timer := time.NewTimer(backoff)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
			backoff *= 2
			if backoff > 5*time.Minute {
				backoff = 5 * time.Minute
			}
			continue
		}
		backoff = time.Second
	}
}

func (c *AgentClient) connectOnce(ctx context.Context) error {
	token, err := c.login(ctx)
	if err != nil {
		return err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsURL(), nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := c.writeMessage(conn, "auth", protocol.AuthPayload{Token: token}); err != nil {
		return err
	}
	if err := c.waitAuthResult(conn); err != nil {
		return err
	}
	if err := c.sendDeltaReport(ctx, conn, ""); err != nil {
		return err
	}
	return c.messageLoop(ctx, conn)
}

func (c *AgentClient) login(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"email":    c.cfg.Email,
		"password": c.cfg.Password,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.SaaSURL, "/")+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Token == "" {
		return "", fmt.Errorf("login failed: http %d", resp.StatusCode)
	}
	return result.Token, nil
}

func (c *AgentClient) waitAuthResult(conn *websocket.Conn) error {
	var env protocol.Envelope
	if err := conn.ReadJSON(&env); err != nil {
		return err
	}
	if env.Type != "auth_result" {
		return fmt.Errorf("unexpected first response: %s", env.Type)
	}
	var result protocol.AuthResult
	if err := json.Unmarshal(env.Payload, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("auth failed: %s", result.Error)
	}
	return nil
}

func (c *AgentClient) messageLoop(ctx context.Context, conn *websocket.Conn) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	errCh := make(chan error, 1)
	go func() {
		for {
			var env protocol.Envelope
			if err := conn.ReadJSON(&env); err != nil {
				errCh <- err
				return
			}
			switch env.Type {
			case "command":
				var cmd protocol.TradeCommand
				if err := json.Unmarshal(env.Payload, &cmd); err != nil {
					errCh <- err
					return
				}
				_ = c.writeMessage(conn, "command_ack", protocol.CommandAck{
					ClientOrderID: cmd.ClientOrderID,
					ReceivedAt:    time.Now().UnixMilli(),
				})
				go c.executeCommand(ctx, conn, cmd)
			case "heartbeat_ack", "report_ack":
			default:
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case <-ticker.C:
			if err := c.writeMessage(conn, "heartbeat", protocol.Heartbeat{Timestamp: time.Now().UnixMilli()}); err != nil {
				return err
			}
		}
	}
}

func (c *AgentClient) executeCommand(ctx context.Context, conn *websocket.Conn, cmd protocol.TradeCommand) {
	execution, err := c.exchange.PlaceOrder(ctx, cmd)
	if err != nil {
		execution = protocol.Execution{
			ClientOrderID: cmd.ClientOrderID,
			Action:        cmd.Action,
			Symbol:        cmd.Symbol,
			Status:        "failed",
		}
	}
	balances, _ := c.exchange.GetBalances(ctx)
	_ = c.writeMessage(conn, "delta_report", protocol.DeltaReport{
		ClientOrderID: cmd.ClientOrderID,
		Balances:      balances,
		Execution:     &execution,
	})
}

func (c *AgentClient) sendDeltaReport(ctx context.Context, conn *websocket.Conn, clientOrderID string) error {
	balances, err := c.exchange.GetBalances(ctx)
	if err != nil {
		return err
	}
	return c.writeMessage(conn, "delta_report", protocol.DeltaReport{
		ClientOrderID: clientOrderID,
		Balances:      balances,
	})
}

func (c *AgentClient) writeMessage(conn *websocket.Conn, typ string, payload any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.WriteJSON(protocol.Message{Type: typ, Payload: payload})
}

func (c *AgentClient) wsURL() string {
	base, err := url.Parse(c.cfg.SaaSURL)
	if err != nil {
		return strings.TrimRight(c.cfg.SaaSURL, "/") + "/ws/agent"
	}
	if base.Scheme == "https" {
		base.Scheme = "wss"
	} else {
		base.Scheme = "ws"
	}
	base.Path = "/ws/agent"
	base.RawQuery = ""
	return base.String()
}
