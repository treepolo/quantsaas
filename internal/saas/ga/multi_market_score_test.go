package ga

import (
	"math"
	"testing"
)

func TestMultiMarketReturnScoreUsesSumOfLogAnnualizedReturns(t *testing.T) {
	first, firstAnnualized, ok := multiMarketReturnScore(0.21, 2)
	if !ok {
		t.Fatal("first market return should be defined")
	}
	second, secondAnnualized, ok := multiMarketReturnScore(-0.10, 1)
	if !ok {
		t.Fatal("second market return should be defined")
	}
	third, thirdAnnualized, ok := multiMarketReturnScore(0.331, 3)
	if !ok {
		t.Fatal("third market return should be defined")
	}
	want := math.Log(1.10) + math.Log(0.90) + math.Log(1.10)
	if math.Abs((first+second+third)-want) > 1e-12 {
		t.Fatalf("score = %.15f, want %.15f", first+second+third, want)
	}
	if math.Abs(firstAnnualized-(math.Sqrt(1.21)-1)) > 1e-12 {
		t.Fatalf("first annualized return = %.15f", firstAnnualized)
	}
	if math.Abs(secondAnnualized-(-0.10)) > 1e-12 {
		t.Fatalf("second annualized return = %.15f", secondAnnualized)
	}
	if math.Abs(thirdAnnualized-0.10) > 1e-12 {
		t.Fatalf("third annualized return = %.15f", thirdAnnualized)
	}
}

func TestMultiMarketReturnScoreRejectsUndefinedGrowth(t *testing.T) {
	if _, _, ok := multiMarketReturnScore(-1, 1); ok {
		t.Fatal("total loss must be undefined")
	}
	if _, _, ok := multiMarketReturnScore(0.1, 0); ok {
		t.Fatal("zero duration must be undefined")
	}
}

func TestAggregateMultiMarketDrawdownUsesMaximumAndFatalThreshold(t *testing.T) {
	maxDrawdown, fatal := aggregateMultiMarketDrawdown(0, 0.42)
	if maxDrawdown != 0.42 || fatal {
		t.Fatalf("first aggregate = %.2f fatal=%v, want 0.42 false", maxDrawdown, fatal)
	}
	maxDrawdown, fatal = aggregateMultiMarketDrawdown(maxDrawdown, 0.37)
	if maxDrawdown != 0.42 || fatal {
		t.Fatalf("second aggregate = %.2f fatal=%v, want 0.42 false", maxDrawdown, fatal)
	}
	maxDrawdown, fatal = aggregateMultiMarketDrawdown(maxDrawdown, 0.88)
	if maxDrawdown != 0.88 || !fatal {
		t.Fatalf("fatal aggregate = %.2f fatal=%v, want 0.88 true", maxDrawdown, fatal)
	}
}
