package dynamicparam

import (
	"fmt"

	"quantsaas/internal/backtestcore"
)

func ParameterProvider(path MaterializedPath, modelVersion, policyVersion string) (backtestcore.ParameterProvider, error) {
	if path.SchemaVersion != PredictionSchemaVersion || path.ModelArtifactHash == "" || path.PolicyHash == "" {
		return nil, fmt.Errorf("invalid materialized dynamic parameter path")
	}
	byTime := make(map[int64]EffectiveSnapshot, len(path.Diagnostics))
	for _, diagnostic := range path.Diagnostics {
		if diagnostic.TimeMs <= 0 {
			return nil, fmt.Errorf("materialized diagnostic is missing time")
		}
		byTime[diagnostic.TimeMs] = diagnostic.Effective
	}
	return func(context backtestcore.ParameterContext) (backtestcore.EffectiveParameters, error) {
		snapshot, ok := byTime[context.Bar.OpenTime]
		if !ok {
			return backtestcore.EffectiveParameters{}, fmt.Errorf("materialized effective parameters are missing")
		}
		fallback := ""
		if len(snapshot.FallbackEvents) > 0 {
			fallback = snapshot.FallbackEvents[0]
		}
		return backtestcore.EffectiveParameters{Chromosome: snapshot.Chromosome, Metadata: backtestcore.ParameterMetadata{
			StructureState: snapshot.State.StateType, OccurrenceID: snapshot.State.OccurrenceID,
			ModelVersion: modelVersion, PolicyVersion: policyVersion, MaterializedHash: path.PredictionHash, FallbackEvent: fallback,
		}}, nil
	}, nil
}
