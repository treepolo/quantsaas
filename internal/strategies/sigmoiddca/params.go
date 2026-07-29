package sigmoiddca

import (
	"encoding/json"
	"strings"

	"quantsaas/internal/quant"
)

const (
	PositionStructureDualLayer    = "dual_layer"
	PositionStructureFloatingOnly = "floating_only"
)

type Params struct {
	Chromosome          quant.Chromosome `json:"sigmoid_dca_config"`
	Spawn               quant.SpawnPoint `json:"spawn_point"`
	PositionStructure   string           `json:"position_structure,omitempty"`
	DisableMinimumTrade bool             `json:"-"`
}

func DefaultParams() Params {
	return Params{
		Chromosome:        quant.DefaultSeedChromosome,
		PositionStructure: PositionStructureDualLayer,
		Spawn: quant.SpawnPoint{
			Policy: quant.CapitalPolicy{
				MonthlyInjectUSDT: 100,
			},
			Risk: quant.RiskBounds{
				MaxDrawdownPct: 0.88,
				LotStep:        0.000001,
				LotMin:         0.00001,
			},
		},
	}
}

func ParseParamsFromParamPack(raw []byte) Params {
	params := DefaultParams()
	if len(raw) == 0 {
		return params
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return params
	}
	params.Chromosome = quant.ClampChromosome(params.Chromosome)
	params.PositionStructure = NormalizePositionStructure(params.PositionStructure)
	return params
}

func NormalizePositionStructure(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case PositionStructureFloatingOnly:
		return PositionStructureFloatingOnly
	default:
		return PositionStructureDualLayer
	}
}

func (p Params) FloatingOnly() bool {
	return NormalizePositionStructure(p.PositionStructure) == PositionStructureFloatingOnly
}
