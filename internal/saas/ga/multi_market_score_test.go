package ga

import (
	"math"
	"testing"
)

func TestMultiMarketReturnScoreUsesSumOfLogTotalReturns(t *testing.T) {
	first, firstAnnualized, ok := multiMarketReturnScore(0.21, 2)
	if !ok {
		t.Fatal("first market return should be defined")
	}
	second, secondAnnualized, ok := multiMarketReturnScore(-0.10, 1)
	if !ok {
		t.Fatal("second market return should be defined")
	}
	want := math.Log(1.21) + math.Log(0.90)
	if math.Abs((first+second)-want) > 1e-12 {
		t.Fatalf("score = %.15f, want %.15f", first+second, want)
	}
	if math.Abs(firstAnnualized-(math.Sqrt(1.21)-1)) > 1e-12 {
		t.Fatalf("first annualized return = %.15f", firstAnnualized)
	}
	if math.Abs(secondAnnualized-(-0.10)) > 1e-12 {
		t.Fatalf("second annualized return = %.15f", secondAnnualized)
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
