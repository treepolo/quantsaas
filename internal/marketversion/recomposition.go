package marketversion

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"quantsaas/internal/compute"
)

var (
	ErrInvalidBar    = errors.New("行情包含不合法 K 線")
	ErrInvalidPlan   = errors.New("片段重組計畫不正確")
	ErrCalendarSlots = errors.New("交易日曆時間槽不足或不合法")
	ErrNumericResult = errors.New("片段重組產生不合法數值")
)

func NormalizePlan(plan GenerationPlan) (GenerationPlan, []byte, string, error) {
	if strings.TrimSpace(plan.SchemaVersion) == "" {
		plan.SchemaVersion = RecompositionPlanVersion
	}
	if strings.TrimSpace(plan.AlgorithmVersion) == "" {
		plan.AlgorithmVersion = RecompositionAlgorithm
	}
	if strings.TrimSpace(plan.PrecisionVersion) == "" {
		plan.PrecisionVersion = PricePrecisionVersion
	}
	if strings.TrimSpace(plan.CalendarVersion) == "" {
		plan.CalendarVersion = CalendarFromVersionVersion
	}
	if plan.SchemaVersion != RecompositionPlanVersion || plan.AlgorithmVersion != RecompositionAlgorithm ||
		plan.PrecisionVersion != PricePrecisionVersion || plan.CalendarVersion != CalendarFromVersionVersion {
		return GenerationPlan{}, nil, "", fmt.Errorf("%w: unsupported plan version", ErrInvalidPlan)
	}
	plan.Interval = strings.TrimSpace(plan.Interval)
	plan.TargetMarket = strings.TrimSpace(plan.TargetMarket)
	plan.TargetTimezone = strings.TrimSpace(plan.TargetTimezone)
	plan.CalendarHash = strings.TrimSpace(plan.CalendarHash)
	if plan.Interval == "" || plan.TargetMarket == "" || plan.TargetTimezone == "" || plan.CalendarHash == "" || plan.OutputStartTimeMs <= 0 {
		return GenerationPlan{}, nil, "", fmt.Errorf("%w: incomplete calendar identity", ErrInvalidPlan)
	}
	if len(plan.Segments) == 0 {
		return GenerationPlan{}, nil, "", fmt.Errorf("%w: at least one segment is required", ErrInvalidPlan)
	}
	sort.SliceStable(plan.Segments, func(i, j int) bool { return plan.Segments[i].Order < plan.Segments[j].Order })
	seen := make(map[string]bool, len(plan.Segments))
	total := 0
	for index := range plan.Segments {
		segment := &plan.Segments[index]
		segment.ItemID = strings.TrimSpace(segment.ItemID)
		if segment.ItemID == "" || seen[segment.ItemID] || segment.Order != index || segment.RepeatCount <= 0 || segment.BarCount <= 0 ||
			segment.StartTimeMs <= 0 || segment.EndTimeMs < segment.StartTimeMs || segment.Source.VersionID == 0 ||
			strings.TrimSpace(segment.Source.ContentHash) == "" || segment.Source.Interval != plan.Interval {
			return GenerationPlan{}, nil, "", fmt.Errorf("%w: segment %d is invalid", ErrInvalidPlan, index+1)
		}
		seen[segment.ItemID] = true
		if segment.BarCount > math.MaxInt/segment.RepeatCount || total > math.MaxInt-segment.BarCount*segment.RepeatCount {
			return GenerationPlan{}, nil, "", fmt.Errorf("%w: output bar count overflow", ErrInvalidPlan)
		}
		total += segment.BarCount * segment.RepeatCount
	}
	if plan.TotalOutputBars != 0 && plan.TotalOutputBars != total {
		return GenerationPlan{}, nil, "", fmt.Errorf("%w: output bar count mismatch", ErrInvalidPlan)
	}
	plan.TotalOutputBars = total
	canonicalPlan := plan
	canonicalPlan.Segments = append([]SegmentPlan(nil), plan.Segments...)
	for index := range canonicalPlan.Segments {
		canonicalPlan.Segments[index].Bars = nil
	}
	raw, err := compute.CanonicalJSON(canonicalPlan)
	if err != nil {
		return GenerationPlan{}, nil, "", err
	}
	return plan, raw, "market-plan:v1:" + compute.HashBytes(raw), nil
}

func Recompose(rawPlan GenerationPlan, outputSlots []int64) (RecompositionResult, error) {
	plan, _, _, err := NormalizePlan(rawPlan)
	if err != nil {
		return RecompositionResult{}, err
	}
	if err := validateSlots(outputSlots, plan.TotalOutputBars, plan.OutputStartTimeMs); err != nil {
		return RecompositionResult{}, err
	}
	result := RecompositionResult{
		Bars: make([]Bar, 0, plan.TotalOutputBars), Instances: make([]SegmentInstance, 0),
		Lineage: make([]BarLineage, 0, plan.TotalOutputBars),
	}
	outputOrdinal := 0
	for _, segment := range plan.Segments {
		if len(segment.Bars) != segment.BarCount {
			return RecompositionResult{}, fmt.Errorf("%w: segment %s bar count mismatch", ErrInvalidPlan, segment.ItemID)
		}
		if err := ValidateBars(segment.Bars, false); err != nil {
			return RecompositionResult{}, fmt.Errorf("%w: segment %s: %v", ErrInvalidPlan, segment.ItemID, err)
		}
		firstOpen := segment.Bars[0].Open
		for repeat := 0; repeat < segment.RepeatCount; repeat++ {
			anchorMissing := !segment.PreviousClosePresent
			anchorValue := segment.PreviousClose
			if anchorMissing {
				anchorValue = firstOpen
			}
			multiplier := 1.0
			if outputOrdinal > 0 {
				previousOutputClose := result.Bars[len(result.Bars)-1].Close
				if !finitePositive(anchorValue) {
					return RecompositionResult{}, fmt.Errorf("%w: segment %s has no valid anchor", ErrInvalidPlan, segment.ItemID)
				}
				multiplier = previousOutputClose / anchorValue
			}
			if !finitePositive(multiplier) {
				return RecompositionResult{}, ErrNumericResult
			}
			instanceID := fmt.Sprintf("%s:%d", segment.ItemID, repeat+1)
			instanceStart := outputOrdinal
			for _, source := range segment.Bars {
				bar := source
				bar.Ordinal = outputOrdinal
				bar.OpenTime = outputSlots[outputOrdinal]
				bar.Open = quantizePrice(source.Open * multiplier)
				bar.High = quantizePrice(source.High * multiplier)
				bar.Low = quantizePrice(source.Low * multiplier)
				bar.Close = quantizePrice(source.Close * multiplier)
				bar.Volume = quantizeVolume(source.Volume)
				if err := ValidateBar(bar); err != nil {
					return RecompositionResult{}, fmt.Errorf("%w: output %d: %v", ErrNumericResult, outputOrdinal, err)
				}
				result.Bars = append(result.Bars, bar)
				result.Lineage = append(result.Lineage, BarLineage{
					OutputOrdinal: outputOrdinal, OutputOpenTime: bar.OpenTime, SegmentInstanceID: instanceID,
					SourceVersionID: segment.Source.VersionID, SourceContentHash: segment.Source.ContentHash,
					SourceOrdinal: source.Ordinal, SourceOpenTime: source.OpenTime,
				})
				outputOrdinal++
			}
			actualGap := 0.0
			if instanceStart > 0 {
				actualGap = result.Bars[instanceStart].Open/result.Bars[instanceStart-1].Close - 1
			}
			instance := SegmentInstance{
				InstanceID: instanceID, SegmentItemID: segment.ItemID, Order: len(result.Instances), RepeatOrdinal: repeat + 1,
				SourceVersionID: segment.Source.VersionID, SourceContentHash: segment.Source.ContentHash,
				SourceStartTimeMs: segment.StartTimeMs, SourceEndTimeMs: segment.EndTimeMs,
				OutputStartOrdinal: instanceStart, OutputEndOrdinal: outputOrdinal - 1,
				OutputStartTimeMs: result.Bars[instanceStart].OpenTime, OutputEndTimeMs: result.Bars[outputOrdinal-1].OpenTime,
				ScaleMultiplier: multiplier, SourceGapRatio: segment.SourceGapRatio, ActualGapRatio: actualGap,
				AnchorMissing: anchorMissing, AnchorValue: anchorValue,
			}
			if anchorMissing {
				result.AnchorWarnings++
			}
			result.Instances = append(result.Instances, instance)
		}
	}
	if outputOrdinal != plan.TotalOutputBars {
		return RecompositionResult{}, fmt.Errorf("%w: output count mismatch", ErrNumericResult)
	}
	contentHash, err := HashRecompositionContent(result.Bars, result.Lineage)
	if err != nil {
		return RecompositionResult{}, err
	}
	result.ContentHash = contentHash
	return result, nil
}

// HashRecompositionContent calculates the immutable content identity shared by
// preview, formal generation and integrity audits.
func HashRecompositionContent(bars []Bar, lineage []BarLineage) (string, error) {
	if err := ValidateBars(bars, true); err != nil {
		return "", err
	}
	if len(lineage) != len(bars) {
		return "", ErrNumericResult
	}
	for index := range lineage {
		if lineage[index].OutputOrdinal != index || lineage[index].OutputOpenTime != bars[index].OpenTime ||
			lineage[index].SegmentInstanceID == "" || lineage[index].SourceVersionID == 0 ||
			strings.TrimSpace(lineage[index].SourceContentHash) == "" || lineage[index].SourceOpenTime <= 0 {
			return "", ErrNumericResult
		}
	}
	contentRaw, err := compute.CanonicalJSON(struct {
		SchemaVersion    string         `json:"schema_version"`
		AlgorithmVersion string         `json:"algorithm_version"`
		PrecisionVersion string         `json:"precision_version"`
		Bars             []canonicalBar `json:"bars"`
		Lineage          []BarLineage   `json:"lineage"`
	}{BarSchemaVersion, RecompositionAlgorithm, PricePrecisionVersion, canonicalBars(bars), lineage})
	if err != nil {
		return "", err
	}
	return "market-content:v1:" + compute.HashBytes(contentRaw), nil
}

func ValidateBar(bar Bar) error {
	if bar.OpenTime <= 0 || !finitePositive(bar.Open) || !finitePositive(bar.High) || !finitePositive(bar.Low) ||
		!finitePositive(bar.Close) || math.IsNaN(bar.Volume) || math.IsInf(bar.Volume, 0) || bar.Volume < 0 ||
		bar.High < math.Max(bar.Open, bar.Close) || bar.Low > math.Min(bar.Open, bar.Close) || bar.High < bar.Low {
		return ErrInvalidBar
	}
	return nil
}

func ValidateBars(bars []Bar, requireOrdinal bool) error {
	if len(bars) == 0 {
		return ErrInvalidBar
	}
	for index, bar := range bars {
		if err := ValidateBar(bar); err != nil {
			return fmt.Errorf("bar %d: %w", index, err)
		}
		if requireOrdinal && bar.Ordinal != index {
			return fmt.Errorf("bar %d: %w", index, ErrInvalidBar)
		}
		if index > 0 && bar.OpenTime <= bars[index-1].OpenTime {
			return fmt.Errorf("bar %d time is not increasing: %w", index, ErrInvalidBar)
		}
	}
	return nil
}

func validateSlots(slots []int64, expected int, start int64) error {
	if len(slots) != expected || len(slots) == 0 || slots[0] != start {
		return ErrCalendarSlots
	}
	for index, slot := range slots {
		if slot <= 0 || (index > 0 && slot <= slots[index-1]) {
			return ErrCalendarSlots
		}
	}
	return nil
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func quantizePrice(value float64) float64 {
	if !finitePositive(value) || math.Abs(value) > math.MaxFloat64/1e10 {
		return value
	}
	return math.Round(value*1e10) / 1e10
}

func quantizeVolume(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > math.MaxFloat64/1e10 {
		return value
	}
	return math.Round(value*1e10) / 1e10
}

type canonicalBar struct {
	Ordinal  int    `json:"ordinal"`
	OpenTime int64  `json:"open_time"`
	Open     string `json:"open"`
	High     string `json:"high"`
	Low      string `json:"low"`
	Close    string `json:"close"`
	Volume   string `json:"volume"`
}

func canonicalBars(bars []Bar) []canonicalBar {
	result := make([]canonicalBar, 0, len(bars))
	for _, bar := range bars {
		result = append(result, canonicalBar{
			Ordinal: bar.Ordinal, OpenTime: bar.OpenTime,
			Open: decimal10(bar.Open), High: decimal10(bar.High), Low: decimal10(bar.Low), Close: decimal10(bar.Close), Volume: decimal10(bar.Volume),
		})
	}
	return result
}

func decimal10(value float64) string {
	return fmt.Sprintf("%.10f", value)
}
