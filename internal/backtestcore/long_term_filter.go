package backtestcore

import (
	"math"
	"time"

	"quantsaas/internal/quant"
)

const (
	LongTermFilterSignalEnter = "enter"
	LongTermFilterSignalExit  = "exit"
)

type LongTermFilterObservation struct {
	Ready       bool
	RiskOff     bool
	CurrentSMA  float64
	PreviousSMA float64
	Signal      string
}

type LongTermFilter struct {
	config      LongTermFilterConfig
	monthCloses []float64
	riskOff     bool
	currentSMA  float64
	previousSMA float64
	ready       bool
}

func NewLongTermFilter(config LongTermFilterConfig) *LongTermFilter {
	return &LongTermFilter{config: config}
}

// Observe accepts one daily bar and only closes a month when a following bar
// proves that the series has moved into a new month. The final partial month is
// therefore never treated as complete.
func (f *LongTermFilter) Observe(index int, bars []quant.Bar) LongTermFilterObservation {
	observation := LongTermFilterObservation{
		Ready:       f.ready,
		RiskOff:     f.riskOff,
		CurrentSMA:  f.currentSMA,
		PreviousSMA: f.previousSMA,
	}
	if !f.config.Enabled || index < 0 || index >= len(bars)-1 {
		return observation
	}
	bar := bars[index]
	next := bars[index+1]
	if sameUTCMonth(bar.OpenTime, next.OpenTime) || bar.Close <= 0 || math.IsNaN(bar.Close) || math.IsInf(bar.Close, 0) {
		return observation
	}
	f.monthCloses = append(f.monthCloses, bar.Close)
	n := f.config.Months
	if n <= 0 || len(f.monthCloses) < n+1 {
		return observation
	}
	end := len(f.monthCloses)
	f.previousSMA = average(f.monthCloses[end-n-1 : end-1])
	f.currentSMA = average(f.monthCloses[end-n : end])
	f.ready = true
	signal := ""
	switch {
	case f.currentSMA < f.previousSMA && !f.riskOff:
		f.riskOff = true
		signal = LongTermFilterSignalEnter
	case f.currentSMA > f.previousSMA && f.riskOff:
		f.riskOff = false
		signal = LongTermFilterSignalExit
	}
	return LongTermFilterObservation{
		Ready:       true,
		RiskOff:     f.riskOff,
		CurrentSMA:  f.currentSMA,
		PreviousSMA: f.previousSMA,
		Signal:      signal,
	}
}

func sameUTCMonth(left int64, right int64) bool {
	a := time.UnixMilli(left).UTC()
	b := time.UnixMilli(right).UTC()
	return a.Year() == b.Year() && a.Month() == b.Month()
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}
