package quant

import "math"

func ActualExposureWeight(assetQuantity float64, price float64, totalEquity float64) float64 {
	if assetQuantity <= 0 || price <= 0 || totalEquity <= 0 {
		return 0
	}
	if math.IsNaN(assetQuantity) || math.IsInf(assetQuantity, 0) ||
		math.IsNaN(price) || math.IsInf(price, 0) ||
		math.IsNaN(totalEquity) || math.IsInf(totalEquity, 0) {
		return 0
	}
	return ClipFloat64(assetQuantity*price/totalEquity, 0, 1)
}
