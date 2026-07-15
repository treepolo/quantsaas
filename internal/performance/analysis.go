package performance

import (
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	dayMilliseconds = int64(24 * time.Hour / time.Millisecond)
	averageYearDays = 365.2425
)

type timedReturn struct {
	timeMs int64
	value  float64
}

type periodEndpoint struct {
	key    string
	timeMs int64
	value  float64
}

func Analyze(path []Point, annualizationPath []Point, betaBenchmark []SeriesPoint, config Config) (Result, error) {
	if err := validateConfig(config); err != nil {
		return Result{}, err
	}
	if err := validatePath("source", path, true); err != nil {
		return Result{}, err
	}
	if len(annualizationPath) == 0 {
		annualizationPath = path
	}
	if err := validatePath("annualization", annualizationPath, true); err != nil {
		return Result{}, err
	}
	if err := validateSeries(betaBenchmark); err != nil {
		return Result{}, err
	}

	bins := config.HistogramBins
	if bins == 0 {
		bins = DefaultHistogramBins
	}
	daily := periodReturns(path, PeriodDaily)
	weekly := periodReturns(path, PeriodWeekly)
	monthly := periodReturns(path, PeriodMonthly)
	dailyPath := aggregatePath(path, PeriodDaily)
	dailyValues := returnValues(daily)
	weeklyValues := returnValues(weekly)
	monthlyValues := returnValues(monthly)

	strategyAnnualized := annualizedReturn(annualizationPath, false)
	benchmarkAnnualized := annualizedReturn(annualizationPath, true)
	var annualizedDifference *float64
	if strategyAnnualized != nil && benchmarkAnnualized != nil {
		value := *strategyAnnualized - *benchmarkAnnualized
		annualizedDifference = &value
	}

	finalRatio := path[len(path)-1].NAV / path[len(path)-1].BenchmarkNAV
	if finalRatio <= 0 || math.IsNaN(finalRatio) || math.IsInf(finalRatio, 0) {
		return Result{}, fmt.Errorf("final NAV ratio must be positive and finite")
	}
	underwaterStats, underwaterChart := buildUnderwater(path)
	accumulation := buildAccumulation(dailyPath)
	exposureStats, exposureChart := buildExposure(dailyPath, path[0].NAV, path[len(path)-1].NAV)

	summary := Summary{
		SchemaVersion:      SummarySchemaVersion,
		AnalysisVersion:    AnalysisVersion,
		AggregationVersion: AggregationUTCVersion,
		Relative: RelativePerformance{
			FinalNAVRatio:                     finalRatio,
			LogFinalNAVRatio:                  math.Log(finalRatio),
			StrategyNoCashFlowAnnualized:      strategyAnnualized,
			BenchmarkNoCashFlowAnnualized:     benchmarkAnnualized,
			NoCashFlowAnnualizedDifference:    annualizedDifference,
			AnnualizationFormulaVersion:       AnnualizationVersion,
			AnnualizationUsesNoCashFlowResult: true,
		},
		Distributions: map[string]DistributionStats{
			PeriodDaily:   describeReturns(PeriodDaily, dailyValues),
			PeriodWeekly:  describeReturns(PeriodWeekly, weeklyValues),
			PeriodMonthly: describeReturns(PeriodMonthly, monthlyValues),
		},
		LongestUnderwater: underwaterStats,
		Sortino:           calculateSortino(daily, config.RiskFreeAnnualRate),
		Beta:              calculateBeta(path, betaBenchmark),
		Exposure:          exposureStats,
	}
	return Result{
		Summary: summary,
		Charts: Charts{
			DailyDistribution:   buildDistributionChart(ChartDistributionDaily, PeriodDaily, dailyValues, bins),
			WeeklyDistribution:  buildDistributionChart(ChartDistributionWeekly, PeriodWeekly, weeklyValues, bins),
			MonthlyDistribution: buildDistributionChart(ChartDistributionMonthly, PeriodMonthly, monthlyValues, bins),
			Accumulation:        accumulation,
			Underwater:          underwaterChart,
			Exposure:            exposureChart,
		},
	}, nil
}

func validateConfig(config Config) error {
	if math.IsNaN(config.RiskFreeAnnualRate) || math.IsInf(config.RiskFreeAnnualRate, 0) || config.RiskFreeAnnualRate <= -1 {
		return fmt.Errorf("risk-free annual rate must be finite and greater than -1")
	}
	if config.HistogramBins < 0 || config.HistogramBins > MaximumHistogramBins {
		return fmt.Errorf("histogram bins must be between 1 and %d", MaximumHistogramBins)
	}
	return nil
}

func validatePath(name string, path []Point, requireBenchmark bool) error {
	if len(path) < 2 {
		return fmt.Errorf("%s path requires at least two points", name)
	}
	previous := int64(0)
	for index, point := range path {
		if index > 0 && point.TimeMs <= previous {
			return fmt.Errorf("%s path timestamps must be strictly increasing", name)
		}
		previous = point.TimeMs
		if point.NAV <= 0 || math.IsNaN(point.NAV) || math.IsInf(point.NAV, 0) {
			return fmt.Errorf("%s path point %d has invalid NAV", name, index)
		}
		if requireBenchmark && (point.BenchmarkNAV <= 0 || math.IsNaN(point.BenchmarkNAV) || math.IsInf(point.BenchmarkNAV, 0)) {
			return fmt.Errorf("%s path point %d has invalid benchmark NAV", name, index)
		}
		if point.ActualExposure < 0 || point.ActualExposure > 1 || math.IsNaN(point.ActualExposure) || math.IsInf(point.ActualExposure, 0) {
			return fmt.Errorf("%s path point %d has invalid actual exposure", name, index)
		}
	}
	return nil
}

func validateSeries(points []SeriesPoint) error {
	previous := int64(0)
	for index, point := range points {
		if index > 0 && point.TimeMs <= previous {
			return fmt.Errorf("beta benchmark timestamps must be strictly increasing")
		}
		previous = point.TimeMs
		if point.Value <= 0 || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
			return fmt.Errorf("beta benchmark point %d has invalid value", index)
		}
	}
	return nil
}

func periodReturns(path []Point, period string) []timedReturn {
	aggregated := aggregatePath(path, period)
	endpoints := make([]periodEndpoint, 0, len(aggregated))
	for _, point := range aggregated {
		endpoints = append(endpoints, periodEndpoint{key: periodKey(point.TimeMs, period), timeMs: point.TimeMs, value: point.NAV})
	}
	returns := make([]timedReturn, 0, len(endpoints)-1)
	for index := 1; index < len(endpoints); index++ {
		returns = append(returns, timedReturn{
			timeMs: endpoints[index].timeMs,
			value:  endpoints[index].value/endpoints[index-1].value - 1,
		})
	}
	return returns
}

func aggregatePath(path []Point, period string) []Point {
	aggregated := make([]Point, 0, len(path))
	for _, point := range path {
		key := periodKey(point.TimeMs, period)
		if len(aggregated) > 0 && periodKey(aggregated[len(aggregated)-1].TimeMs, period) == key {
			aggregated[len(aggregated)-1] = point
			continue
		}
		aggregated = append(aggregated, point)
	}
	return aggregated
}

func periodKey(timeMs int64, period string) string {
	timestamp := time.UnixMilli(timeMs).UTC()
	switch period {
	case PeriodWeekly:
		year, week := timestamp.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	case PeriodMonthly:
		return timestamp.Format("2006-01")
	default:
		return timestamp.Format("2006-01-02")
	}
}

func returnValues(returns []timedReturn) []float64 {
	values := make([]float64, 0, len(returns))
	for _, item := range returns {
		values = append(values, item.value)
	}
	return values
}

func describeReturns(period string, values []float64) DistributionStats {
	stats := DistributionStats{
		Period: period,
		Count:  len(values),
		Quantiles: map[string]float64{
			"p05": 0, "p25": 0, "p50": 0, "p75": 0, "p95": 0,
		},
		StatsVersion: DistributionStatsVersion,
	}
	if len(values) == 0 {
		return stats
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	stats.Minimum = sorted[0]
	stats.Maximum = sorted[len(sorted)-1]
	for _, value := range values {
		stats.Mean += value
	}
	stats.Mean /= float64(len(values))
	stats.Median = linearQuantile(sorted, 0.5)
	stats.Quantiles = map[string]float64{
		"p05": linearQuantile(sorted, 0.05),
		"p25": linearQuantile(sorted, 0.25),
		"p50": stats.Median,
		"p75": linearQuantile(sorted, 0.75),
		"p95": linearQuantile(sorted, 0.95),
	}
	second, third, fourth := 0.0, 0.0, 0.0
	for _, value := range values {
		delta := value - stats.Mean
		second += delta * delta
		third += delta * delta * delta
		fourth += delta * delta * delta * delta
	}
	count := float64(len(values))
	second /= count
	third /= count
	fourth /= count
	stats.StdDev = math.Sqrt(second)
	if stats.StdDev > 0 {
		stats.Skewness = third / math.Pow(stats.StdDev, 3)
		stats.ExcessKurtosis = fourth/math.Pow(stats.StdDev, 4) - 3
	}
	return stats
}

func linearQuantile(sorted []float64, probability float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	position := probability * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func buildDistributionChart(kind string, period string, values []float64, requestedBins int) DistributionChart {
	chart := DistributionChart{SchemaVersion: ChartSchemaVersion, Kind: kind, Period: period, Bins: []HistogramBin{}}
	if len(values) == 0 {
		return chart
	}
	minimum, maximum := values[0], values[0]
	for _, value := range values[1:] {
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	if minimum == maximum {
		chart.Bins = []HistogramBin{{Lower: minimum, Upper: maximum, Count: len(values)}}
		return chart
	}
	binCount := requestedBins
	if binCount > len(values) {
		binCount = len(values)
	}
	if binCount < 1 {
		binCount = 1
	}
	width := (maximum - minimum) / float64(binCount)
	chart.Bins = make([]HistogramBin, binCount)
	for index := range chart.Bins {
		chart.Bins[index] = HistogramBin{Lower: minimum + float64(index)*width, Upper: minimum + float64(index+1)*width}
	}
	chart.Bins[len(chart.Bins)-1].Upper = maximum
	for _, value := range values {
		index := int((value - minimum) / width)
		if index >= binCount {
			index = binCount - 1
		}
		chart.Bins[index].Count++
	}
	return chart
}

func buildAccumulation(path []Point) ReturnAccumulationChart {
	chart := ReturnAccumulationChart{SchemaVersion: ChartSchemaVersion, Kind: ChartReturnAccumulation, Points: []AccumulationPoint{}}
	base := path[0].NAV
	sum := 0.0
	for index := 1; index < len(path); index++ {
		dailyReturn := path[index].NAV/path[index-1].NAV - 1
		sum += dailyReturn
		chart.Points = append(chart.Points, AccumulationPoint{
			TimeMs:           path[index].TimeMs,
			DailyReturn:      dailyReturn,
			ArithmeticSum:    sum,
			CompoundedReturn: path[index].NAV/base - 1,
		})
	}
	return chart
}

func buildUnderwater(path []Point) (UnderwaterStats, UnderwaterChart) {
	chart := UnderwaterChart{SchemaVersion: ChartSchemaVersion, Kind: ChartUnderwater, Points: make([]UnderwaterPoint, 0, len(path))}
	peak := path[0].NAV
	start := int64(0)
	points := 0
	longest := UnderwaterStats{}
	for _, point := range path {
		if point.NAV >= peak {
			if start != 0 {
				days := float64(point.TimeMs-start) / float64(dayMilliseconds)
				if days > longest.LongestDays || (days == longest.LongestDays && points > longest.LongestPoints) {
					longest = UnderwaterStats{LongestDays: days, LongestPoints: points, StartedAtMs: start, RecoveredAtMs: point.TimeMs, RecoveryCompleted: true}
				}
			}
			peak = point.NAV
			start = 0
			points = 0
		} else {
			if start == 0 {
				start = point.TimeMs
			}
			points++
		}
		underwaterDays := 0.0
		if start != 0 {
			underwaterDays = float64(point.TimeMs-start) / float64(dayMilliseconds)
		}
		chart.Points = append(chart.Points, UnderwaterPoint{TimeMs: point.TimeMs, Drawdown: point.NAV/peak - 1, UnderwaterDays: underwaterDays})
	}
	if start != 0 {
		lastTime := path[len(path)-1].TimeMs
		days := float64(lastTime-start) / float64(dayMilliseconds)
		if days > longest.LongestDays || (days == longest.LongestDays && points > longest.LongestPoints) {
			longest = UnderwaterStats{LongestDays: days, LongestPoints: points, StartedAtMs: start, RecoveryCompleted: false}
		}
	}
	return longest, chart
}

func buildExposure(path []Point, initialNAV float64, finalNAV float64) (ExposureStats, ExposureChart) {
	chart := ExposureChart{SchemaVersion: ChartSchemaVersion, Kind: ChartExposure, Points: make([]ExposurePoint, 0, len(path))}
	exposed := 0
	total := 0.0
	for _, point := range path {
		if point.ActualExposure > 0 {
			exposed++
		}
		total += point.ActualExposure
		chart.Points = append(chart.Points, ExposurePoint{TimeMs: point.TimeMs, ActualExposureWeight: point.ActualExposure})
	}
	average := total / float64(len(path))
	stats := ExposureStats{
		ExposureDaysRatio:        float64(exposed) / float64(len(path)),
		AverageActualExposure:    average,
		ExposureAdjustedReadable: average > 0,
	}
	if average > 0 {
		value := (finalNAV/initialNAV - 1) / average
		stats.ExposureAdjustedReturn = &value
	}
	return stats, chart
}

func annualizedReturn(path []Point, benchmark bool) *float64 {
	start, end := path[0].NAV, path[len(path)-1].NAV
	if benchmark {
		start, end = path[0].BenchmarkNAV, path[len(path)-1].BenchmarkNAV
	}
	days := float64(path[len(path)-1].TimeMs-path[0].TimeMs) / float64(dayMilliseconds)
	if start <= 0 || end <= 0 || days <= 0 {
		return nil
	}
	value := math.Pow(end/start, averageYearDays/days) - 1
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func calculateSortino(returns []timedReturn, annualRiskFree float64) SortinoStats {
	stats := SortinoStats{RiskFreeAnnualRate: annualRiskFree, ObservationCount: len(returns), FormulaVersion: SortinoFormulaVersion}
	if len(returns) < 2 {
		stats.UnavailableReason = "insufficient_observations"
		return stats
	}
	days := float64(returns[len(returns)-1].timeMs-returns[0].timeMs) / float64(dayMilliseconds)
	if days <= 0 {
		stats.UnavailableReason = "invalid_time_range"
		return stats
	}
	periodsPerYear := float64(len(returns)-1) * averageYearDays / days
	if periodsPerYear <= 0 || math.IsNaN(periodsPerYear) || math.IsInf(periodsPerYear, 0) {
		stats.UnavailableReason = "invalid_annualization_basis"
		return stats
	}
	stats.PeriodsPerYear = periodsPerYear
	periodRiskFree := math.Pow(1+annualRiskFree, 1/periodsPerYear) - 1
	meanExcess := 0.0
	downsideSquared := 0.0
	for _, item := range returns {
		excess := item.value - periodRiskFree
		meanExcess += excess
		if excess < 0 {
			downsideSquared += excess * excess
		}
	}
	meanExcess /= float64(len(returns))
	downsideDeviation := math.Sqrt(downsideSquared / float64(len(returns)))
	if downsideDeviation == 0 {
		stats.UnavailableReason = "no_downside_deviation"
		return stats
	}
	value := meanExcess / downsideDeviation * math.Sqrt(periodsPerYear)
	stats.Value = &value
	return stats
}

func calculateBeta(path []Point, benchmark []SeriesPoint) BetaStats {
	stats := BetaStats{FormulaVersion: BetaFormulaVersion}
	if len(benchmark) == 0 {
		stats.UnavailableReason = "benchmark_not_selected"
		return stats
	}
	benchmarkByDay := make(map[string]SeriesPoint, len(benchmark))
	for _, item := range benchmark {
		benchmarkByDay[periodKey(item.TimeMs, PeriodDaily)] = item
	}
	type alignedPoint struct {
		strategy  float64
		benchmark float64
	}
	aligned := make([]alignedPoint, 0, len(path))
	for _, point := range aggregatePath(path, PeriodDaily) {
		if benchmarkPoint, ok := benchmarkByDay[periodKey(point.TimeMs, PeriodDaily)]; ok {
			aligned = append(aligned, alignedPoint{strategy: point.NAV, benchmark: benchmarkPoint.Value})
		}
	}
	x := make([]float64, 0, max(0, len(aligned)-1))
	y := make([]float64, 0, max(0, len(aligned)-1))
	for index := 1; index < len(aligned); index++ {
		x = append(x, aligned[index].strategy/aligned[index-1].strategy-1)
		y = append(y, aligned[index].benchmark/aligned[index-1].benchmark-1)
	}
	stats.ObservationCount = len(x)
	if len(x) < 2 {
		stats.UnavailableReason = "insufficient_aligned_observations"
		return stats
	}
	meanX, meanY := 0.0, 0.0
	for index := range x {
		meanX += x[index]
		meanY += y[index]
	}
	meanX /= float64(len(x))
	meanY /= float64(len(y))
	covariance, variance := 0.0, 0.0
	for index := range x {
		covariance += (x[index] - meanX) * (y[index] - meanY)
		variance += (y[index] - meanY) * (y[index] - meanY)
	}
	if variance == 0 {
		stats.UnavailableReason = "zero_benchmark_variance"
		return stats
	}
	value := covariance / variance
	stats.Value = &value
	return stats
}
