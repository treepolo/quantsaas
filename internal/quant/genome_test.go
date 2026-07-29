package quant

import "testing"

func TestValidateChromosomeRejectsInvalidForceThresholdOrder(t *testing.T) {
	c := DefaultSeedChromosome
	c.ForceFullThreshold = 0.60
	c.ForceEmptyThreshold = 0.80

	if err := ValidateChromosome(c); err == nil {
		t.Fatal("expected invalid force threshold order to fail")
	}
}

func TestClampChromosomeAllowsHighRebalanceThreshold(t *testing.T) {
	c := DefaultSeedChromosome
	c.RebalanceThreshold = 0.75

	clamped := ClampChromosome(c)

	if clamped.RebalanceThreshold != 0.75 {
		t.Fatalf("rebalance threshold = %.2f, want 0.75", clamped.RebalanceThreshold)
	}
}

func TestClampChromosomePreservesDisabledMicroReserve(t *testing.T) {
	c := DefaultSeedChromosome
	c.MicroReservePct = 0

	if got := ClampChromosome(c).MicroReservePct; got != 0 {
		t.Fatalf("disabled micro reserve = %.2f, want 0", got)
	}
}
