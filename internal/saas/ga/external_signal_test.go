package ga

import (
	"testing"

	"quantsaas/internal/quant"
)

func TestNormalizeGeneDisablesExternalSignalWithoutIndicators(t *testing.T) {
	e := SigmoidDCAEvolvable{}
	gene := quant.DefaultSeedChromosome
	gene.ExternalSignalWeight = 1.5

	disabled := e.NormalizeGene(gene, GeneOptions{}).(quant.Chromosome)
	if disabled.ExternalSignalWeight != 0 {
		t.Fatalf("external signal weight = %.4f, want 0 without indicators", disabled.ExternalSignalWeight)
	}

	enabled := e.NormalizeGene(gene, GeneOptions{EnableExternalSignal: true, IndicatorSeriesIDs: []string{"CREDIT_SPREAD"}}).(quant.Chromosome)
	if enabled.ExternalSignalWeight == 0 {
		t.Fatal("external signal weight should be preserved when indicators are enabled")
	}
}
