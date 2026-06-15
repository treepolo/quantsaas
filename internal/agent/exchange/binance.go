package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	agentconfig "quantsaas/internal/agent/config"
	"quantsaas/internal/protocol"
)

const (
	binanceLiveURL    = "https://api.binance.com"
	binanceTestnetURL = "https://testnet.binance.vision"
)

type BinanceClient struct {
	baseURL    string
	apiKey     string
	secretKey  string
	httpClient *http.Client
}

type binanceError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type binanceOrderResponse struct {
	Symbol              string `json:"symbol"`
	OrderID             int64  `json:"orderId"`
	ClientOrderID       string `json:"clientOrderId"`
	TransactTime        int64  `json:"transactTime"`
	ExecutedQty         string `json:"executedQty"`
	CummulativeQuoteQty string `json:"cummulativeQuoteQty"`
	Status              string `json:"status"`
	Fills               []struct {
		Price           string `json:"price"`
		Qty             string `json:"qty"`
		Commission      string `json:"commission"`
		CommissionAsset string `json:"commissionAsset"`
	} `json:"fills"`
}

func NewBinanceClient(cfg agentconfig.ExchangeConfig) *BinanceClient {
	baseURL := binanceLiveURL
	if cfg.Sandbox {
		baseURL = binanceTestnetURL
	}
	if cfg.BaseURL != "" {
		baseURL = strings.TrimRight(cfg.BaseURL, "/")
	}
	return &BinanceClient{
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		secretKey:  cfg.SecretKey,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *BinanceClient) PlaceOrder(ctx context.Context, cmd protocol.TradeCommand) (protocol.Execution, error) {
	params := url.Values{}
	params.Set("symbol", strings.ToUpper(cmd.Symbol))
	params.Set("side", strings.ToUpper(cmd.Action))
	params.Set("type", "MARKET")
	params.Set("newClientOrderId", cmd.ClientOrderID)
	params.Set("newOrderRespType", "FULL")

	switch strings.ToUpper(cmd.Action) {
	case "BUY":
		if strings.TrimSpace(cmd.AmountUSDT) == "" {
			return protocol.Execution{}, fmt.Errorf("binance buy order requires amount_usdt")
		}
		params.Set("quoteOrderQty", cmd.AmountUSDT)
	case "SELL":
		if strings.TrimSpace(cmd.QtyAsset) == "" {
			return protocol.Execution{}, fmt.Errorf("binance sell order requires qty_asset")
		}
		params.Set("quantity", cmd.QtyAsset)
	default:
		return protocol.Execution{}, fmt.Errorf("unsupported trade action %q", cmd.Action)
	}

	var resp binanceOrderResponse
	if err := c.doSigned(ctx, http.MethodPost, "/api/v3/order", params, &resp); err != nil {
		return protocol.Execution{}, err
	}
	status := "filled"
	if resp.Status != "FILLED" && resp.Status != "PARTIALLY_FILLED" {
		status = "failed"
	}

	return protocol.Execution{
		ClientOrderID: cmd.ClientOrderID,
		Action:        strings.ToUpper(cmd.Action),
		Symbol:        strings.ToUpper(cmd.Symbol),
		FilledQty:     resp.ExecutedQty,
		FilledPrice:   averageFillPrice(resp),
		Fee:           totalCommission(resp),
		Status:        status,
	}, nil
}

func (c *BinanceClient) GetBalances(ctx context.Context) ([]protocol.Balance, error) {
	params := url.Values{}
	params.Set("omitZeroBalances", "true")

	var resp struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := c.doSigned(ctx, http.MethodGet, "/api/v3/account", params, &resp); err != nil {
		return nil, err
	}

	balances := make([]protocol.Balance, 0, len(resp.Balances))
	for _, item := range resp.Balances {
		balances = append(balances, protocol.Balance{
			Asset:     item.Asset,
			Available: item.Free,
			Frozen:    item.Locked,
		})
	}
	return balances, nil
}

func (c *BinanceClient) doSigned(ctx context.Context, method string, path string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	}
	if params.Get("recvWindow") == "" {
		params.Set("recvWindow", "5000")
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))

	payload := params.Encode()
	signature := signHMACSHA256(payload, c.secretKey)
	endpoint := c.baseURL + path + "?" + payload + "&signature=" + signature

	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr binanceError
		if err := json.Unmarshal(raw, &apiErr); err == nil && apiErr.Msg != "" {
			return fmt.Errorf("binance http %d: %d %s", resp.StatusCode, apiErr.Code, apiErr.Msg)
		}
		return fmt.Errorf("binance http %d: %s", resp.StatusCode, string(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func signHMACSHA256(payload string, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func averageFillPrice(resp binanceOrderResponse) string {
	executedQty := parseDecimal(resp.ExecutedQty)
	quoteQty := parseDecimal(resp.CummulativeQuoteQty)
	if executedQty > 0 && quoteQty > 0 {
		return formatDecimal(quoteQty / executedQty)
	}
	if len(resp.Fills) > 0 {
		return resp.Fills[0].Price
	}
	return ""
}

func totalCommission(resp binanceOrderResponse) string {
	total := 0.0
	for _, fill := range resp.Fills {
		total += parseDecimal(fill.Commission)
	}
	if total <= 0 {
		return ""
	}
	return formatDecimal(total)
}

func parseDecimal(value string) float64 {
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func formatDecimal(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
