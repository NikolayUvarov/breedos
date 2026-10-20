package main

import (
	"testing"
)

// Issue 35 — time axis.

func timeAxisBase() SimRequest {
	return SimRequest{Seed: 11, PopulationSize: 200, Markers: 500, QTLCount: 40, Generations: 10, SelectionPercent: 10, Heritability: 0.4, StrategySet: "advanced", Replicates: 3}
}

func TestTimeAxisWithEqualCyclesMatchesGenerationsOnly(t *testing.T) {
	base, err := runSimulation(timeAxisBase())
	if err != nil {
		t.Fatal(err)
	}
	req := timeAxisBase()
	req.CycleYears, req.GenomicCycleYears = 4, 4
	timed, err := runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range base.Strategies {
		ts := timed.Strategies[i]
		if s.Final.GeneticGain != ts.Final.GeneticGain || s.Final.Inbreeding != ts.Final.Inbreeding {
			t.Fatalf("%s: equal cycles must not change results (gain %v vs %v, F %v vs %v)", s.Code, s.Final.GeneticGain, ts.Final.GeneticGain, s.Final.Inbreeding, ts.Final.Inbreeding)
		}
		if ts.Final.Years != 40 || ts.Metrics[len(ts.Metrics)-1].Year != 40 {
			t.Fatalf("%s: horizon should be 10 × 4 = 40 years, got %v", s.Code, ts.Final.Years)
		}
	}
}

func TestShorterGenomicCycleWinsPerYear(t *testing.T) {
	req := timeAxisBase()
	req.CycleYears, req.GenomicCycleYears = 4, 2
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	gs, ph := findByCode(resp.Strategies, "genomic"), findByCode(resp.Strategies, "phenotype")
	if gs == nil || ph == nil {
		t.Fatal("genomic and phenotype strategies expected in the advanced set")
	}
	if g, p := len(gs.Metrics)-1, len(ph.Metrics)-1; g != 2*p {
		t.Fatalf("GS on a 2-year cycle should run twice the generations of a 4-year cycle: %d vs %d", g, p)
	}
	if gs.Final.Years != ph.Final.Years {
		t.Fatalf("strategies must share the calendar horizon: %v vs %v", gs.Final.Years, ph.Final.Years)
	}
	if gs.Final.GainSigmaPerYear <= ph.Final.GainSigmaPerYear {
		t.Fatalf("equal per-cycle accuracy, half the cycle: GS gain/yr %.3f should beat phenotype %.3f", gs.Final.GainSigmaPerYear, ph.Final.GainSigmaPerYear)
	}
	if gs.Final.DeltaFPerYear <= ph.Final.DeltaFPerYear {
		t.Fatalf("faster cycling should also raise ΔF per year: %.4f vs %.4f", gs.Final.DeltaFPerYear, ph.Final.DeltaFPerYear)
	}
}

func TestTimeAxisValidation(t *testing.T) {
	req := timeAxisBase()
	normalizeRequest(&req)
	req.GenomicCycleYears = 2
	if err := validateRequest(req, 9); err == nil {
		t.Fatal("genomic_cycle_years without cycle_years must be rejected")
	}
	req.CycleYears, req.GenomicCycleYears, req.Generations = 10, 0.1, 30
	if err := validateRequest(req, 9); err == nil {
		t.Fatal("a genomic cycle that needs more than 200 generations must be rejected")
	}
}
