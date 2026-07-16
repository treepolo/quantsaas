package perturbation

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	compute "quantsaas/internal/compute"
)

func CanonicalDecimal(input string, positive bool) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" || strings.ContainsAny(value, "eE") || strings.HasPrefix(value, "+") {
		return "", ErrInvalidAlpha
	}
	rational, ok := new(big.Rat).SetString(value)
	if !ok || (positive && rational.Sign() <= 0) {
		return "", ErrInvalidAlpha
	}
	if rational.Sign() == 0 {
		return "0", nil
	}
	// A terminating base-10 decimal has no prime denominator factors except 2 and 5.
	denominator := new(big.Int).Set(rational.Denom())
	two, five, zero := big.NewInt(2), big.NewInt(5), big.NewInt(0)
	for new(big.Int).Mod(denominator, two).Cmp(zero) == 0 {
		denominator.Div(denominator, two)
	}
	for new(big.Int).Mod(denominator, five).Cmp(zero) == 0 {
		denominator.Div(denominator, five)
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", ErrInvalidAlpha
	}
	parts := strings.SplitN(value, ".", 2)
	scale := 0
	if len(parts) == 2 {
		scale = len(parts[1])
	}
	canonical := rational.FloatString(scale)
	canonical = strings.TrimRight(strings.TrimRight(canonical, "0"), ".")
	if canonical == "-0" || canonical == "" {
		canonical = "0"
	}
	return canonical, nil
}

func ParseAlpha(input string) (string, float64, error) {
	canonical, err := CanonicalDecimal(input, true)
	if err != nil {
		return "", 0, err
	}
	value, err := strconv.ParseFloat(canonical, 64)
	if err != nil || !finite(value) || value <= 0 {
		return "", 0, ErrInvalidAlpha
	}
	return canonical, value, nil
}

func ParseSeed(input string) (string, uint64, error) {
	value := strings.TrimSpace(input)
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return "", 0, ErrInvalidSeed
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != strings.TrimLeft(value, "0") && !(parsed == 0 && strings.Trim(value, "0") == "") {
		return "", 0, ErrInvalidSeed
	}
	return strconv.FormatUint(parsed, 10), parsed, nil
}

func SourceContentHash(source SourceIdentity, start, end int64, previousClose *float64, bars []Bar) (string, error) {
	canonicalBars, err := canonicalBars(bars)
	if err != nil {
		return "", err
	}
	present := previousClose != nil
	previous := ""
	if present {
		if !finite(*previousClose) || *previousClose <= 0 {
			return "", ErrInvalidBar
		}
		previous = canonicalFloat(*previousClose)
	}
	raw, err := compute.CanonicalJSON(map[string]any{"snapshot_schema_version": SnapshotSchema, "source": source, "start_time": start, "end_time": end, "previous_close_present": present, "previous_close": previous, "bars": canonicalBars})
	if err != nil {
		return "", err
	}
	return "perturbation-source:v1:" + compute.HashBytes(raw), nil
}

func RecipeHash(sourceHash, seed, alpha string) (string, error) {
	raw, err := compute.CanonicalJSON(map[string]any{"schema_version": RecipeSchema, "source_content_hash": sourceHash, "algorithm_version": AlgorithmVersion, "seed": seed, "alpha": alpha})
	if err != nil {
		return "", err
	}
	return "perturbation-recipe:v1:" + compute.HashBytes(raw), nil
}

func GeneratedContentHash(target SourceIdentity, bars []Bar) (string, error) {
	canonicalBars, err := canonicalBars(bars)
	if err != nil {
		return "", err
	}
	raw, err := compute.CanonicalJSON(map[string]any{"output_schema_version": OutputSchema, "target": target, "bars": canonicalBars})
	if err != nil {
		return "", err
	}
	return "perturbation-content:v1:" + compute.HashBytes(raw), nil
}

func canonicalBars(bars []Bar) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(bars))
	for index, bar := range bars {
		if err := ValidateBar(bar); err != nil || index > 0 && bar.OpenTime <= bars[index-1].OpenTime {
			return nil, ErrInvalidBar
		}
		result = append(result, map[string]any{"open_time": bar.OpenTime, "open": canonicalFloat(bar.Open), "high": canonicalFloat(bar.High), "low": canonicalFloat(bar.Low), "close": canonicalFloat(bar.Close), "volume": canonicalFloat(bar.Volume), "metadata": nil})
	}
	return result, nil
}

func canonicalFloat(value float64) string {
	if value == 0 {
		return "0"
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Sprint(value)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func CanonicalHash(value any) (string, json.RawMessage, error) {
	raw, err := compute.CanonicalJSON(value)
	if err != nil {
		return "", nil, err
	}
	return compute.HashBytes(raw), raw, nil
}
