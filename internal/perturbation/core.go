package perturbation

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
)

var (
	ErrInvalidBar   = errors.New("invalid_source_bar")
	ErrInvalidSeed  = errors.New("invalid_seed")
	ErrInvalidAlpha = errors.New("invalid_alpha")
	ErrNumeric      = errors.New("numeric_overflow")
)

func ValidateBar(bar Bar) error {
	values := []float64{bar.Open, bar.High, bar.Low, bar.Close, bar.Volume}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return ErrInvalidBar
		}
	}
	if bar.Open <= 0 || bar.High <= 0 || bar.Low <= 0 || bar.Close <= 0 || bar.Volume < 0 ||
		bar.High < math.Max(bar.Open, bar.Close) || bar.Low > math.Min(bar.Open, bar.Close) {
		return ErrInvalidBar
	}
	return nil
}

func ToCoordinates(bar Bar) (Coordinates, error) {
	if err := ValidateBar(bar); err != nil {
		return Coordinates{}, err
	}
	return Coordinates{
		Middle:    (math.Log(bar.Open) + math.Log(bar.Close)) / 2,
		Body:      math.Log(bar.Close / bar.Open),
		UpperWick: math.Log(bar.High / math.Max(bar.Open, bar.Close)),
		LowerWick: math.Log(math.Min(bar.Open, bar.Close) / bar.Low),
	}, nil
}

func FromCoordinates(openTime int64, volume float64, c Coordinates) (Bar, error) {
	bar := Bar{OpenTime: openTime, Volume: volume}
	bar.Open = math.Exp(c.Middle - c.Body/2)
	bar.Close = math.Exp(c.Middle + c.Body/2)
	bar.High = math.Max(bar.Open, bar.Close) * math.Exp(c.UpperWick)
	bar.Low = math.Min(bar.Open, bar.Close) / math.Exp(c.LowerWick)
	if err := ValidateBar(bar); err != nil {
		return Bar{}, ErrNumeric
	}
	return bar, nil
}

func TrueRangeScale(bar Bar, previousClose *float64) (float64, error) {
	if err := ValidateBar(bar); err != nil {
		return 0, err
	}
	highLow := math.Log(bar.High / bar.Low)
	if previousClose == nil {
		return highLow, nil
	}
	if *previousClose <= 0 || math.IsNaN(*previousClose) || math.IsInf(*previousClose, 0) {
		return 0, ErrInvalidBar
	}
	return math.Max(highLow, math.Max(math.Abs(math.Log(bar.High / *previousClose)), math.Abs(math.Log(bar.Low / *previousClose)))), nil
}

func HashToUnit(seed uint64, source SourceIdentity, openTime int64, coordinate, drawIndex uint8) (float64, error) {
	if coordinate > 3 || drawIndex > 1 || source.InstrumentID == "" || source.DataSource == "" || source.Symbol == "" || source.Interval == "" {
		return 0, ErrInvalidSeed
	}
	h := sha256.New()
	writeText := func(value string) {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len([]byte(value))))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(value))
	}
	writeText(HashDomain)
	var seedBytes [8]byte
	binary.BigEndian.PutUint64(seedBytes[:], seed)
	_, _ = h.Write(seedBytes[:])
	writeText(source.InstrumentID)
	writeText(source.DataSource)
	writeText(source.Symbol)
	writeText(source.Interval)
	var timeBytes [8]byte
	binary.BigEndian.PutUint64(timeBytes[:], uint64(openTime))
	_, _ = h.Write(timeBytes[:])
	_, _ = h.Write([]byte{coordinate, drawIndex})
	digest := h.Sum(nil)
	r := binary.BigEndian.Uint64(digest[:8]) >> 11
	return float64(r) / float64(uint64(1)<<53), nil
}

func Triangular(seed uint64, source SourceIdentity, openTime int64, coordinate uint8) (float64, error) {
	u0, err := HashToUnit(seed, source, openTime, coordinate, 0)
	if err != nil {
		return 0, err
	}
	u1, err := HashToUnit(seed, source, openTime, coordinate, 1)
	if err != nil {
		return 0, err
	}
	return u0 - u1, nil
}

func PerturbBar(source SourceIdentity, bar Bar, previousClose *float64, seed uint64, alpha float64) (Bar, Deviation, error) {
	if alpha <= 0 || math.IsNaN(alpha) || math.IsInf(alpha, 0) {
		return Bar{}, Deviation{}, ErrInvalidAlpha
	}
	c, err := ToCoordinates(bar)
	if err != nil {
		return Bar{}, Deviation{}, err
	}
	scale, err := TrueRangeScale(bar, previousClose)
	if err != nil {
		return Bar{}, Deviation{}, err
	}
	if scale == 0 {
		return bar, Deviation{}, nil
	}
	z := [4]float64{}
	for coordinate := uint8(0); coordinate < 4; coordinate++ {
		z[coordinate], err = Triangular(seed, source, bar.OpenTime, coordinate)
		if err != nil {
			return Bar{}, Deviation{}, err
		}
	}
	amount := alpha * scale
	c.Middle += amount * z[0]
	c.Body += amount * z[1]
	c.UpperWick += math.Min(amount, c.UpperWick) * z[2]
	c.LowerWick += math.Min(amount, c.LowerWick) * z[3]
	generated, err := FromCoordinates(bar.OpenTime, bar.Volume, c)
	if err != nil {
		return Bar{}, Deviation{}, err
	}
	d := Deviation{
		Open: math.Abs(math.Log(generated.Open / bar.Open)), High: math.Abs(math.Log(generated.High / bar.High)),
		Low: math.Abs(math.Log(generated.Low / bar.Low)), Close: math.Abs(math.Log(generated.Close / bar.Close)),
	}
	d.Bar = math.Max(math.Max(d.Open, d.High), math.Max(d.Low, d.Close))
	return generated, d, nil
}

func Generate(source SourceIdentity, bars []Bar, previousClose *float64, seed uint64, alpha float64) (Generated, error) {
	if len(bars) == 0 {
		return Generated{}, ErrInvalidBar
	}
	result := Generated{Bars: make([]Bar, 0, len(bars)), Deviations: make([]Deviation, 0, len(bars))}
	var prior *float64
	if previousClose != nil {
		value := *previousClose
		prior = &value
	}
	for index, sourceBar := range bars {
		if index > 0 && sourceBar.OpenTime <= bars[index-1].OpenTime {
			return Generated{}, ErrInvalidBar
		}
		generated, _, err := PerturbBar(source, sourceBar, prior, seed, alpha)
		if err != nil {
			return Generated{}, fmt.Errorf("bar %d at %d: %w", index, sourceBar.OpenTime, err)
		}
		generated, err = QuantizeBar(generated)
		if err != nil {
			return Generated{}, fmt.Errorf("bar %d at %d: %w", index, sourceBar.OpenTime, err)
		}
		deviation := Deviation{
			Open: math.Abs(math.Log(generated.Open / sourceBar.Open)), High: math.Abs(math.Log(generated.High / sourceBar.High)),
			Low: math.Abs(math.Log(generated.Low / sourceBar.Low)), Close: math.Abs(math.Log(generated.Close / sourceBar.Close)),
		}
		deviation.Bar = math.Max(math.Max(deviation.Open, deviation.High), math.Max(deviation.Low, deviation.Close))
		result.Bars = append(result.Bars, generated)
		result.Deviations = append(result.Deviations, deviation)
		value := sourceBar.Close
		prior = &value
	}
	result.Summary = SummarizeDeviation(result.Deviations)
	return result, nil
}

func QuantizeBar(bar Bar) (Bar, error) {
	quantize := func(value float64) float64 { return math.Round(value*1e10) / 1e10 }
	bar.Open, bar.High, bar.Low, bar.Close, bar.Volume = quantize(bar.Open), quantize(bar.High), quantize(bar.Low), quantize(bar.Close), quantize(bar.Volume)
	if err := ValidateBar(bar); err != nil {
		return Bar{}, ErrNumeric
	}
	return bar, nil
}

func Quantile(values []float64, p float64) float64 {
	if len(values) == 0 || p < 0 || p > 1 {
		return math.NaN()
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	h := float64(len(ordered)-1) * p
	i, j := int(math.Floor(h)), int(math.Ceil(h))
	return ordered[i] + (h-float64(i))*(ordered[j]-ordered[i])
}

func SummarizeDeviation(values []Deviation) DeviationSummary {
	if len(values) == 0 {
		return DeviationSummary{}
	}
	bars := make([]float64, 0, len(values))
	var summary DeviationSummary
	for _, value := range values {
		bars = append(bars, value.Bar)
		summary.Maximum = math.Max(summary.Maximum, value.Bar)
		summary.OpenMax = math.Max(summary.OpenMax, value.Open)
		summary.HighMax = math.Max(summary.HighMax, value.High)
		summary.LowMax = math.Max(summary.LowMax, value.Low)
		summary.CloseMax = math.Max(summary.CloseMax, value.Close)
	}
	summary.Median, summary.P95 = Quantile(bars, .5), Quantile(bars, .95)
	return summary
}

func Describe(values []float64) DescriptiveStats {
	clean := make([]float64, 0, len(values))
	for _, value := range values {
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			clean = append(clean, value)
		}
	}
	if len(clean) == 0 {
		return DescriptiveStats{}
	}
	stats := DescriptiveStats{Available: true, Count: len(clean)}
	for _, value := range clean {
		stats.Mean += value
	}
	stats.Mean /= float64(len(clean))
	for _, value := range clean {
		delta := value - stats.Mean
		stats.StdDev += delta * delta
	}
	stats.StdDev = math.Sqrt(stats.StdDev / float64(len(clean)))
	ordered := append([]float64(nil), clean...)
	sort.Float64s(ordered)
	stats.Minimum, stats.Maximum = ordered[0], ordered[len(ordered)-1]
	stats.Median, stats.P05, stats.P25, stats.P75, stats.P95 = Quantile(ordered, .5), Quantile(ordered, .05), Quantile(ordered, .25), Quantile(ordered, .75), Quantile(ordered, .95)
	return stats
}

func RelativePerformance(parameterFinalNAV, dcaFinalNAV, parameterReturn, dcaReturn, parameterDrawdown, dcaDrawdown float64) RelativeMetrics {
	result := RelativeMetrics{}
	returnOK := finite(parameterReturn) && finite(dcaReturn)
	drawdownOK := finite(parameterDrawdown) && finite(dcaDrawdown)
	if returnOK && drawdownOK {
		returnWon, drawdownWon := parameterReturn > dcaReturn, parameterDrawdown < dcaDrawdown
		switch {
		case returnWon && drawdownWon:
			result.Qualification = QualificationQualified
		case !returnWon && drawdownWon:
			result.Qualification = QualificationReturnFailed
		case returnWon && !drawdownWon:
			result.Qualification = QualificationDrawdownFailed
		default:
			result.Qualification = QualificationBothFailed
		}
	}
	if !finite(parameterFinalNAV) || !finite(dcaFinalNAV) || parameterFinalNAV <= 0 || dcaFinalNAV <= 0 {
		result.UnavailableReason = "non_positive_or_non_finite_nav"
		return result
	}
	finalRatio := parameterFinalNAV / dcaFinalNAV
	logFinal := math.Log(finalRatio)
	if !finite(finalRatio) || !finite(logFinal) {
		result.UnavailableReason = "invalid_final_nav_ratio"
		return result
	}
	result.FinalNAVRatio, result.LogFinalNAVRatio = &finalRatio, &logFinal
	if !drawdownOK || 1-dcaDrawdown <= 0 {
		result.UnavailableReason = "invalid_drawdown_residual_denominator"
		return result
	}
	residual := (1 - parameterDrawdown) / (1 - dcaDrawdown)
	if residual <= 0 || !finite(residual) {
		result.UnavailableReason = "invalid_drawdown_residual_ratio"
		return result
	}
	logResidual, composite := math.Log(residual), logFinal*residual
	if !finite(logResidual) || !finite(composite) {
		result.UnavailableReason = "invalid_relative_metric"
		return result
	}
	result.DrawdownResidualRatio, result.LogDrawdownResidualRatio, result.PerformanceDrawdownComposite = &residual, &logResidual, &composite
	return result
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
