package main

import (
	"math"
	"math/rand"
	"testing"
)

// Issue 36 — optimum contribution selection.

func TestOCSContributionsOnUnrelatedFounders(t *testing.T) {
	// Unrelated, non-inbred founders: F = I/2, so c'Fc = Σc²/2 and the
	// constraint c'Fc = ΔF fixes Σc² = 2ΔF.
	const n, target = 60, 0.02
	F := make([][]float64, n)
	g := make([]float64, n)
	for i := range F {
		F[i] = make([]float64, n)
		F[i][i] = 0.5
		g[i] = float64(n-i) / n
	}
	c := ocsContributions(g, F, target)
	var sum, sq float64
	for i, v := range c {
		if v < 0 {
			t.Fatalf("negative contribution %v at %d", v, i)
		}
		sum += v
		sq += v * v
	}
	if math.Abs(sum-1) > 1e-6 {
		t.Fatalf("contributions must sum to 1, got %v", sum)
	}
	if math.Abs(sq/2-target) > 1e-3 {
		t.Fatalf("c'Fc = %.5f, want target %.5f", sq/2, target)
	}
	if c[0] <= c[10] {
		t.Fatal("a better candidate must not get a smaller contribution")
	}
}

func TestAllocateGametesExactAndNoSelfing(t *testing.T) {
	ids := []int{3, 7, 9, 12}
	c := []float64{0.4, 0.3, 0.2, 0.1}
	g := allocateGametes(ids, c, 50, rand.New(rand.NewSource(1)))
	if len(g) != 100 {
		t.Fatalf("expected 2·50 gametes, got %d", len(g))
	}
	count := map[int]int{}
	for i, p := range g {
		count[p]++
		if i%2 == 1 && g[i-1] == p {
			t.Fatalf("self-pair at offspring %d", i/2)
		}
	}
	if count[3] != 40 || count[7] != 30 || count[9] != 20 || count[12] != 10 {
		t.Fatalf("gamete counts must follow contributions exactly, got %v", count)
	}
}

func ocsRun(t *testing.T, extra func(*SimRequest)) map[string]FinalStats {
	t.Helper()
	req := SimRequest{Seed: 5, PopulationSize: 200, Markers: 400, QTLCount: 40, Generations: 15, SelectionPercent: 10, Heritability: 0.4, StrategySet: "advanced", Replicates: 3}
	extra(&req)
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

func TestOCSHoldsTargetDeltaF(t *testing.T) {
	for _, target := range []float64{0.005, 0.01, 0.02} {
		f := ocsRun(t, func(r *SimRequest) { r.TargetDeltaF = target })["ocs"]
		if rel := math.Abs(f.MeanDeltaF-target) / target; rel > 0.3 {
			t.Errorf("target ΔF %.3f: realised %.4f (%.0f%% off)", target, f.MeanDeltaF, 100*rel)
		}
	}
}

// Under mass selection on phenotype, OCS at a given ΔF should match
// truncation selection tuned to the same ΔF (measured: 7.21 vs 7.16 σA at
// ≈1%). This guards against a regression that wastes the ΔF budget.
func TestOCSGainComparableToTruncationAtEqualDeltaF(t *testing.T) {
	ocs := ocsRun(t, func(r *SimRequest) { r.TargetDeltaF = 0.01 })["ocs"]
	trunc := ocsRun(t, func(r *SimRequest) { r.SelectionPercent = 30 })["phenotype"]
	if math.Abs(trunc.MeanDeltaF-ocs.MeanDeltaF) > 0.005 {
		t.Fatalf("comparison needs similar ΔF: OCS %.4f vs truncation %.4f", ocs.MeanDeltaF, trunc.MeanDeltaF)
	}
	if ocs.GeneticGainSigma < 0.9*trunc.GeneticGainSigma {
		t.Fatalf("OCS gain %.2f σA is below 90%% of truncation %.2f σA at similar ΔF", ocs.GeneticGainSigma, trunc.GeneticGainSigma)
	}
}
