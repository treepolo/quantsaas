package protocol

import "encoding/json"

type Message struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type AuthPayload struct {
	Token string `json:"token"`
}

type AuthResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type Heartbeat struct {
	Timestamp int64 `json:"timestamp"`
}

type CommandAck struct {
	ClientOrderID string `json:"client_order_id"`
	ReceivedAt    int64  `json:"received_at"`
}

type TradeCommand struct {
	ClientOrderID string `json:"client_order_id"`
	Action        string `json:"action"`
	Engine        string `json:"engine"`
	Symbol        string `json:"symbol"`
	AmountUSDT    string `json:"amount_usdt,omitempty"`
	QtyAsset      string `json:"qty_asset,omitempty"`
	LotType       string `json:"lot_type"`
}

type Balance struct {
	Asset     string `json:"asset"`
	Available string `json:"available"`
	Frozen    string `json:"frozen"`
}

type Execution struct {
	ClientOrderID string `json:"client_order_id"`
	Action        string `json:"action"`
	Symbol        string `json:"symbol"`
	FilledQty     string `json:"filled_qty"`
	FilledPrice   string `json:"filled_price"`
	Fee           string `json:"fee"`
	Status        string `json:"status"`
}

type DeltaReport struct {
	ClientOrderID string     `json:"client_order_id"`
	Balances      []Balance  `json:"balances"`
	Execution     *Execution `json:"execution,omitempty"`
}
