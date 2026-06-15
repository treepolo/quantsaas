package quant

func ExtractCloses(bars []Bar) []float64 {
	closes := make([]float64, 0, len(bars))
	for _, bar := range bars {
		closes = append(closes, bar.Close)
	}
	return closes
}

func ExtractTimestamps(bars []Bar) []int64 {
	timestamps := make([]int64, 0, len(bars))
	for _, bar := range bars {
		timestamps = append(timestamps, bar.OpenTime)
	}
	return timestamps
}
