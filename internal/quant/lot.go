package quant

import "time"

type SpotLot struct {
	ID           uint
	LotType      string
	Amount       float64
	CostPrice    float64
	CreatedAt    time.Time
	IsColdSealed bool
}

func SumDeadBTC(lots []SpotLot) float64 {
	var total float64
	for _, lot := range lots {
		if lot.LotType == LotTypeDeadStack && !lot.IsColdSealed {
			total += lot.Amount
		}
	}
	return total
}

func SumFloatBTC(lots []SpotLot) float64 {
	var total float64
	for _, lot := range lots {
		if lot.LotType == LotTypeFloating {
			total += lot.Amount
		}
	}
	return total
}

func SumColdSealedBTC(lots []SpotLot) float64 {
	var total float64
	for _, lot := range lots {
		if lot.LotType == LotTypeColdSealed || lot.IsColdSealed {
			total += lot.Amount
		}
	}
	return total
}

func SoftRelease(lots []SpotLot, now time.Time, minAgeMonths int, maxReleasePct float64, sellableGapBTC float64) ([]SpotLot, float64) {
	if minAgeMonths < 0 {
		minAgeMonths = 0
	}
	cutoff := now.AddDate(0, -minAgeMonths, 0)
	maxRelease := SumDeadBTC(lots) * ClipFloat64(maxReleasePct, 0, 1)
	target := minPositive(maxRelease, sellableGapBTC)
	if target <= 0 {
		return cloneLots(lots), 0
	}

	return releaseFromDeadLots(lots, target, func(lot SpotLot) bool {
		return !lot.IsColdSealed &&
			lot.LotType == LotTypeDeadStack &&
			!lot.CreatedAt.After(cutoff)
	})
}

func HardRelease(lots []SpotLot, requiredBTC float64, maxReleasePct float64) ([]SpotLot, float64) {
	maxRelease := SumDeadBTC(lots) * ClipFloat64(maxReleasePct, 0, 1)
	target := minPositive(maxRelease, requiredBTC)
	if target <= 0 {
		return cloneLots(lots), 0
	}

	return releaseFromDeadLots(lots, target, func(lot SpotLot) bool {
		return !lot.IsColdSealed && lot.LotType == LotTypeDeadStack
	})
}

func releaseFromDeadLots(lots []SpotLot, target float64, eligible func(SpotLot) bool) ([]SpotLot, float64) {
	next := cloneLots(lots)
	var released float64
	var newFloating []SpotLot

	for i := range next {
		if released >= target {
			break
		}
		lot := next[i]
		if !eligible(lot) || lot.Amount <= 0 {
			continue
		}

		amount := lot.Amount
		remainingTarget := target - released
		if amount > remainingTarget {
			amount = remainingTarget
		}

		if amount >= lot.Amount {
			next[i].LotType = LotTypeFloating
			next[i].IsColdSealed = false
		} else {
			next[i].Amount -= amount
			newFloating = append(newFloating, SpotLot{
				LotType:      LotTypeFloating,
				Amount:       amount,
				CostPrice:    lot.CostPrice,
				CreatedAt:    lot.CreatedAt,
				IsColdSealed: false,
			})
		}
		released += amount
	}

	next = append(next, newFloating...)
	return next, released
}

func cloneLots(lots []SpotLot) []SpotLot {
	next := make([]SpotLot, len(lots))
	copy(next, lots)
	return next
}

func minPositive(a, b float64) float64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a < b {
		return a
	}
	return b
}
