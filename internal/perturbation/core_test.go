package perturbation

import (
	"math"
	"testing"
)

func testIdentity() SourceIdentity {
	return SourceIdentity{InstrumentID: "SOXL", DataSource: "yahoo", Symbol: "SOXL", Interval: "1d"}
}

func TestCoordinatesAlphaZeroRoundTripAndWicks(t *testing.T) {
	for _, bar := range []Bar{
		{OpenTime: 1, Open: 100, High: 110, Low: 90, Close: 105, Volume: 7},
		{OpenTime: 2, Open: 100, High: 100, Low: 95, Close: 100, Volume: 0},
	} {
		coordinates, err := ToCoordinates(bar)
		if err != nil {
			t.Fatal(err)
		}
		roundtrip, err := FromCoordinates(bar.OpenTime, bar.Volume, coordinates)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range [][2]float64{{bar.Open, roundtrip.Open}, {bar.High, roundtrip.High}, {bar.Low, roundtrip.Low}, {bar.Close, roundtrip.Close}} {
			if math.Abs(pair[0]-pair[1]) > 1e-10 {
				t.Fatalf("roundtrip mismatch: %v", pair)
			}
		}
	}
}

func TestTrueRangeAndDeterministicHash(t *testing.T) {
	bar := Bar{OpenTime: 1700000000000, Open: 110, High: 120, Low: 100, Close: 115, Volume: 1}
	previous := 80.0
	scale, err := TrueRangeScale(bar, &previous)
	if err != nil {
		t.Fatal(err)
	}
	expected := math.Max(math.Log(1.2), math.Max(math.Abs(math.Log(1.5)), math.Abs(math.Log(1.25))))
	if math.Abs(scale-expected) > 1e-15 {
		t.Fatalf("scale=%v expected=%v", scale, expected)
	}
	u1, err := HashToUnit(math.MaxUint64, testIdentity(), bar.OpenTime, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	u2, _ := HashToUnit(math.MaxUint64, testIdentity(), bar.OpenTime, 3, 1)
	if u1 != u2 || u1 < 0 || u1 >= 1 {
		t.Fatalf("unit is not deterministic: %v %v", u1, u2)
	}
	if u1 != 0.9457769880989262 {
		t.Fatalf("unexpected fixed vector %0.17g", u1)
	}
}

func TestGenerateAnchorsSourceAndSharesDirectionsAcrossAlpha(t *testing.T) {
	bars := []Bar{{OpenTime: 1000, Open: 100, High: 110, Low: 90, Close: 105, Volume: 5}, {OpenTime: 2000, Open: 200, High: 220, Low: 180, Close: 190, Volume: 8}}
	first, err := Generate(testIdentity(), bars, nil, 42, .01)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(testIdentity(), bars, nil, 42, .05)
	if err != nil {
		t.Fatal(err)
	}
	for i := range bars {
		d1 := math.Log(first.Bars[i].Open / bars[i].Open)
		d2 := math.Log(second.Bars[i].Open / bars[i].Open)
		if d1 != 0 && math.Signbit(d1) != math.Signbit(d2) {
			t.Fatalf("alpha direction changed at %d", i)
		}
	}
	if math.Abs(first.Bars[1].Open-bars[1].Open) > 50 {
		t.Fatal("second bar accumulated from previous perturbed bar")
	}
	flat := []Bar{{OpenTime: 1, Open: 10, High: 10, Low: 10, Close: 10}}
	unchanged, err := Generate(testIdentity(), flat, nil, 99, 10)
	if err != nil || unchanged.Bars[0] != flat[0] {
		t.Fatalf("flat bar changed: %#v %v", unchanged, err)
	}
}

func TestCanonicalAndStatisticsAndRelativeMetrics(t *testing.T) {
	for _, input := range []string{"0.05", "0.050", "00.0500"} {
		canonical, _, err := ParseAlpha(input)
		if err != nil || canonical != "0.05" {
			t.Fatalf("canonical %q = %q %v", input, canonical, err)
		}
	}
	zero, err := CanonicalDecimal("-0", false)
	if err != nil || zero != "0" {
		t.Fatalf("negative zero: %q %v", zero, err)
	}
	stats := Describe([]float64{1, 2, 3, 4})
	if stats.Mean != 2.5 || stats.Median != 2.5 || math.Abs(stats.StdDev-math.Sqrt(1.25)) > 1e-15 || stats.P25 != 1.75 || math.Abs(stats.P95-3.85) > 1e-15 {
		t.Fatalf("stats=%+v", stats)
	}
	metrics := RelativePerformance(120, 100, .2, .1, .1, .2)
	if metrics.Qualification != QualificationQualified || metrics.FinalNAVRatio == nil || *metrics.FinalNAVRatio != 1.2 || metrics.PerformanceDrawdownComposite == nil {
		t.Fatalf("metrics=%+v", metrics)
	}
	tie := RelativePerformance(100, 100, .1, .1, .2, .2)
	if tie.Qualification != QualificationBothFailed {
		t.Fatalf("ties must fail strictly: %+v", tie)
	}
}

func TestSourceAndRecipeHashes(t *testing.T) {
	bars := []Bar{{OpenTime: 1, Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 2}}
	previous := 9.5
	hash1, err := SourceContentHash(testIdentity(), 1, 1, &previous, bars)
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]Bar(nil), bars...)
	changed[0].Volume = 3
	hash2, _ := SourceContentHash(testIdentity(), 1, 1, &previous, changed)
	if hash1 == hash2 {
		t.Fatal("source hash ignored persisted bar field")
	}
	recipe1, _ := RecipeHash(hash1, "18446744073709551615", "0.05")
	recipe2, _ := RecipeHash(hash1, "18446744073709551615", "0.050")
	if recipe1 == recipe2 {
		t.Fatal("recipe requires caller canonical alpha and must expose mismatch")
	}
}
