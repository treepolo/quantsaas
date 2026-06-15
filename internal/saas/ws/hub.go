package ws

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"quantsaas/internal/protocol"
	"quantsaas/internal/saas/auth"
	saasstore "quantsaas/internal/saas/store"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Hub struct {
	db       *gorm.DB
	auth     *auth.Service
	logger   *zap.Logger
	upgrader websocket.Upgrader
	conns    sync.Map
}

type AgentConn struct {
	userID  uint
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func NewHub(db *gorm.DB, authService *auth.Service, logger *zap.Logger) *Hub {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Hub{
		db:     db,
		auth:   authService,
		logger: logger,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (h *Hub) SendToAgent(userID uint, cmd protocol.TradeCommand) error {
	value, ok := h.conns.Load(userID)
	if !ok {
		return fmt.Errorf("agent not connected")
	}
	conn := value.(*AgentConn)
	return conn.write("command", cmd)
}

func (h *Hub) IsAgentConnected(userID uint) bool {
	_, ok := h.conns.Load(userID)
	return ok
}

func (h *Hub) HandleConnection(c *gin.Context) {
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Warn("websocket upgrade failed", zap.Error(err))
		return
	}

	agent, err := h.authenticate(conn)
	if err != nil {
		_ = conn.WriteJSON(protocol.Message{Type: "auth_result", Payload: protocol.AuthResult{OK: false, Error: err.Error()}})
		_ = conn.Close()
		return
	}
	defer func() {
		h.conns.Delete(agent.userID)
		_ = conn.Close()
	}()

	h.conns.Store(agent.userID, agent)
	_ = agent.write("auth_result", protocol.AuthResult{OK: true})
	h.readLoop(agent)
}

func (h *Hub) authenticate(conn *websocket.Conn) (*AgentConn, error) {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var env protocol.Envelope
	if err := conn.ReadJSON(&env); err != nil {
		return nil, err
	}
	if env.Type != "auth" {
		return nil, fmt.Errorf("first message must be auth")
	}
	var payload protocol.AuthPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return nil, err
	}
	claims, err := h.auth.ParseToken(payload.Token)
	if err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	return &AgentConn{userID: claims.UserID, conn: conn}, nil
}

func (h *Hub) readLoop(agent *AgentConn) {
	for {
		var env protocol.Envelope
		if err := agent.conn.ReadJSON(&env); err != nil {
			h.logger.Info("agent websocket closed", zap.Uint("user_id", agent.userID), zap.Error(err))
			return
		}
		switch env.Type {
		case "heartbeat":
			_ = agent.write("heartbeat_ack", protocol.Heartbeat{Timestamp: time.Now().UnixMilli()})
		case "delta_report":
			var report protocol.DeltaReport
			if err := json.Unmarshal(env.Payload, &report); err != nil {
				h.logger.Warn("invalid delta report", zap.Error(err))
				continue
			}
			if err := h.processDeltaReport(agent.userID, report); err != nil {
				h.logger.Warn("process delta report failed", zap.Error(err))
				continue
			}
			_ = agent.write("report_ack", map[string]string{"client_order_id": report.ClientOrderID})
		default:
			h.logger.Debug("ignored websocket message", zap.String("type", env.Type))
		}
	}
}

func (c *AgentConn) write(typ string, payload any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(protocol.Message{Type: typ, Payload: payload})
}

func (h *Hub) CloseAll() {
	h.conns.Range(func(key any, value any) bool {
		conn := value.(*AgentConn)
		_ = conn.conn.Close()
		h.conns.Delete(key)
		return true
	})
}

var _ interface {
	SendToAgent(uint, protocol.TradeCommand) error
} = (*Hub)(nil)

var _ = saasstore.ExecutionStatusPending
