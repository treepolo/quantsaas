package sigmoiddca

const (
	StrategyID          = "sigmoid-dca-btc"
	StrategyName        = "動態均衡 BTC 策略"
	StrategyVersion     = "0.1.0"
	StrategyDescription = "以宏觀定投累積底倉，並用 Sigmoid 動態天平管理浮動倉。"
	IsSpot              = true
)

type Manifest struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Version             string `json:"version"`
	IsSpot              bool   `json:"is_spot"`
	Description         string `json:"description"`
	RequiredHistoryBars int    `json:"required_history_bars"`
	RequiresVolume      bool   `json:"requires_volume"`
}

func StrategyManifest() Manifest {
	return Manifest{
		ID:                  StrategyID,
		Name:                StrategyName,
		Version:             StrategyVersion,
		IsSpot:              IsSpot,
		Description:         StrategyDescription,
		RequiredHistoryBars: RequiredHistoryBars,
		RequiresVolume:      false,
	}
}
