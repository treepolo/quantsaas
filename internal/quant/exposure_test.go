package quant

import (
	"math"
	"testing"
)

func TestActualExposureWeight(t *testing.T) {
	tests := []struct {
		name     string
		quantity float64
		price    float64
		equity   float64
		want     float64
	}{
		{name: "partial", quantity: 5, price: 100, equity: 1000, want: 0.5},
		{name: "full", quantity: 10, price: 100, equity: 1000, want: 1},
		{name: "clip", quantity: 20, price: 100, equity: 1000, want: 1},
		{name: "zero price", quantity: 5, price: 0, equity: 1000, want: 0},
		{name: "zero equity", quantity: 5, price: 100, equity: 0, want: 0},
		{name: "nan", quantity: math.NaN(), price: 100, equity: 1000, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ActualExposureWeight(tt.quantity, tt.price, tt.equity); math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("weight = %f, want %f", got, tt.want)
			}
		})
	}
}
