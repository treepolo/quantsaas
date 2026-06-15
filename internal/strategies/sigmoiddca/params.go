package sigmoiddca

import (
	"encoding/json"

	"quantsaas/internal/quant"
)

type Params struct {
	Chromosome quant.Chromosome `json:"sigmoid_dca_config"`
	Spawn      quant.SpawnPoint `json:"spawn_point"`
}

func DefaultParams() Params {
	return Params{
		Chromosome: quant.DefaultSeedChromosome,
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
	return params
}
