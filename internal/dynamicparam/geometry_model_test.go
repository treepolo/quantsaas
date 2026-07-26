package dynamicparam

import (
	"math"
	"testing"
)

func TestGeometryModelProducesNonNegativeJointRegions(t *testing.T) {
	bars := geometryBars(
		[4]float64{10, 12, 9, 11}, [4]float64{11, 14, 10, 12}, [4]float64{12, 15, 10, 13},
		[4]float64{13, 17, 11, 14}, [4]float64{14, 18, 12, 15}, [4]float64{15, 20, 13, 16},
		[4]float64{16, 21, 14, 17}, [4]float64{17, 23, 15, 18}, [4]float64{18, 24, 16, 19},
		[4]float64{19, 25, 17, 20}, [4]float64{20, 27, 18, 21}, [4]float64{21, 28, 19, 22},
	)
	samples, err := BuildGeometrySamples(bars, 3, HorizonOneDay)
	if err != nil {
		t.Fatal(err)
	}
	model, err := TrainGeometryModel(samples, HorizonOneDay, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	distribution, err := model.Predict(samples[len(samples)-1].Feature)
	if err != nil {
		t.Fatal(err)
	}
	if len(distribution.Regions) != 2 {
		t.Fatalf("regions = %d, want 2", len(distribution.Regions))
	}
	total := 0.0
	for _, region := range distribution.Regions {
		if region.AreaLower < 0 {
			t.Fatalf("negative area lower bound: %v", region.AreaLower)
		}
		total += region.Probability
	}
	if total < 0.999999 || total > 1.000001 {
		t.Fatalf("probability total = %v", total)
	}
	if math.IsNaN(model.Report.JointNLL) || math.IsInf(model.Report.JointNLL, 0) {
		t.Fatalf("joint nll = %v", model.Report.JointNLL)
	}
}

func TestGeometryModelRejectsUnsupportedRegionCount(t *testing.T) {
	if _, err := TrainGeometryModel(make([]GeometrySample, 12), HorizonOneDay, 3, 4); err == nil {
		t.Fatal("expected unsupported region count")
	}
}

func TestGeometryTailUnderflowIsScored(t *testing.T) {
	model := GeometryModel{
		SchemaVersion: GeometryModelSchemaVersion,
		Horizon:       HorizonOneDay,
		Regions: []GeometryRegionModel{{
			Weight: 1, AreaStdDev: 1, SlopeStdDev: 1,
		}},
	}
	samples := []GeometrySample{{
		Feature: GeometryPoint{SchemaVersion: GeometrySchemaVersion, Available: true},
		Target:  GeometryTarget{SchemaVersion: GeometrySchemaVersion, Available: true, CoverageArea: 1e10, TrendSlope: 1e10},
	}}
	if _, err := GeometryJointNLL(model, samples); err != nil {
		t.Fatalf("finite tail probability should be scored instead of rejected: %v", err)
	}
}
