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
	BacktestStatusCancelled = "cancelled"

	BacktestResultStatusPending     = "pending"
	BacktestResultStatusRunning     = "running"
	BacktestResultStatusCompleted   = "completed"
	BacktestResultStatusFailed      = "failed"
	BacktestResultStatusCancelled   = "cancelled"
	BacktestResultStatusInvalidated = "invalidated"
	BacktestResultStatusArchived    = "archived"

	BacktestPathStatePending   = "pending"
	BacktestPathStateAvailable = "available"
	BacktestPathStateDeleted   = "deleted"

	PerformanceReportStatusPending     = "pending"
	PerformanceReportStatusRunning     = "running"
	PerformanceReportStatusCompleted   = "completed"
	PerformanceReportStatusFailed      = "failed"
	PerformanceReportStatusCancelled   = "cancelled"
	PerformanceReportStatusInvalidated = "invalidated"
	PerformanceReportStatusArchived    = "archived"

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
	DeletedAt     gorm.DeletedAt `gorm:"index"`
	StrategyID    string         `gorm:"size:80;not null;index"`
	InstrumentID  string         `gorm:"size:32;not null;index;default:BTCUSDT"`
	DataSource    string         `gorm:"size:32;not null;index;default:binance"`
	Interval      string         `gorm:"size:16;not null;index;default:1d"`
	ExecutionMode string         `gorm:"size:32;not null;index;default:close_same_bar"`
	Role          string         `gorm:"size:24;not null;index"`
	Name          string         `gorm:"size:160"`
	Notes         string         `gorm:"type:text"`
	Tags          JSONB          `gorm:"type:jsonb;not null;default:'[]'::jsonb"`
	SearchConfig  JSONB          `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ParamPack     JSONB          `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ScoreTotal    float64        `gorm:"type:numeric(30,10);not null;default:0"`
	MaxDrawdown   float64        `gorm:"type:numeric(30,10);not null;default:0"`
	WindowScore   JSONB          `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ActivatedAt   *time.Time
}

type GeneObservation struct {
	ID            uint `gorm:"primaryKey"`
	CreatedAt     time.Time
	StrategyID    string  `gorm:"size:80;not null;index"`
	InstrumentID  string  `gorm:"size:32;not null;index;default:BTCUSDT"`
	DataSource    string  `gorm:"size:32;not null;index;default:binance"`
	Interval      string  `gorm:"size:16;not null;index;default:1d"`
	ExecutionMode string  `gorm:"size:32;not null;index;default:close_same_bar"`
	TrainStartMs  int64   `gorm:"not null;default:0;index"`
	TrainEndMs    int64   `gorm:"not null;default:0;index"`
	SpawnMode     string  `gorm:"size:32;not null;index;default:inherit"`
	SearchHash    string  `gorm:"size:64;not null;index;uniqueIndex:idx_gene_observation_unique"`
	TaskID        uint    `gorm:"not null;index"`
	Generation    int     `gorm:"not null;index"`
	Individual    int     `gorm:"not null;default:0"`
	Fingerprint   string  `gorm:"size:32;not null;index;uniqueIndex:idx_gene_observation_unique"`
	ParamPack     JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ScoreTotal    float64 `gorm:"type:numeric(30,10);not null;default:0;index"`
	MaxDrawdown   float64 `gorm:"type:numeric(30,10);not null;default:0"`
	Fatal         bool    `gorm:"not null;default:false"`
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

// BacktestSpec is the immutable identity of one fully resolved backtest input.
// Snapshot is the canonical payload covered by ContentHash and BacktestKey.
type BacktestSpec struct {
	ID                uint `gorm:"primaryKey"`
	CreatedAt         time.Time
	BacktestKey       string `gorm:"size:96;not null;uniqueIndex"`
	SchemaVersion     string `gorm:"size:40;not null"`
	ContentHash       string `gorm:"size:80;not null;index"`
	StrategyID        string `gorm:"size:80;not null;index"`
	StrategyVersion   string `gorm:"size:40;not null;index"`
	InstrumentID      string `gorm:"size:32;not null;index"`
	Symbol            string `gorm:"size:64;not null;index"`
	DataSource        string `gorm:"size:32;not null;index"`
	Interval          string `gorm:"size:16;not null;index"`
	ExecutionMode     string `gorm:"size:32;not null;index"`
	StartTimeMs       int64  `gorm:"not null;index"`
	EndTimeMs         int64  `gorm:"not null;index"`
	ParameterHash     string `gorm:"size:80;not null;index"`
	DatasetHash       string `gorm:"size:80;not null;index"`
	CoreVersion       string `gorm:"size:40;not null;index"`
	PositionStructure string `gorm:"size:40;not null;index"`
	Snapshot          JSONB  `gorm:"type:jsonb;not null"`
}

// BacktestResult owns lifecycle state. Completed content remains immutable;
// invalidation and archival only change lifecycle fields and ActiveKey.
type BacktestResult struct {
	ID                 uint `gorm:"primaryKey"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
	BacktestSpecID     uint    `gorm:"not null;index"`
	BacktestKey        string  `gorm:"size:96;not null;index"`
	ActiveKey          *string `gorm:"size:160;uniqueIndex"`
	ResultVersion      string  `gorm:"size:40;not null;index"`
	Status             string  `gorm:"size:24;not null;index"`
	SummaryHash        string  `gorm:"size:80"`
	PathManifest       JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	PathManifestHash   string  `gorm:"size:80"`
	ContentHash        string  `gorm:"size:80;index"`
	PathBlockCount     int     `gorm:"not null;default:0"`
	PathPointCount     int     `gorm:"not null;default:0"`
	PathState          string  `gorm:"size:24;not null;default:pending;index"`
	ErrorMessage       string  `gorm:"type:text"`
	InvalidationReason string  `gorm:"type:text"`
	StartedAt          *time.Time
	CompletedAt        *time.Time
	InvalidatedAt      *time.Time
	ArchivedAt         *time.Time
	PathDeletedAt      *time.Time

	Spec               BacktestSpec           `gorm:"foreignKey:BacktestSpecID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	Summary            *BacktestResultSummary `gorm:"foreignKey:BacktestResultID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	PathBlocks         []BacktestPathBlock    `gorm:"foreignKey:BacktestResultID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	PerformanceReports []PerformanceReport    `gorm:"foreignKey:BacktestResultID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
}

// BacktestResultSummary is the immutable, query-friendly summary payload.
type BacktestResultSummary struct {
	ID                      uint `gorm:"primaryKey"`
	CreatedAt               time.Time
	BacktestResultID        uint     `gorm:"not null;uniqueIndex"`
	SchemaVersion           string   `gorm:"size:40;not null"`
	ContentHash             string   `gorm:"size:80;not null;index"`
	ROI                     float64  `gorm:"type:double precision;not null"`
	FinalEquity             float64  `gorm:"type:double precision;not null"`
	MaxDrawdown             float64  `gorm:"type:double precision;not null"`
	TradeCount              int      `gorm:"not null"`
	ExposureDaysRatio       float64  `gorm:"type:double precision;not null"`
	AverageActualExposure   float64  `gorm:"type:double precision;not null"`
	LongestUnderwaterDays   float64  `gorm:"type:double precision;not null"`
	LongestUnderwaterPoints int      `gorm:"not null"`
	Sortino                 *float64 `gorm:"type:double precision"`
	Beta                    *float64 `gorm:"type:double precision"`
	Payload                 JSONB    `gorm:"type:jsonb;not null"`

	BacktestResult BacktestResult `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
}

// BacktestPathBlock stores an ordered immutable slice of the standardized path.
type BacktestPathBlock struct {
	ID               uint `gorm:"primaryKey"`
	CreatedAt        time.Time
	BacktestResultID uint   `gorm:"not null;index;uniqueIndex:idx_backtest_path_block_order"`
	BlockIndex       int    `gorm:"not null;uniqueIndex:idx_backtest_path_block_order"`
	SchemaVersion    string `gorm:"size:40;not null"`
	StartPointIndex  int    `gorm:"not null"`
	EndPointIndex    int    `gorm:"not null"`
	StartTimeMs      int64  `gorm:"not null;index"`
	EndTimeMs        int64  `gorm:"not null;index"`
	PointCount       int    `gorm:"not null"`
	ContentHash      string `gorm:"size:80;not null;index"`
	Payload          JSONB  `gorm:"type:jsonb;not null"`

	BacktestResult BacktestResult `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
}

// PerformanceReport is one immutable, versioned analysis of a standardized
// backtest result. ActiveKey provides concurrent deduplication for the current
// report schema while archived and invalidated reports remain traceable.
type PerformanceReport struct {
	ID                             uint `gorm:"primaryKey"`
	CreatedAt                      time.Time
	UpdatedAt                      time.Time
	BacktestResultID               uint    `gorm:"not null;index"`
	AnnualizationBacktestResultID  uint    `gorm:"not null;index"`
	AnalysisKey                    string  `gorm:"size:96;not null;index"`
	ActiveKey                      *string `gorm:"size:160;uniqueIndex"`
	AnalysisVersion                string  `gorm:"size:40;not null;index"`
	SchemaVersion                  string  `gorm:"size:40;not null;index"`
	SettingsHash                   string  `gorm:"size:80;not null;index"`
	Settings                       JSONB   `gorm:"type:jsonb;not null"`
	SourceResultContentHash        string  `gorm:"size:80;not null;index"`
	AnnualizationResultContentHash string  `gorm:"size:80;not null;index"`
	Status                         string  `gorm:"size:24;not null;index"`
	SummaryHash                    string  `gorm:"size:80"`
	ChartManifest                  JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ChartManifestHash              string  `gorm:"size:80"`
	ContentHash                    string  `gorm:"size:80;index"`
	ErrorMessage                   string  `gorm:"type:text"`
	InvalidationReason             string  `gorm:"type:text"`
	StartedAt                      *time.Time
	CompletedAt                    *time.Time
	InvalidatedAt                  *time.Time
	ArchivedAt                     *time.Time

	BacktestResult              BacktestResult                `gorm:"foreignKey:BacktestResultID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	AnnualizationBacktestResult BacktestResult                `gorm:"foreignKey:AnnualizationBacktestResultID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	Summary                     *PerformanceReportSummary     `gorm:"foreignKey:PerformanceReportID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	ChartBlocks                 []PerformanceReportChartBlock `gorm:"foreignKey:PerformanceReportID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
}

// PerformanceReportSummary contains both query columns and the canonical
// immutable summary payload covered by ContentHash.
type PerformanceReportSummary struct {
	ID                             uint `gorm:"primaryKey"`
	CreatedAt                      time.Time
	PerformanceReportID            uint     `gorm:"not null;uniqueIndex"`
	SchemaVersion                  string   `gorm:"size:40;not null"`
	ContentHash                    string   `gorm:"size:80;not null;index"`
	FinalNAVRatio                  float64  `gorm:"type:double precision;not null"`
	LogFinalNAVRatio               float64  `gorm:"type:double precision;not null"`
	StrategyNoCashFlowAnnualized   *float64 `gorm:"type:double precision"`
	BenchmarkNoCashFlowAnnualized  *float64 `gorm:"type:double precision"`
	NoCashFlowAnnualizedDifference *float64 `gorm:"type:double precision"`
	Sortino                        *float64 `gorm:"type:double precision"`
	Beta                           *float64 `gorm:"type:double precision"`
	LongestUnderwaterDays          float64  `gorm:"type:double precision;not null"`
	ExposureDaysRatio              float64  `gorm:"type:double precision;not null"`
	AverageActualExposure          float64  `gorm:"type:double precision;not null"`
	ExposureAdjustedReturn         *float64 `gorm:"type:double precision"`
	Payload                        JSONB    `gorm:"type:jsonb;not null"`

	PerformanceReport PerformanceReport `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
}

// PerformanceReportChartBlock stores one independently loadable chart data
// block so opening a summary does not fetch every histogram and time series.
type PerformanceReportChartBlock struct {
	ID                  uint `gorm:"primaryKey"`
	CreatedAt           time.Time
	PerformanceReportID uint   `gorm:"not null;index;uniqueIndex:idx_performance_report_chart_kind"`
	Kind                string `gorm:"size:48;not null;uniqueIndex:idx_performance_report_chart_kind"`
	SchemaVersion       string `gorm:"size:40;not null"`
	ContentHash         string `gorm:"size:80;not null;index"`
	PointCount          int    `gorm:"not null;default:0"`
	Payload             JSONB  `gorm:"type:jsonb;not null"`

	PerformanceReport PerformanceReport `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
}

// ComputeTask is the PostgreSQL source of truth for one standalone atomic
// computation, one composite research workflow, or one explicitly started
// stage within a composite workflow. The immutable manifest and versions make
// unfinished work resumable without regenerating a different plan.
type ComputeTask struct {
	ID                  uint `gorm:"primaryKey"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
	UserID              uint    `gorm:"not null;index"`
	ParentTaskID        *uint   `gorm:"index;uniqueIndex:idx_compute_task_stage_key"`
	Kind                string  `gorm:"size:24;not null;index"`
	TaskType            string  `gorm:"size:80;not null;index"`
	Title               string  `gorm:"size:160;not null"`
	PlanKey             string  `gorm:"size:96;not null;index"`
	ActiveKey           *string `gorm:"size:192;uniqueIndex"`
	TaskSchemaVersion   string  `gorm:"size:40;not null"`
	LifecycleVersion    string  `gorm:"size:40;not null"`
	ExecutorType        string  `gorm:"size:80;not null;default:'';index"`
	ExecutorVersion     string  `gorm:"size:40;not null;default:''"`
	ResultSchemaVersion string  `gorm:"size:40;not null;default:''"`
	SettingsHash        string  `gorm:"size:80;not null;default:'';index"`
	Settings            JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ResearchSettingID   string  `gorm:"size:96;not null;default:'';index"`
	ResearchSettingHash string  `gorm:"size:80;not null;default:'';index"`
	StageKey            string  `gorm:"size:80;not null;default:'';uniqueIndex:idx_compute_task_stage_key"`
	StageType           string  `gorm:"size:80;not null;default:''"`
	StageOrder          int     `gorm:"not null;default:0"`
	ManifestVersion     string  `gorm:"size:40;not null;default:''"`
	ManifestHash        string  `gorm:"size:80;not null;default:'';index"`
	Manifest            JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	TotalItems          int     `gorm:"not null;default:0"`
	EstimatedUnits      int64   `gorm:"not null;default:0"`
	UnknownUnitItems    int     `gorm:"not null;default:0"`
	CacheHitCount       int     `gorm:"not null;default:0"`
	NewItemCount        int     `gorm:"not null;default:0"`
	ValidResultCount    int     `gorm:"not null;default:0"`
	FailedCount         int     `gorm:"not null;default:0"`
	MissingCount        int     `gorm:"not null;default:0"`
	CancelledCount      int     `gorm:"not null;default:0"`
	Progress            float64 `gorm:"type:numeric(10,6);not null;default:0"`
	Status              string  `gorm:"size:24;not null;index"`
	ErrorMessage        string  `gorm:"type:text"`
	Attempt             int     `gorm:"not null;default:0"`
	RNGAlgorithm        string  `gorm:"size:48;not null;default:''"`
	RNGVersion          string  `gorm:"size:40;not null;default:''"`
	RootSeed            *int64
	RNGPosition         int64  `gorm:"not null;default:0"`
	Checkpoint          JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	CheckpointHash      string `gorm:"size:80;not null;default:''"`
	CancelRequestedAt   *time.Time
	StartedAt           *time.Time
	CompletedAt         *time.Time
	CancelledAt         *time.Time
	InvalidatedAt       *time.Time

	Parent       *ComputeTask            `gorm:"foreignKey:ParentTaskID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	Children     []ComputeTask           `gorm:"foreignKey:ParentTaskID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	Items        []ComputeTaskItem       `gorm:"foreignKey:ComputeTaskID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	Dependencies []ComputeTaskDependency `gorm:"foreignKey:ComputeTaskID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
}

// ComputeTaskDependency stores an explicit stage dependency. A composite
// workflow never infers dependency order from timestamps or in-memory state.
type ComputeTaskDependency struct {
	ID              uint `gorm:"primaryKey"`
	CreatedAt       time.Time
	ComputeTaskID   uint `gorm:"not null;index;uniqueIndex:idx_compute_task_dependency"`
	DependsOnTaskID uint `gorm:"not null;index;uniqueIndex:idx_compute_task_dependency"`

	Task      ComputeTask `gorm:"foreignKey:ComputeTaskID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	DependsOn ComputeTask `gorm:"foreignKey:DependsOnTaskID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
}

// ComputeTaskItem is one finite, independently retryable manifest item. Result
// stores small canonical output metadata or an immutable domain result
// reference; large domain payloads remain in their owning tables.
type ComputeTaskItem struct {
	ID             uint `gorm:"primaryKey"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ComputeTaskID  uint    `gorm:"not null;index;uniqueIndex:idx_compute_task_item_key;uniqueIndex:idx_compute_task_item_index"`
	ItemIndex      int     `gorm:"not null;uniqueIndex:idx_compute_task_item_index"`
	ItemKey        string  `gorm:"size:160;not null;uniqueIndex:idx_compute_task_item_key"`
	BaseCacheKey   string  `gorm:"size:192;not null"`
	CacheKey       string  `gorm:"size:96;not null;index"`
	InputHash      string  `gorm:"size:80;not null;index"`
	Input          JSONB   `gorm:"type:jsonb;not null"`
	EstimatedUnits int64   `gorm:"not null;default:0"`
	Status         string  `gorm:"size:24;not null;index"`
	Progress       float64 `gorm:"type:numeric(10,6);not null;default:0"`
	Attempt        int     `gorm:"not null;default:0"`
	CacheEntryID   *uint   `gorm:"index"`
	Result         JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ResultHash     string  `gorm:"size:80;not null;default:'';index"`
	ErrorMessage   string  `gorm:"type:text"`
	Checkpoint     JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	CheckpointHash string  `gorm:"size:80;not null;default:''"`
	RNGPosition    int64   `gorm:"not null;default:0"`
	LeaseOwner     string  `gorm:"size:96;not null;default:'';index"`
	LeaseExpiresAt *time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
	FailedAt       *time.Time
	CancelledAt    *time.Time

	Task       ComputeTask        `gorm:"foreignKey:ComputeTaskID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	CacheEntry *ComputeCacheEntry `gorm:"foreignKey:CacheEntryID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;" json:"-"`
}

// ComputeCacheEntry is the durable, versioned item cache. ActiveKey is scoped
// by user in the service so cached result metadata cannot leak across users;
// domain stores such as P03 may still safely deduplicate their own immutable
// results under their existing authorization rules.
type ComputeCacheEntry struct {
	ID                  uint `gorm:"primaryKey"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
	OwnerUserID         uint    `gorm:"not null;index"`
	CacheKey            string  `gorm:"size:96;not null;index"`
	ActiveKey           *string `gorm:"size:192;uniqueIndex"`
	SchemaVersion       string  `gorm:"size:40;not null"`
	ExecutorType        string  `gorm:"size:80;not null;index"`
	ExecutorVersion     string  `gorm:"size:40;not null"`
	ResultSchemaVersion string  `gorm:"size:40;not null"`
	InputHash           string  `gorm:"size:80;not null;index"`
	Status              string  `gorm:"size:24;not null;index"`
	Result              JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ContentHash         string  `gorm:"size:80;not null;default:'';index"`
	SourceTaskItemID    *uint   `gorm:"index"`
	Attempt             int     `gorm:"not null;default:0"`
	LeaseOwner          string  `gorm:"size:96;not null;default:'';index"`
	LeaseExpiresAt      *time.Time
	ErrorMessage        string `gorm:"type:text"`
	CompletedAt         *time.Time
	InvalidatedAt       *time.Time
	ArchivedAt          *time.Time
}

// RobustnessStudy owns one immutable P08 center scan, multidimensional sample,
// or imported M evaluation-point collection. PostgreSQL is the source of truth;
// ComputeTask only owns execution lifecycle and reusable item cache.
type RobustnessStudy struct {
	ID                  uint `gorm:"primaryKey"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
	OwnerUserID         uint   `gorm:"not null;index;uniqueIndex:idx_robustness_study_key"`
	StudyKey            string `gorm:"size:96;not null;uniqueIndex:idx_robustness_study_key"`
	Name                string `gorm:"size:160;not null"`
	Mode                string `gorm:"size:32;not null;index"`
	Status              string `gorm:"size:24;not null;index"`
	SettingVersion      string `gorm:"size:40;not null"`
	SettingHash         string `gorm:"size:80;not null;index"`
	Settings            JSONB  `gorm:"type:jsonb;not null"`
	SpaceVersion        string `gorm:"size:40;not null"`
	SpaceHash           string `gorm:"size:80;not null;index"`
	ParameterSpace      JSONB  `gorm:"type:jsonb;not null"`
	CenterPointKey      string `gorm:"size:160;not null;default:''"`
	SourceGenomeID      *uint  `gorm:"index"`
	ComputeTaskID       *uint  `gorm:"index"`
	ExpectedPointCount  int    `gorm:"not null;default:0"`
	ActualPointCount    int    `gorm:"not null;default:0"`
	PredictedPointCount int    `gorm:"not null;default:0"`
	CompletedAt         *time.Time
	ArchivedAt          *time.Time

	Points    []RobustnessEvaluationPoint  `gorm:"foreignKey:StudyID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	Snapshots []RobustnessAnalysisSnapshot `gorm:"foreignKey:StudyID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
}

type RobustnessEvaluationPoint struct {
	ID                        uint `gorm:"primaryKey"`
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	StudyID                   uint   `gorm:"not null;index;uniqueIndex:idx_robustness_point_key"`
	PointKey                  string `gorm:"size:160;not null;uniqueIndex:idx_robustness_point_key"`
	Kind                      string `gorm:"size:24;not null;index"`
	State                     string `gorm:"size:24;not null;index"`
	CoordinateHash            string `gorm:"size:80;not null;index"`
	Coordinates               JSONB  `gorm:"type:jsonb;not null"`
	ParameterHash             string `gorm:"size:80;not null;index"`
	Parameters                JSONB  `gorm:"type:jsonb;not null"`
	BacktestResultID          *uint  `gorm:"index"`
	BacktestResultVersion     string `gorm:"size:40;not null;default:''"`
	BacktestResultContentHash string `gorm:"size:80;not null;default:'';index"`
	MetricsVersion            string `gorm:"size:40;not null;default:''"`
	MetricsHash               string `gorm:"size:80;not null;default:'';index"`
	Metrics                   JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	SourceStage               string `gorm:"size:80;not null;default:''"`
	SamplingBatch             string `gorm:"size:80;not null;default:''"`
	PredictionMetadata        JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`

	Study          RobustnessStudy `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	BacktestResult *BacktestResult `gorm:"foreignKey:BacktestResultID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
}

type RobustnessAnalysisSnapshot struct {
	ID                  uint `gorm:"primaryKey"`
	CreatedAt           time.Time
	StudyID             uint   `gorm:"not null;index;uniqueIndex:idx_robustness_analysis_key"`
	AnalysisKey         string `gorm:"size:96;not null;uniqueIndex:idx_robustness_analysis_key"`
	AnalysisVersion     string `gorm:"size:40;not null"`
	ConnectivityVersion string `gorm:"size:40;not null"`
	DistanceVersion     string `gorm:"size:40;not null"`
	FrontierVersion     string `gorm:"size:40;not null"`
	CenterVersion       string `gorm:"size:40;not null"`
	PointSetHash        string `gorm:"size:80;not null;index"`
	SettingsHash        string `gorm:"size:80;not null;index"`
	Metric              string `gorm:"size:48;not null"`
	Radii               JSONB  `gorm:"type:jsonb;not null"`
	Payload             JSONB  `gorm:"type:jsonb;not null"`
	ContentHash         string `gorm:"size:80;not null;index"`

	Study RobustnessStudy `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
}

type BacktestRun struct {
	ID               uint `gorm:"primaryKey"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
	UserID           uint   `gorm:"not null;index"`
	StrategyID       string `gorm:"size:80;not null;index"`
	InstanceID       *uint  `gorm:"index"`
	InstrumentID     string `gorm:"size:32;not null;index;default:BTCUSDT"`
	DataSource       string `gorm:"size:32;not null;index;default:binance"`
	ExecutionMode    string `gorm:"size:32;not null;index;default:close_same_bar"`
	StartTimeMs      int64  `gorm:"not null;default:0"`
	EndTimeMs        int64  `gorm:"not null;default:0"`
	Symbol           string `gorm:"size:32;not null;index"`
	Interval         string `gorm:"size:16;not null;index"`
	Source           string `gorm:"size:32;not null;index"`
	Status           string `gorm:"size:32;not null;index"`
	BacktestResultID *uint  `gorm:"index"`
	ReusedResult     bool   `gorm:"not null;default:false"`
	Request          JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	Result           JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ErrorMessage     string `gorm:"type:text"`
	StartedAt        *time.Time
	FinishedAt       *time.Time

	User           User              `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Instance       *StrategyInstance `gorm:"constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
	BacktestResult *BacktestResult   `gorm:"foreignKey:BacktestResultID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
}

type ResearchInstrument struct {
	ID                  string `gorm:"size:32;primaryKey"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Symbol              string `gorm:"size:64;not null;uniqueIndex"`
	DisplayName         string `gorm:"size:160;not null"`
	DataSource          string `gorm:"size:32;not null;index"`
	SupportedIntervals  JSONB  `gorm:"type:jsonb;not null;default:'[]'::jsonb"`
	AvailableStartMs    JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	Market              string `gorm:"size:32;not null;default:global;index"`
	SortOrder           int    `gorm:"not null;default:1000;index"`
	Enabled             bool   `gorm:"not null;default:true;index"`
	LastAutoUpdateAt    *time.Time
	LastAutoUpdateError string `gorm:"type:text"`
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

type KLineObservationMetadata struct {
	ID                uint `gorm:"primaryKey"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
	InstrumentID      string `gorm:"size:32;not null;index"`
	Source            string `gorm:"size:32;not null;index;uniqueIndex:idx_kline_observation_metadata_identity"`
	Symbol            string `gorm:"size:64;not null;index;uniqueIndex:idx_kline_observation_metadata_identity"`
	Interval          string `gorm:"size:16;not null;index;uniqueIndex:idx_kline_observation_metadata_identity"`
	OpenTime          int64  `gorm:"not null;index;uniqueIndex:idx_kline_observation_metadata_identity"`
	ObservationTimeMs int64  `gorm:"not null;default:0"`
	RealtimeStartMs   int64  `gorm:"not null;default:0"`
	RealtimeEndMs     int64  `gorm:"not null;default:0"`
	AvailableAtMs     int64  `gorm:"not null;default:0;index"`
	AvailabilityRule  string `gorm:"size:80;not null;default:fred_release_date_v2"`
}

func (KLineObservationMetadata) TableName() string {
	return "k_line_observation_metadata"
}

type DatasetMetadata struct {
	ID              uint `gorm:"primaryKey"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	InstrumentID    string `gorm:"size:32;not null;index;uniqueIndex:idx_dataset_metadata_identity"`
	DataSource      string `gorm:"size:32;not null;index;uniqueIndex:idx_dataset_metadata_identity"`
	Symbol          string `gorm:"size:64;not null;index;uniqueIndex:idx_dataset_metadata_identity"`
	Interval        string `gorm:"size:16;not null;index;uniqueIndex:idx_dataset_metadata_identity"`
	PriceAdjustment string `gorm:"size:64;not null;default:legacy_unknown"`
	ImportedStartMs int64  `gorm:"not null;default:0"`
	ImportedEndMs   int64  `gorm:"not null;default:0"`
	FullCoverage    bool   `gorm:"not null;default:false"`
}

func (DatasetMetadata) TableName() string {
	return "dataset_metadata"
}

// MarketSeries is the user-visible logical identity for immutable generated
// market-data versions. Display metadata can change; published version content
// never does.
type MarketSeries struct {
	ID          uint `gorm:"primaryKey"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	OwnerUserID uint       `gorm:"not null;index;uniqueIndex:idx_market_series_owner_name"`
	Name        string     `gorm:"size:160;not null;uniqueIndex:idx_market_series_owner_name"`
	Notes       string     `gorm:"type:text"`
	Tags        JSONB      `gorm:"type:jsonb;not null;default:'[]'::jsonb"`
	ArchivedAt  *time.Time `gorm:"index"`

	Owner User `gorm:"foreignKey:OwnerUserID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

// MarketDataVersion is the common immutable market-data artifact contract used
// by source snapshots, segment recompositions and future perturbation versions.
// SnapshotKey is populated only for deduplicated internal source snapshots.
type MarketDataVersion struct {
	ID                      uint `gorm:"primaryKey"`
	CreatedAt               time.Time
	UpdatedAt               time.Time
	OwnerUserID             uint    `gorm:"not null;index"`
	MarketSeriesID          *uint   `gorm:"index;uniqueIndex:idx_market_version_series_number"`
	VersionNumber           int     `gorm:"not null;default:0;uniqueIndex:idx_market_version_series_number"`
	SnapshotKey             *string `gorm:"size:192;uniqueIndex"`
	SchemaVersion           string  `gorm:"size:48;not null;index"`
	BarSchemaVersion        string  `gorm:"size:48;not null"`
	ArtifactKind            string  `gorm:"size:48;not null;index"`
	GeneratorVersion        string  `gorm:"size:64;not null;index"`
	PrecisionVersion        string  `gorm:"size:48;not null"`
	Status                  string  `gorm:"size:24;not null;index"`
	IntegrityStatus         string  `gorm:"size:24;not null;index"`
	ContentHash             string  `gorm:"size:128;not null;index"`
	PlanHash                string  `gorm:"size:128;not null;index"`
	Plan                    JSONB   `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	InstrumentID            string  `gorm:"size:32;not null;index"`
	DataSource              string  `gorm:"size:32;not null;index"`
	Symbol                  string  `gorm:"size:64;not null;index"`
	Market                  string  `gorm:"size:32;not null;index"`
	Timezone                string  `gorm:"size:64;not null"`
	Interval                string  `gorm:"size:16;not null;index"`
	CalendarID              string  `gorm:"size:96;not null"`
	CalendarVersion         string  `gorm:"size:48;not null"`
	CalendarHash            string  `gorm:"size:128;not null"`
	BarCount                int     `gorm:"not null;default:0"`
	StartTimeMs             int64   `gorm:"not null;default:0;index"`
	EndTimeMs               int64   `gorm:"not null;default:0;index"`
	PreviousClosePresent    bool    `gorm:"not null;default:false"`
	PreviousClose           float64 `gorm:"type:numeric(30,10);not null;default:0"`
	HasPerturbationAncestor bool    `gorm:"not null;default:false;index"`
	InternalOnly            bool    `gorm:"not null;default:false;index"`
	Published               bool    `gorm:"not null;default:false;index"`
	OutputInstrumentID      *string `gorm:"size:32;uniqueIndex"`
	ComputeTaskID           *uint   `gorm:"index"`
	CompletedAt             *time.Time
	ArchivedAt              *time.Time `gorm:"index"`
	ErrorCode               string     `gorm:"size:64"`
	ErrorMessage            string     `gorm:"type:text"`

	Owner        User          `gorm:"foreignKey:OwnerUserID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	MarketSeries *MarketSeries `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	ComputeTask  *ComputeTask  `gorm:"foreignKey:ComputeTaskID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
}

type MarketDataVersionBar struct {
	ID        uint `gorm:"primaryKey"`
	CreatedAt time.Time
	VersionID uint    `gorm:"not null;index;uniqueIndex:idx_market_version_bar_ordinal;uniqueIndex:idx_market_version_bar_time"`
	Ordinal   int     `gorm:"not null;uniqueIndex:idx_market_version_bar_ordinal"`
	OpenTime  int64   `gorm:"not null;index;uniqueIndex:idx_market_version_bar_time"`
	Open      float64 `gorm:"type:numeric(30,10);not null"`
	High      float64 `gorm:"type:numeric(30,10);not null"`
	Low       float64 `gorm:"type:numeric(30,10);not null"`
	Close     float64 `gorm:"type:numeric(30,10);not null"`
	Volume    float64 `gorm:"type:numeric(30,10);not null"`

	Version MarketDataVersion `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

type MarketDataVersionSource struct {
	ID              uint `gorm:"primaryKey"`
	CreatedAt       time.Time
	VersionID       uint   `gorm:"not null;index;uniqueIndex:idx_market_version_source"`
	SourceVersionID uint   `gorm:"not null;index;uniqueIndex:idx_market_version_source"`
	SourceOrder     int    `gorm:"not null;default:0;uniqueIndex:idx_market_version_source"`
	SourceRole      string `gorm:"size:32;not null;default:segment"`
	SourceHash      string `gorm:"size:128;not null"`

	Version       MarketDataVersion `gorm:"foreignKey:VersionID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	SourceVersion MarketDataVersion `gorm:"foreignKey:SourceVersionID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

type RecompositionPlan struct {
	ID                 uint `gorm:"primaryKey"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
	OwnerUserID        uint   `gorm:"not null;index;uniqueIndex:idx_recomposition_plan_owner_hash"`
	PlanHash           string `gorm:"size:128;not null;uniqueIndex:idx_recomposition_plan_owner_hash"`
	SchemaVersion      string `gorm:"size:48;not null"`
	AlgorithmVersion   string `gorm:"size:64;not null"`
	PrecisionVersion   string `gorm:"size:48;not null"`
	Status             string `gorm:"size:24;not null;index"`
	Interval           string `gorm:"size:16;not null;index"`
	TargetMarket       string `gorm:"size:32;not null"`
	TargetTimezone     string `gorm:"size:64;not null"`
	CalendarVersionID  uint   `gorm:"not null;index"`
	CalendarVersion    string `gorm:"size:48;not null"`
	CalendarHash       string `gorm:"size:128;not null"`
	OutputStartTimeMs  int64  `gorm:"not null;index"`
	OutputEndTimeMs    int64  `gorm:"not null;default:0"`
	SegmentCount       int    `gorm:"not null"`
	InstanceCount      int    `gorm:"not null"`
	TotalOutputBars    int    `gorm:"not null"`
	EstimatedReadBars  int    `gorm:"not null"`
	EstimatedWriteBars int    `gorm:"not null"`
	EstimatedBytes     int64  `gorm:"not null"`
	AnchorWarningCount int    `gorm:"not null;default:0"`
	ContentHash        string `gorm:"size:128;not null"`
	CanonicalPlan      JSONB  `gorm:"type:jsonb;not null"`
	Instances          JSONB  `gorm:"type:jsonb;not null;default:'[]'::jsonb"`
	PreviewTaskID      *uint  `gorm:"index"`
	CompletedAt        *time.Time
	ErrorCode          string `gorm:"size:64"`
	ErrorMessage       string `gorm:"type:text"`

	Owner          User              `gorm:"foreignKey:OwnerUserID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	CalendarSource MarketDataVersion `gorm:"foreignKey:CalendarVersionID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	PreviewTask    *ComputeTask      `gorm:"foreignKey:PreviewTaskID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
}

type RecompositionPlanSegment struct {
	ID                   uint `gorm:"primaryKey"`
	CreatedAt            time.Time
	PlanID               uint     `gorm:"not null;index;uniqueIndex:idx_recomposition_plan_segment"`
	ItemID               string   `gorm:"size:80;not null;uniqueIndex:idx_recomposition_plan_segment"`
	SegmentOrder         int      `gorm:"not null;uniqueIndex:idx_recomposition_plan_segment"`
	SourceVersionID      uint     `gorm:"not null;index"`
	SourceContentHash    string   `gorm:"size:128;not null"`
	SourceStartTimeMs    int64    `gorm:"not null"`
	SourceEndTimeMs      int64    `gorm:"not null"`
	BarCount             int      `gorm:"not null"`
	RepeatCount          int      `gorm:"not null"`
	PreviousClosePresent bool     `gorm:"not null"`
	PreviousClose        float64  `gorm:"type:numeric(30,10);not null;default:0"`
	FirstOpen            float64  `gorm:"type:numeric(30,10);not null"`
	SourceGapRatio       *float64 `gorm:"type:numeric(30,16)"`

	Plan          RecompositionPlan `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	SourceVersion MarketDataVersion `gorm:"foreignKey:SourceVersionID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

type RecompositionPreviewBar struct {
	ID                uint `gorm:"primaryKey"`
	CreatedAt         time.Time
	PlanID            uint    `gorm:"not null;index;uniqueIndex:idx_recomposition_preview_bar"`
	Ordinal           int     `gorm:"not null;uniqueIndex:idx_recomposition_preview_bar"`
	OpenTime          int64   `gorm:"not null;index"`
	Open              float64 `gorm:"type:numeric(30,10);not null"`
	High              float64 `gorm:"type:numeric(30,10);not null"`
	Low               float64 `gorm:"type:numeric(30,10);not null"`
	Close             float64 `gorm:"type:numeric(30,10);not null"`
	Volume            float64 `gorm:"type:numeric(30,10);not null"`
	SegmentInstanceID string  `gorm:"size:96;not null;index"`
	SourceVersionID   uint    `gorm:"not null;index"`
	SourceOrdinal     int     `gorm:"not null"`
	SourceOpenTime    int64   `gorm:"not null"`

	Plan RecompositionPlan `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

type RecompositionGeneration struct {
	ID                uint `gorm:"primaryKey"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
	OwnerUserID       uint   `gorm:"not null;index;uniqueIndex:idx_recomposition_generation_idempotency"`
	IdempotencyKey    string `gorm:"size:128;not null;uniqueIndex:idx_recomposition_generation_idempotency"`
	PlanID            uint   `gorm:"not null;index"`
	PlanHash          string `gorm:"size:128;not null"`
	MarketSeriesID    uint   `gorm:"not null;index;uniqueIndex:idx_recomposition_generation_series_version"`
	VersionNumber     int    `gorm:"not null;uniqueIndex:idx_recomposition_generation_series_version"`
	OutputVersionID   uint   `gorm:"not null;index;uniqueIndex"`
	ComputeTaskID     *uint  `gorm:"index"`
	Status            string `gorm:"size:24;not null;index"`
	ExpandedAt        *time.Time
	CalendarCheckedAt *time.Time
	PublishedAt       *time.Time
	ErrorCode         string `gorm:"size:64"`
	ErrorMessage      string `gorm:"type:text"`

	Owner         User              `gorm:"foreignKey:OwnerUserID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	Plan          RecompositionPlan `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	MarketSeries  MarketSeries      `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	OutputVersion MarketDataVersion `gorm:"foreignKey:OutputVersionID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	ComputeTask   *ComputeTask      `gorm:"foreignKey:ComputeTaskID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
}

type RecompositionSegmentInstance struct {
	ID                 uint `gorm:"primaryKey"`
	CreatedAt          time.Time
	VersionID          uint     `gorm:"not null;index;uniqueIndex:idx_recomposition_instance"`
	InstanceKey        string   `gorm:"size:96;not null;uniqueIndex:idx_recomposition_instance"`
	SegmentItemID      string   `gorm:"size:80;not null;index"`
	InstanceOrder      int      `gorm:"not null;index"`
	RepeatOrdinal      int      `gorm:"not null"`
	SourceVersionID    uint     `gorm:"not null;index"`
	SourceContentHash  string   `gorm:"size:128;not null"`
	SourceStartTimeMs  int64    `gorm:"not null"`
	SourceEndTimeMs    int64    `gorm:"not null"`
	OutputStartOrdinal int      `gorm:"not null"`
	OutputEndOrdinal   int      `gorm:"not null"`
	OutputStartTimeMs  int64    `gorm:"not null"`
	OutputEndTimeMs    int64    `gorm:"not null"`
	ScaleMultiplier    float64  `gorm:"type:numeric(30,16);not null"`
	SourceGapRatio     *float64 `gorm:"type:numeric(30,16)"`
	ActualGapRatio     float64  `gorm:"type:numeric(30,16);not null"`
	AnchorMissing      bool     `gorm:"not null"`
	AnchorValue        float64  `gorm:"type:numeric(30,10);not null"`

	Version       MarketDataVersion `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	SourceVersion MarketDataVersion `gorm:"foreignKey:SourceVersionID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

type RecompositionBarLineage struct {
	ID                 uint `gorm:"primaryKey"`
	CreatedAt          time.Time
	VersionID          uint   `gorm:"not null;index;uniqueIndex:idx_recomposition_bar_lineage"`
	OutputOrdinal      int    `gorm:"not null;uniqueIndex:idx_recomposition_bar_lineage"`
	OutputOpenTime     int64  `gorm:"not null;index"`
	SegmentInstanceKey string `gorm:"size:96;not null;index"`
	SourceVersionID    uint   `gorm:"not null;index"`
	SourceContentHash  string `gorm:"size:128;not null"`
	SourceOrdinal      int    `gorm:"not null"`
	SourceOpenTime     int64  `gorm:"not null"`

	Version       MarketDataVersion `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	SourceVersion MarketDataVersion `gorm:"foreignKey:SourceVersionID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

type ResearchDataset struct {
	ID                           uint `gorm:"primaryKey"`
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
	Name                         string `gorm:"size:160;not null"`
	Notes                        string `gorm:"type:text"`
	PrimaryInstrumentID          string `gorm:"size:32;not null;index"`
	PrimaryDataSource            string `gorm:"size:32;not null;index"`
	PrimarySymbol                string `gorm:"size:64;not null;index"`
	PrimaryInterval              string `gorm:"size:16;not null;index"`
	PrimaryMarketDataVersionID   *uint  `gorm:"index"`
	PrimaryMarketDataContentHash string `gorm:"size:128;not null;default:''"`
	StartTimeMs                  int64  `gorm:"not null;default:0;index"`
	EndTimeMs                    int64  `gorm:"not null;default:0;index"`
	MissingPolicy                string `gorm:"size:32;not null;default:forward_fill"`
	IndicatorAlgorithm           string `gorm:"size:80;not null;default:''"`
	Config                       JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
}

type ResearchDatasetSeries struct {
	ID                    uint `gorm:"primaryKey"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
	DatasetID             uint   `gorm:"not null;index;uniqueIndex:idx_research_dataset_series_unique"`
	Role                  string `gorm:"size:24;not null;index;uniqueIndex:idx_research_dataset_series_unique"`
	SortOrder             int    `gorm:"not null;default:0;uniqueIndex:idx_research_dataset_series_unique"`
	InstrumentID          string `gorm:"size:32;not null;index"`
	DataSource            string `gorm:"size:32;not null;index"`
	Symbol                string `gorm:"size:64;not null;index"`
	Interval              string `gorm:"size:16;not null;index"`
	MarketDataVersionID   *uint  `gorm:"index"`
	MarketDataContentHash string `gorm:"size:128;not null;default:''"`

	Dataset ResearchDataset `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
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

// DynamicModelStudy owns one immutable P09 training configuration and its
// explicit P05 computation lifecycle. Model artifacts and reports are append-only.
type DynamicModelStudy struct {
	ID                    uint `gorm:"primaryKey"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
	OwnerUserID           uint   `gorm:"not null;index;uniqueIndex:idx_dynamic_study_owner_key"`
	StudyKey              string `gorm:"size:128;not null;uniqueIndex:idx_dynamic_study_owner_key"`
	Name                  string `gorm:"size:180;not null"`
	Status                string `gorm:"size:32;not null;index"`
	Route                 string `gorm:"size:32;not null;index"`
	InstrumentID          string `gorm:"size:32;not null;index"`
	DataSource            string `gorm:"size:32;not null;index"`
	Symbol                string `gorm:"size:64;not null"`
	Interval              string `gorm:"size:16;not null;index"`
	ExecutionMode         string `gorm:"size:32;not null;index"`
	TrainStartTimeMs      int64  `gorm:"not null;index"`
	TrainEndTimeMs        int64  `gorm:"not null;index"`
	DatasetHash           string `gorm:"size:128;not null;index"`
	SettingVersion        string `gorm:"size:48;not null"`
	SettingHash           string `gorm:"size:128;not null;index"`
	Settings              JSONB  `gorm:"type:jsonb;not null"`
	ComputeTaskID         *uint  `gorm:"index"`
	MaterializationTaskID *uint  `gorm:"index"`
	ArtifactSetHash       string `gorm:"size:128;not null;default:''"`
	PredictionSnapshotID  *uint  `gorm:"index"`
	PolicyArtifactID      *uint  `gorm:"index"`
	MaterializationID     *uint  `gorm:"index"`
	ReportSnapshotID      *uint  `gorm:"index"`
	ErrorCode             string `gorm:"size:64"`
	ErrorMessage          string `gorm:"type:text"`
	CompletedAt           *time.Time
	ArchivedAt            *time.Time

	Owner               User         `gorm:"foreignKey:OwnerUserID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	ComputeTask         *ComputeTask `gorm:"foreignKey:ComputeTaskID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
	MaterializationTask *ComputeTask `gorm:"foreignKey:MaterializationTaskID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
}

type DynamicModelArtifact struct {
	ID                  uint `gorm:"primaryKey"`
	CreatedAt           time.Time
	StudyID             uint   `gorm:"not null;index;uniqueIndex:idx_dynamic_artifact_identity"`
	ArtifactKey         string `gorm:"size:128;not null;uniqueIndex:idx_dynamic_artifact_identity"`
	SchemaVersion       string `gorm:"size:48;not null"`
	Route               string `gorm:"size:32;not null;index"`
	Horizon             int    `gorm:"not null;index"`
	TargetKind          string `gorm:"size:32;not null;index"`
	Lookback            int    `gorm:"not null"`
	DatasetHash         string `gorm:"size:128;not null;index"`
	TrainingStartTimeMs int64  `gorm:"not null"`
	TrainingEndTimeMs   int64  `gorm:"not null"`
	ContentHash         string `gorm:"size:128;not null;index"`
	Payload             JSONB  `gorm:"type:jsonb;not null"`
	ArchivedAt          *time.Time

	Study DynamicModelStudy `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

type DynamicPredictionSnapshot struct {
	ID                uint `gorm:"primaryKey"`
	CreatedAt         time.Time
	StudyID           uint   `gorm:"not null;index;uniqueIndex:idx_dynamic_prediction_identity"`
	SnapshotKey       string `gorm:"size:128;not null;uniqueIndex:idx_dynamic_prediction_identity"`
	SchemaVersion     string `gorm:"size:48;not null"`
	ArtifactSetHash   string `gorm:"size:128;not null;index"`
	DatasetHash       string `gorm:"size:128;not null;index"`
	PredictionCount   int    `gorm:"not null"`
	StartTimeMs       int64  `gorm:"not null;index"`
	EndTimeMs         int64  `gorm:"not null;index"`
	BlockManifestHash string `gorm:"size:128;not null"`
	BlockManifest     JSONB  `gorm:"type:jsonb;not null"`
	ContentHash       string `gorm:"size:128;not null;index"`
	ArchivedAt        *time.Time

	Study DynamicModelStudy `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

type DynamicPolicyArtifact struct {
	ID                    uint `gorm:"primaryKey"`
	CreatedAt             time.Time
	OwnerUserID           uint   `gorm:"not null;index;uniqueIndex:idx_dynamic_policy_owner_key"`
	StudyID               uint   `gorm:"not null;index"`
	PolicyKey             string `gorm:"size:128;not null;uniqueIndex:idx_dynamic_policy_owner_key"`
	SchemaVersion         string `gorm:"size:48;not null"`
	ArtifactSetHash       string `gorm:"size:128;not null;index"`
	PredictionSnapshotID  uint   `gorm:"not null;index"`
	ContentHash           string `gorm:"size:128;not null;index"`
	Payload               JSONB  `gorm:"type:jsonb;not null"`
	ParameterSpaceVersion string `gorm:"size:48;not null"`
	ParameterSpaceHash    string `gorm:"size:128;not null;index"`
	ParameterSpace        JSONB  `gorm:"type:jsonb;not null"`
	ArchivedAt            *time.Time

	Owner              User                      `gorm:"foreignKey:OwnerUserID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	Study              DynamicModelStudy         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	PredictionSnapshot DynamicPredictionSnapshot `gorm:"foreignKey:PredictionSnapshotID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

type DynamicMaterialization struct {
	ID                        uint `gorm:"primaryKey"`
	CreatedAt                 time.Time
	OwnerUserID               uint   `gorm:"not null;index;uniqueIndex:idx_dynamic_materialization_owner_key"`
	MaterializationKey        string `gorm:"size:128;not null;uniqueIndex:idx_dynamic_materialization_owner_key"`
	SchemaVersion             string `gorm:"size:48;not null"`
	StudyID                   uint   `gorm:"not null;index"`
	PredictionSnapshotID      uint   `gorm:"not null;index"`
	PolicyArtifactID          uint   `gorm:"not null;index"`
	ContentHash               string `gorm:"size:128;not null;index"`
	BlockManifestHash         string `gorm:"size:128;not null"`
	BlockManifest             JSONB  `gorm:"type:jsonb;not null"`
	BacktestResultID          *uint  `gorm:"index"`
	BacktestResultVersion     string `gorm:"size:48"`
	BacktestResultContentHash string `gorm:"size:128"`
	ArchivedAt                *time.Time

	Owner              User                      `gorm:"foreignKey:OwnerUserID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	Study              DynamicModelStudy         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	PredictionSnapshot DynamicPredictionSnapshot `gorm:"foreignKey:PredictionSnapshotID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	PolicyArtifact     DynamicPolicyArtifact     `gorm:"foreignKey:PolicyArtifactID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	BacktestResult     *BacktestResult           `gorm:"foreignKey:BacktestResultID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

type DynamicModelReportSnapshot struct {
	ID                   uint `gorm:"primaryKey"`
	CreatedAt            time.Time
	StudyID              uint   `gorm:"not null;index;uniqueIndex:idx_dynamic_report_identity"`
	SnapshotKey          string `gorm:"size:128;not null;uniqueIndex:idx_dynamic_report_identity"`
	SchemaVersion        string `gorm:"size:48;not null"`
	FormulaVersion       string `gorm:"size:48;not null"`
	ArtifactSetHash      string `gorm:"size:128;not null;index"`
	PredictionSnapshotID uint   `gorm:"not null;index"`
	PolicyArtifactID     *uint  `gorm:"index"`
	MaterializationID    *uint  `gorm:"index"`
	ActualStartTimeMs    int64  `gorm:"not null;index"`
	ActualEndTimeMs      int64  `gorm:"not null;index"`
	Completeness         string `gorm:"size:32;not null;index"`
	BlockManifestHash    string `gorm:"size:128;not null"`
	BlockManifest        JSONB  `gorm:"type:jsonb;not null"`
	ContentHash          string `gorm:"size:128;not null;index"`
	ArchivedAt           *time.Time

	Study              DynamicModelStudy         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
	PredictionSnapshot DynamicPredictionSnapshot `gorm:"foreignKey:PredictionSnapshotID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

// DynamicReportBlock stores immutable, lazy-loadable prediction, calibration,
// state, effective-parameter and report payloads for P09 and the future N adapter.
type DynamicReportBlock struct {
	ID             uint `gorm:"primaryKey"`
	CreatedAt      time.Time
	StudyID        uint   `gorm:"not null;index"`
	OwnerKind      string `gorm:"size:32;not null;index;uniqueIndex:idx_dynamic_report_block"`
	OwnerID        uint   `gorm:"not null;index;uniqueIndex:idx_dynamic_report_block"`
	BlockID        string `gorm:"size:96;not null;uniqueIndex:idx_dynamic_report_block"`
	BlockKind      string `gorm:"size:48;not null;index"`
	SchemaVersion  string `gorm:"size:48;not null"`
	FormulaVersion string `gorm:"size:48;not null"`
	BlockIndex     int    `gorm:"not null"`
	StartTimeMs    int64  `gorm:"not null;index"`
	EndTimeMs      int64  `gorm:"not null;index"`
	PointCount     int    `gorm:"not null"`
	ContentHash    string `gorm:"size:128;not null;index"`
	Payload        JSONB  `gorm:"type:jsonb;not null"`

	Study DynamicModelStudy `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;"`
}

// ResearchConfiguration is the immutable P10 research identity. Human-editable
// metadata is deliberately kept in ResearchConfigurationMetadata.
type ResearchConfiguration struct {
	ID                    uint `gorm:"primaryKey"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
	OwnerUserID           uint   `gorm:"not null;index;uniqueIndex:idx_research_configuration_owner_hash"`
	ConfigHash            string `gorm:"size:128;not null;uniqueIndex:idx_research_configuration_owner_hash"`
	SchemaVersion         string `gorm:"size:48;not null"`
	StrategyID            string `gorm:"size:80;not null;index"`
	InstrumentID          string `gorm:"size:64;not null;index"`
	DataSource            string `gorm:"size:64;not null;index"`
	Symbol                string `gorm:"size:64;not null"`
	Interval              string `gorm:"size:16;not null;index"`
	DatasetHash           string `gorm:"size:128;not null;index"`
	StartTimeMs           int64  `gorm:"not null;index"`
	EndTimeMs             int64  `gorm:"not null;index"`
	ExecutionMode         string `gorm:"size:32;not null;index"`
	ParameterSpaceVersion string `gorm:"size:48;not null"`
	ParameterSpaceHash    string `gorm:"size:128;not null;index"`
	ParameterSpace        JSONB  `gorm:"type:jsonb;not null"`
	DynamicMode           bool   `gorm:"not null;default:false;index"`
	DynamicStudyID        *uint  `gorm:"index"`
	DynamicPolicyID       *uint  `gorm:"index"`
	Canonical             JSONB  `gorm:"type:jsonb;not null"`
	ArchivedAt            *time.Time

	Owner    User                           `gorm:"foreignKey:OwnerUserID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	Metadata *ResearchConfigurationMetadata `gorm:"foreignKey:ConfigurationID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
}

type ResearchConfigurationMetadata struct {
	ID              uint `gorm:"primaryKey"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ConfigurationID uint   `gorm:"not null;uniqueIndex"`
	Name            string `gorm:"size:180;not null"`
	Notes           string `gorm:"type:text"`
	Tags            JSONB  `gorm:"type:jsonb;not null;default:'[]'::jsonb"`
	ArchivedAt      *time.Time

	Configuration ResearchConfiguration `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
}

type ResearchRun struct {
	ID                     uint `gorm:"primaryKey"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
	OwnerUserID            uint   `gorm:"not null;index"`
	ConfigurationID        uint   `gorm:"not null;index"`
	RunKey                 string `gorm:"size:128;not null;uniqueIndex:idx_research_run_owner_key"`
	SamplerVersion         string `gorm:"size:48;not null"`
	RootSeed               int64  `gorm:"not null;default:0"`
	NextSobolIndex         int64  `gorm:"not null;default:0"`
	GlobalUniquePointCount int    `gorm:"not null;default:0"`
	GlobalBatchCount       int    `gorm:"not null;default:0"`
	ExplorationStatus      string `gorm:"size:40;not null;index"`
	Status                 string `gorm:"size:32;not null;index"`
	StartedAt              *time.Time
	CompletedAt            *time.Time
	PausedAt               *time.Time
	CancelledAt            *time.Time

	Configuration ResearchConfiguration `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
}

type ResearchStage struct {
	ID              uint `gorm:"primaryKey"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	RunID           uint   `gorm:"not null;index;uniqueIndex:idx_research_stage_run_ordinal"`
	Ordinal         int    `gorm:"not null;uniqueIndex:idx_research_stage_run_ordinal"`
	StageKey        string `gorm:"size:128;not null"`
	StageType       string `gorm:"size:48;not null;index"`
	ManifestHash    string `gorm:"size:128;not null;index"`
	Manifest        JSONB  `gorm:"type:jsonb;not null"`
	ComputeTaskID   *uint  `gorm:"index"`
	Status          string `gorm:"size:32;not null;index"`
	RequestedCount  int    `gorm:"not null;default:0"`
	UniqueCount     int    `gorm:"not null;default:0"`
	CacheHitCount   int    `gorm:"not null;default:0"`
	CompletedCount  int    `gorm:"not null;default:0"`
	FailedCount     int    `gorm:"not null;default:0"`
	MissingCount    int    `gorm:"not null;default:0"`
	SobolStartIndex int64  `gorm:"not null;default:0"`
	SobolEndIndex   int64  `gorm:"not null;default:0"`
	ErrorMessage    string `gorm:"type:text"`
	CompletedAt     *time.Time

	Run         ResearchRun  `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	ComputeTask *ComputeTask `gorm:"foreignKey:ComputeTaskID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;" json:"-"`
}

type ResearchEvaluationPoint struct {
	ID                        uint `gorm:"primaryKey"`
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	ConfigurationID           uint   `gorm:"not null;index;uniqueIndex:idx_research_point_configuration_vector"`
	VectorHash                string `gorm:"size:128;not null;uniqueIndex:idx_research_point_configuration_vector"`
	CoordinateKey             string `gorm:"size:512;not null"`
	Coordinates               JSONB  `gorm:"type:jsonb;not null"`
	Parameters                JSONB  `gorm:"type:jsonb;not null"`
	Legality                  string `gorm:"size:24;not null;index"`
	Status                    string `gorm:"size:32;not null;index"`
	BacktestResultID          *uint  `gorm:"index"`
	BacktestResultVersion     string `gorm:"size:48"`
	BacktestResultContentHash string `gorm:"size:128;index"`
	MetricsVersion            string `gorm:"size:48"`
	MetricsHash               string `gorm:"size:128;index"`
	Metrics                   JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	Qualified                 bool   `gorm:"not null;default:false;index"`

	Configuration  ResearchConfiguration `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	BacktestResult *BacktestResult       `gorm:"foreignKey:BacktestResultID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
}

type ResearchPointOrigin struct {
	ID         uint `gorm:"primaryKey"`
	CreatedAt  time.Time
	PointID    uint   `gorm:"not null;index;uniqueIndex:idx_research_point_origin"`
	RunID      uint   `gorm:"not null;index;uniqueIndex:idx_research_point_origin"`
	StageID    uint   `gorm:"not null;index;uniqueIndex:idx_research_point_origin"`
	OriginKey  string `gorm:"size:160;not null;uniqueIndex:idx_research_point_origin"`
	OriginType string `gorm:"size:48;not null;index"`
	SobolIndex *int64 `gorm:"index"`
	Reason     JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
}

type ResearchAnalysisSnapshot struct {
	ID                   uint `gorm:"primaryKey"`
	CreatedAt            time.Time
	ConfigurationID      uint   `gorm:"not null;index"`
	SnapshotKey          string `gorm:"size:128;not null;uniqueIndex"`
	SchemaVersion        string `gorm:"size:48;not null"`
	PointSetHash         string `gorm:"size:128;not null;index"`
	MetricsVersion       string `gorm:"size:48;not null"`
	JAnalysisVersion     string `gorm:"size:48;not null"`
	RobustnessStudyID    uint   `gorm:"not null;index"`
	RobustnessSnapshotID uint   `gorm:"not null;index"`
	Completeness         string `gorm:"size:32;not null;index"`
	ContentHash          string `gorm:"size:128;not null;index"`
	Summary              JSONB  `gorm:"type:jsonb;not null"`
}

type RobustRegion struct {
	ID                 uint `gorm:"primaryKey"`
	CreatedAt          time.Time
	AnalysisSnapshotID uint   `gorm:"not null;index;uniqueIndex:idx_research_region_component"`
	ComponentID        string `gorm:"size:128;not null;uniqueIndex:idx_research_region_component"`
	Completeness       string `gorm:"size:32;not null;index"`
	Boundary           JSONB  `gorm:"type:jsonb;not null"`
	Lineage            JSONB  `gorm:"type:jsonb;not null;default:'[]'::jsonb"`
}

type RobustRegionPoint struct {
	ID        uint `gorm:"primaryKey"`
	CreatedAt time.Time
	RegionID  uint `gorm:"not null;index;uniqueIndex:idx_research_region_point"`
	PointID   uint `gorm:"not null;index;uniqueIndex:idx_research_region_point"`
}

type RobustCandidate struct {
	ID                 uint `gorm:"primaryKey"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
	OwnerUserID        uint   `gorm:"not null;index"`
	ConfigurationID    uint   `gorm:"not null;index"`
	PointID            uint   `gorm:"not null;index"`
	AnalysisSnapshotID *uint  `gorm:"index;uniqueIndex:idx_research_candidate_snapshot_point"`
	RegionID           *uint  `gorm:"index"`
	CandidateKey       string `gorm:"size:128;not null;uniqueIndex"`
	Version            string `gorm:"size:48;not null"`
	SourceKind         string `gorm:"size:32;not null;index"`
	Completeness       string `gorm:"size:32;not null;index"`
	Roles              JSONB  `gorm:"type:jsonb;not null"`
	AdoptionUnitHash   string `gorm:"size:128;not null;index"`
	AdoptionUnit       JSONB  `gorm:"type:jsonb;not null"`
	Name               string `gorm:"size:180"`
	Notes              string `gorm:"type:text"`
	Tags               JSONB  `gorm:"type:jsonb;not null;default:'[]'::jsonb"`
	Lineage            JSONB  `gorm:"type:jsonb;not null;default:'[]'::jsonb"`
	ArchivedAt         *time.Time
}

type CandidateAnalysisLink struct {
	ID                uint `gorm:"primaryKey"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
	CandidateID       uint   `gorm:"not null;index;uniqueIndex:idx_candidate_analysis_kind_version"`
	AnalysisKind      string `gorm:"size:16;not null;index;uniqueIndex:idx_candidate_analysis_kind_version"`
	Version           string `gorm:"size:48;not null;uniqueIndex:idx_candidate_analysis_kind_version"`
	Status            string `gorm:"size:32;not null;index"`
	TaskID            *uint  `gorm:"index"`
	SourceID          string `gorm:"size:128"`
	SourceVersion     string `gorm:"size:48"`
	SourceContentHash string `gorm:"size:128"`
	PartialSnapshot   JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	ErrorMessage      string `gorm:"type:text"`
}

type CandidateGeneLink struct {
	ID               uint `gorm:"primaryKey"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CandidateID      uint   `gorm:"not null;uniqueIndex"`
	GeneRecordID     uint   `gorm:"not null;index"`
	CandidateVersion string `gorm:"size:48;not null"`
	ImportedAt       time.Time
	LastPromotedAt   *time.Time
	PromotionAudit   JSONB `gorm:"type:jsonb;not null;default:'[]'::jsonb"`
}

type ResearchSeries struct {
	ID                   uint `gorm:"primaryKey"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
	OwnerUserID          uint   `gorm:"not null;index"`
	SeriesKey            string `gorm:"size:128;not null;uniqueIndex:idx_research_series_owner_key"`
	Name                 string `gorm:"size:180;not null"`
	SchemaVersion        string `gorm:"size:48;not null"`
	CommonBackgroundHash string `gorm:"size:128;not null;index"`
	CommonBackground     JSONB  `gorm:"type:jsonb;not null"`
	ChangedFactors       JSONB  `gorm:"type:jsonb;not null"`
	CommonSchemaHash     string `gorm:"size:128;not null;index"`
	CommonSchema         JSONB  `gorm:"type:jsonb;not null"`
	ArchivedAt           *time.Time
}

// TableName avoids colliding with the pre-existing reference-data
// research_series table, whose string primary key and semantics are unrelated.
func (ResearchSeries) TableName() string { return "parameter_research_series" }

type ResearchSeriesMember struct {
	ID              uint `gorm:"primaryKey"`
	CreatedAt       time.Time
	SeriesID        uint  `gorm:"not null;index;uniqueIndex:idx_research_series_member"`
	ConfigurationID uint  `gorm:"not null;index;uniqueIndex:idx_research_series_member"`
	DisplayOrder    int   `gorm:"not null"`
	FactorValues    JSONB `gorm:"type:jsonb;not null"`
}

func (ResearchSeriesMember) TableName() string { return "parameter_research_series_members" }

type ResearchComparisonSnapshot struct {
	ID                 uint `gorm:"primaryKey"`
	CreatedAt          time.Time
	SeriesID           uint   `gorm:"not null;index"`
	SnapshotKey        string `gorm:"size:128;not null;uniqueIndex"`
	SchemaVersion      string `gorm:"size:48;not null"`
	Eligibility        string `gorm:"size:40;not null;index"`
	EligibilityReasons JSONB  `gorm:"type:jsonb;not null"`
	MemberHashes       JSONB  `gorm:"type:jsonb;not null"`
	CommonManifestHash string `gorm:"size:128;not null;index"`
	CommonManifest     JSONB  `gorm:"type:jsonb;not null"`
	Missing            JSONB  `gorm:"type:jsonb;not null"`
	Differences        JSONB  `gorm:"type:jsonb;not null"`
	ContentHash        string `gorm:"size:128;not null;index"`
}

func (ResearchComparisonSnapshot) TableName() string {
	return "parameter_research_comparison_snapshots"
}

type SurrogateModelSnapshot struct {
	ID                   uint `gorm:"primaryKey"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
	OwnerUserID          uint   `gorm:"not null;index"`
	ConfigurationID      uint   `gorm:"not null;index"`
	RunID                uint   `gorm:"not null;index"`
	SnapshotKey          string `gorm:"size:128;not null;uniqueIndex"`
	SchemaVersion        string `gorm:"size:48;not null"`
	TrainingPointSetHash string `gorm:"size:128;not null;index"`
	BatchFoldHash        string `gorm:"size:128;not null"`
	ModelSettings        JSONB  `gorm:"type:jsonb;not null"`
	OOFMetrics           JSONB  `gorm:"type:jsonb;not null"`
	CanGuideReturn       bool   `gorm:"not null;default:false"`
	CanGuideDrawdown     bool   `gorm:"not null;default:false"`
	CanGuideConservative bool   `gorm:"not null;default:false"`
	ArtifactHash         string `gorm:"size:128;not null;index"`
	Artifact             JSONB  `gorm:"type:jsonb;not null"`
	ContentHash          string `gorm:"size:128;not null;index"`
	Status               string `gorm:"size:32;not null;index"`
	ComputeTaskID        *uint  `gorm:"index"`
}

type SurrogateProposal struct {
	ID                  uint `gorm:"primaryKey"`
	CreatedAt           time.Time
	SurrogateSnapshotID uint   `gorm:"not null;index;uniqueIndex:idx_surrogate_proposal_vector"`
	VectorHash          string `gorm:"size:128;not null;uniqueIndex:idx_surrogate_proposal_vector"`
	ProposalTypes       JSONB  `gorm:"type:jsonb;not null"`
	Coordinates         JSONB  `gorm:"type:jsonb;not null"`
	Parameters          JSONB  `gorm:"type:jsonb;not null"`
	Predictions         JSONB  `gorm:"type:jsonb;not null"`
	Uncertainty         JSONB  `gorm:"type:jsonb;not null"`
	CandidatePoolHash   string `gorm:"size:128;not null"`
	ActualPointID       *uint  `gorm:"index"`
	ActualError         JSONB  `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
}
