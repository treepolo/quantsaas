package marketversion

import (
	"math"
	"testing"
)

func TestRecomposePreservesStandardGapAndOHLC(t *testing.T) {
	gap := 210.0/200.0 - 1
	plan := testPlan([]SegmentPlan{
		testSegment("first", 0, 1, VersionIdentity{VersionID: 1, ContentHash: "source-a", Interval: "1d"}, []Bar{
			{Ordinal: 0, OpenTime: 1, Open: 90, High: 105, Low: 85, Close: 100, Volume: 11},
		}, nil),
		testSegment("second", 1, 1, VersionIdentity{VersionID: 2, ContentHash: "source-b", Interval: "1d"}, []Bar{
			{Ordinal: 0, OpenTime: 2, Open: 210, High: 220, Low: 205, Close: 215, Volume: 22},
		}, ptr(200)),
	})
	plan.Segments[1].SourceGapRatio = &gap
	result, err := Recompose(plan, []int64{1000, 2000})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Bars[1]
	assertClose(t, "open", got.Open, 105)
	assertClose(t, "high", got.High, 110)
	assertClose(t, "low", got.Low, 102.5)
	assertClose(t, "close", got.Close, 107.5)
	assertClose(t, "volume", got.Volume, 22)
	assertClose(t, "gap", result.Instances[1].ActualGapRatio, gap)
	if result.Instances[1].ScaleMultiplier != 0.5 || result.Instances[1].AnchorMissing {
		t.Fatalf("unexpected join metadata: %+v", result.Instances[1])
	}
}

func TestRecomposeRepeatRecalculatesMultiplierAndLineage(t *testing.T) {
	segment := testSegment("repeat", 0, 3, VersionIdentity{VersionID: 7, ContentHash: "source-r", Interval: "1d"}, []Bar{
		{Ordinal: 5, OpenTime: 10, Open: 100, High: 112, Low: 98, Close: 110, Volume: 5},
		{Ordinal: 6, OpenTime: 20, Open: 110, High: 125, Low: 108, Close: 120, Volume: 6},
	}, ptr(90))
	plan := testPlan([]SegmentPlan{segment})
	result, err := Recompose(plan, []int64{1000, 2000, 3000, 4000, 5000, 6000})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Bars) != 6 || len(result.Instances) != 3 || len(result.Lineage) != 6 {
		t.Fatalf("unexpected result sizes: %+v", result)
	}
	assertClose(t, "second repeat multiplier", result.Instances[1].ScaleMultiplier, 120.0/90.0)
	assertClose(t, "third repeat multiplier", result.Instances[2].ScaleMultiplier, result.Bars[3].Close/90.0)
	if result.Lineage[2].SourceOrdinal != 5 || result.Lineage[2].SourceOpenTime != 10 || result.Lineage[2].SegmentInstanceID != "repeat:2" {
		t.Fatalf("lineage mismatch: %+v", result.Lineage[2])
	}
	if result.Bars[4].Volume != 5 || result.Bars[5].Volume != 6 {
		t.Fatal("volume changed across repeated instances")
	}
}

func TestRecomposeMissingAnchorUsesFirstOpenAndZeroGap(t *testing.T) {
	plan := testPlan([]SegmentPlan{
		testSegment("base", 0, 1, VersionIdentity{VersionID: 1, ContentHash: "a", Interval: "1d"}, []Bar{
			{Ordinal: 0, OpenTime: 1, Open: 90, High: 105, Low: 85, Close: 100, Volume: 1},
		}, nil),
		testSegment("missing", 1, 1, VersionIdentity{VersionID: 2, ContentHash: "b", Interval: "1d"}, []Bar{
			{Ordinal: 0, OpenTime: 2, Open: 250, High: 275, Low: 240, Close: 260, Volume: 2},
		}, nil),
	})
	result, err := Recompose(plan, []int64{1000, 2000})
	if err != nil {
		t.Fatal(err)
	}
	join := result.Instances[1]
	if !join.AnchorMissing || join.AnchorValue != 250 {
		t.Fatalf("missing anchor metadata = %+v", join)
	}
	assertClose(t, "fallback multiplier", join.ScaleMultiplier, 0.4)
	assertClose(t, "fallback open", result.Bars[1].Open, 100)
	assertClose(t, "fallback gap", join.ActualGapRatio, 0)
}

func TestRecomposeDeterministicPlanAndContentHash(t *testing.T) {
	plan := testPlan([]SegmentPlan{testSegment("one", 0, 1, VersionIdentity{VersionID: 1, ContentHash: "hash", Interval: "1d"}, []Bar{
		{Ordinal: 0, OpenTime: 1, Open: 1, High: 2, Low: 0.5, Close: 1.5, Volume: 9},
	}, ptr(0.8))})
	_, rawA, hashA, err := NormalizePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	_, rawB, hashB, err := NormalizePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if string(rawA) != string(rawB) || hashA != hashB {
		t.Fatal("canonical plan is not deterministic")
	}
	first, err := Recompose(plan, []int64{1000})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Recompose(plan, []int64{1000})
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentHash != second.ContentHash {
		t.Fatal("content hash changed for identical inputs")
	}
}

func TestRecomposeRejectsIllegalInputsAndSlots(t *testing.T) {
	invalid := testSegment("bad", 0, 1, VersionIdentity{VersionID: 1, ContentHash: "hash", Interval: "1d"}, []Bar{
		{Ordinal: 0, OpenTime: 1, Open: 10, High: 9, Low: 8, Close: 10, Volume: 1},
	}, nil)
	if _, err := Recompose(testPlan([]SegmentPlan{invalid}), []int64{1000}); err == nil {
		t.Fatal("illegal OHLC was accepted")
	}
	valid := testSegment("ok", 0, 1, VersionIdentity{VersionID: 1, ContentHash: "hash", Interval: "1d"}, []Bar{
		{Ordinal: 0, OpenTime: 1, Open: 10, High: 11, Low: 9, Close: 10, Volume: 1},
	}, nil)
	if _, err := Recompose(testPlan([]SegmentPlan{valid}), []int64{999}); err == nil {
		t.Fatal("calendar start mismatch was accepted")
	}
	valid.Bars[0].Open = math.Inf(1)
	if _, err := Recompose(testPlan([]SegmentPlan{valid}), []int64{1000}); err == nil {
		t.Fatal("non-finite price was accepted")
	}
}

func testPlan(segments []SegmentPlan) GenerationPlan {
	total := 0
	for _, segment := range segments {
		total += segment.BarCount * segment.RepeatCount
	}
	return GenerationPlan{
		SchemaVersion: RecompositionPlanVersion, AlgorithmVersion: RecompositionAlgorithm, PrecisionVersion: PricePrecisionVersion,
		Interval: "1d", TargetMarket: "us", TargetTimezone: "America/New_York",
		CalendarSource:  VersionIdentity{VersionID: 99, ContentHash: "calendar", Interval: "1d"},
		CalendarVersion: CalendarFromVersionVersion, CalendarHash: "calendar-hash", OutputStartTimeMs: 1000,
		TotalOutputBars: total, Segments: segments,
	}
}

func testSegment(id string, order int, repeat int, source VersionIdentity, bars []Bar, previousClose *float64) SegmentPlan {
	segment := SegmentPlan{
		ItemID: id, Order: order, Source: source, StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[len(bars)-1].OpenTime,
		BarCount: len(bars), RepeatCount: repeat, FirstOpen: bars[0].Open, Bars: bars,
	}
	if previousClose != nil {
		segment.PreviousClosePresent = true
		segment.PreviousClose = *previousClose
		gap := bars[0].Open/segment.PreviousClose - 1
		segment.SourceGapRatio = &gap
	}
	return segment
}

func ptr(value float64) *float64 { return &value }

func assertClose(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.12f, want %.12f", label, got, want)
	}
}
