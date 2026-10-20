package main

import (
	"math/rand"
	"testing"
)

// Issue 38 — climate as G×E.

func TestGxEFounderCorrelationMatchesTarget(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	pop := makeInitialPopulation(600, 1000, rng)
	effects := makeEffects(1000, 200, rng)
	for _, rg := range []float64{0.3, 0.7} {
		g := buildGxE(effects, &ClimateScenario{Mode: "drought_terminal", Severity: 1, RG: rg}, rand.New(rand.NewSource(9)))
		r := pearson(geneticValues(pop, effects), geneticValues(pop, g.stress))
		if r < rg-0.15 || r > rg+0.15 {
			t.Errorf("rg %.1f: founder correlation %.3f", rg, r)
		}
	}
	if buildGxE(effects, &ClimateScenario{Mode: "normal"}, rng) != nil {
		t.Fatal("normal mode must not build a G×E model")
	}
}

func gxeRun(t *testing.T, sc *ClimateScenario, mutate func(*SimRequest)) map[string]FinalStats {
	t.Helper()
	req := SimRequest{Seed: 13, PopulationSize: 200, Markers: 800, QTLCount: 60, Generations: 15, SelectionPercent: 15, Heritability: 0.5, StrategySet: "advanced", Replicates: 3, Climate: sc}
	if mutate != nil {
		mutate(&req)
	}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]FinalStats{}
	for _, s := range resp.Strategies {
		out[s.Code] = s.Final
	}
	return out
}

// With a weak genetic correlation and frequent stress, selecting in the
// target mix of seasons must beat selecting in normal-season nurseries on
// target-season gain — a ranking a uniform penalty could never change.
func TestTargetEnvironmentSelectionWinsUnderStrongGxE(t *testing.T) {
	f := gxeRun(t, &ClimateScenario{Mode: "combined_postflowering", Severity: 1, Frequency: 0.7, RG: 0.2}, nil)
	te, ph := f["target_env"], f["phenotype"]
	if te.GeneticGain <= ph.GeneticGain {
		t.Fatalf("target-environment selection %.3f should beat nursery truncation %.3f under rg = 0.2", te.GeneticGain, ph.GeneticGain)
	}
	if _, ok := gxeRun(t, nil, func(r *SimRequest) { r.Replicates = 1; r.Generations = 3 })["target_env"]; ok {
		t.Fatal("target_env must only appear with a climate scenario")
	}
}

// The ancestral advantage under stress must come from the lines' genotypes:
// with tolerance 0 they carry no stress-adapted alleles and gain nothing.
func TestAncestralStressAdvantageComesFromGenotypes(t *testing.T) {
	sc := &ClimateScenario{Mode: "drought_terminal", Severity: 1.2, Frequency: 0.8, RG: 0.3}
	run := func(tol float64) float64 {
		return gxeRun(t, sc, func(r *SimRequest) { r.AncestralIntroPercent = 20; r.AncestralStressTolerance = tol })["ancestral_introgression"].GeneticGain
	}
	none, full := run(1e-9), run(1)
	if full <= none {
		t.Fatalf("stress-adapted ancestral lines (t=1) should outperform non-adapted ones (t≈0): %.3f vs %.3f", full, none)
	}
}
