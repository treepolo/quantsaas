package store

import (
	"time"

	"gorm.io/gorm"
)

const (
	InstanceStatusRunning = "RUNNING"
	InstanceStatusStopped = "STOPPED"
	InstanceStatusError   = "ERROR"

	LotTypeDeadStack  = "DEAD_STACK"
	LotTypeFloating   = "FLOATING"
	LotTypeColdSealed = "COLD_SEALED"

	TradeActionBuy  = "BUY"
	TradeActionSell = "SELL"

	TradeEngineMacro = "MACRO"
	TradeEngineMicro = "MICRO"

	ExecutionStatusPending = "pending"
	ExecutionStatusFilled  = "filled"
	ExecutionStatusFailed  = "failed"

	GeneRoleChallenger = "challenger"
	GeneRoleChampion   = "champion"
	GeneRoleRetired    = "retired"

	BacktestStatusRunning   = "running"
	BacktestStatusCompleted = "completed"
	BacktestStatusFailed    = "failed"

	TaskStatusCancelled = "cancelled"

	ExecutionModeCloseSameBar  = "close_same_bar"
	ExecutionModeCloseNextOpen = "close_next_open"
	ExecutionModePreclose10m   = "preclose_10m"
)

type User struct {
	ID           uint `gorm:"primaryKey"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Email        string `gorm:"size:255;uniqueIndex;not null"`
	PasswordHash string `gorm:"size:255;not null"`
	Role         string `gorm:"size:32;not null;default:user"`
	Plan         string `gorm:"size:64;not null;default:free"`
	Status       string `gorm:"size:32;not null;default:active"`
}

type StrategyTemplate struct {
	ID        string `gorm:"size:80;primaryKey"`
	CreatedAt time.Time
	UpdatedAt time.Time
	Name      string `gorm:"size:120;not null"`
	Version   string `gorm:"size:40;not null"`
	IsSpot    bool   `gorm:"not null;default:true"`
	Manifest  JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
}

type StrategyInstance struct {
	ID         uint `gorm:"primaryKey"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  gorm.DeletedAt `gorm:"index"`
	UserID     uint           `gorm:"not null;index"`
	TemplateID string         `gorm:"size:80;not null;index"`
	Name       string         `gorm:"size:120;not null"`
	Symbol     string         `gorm:"size:32;not null;index"`
	Exchange   string         `gorm:"size:64;not null"`
	Status     string         `gorm:"size:20;not null;index;default:STOPPED"`
	Config     JSONB          `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	LastTickAt *time.Time

	User     User             `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	Template StrategyTemplate `gorm:"foreignKey:TemplateID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

type PortfolioState struct {
	ID                   uint `gorm:"primaryKey"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
	InstanceID           uint    `gorm:"not null;uniqueIndex"`
	USDTBalance          float64 `gorm:"type:numeric(30,10);not null;default:0"`
	DeadBTC              float64 `gorm:"type:numeric(30,10);not null;default:0"`
	FloatBTC             float64 `gorm:"type:numeric(30,10);not null;default:0"`
	ColdSealedBTC        float64 `gorm:"type:numeric(30,10);not null;default:0"`
	TotalEquity          float64 `gorm:"type:numeric(30,10);not null;default:0"`
	LastProcessedBarTime int64   `gorm:"not null;default:0"`

	Instance StrategyInstance `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

type RuntimeState struct {
	ID         uint `gorm:"primaryKey"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	InstanceID uint  `gorm:"not null;uniqueIndex"`
	State      JSONB `gorm:"type:jsonb;not null;default:'{}'::jsonb"`

	Instance StrategyInstance `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

type SpotLot struct {
	ID                  uint `gorm:"primaryKey"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
	InstanceID          uint    `gorm:"not null;index"`
	LotType             string  `gorm:"size:24;not null;index"`
	Amount              float64 `gorm:"type:numeric(30,10);not null"`
	CostPrice           float64 `gorm:"type:numeric(30,10);not null"`
	IsColdSealed        bool    `gorm:"not null;default:false"`
	SourceTradeRecordID *uint

	Instance StrategyInstance `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

type TradeRecord struct {
	ID            uint `gorm:"primaryKey"`
	CreatedAt     time.Time
	InstanceID    uint      `gorm:"not null;index"`
	ClientOrderID string    `gorm:"size:120;not null;uniqueIndex"`
	Action        string    `gorm:"size:8;not null"`
	Engine        string    `gorm:"size:12;not null"`
	Symbol        string    `gorm:"size:32;not null;index"`
	LotType       string    `gorm:"size:24;not null"`
	FilledQty     float64   `gorm:"type:numeric(30,10);not null;default:0"`
	FilledPrice   float64   `gorm:"type:numeric(30,10);not null;default:0"`
	Fee           float64   `gorm:"type:numeric(30,10);not null;default:0"`
	ExecutedAt    time.Time `gorm:"not null"`
	RawPayload    JSONB     `gorm:"type:jsonb;not null;default:'{}'::jsonb"`

	Instance StrategyInstance `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

type SpotExecution struct {
	ID            uint `gorm:"primaryKey"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	InstanceID    uint   `gorm:"not null;index"`
	ClientOrderID string `gorm:"size:120;not null;uniqueIndex"`
	Status        string `gorm:"size:20;not null;index;default:pending"`
	Request       JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	Response      JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ErrorMessage  string `gorm:"type:text"`
	CompletedAt   *time.Time

	Instance StrategyInstance `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

type AuditLog struct {
	ID         uint `gorm:"primaryKey"`
	CreatedAt  time.Time
	UserID     *uint  `gorm:"index"`
	InstanceID *uint  `gorm:"index"`
	EventType  string `gorm:"size:120;not null;index"`
	Payload    JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
}

type GeneRecord struct {
	ID            uint `gorm:"primaryKey"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	StrategyID    string  `gorm:"size:80;not null;index"`
	InstrumentID  string  `gorm:"size:32;not null;index;default:BTCUSDT"`
	DataSource    string  `gorm:"size:32;not null;index;default:binance"`
	Interval      string  `gorm:"size:16;not null;index;default:1d"`
	ExecutionMode string  `gorm:"size:32;not null;index;default:close_same_bar"`
	Role          string  `gorm:"size:24;not null;index"`
	ParamPack     JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ScoreTotal    float64 `gorm:"type:numeric(30,10);not null;default:0"`
	MaxDrawdown   float64 `gorm:"type:numeric(30,10);not null;default:0"`
	WindowScore   JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ActivatedAt   *time.Time
}

type EvolutionTask struct {
	ID            uint `gorm:"primaryKey"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	StrategyID    string  `gorm:"size:80;not null;index"`
	InstrumentID  string  `gorm:"size:32;not null;index;default:BTCUSDT"`
	DataSource    string  `gorm:"size:32;not null;index;default:binance"`
	Interval      string  `gorm:"size:16;not null;index;default:1d"`
	ExecutionMode string  `gorm:"size:32;not null;index;default:close_same_bar"`
	TrainStartMs  int64   `gorm:"not null;default:0"`
	TrainEndMs    int64   `gorm:"not null;default:0"`
	Status        string  `gorm:"size:32;not null;index"`
	Progress      float64 `gorm:"type:numeric(10,6);not null;default:0"`
	Config        JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	Result        JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ErrorMessage  string  `gorm:"type:text"`
	StartedAt     *time.Time
	FinishedAt    *time.Time
}

type BacktestRun struct {
	ID            uint `gorm:"primaryKey"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	UserID        uint   `gorm:"not null;index"`
	StrategyID    string `gorm:"size:80;not null;index"`
	InstanceID    *uint  `gorm:"index"`
	InstrumentID  string `gorm:"size:32;not null;index;default:BTCUSDT"`
	DataSource    string `gorm:"size:32;not null;index;default:binance"`
	ExecutionMode string `gorm:"size:32;not null;index;default:close_same_bar"`
	StartTimeMs   int64  `gorm:"not null;default:0"`
	EndTimeMs     int64  `gorm:"not null;default:0"`
	Symbol        string `gorm:"size:32;not null;index"`
	Interval      string `gorm:"size:16;not null;index"`
	Source        string `gorm:"size:32;not null;index"`
	Status        string `gorm:"size:32;not null;index"`
	Request       JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	Result        JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ErrorMessage  string `gorm:"type:text"`
	StartedAt     *time.Time
	FinishedAt    *time.Time

	User     User              `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Instance *StrategyInstance `gorm:"constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
}

type KLine struct {
	ID           uint `gorm:"primaryKey"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	InstrumentID string  `gorm:"size:32;not null;index;default:BTCUSDT"`
	Source       string  `gorm:"size:32;not null;index;default:binance;uniqueIndex:idx_klines_source_symbol_interval_open_time"`
	Symbol       string  `gorm:"size:32;not null;uniqueIndex:idx_klines_symbol_interval_open_time;uniqueIndex:idx_klines_source_symbol_interval_open_time"`
	Interval     string  `gorm:"size:16;not null;uniqueIndex:idx_klines_symbol_interval_open_time;uniqueIndex:idx_klines_source_symbol_interval_open_time"`
	OpenTime     int64   `gorm:"not null;uniqueIndex:idx_klines_symbol_interval_open_time;uniqueIndex:idx_klines_source_symbol_interval_open_time"`
	Open         float64 `gorm:"type:numeric(30,10);not null"`
	High         float64 `gorm:"type:numeric(30,10);not null"`
	Low          float64 `gorm:"type:numeric(30,10);not null"`
	Close        float64 `gorm:"type:numeric(30,10);not null"`
	Volume       float64 `gorm:"type:numeric(30,10);not null"`
}

type DailyExecutionSnapshot struct {
	ID           uint `gorm:"primaryKey"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	InstrumentID string  `gorm:"size:32;not null;index;uniqueIndex:idx_daily_execution_snapshots_identity"`
	DataSource   string  `gorm:"size:32;not null;index;uniqueIndex:idx_daily_execution_snapshots_identity"`
	Symbol       string  `gorm:"size:32;not null;index"`
	TradeDateMs  int64   `gorm:"not null;index;uniqueIndex:idx_daily_execution_snapshots_identity"`
	SnapshotType string  `gorm:"size:32;not null;index;uniqueIndex:idx_daily_execution_snapshots_identity"`
	Price        float64 `gorm:"type:numeric(30,10);not null"`
	Volume       float64 `gorm:"type:numeric(30,10);not null;default:0"`
	ObservedAtMs int64   `gorm:"not null;index"`
}
