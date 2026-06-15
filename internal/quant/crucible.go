package quant

import (
	"sort"
	"time"
)

type CrucibleWindow struct {
	Label       string
	Weight      float64
	Bars        []Bar
	EvalStartMs int64
}

type CrucibleResult struct {
	Window string
	Score  float64
	ROI    float64
	MaxDD  float64
	Alpha  float64
}

func BuildCrucibleWindows(bars []Bar, warmupDays int) []CrucibleWindow {
	if len(bars) == 0 {
		return nil
	}

	sorted := make([]Bar, len(bars))
	copy(sorted, bars)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].OpenTime < sorted[j].OpenTime
	})

	latest := sorted[len(sorted)-1].OpenTime
	warmupMs := int64(warmupDays) * int64(24*time.Hour/time.Millisecond)
	specs := []struct {
		label  string
		days   int
		weight float64
	}{
		{label: "6m", days: 183, weight: 0.10},
		{label: "2y", days: 730, weight: 0.20},
		{label: "5y", days: 1825, weight: 0.30},
	}

	windows := make([]CrucibleWindow, 0, 4)
	for _, spec := range specs {
		evalStart := latest - int64(spec.days)*int64(24*time.Hour/time.Millisecond)
		warmupStart := evalStart - warmupMs
		windowBars := sliceBarsFrom(sorted, warmupStart)
		actualEvalStart := firstBarAtOrAfter(windowBars, evalStart)
		if len(windowBars) == 0 || actualEvalStart == 0 {
			continue
		}
		windows = append(windows, CrucibleWindow{
			Label:       spec.label,
			Weight:      spec.weight,
			Bars:        windowBars,
			EvalStartMs: actualEvalStart,
		})
	}

	windows = append(windows, CrucibleWindow{
		Label:       "full",
		Weight:      0.40,
		Bars:        sorted,
		EvalStartMs: sorted[0].OpenTime,
	})

	sort.SliceStable(windows, func(i, j int) bool {
		return len(windows[i].Bars) < len(windows[j].Bars)
	})
	return windows
}

func sliceBarsFrom(bars []Bar, startMs int64) []Bar {
	idx := sort.Search(len(bars), func(i int) bool {
		return bars[i].OpenTime >= startMs
	})
	if idx >= len(bars) {
		return nil
	}
	out := make([]Bar, len(bars)-idx)
	copy(out, bars[idx:])
	return out
}

func firstBarAtOrAfter(bars []Bar, startMs int64) int64 {
	idx := sort.Search(len(bars), func(i int) bool {
		return bars[i].OpenTime >= startMs
	})
	if idx >= len(bars) {
		return 0
	}
	return bars[idx].OpenTime
}
