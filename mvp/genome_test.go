package main

import (
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"testing"
)

// v0.8.0 — tests that pin the linked-genome model to textbook expectations.

func TestRecombinationFractionMatchesHaldane(t *testing.T) {
	// One 1-Morgan chromosome, two markers at 0.25 M and 0.75 M (d = 0.5 M).
	gm := buildSyntheticGenomeMap(2, 1, 1.0)
	parent := gm.newOrganism()
	parent.geno[0], parent.geno[1] = 1, 1 // coupling phase: both counted alleles on strand A
	rng := rand.New(rand.NewSource(11))
	xo := make([]float64, 0, 8)
	const draws = 40000
	recombinant := 0
	for i := 0; i < draws; i++ {
		child := gm.newOrganism()
		gm.transmit(&parent, &child, 0, 0, rng, xo)
		if child.geno[0] != child.geno[1] {
			recombinant++
		}
	}
	got := float64(recombinant) / draws
	want := (1 - math.Exp(-2*0.5)) / 2 // Haldane: r = (1 − e^(−2d)) / 2 ≈ 0.316
	if math.Abs(got-want) > 0.012 {
		t.Fatalf("recombination fraction at d = 0.5 M: got %.4f, want %.4f (Haldane)", got, want)
	}
}

func TestMarkersOnDifferentChromosomesSegregateIndependently(t *testing.T) {
	gm := buildSyntheticGenomeMap(2, 2, 1.0)
	parent := gm.newOrganism()
	parent.geno[0], parent.geno[1] = 1, 1
	rng := rand.New(rand.NewSource(12))
	xo := make([]float64, 0, 8)
	const draws = 40000
	recombinant := 0
	for i := 0; i < draws; i++ {
		child := gm.newOrganism()
		gm.transmit(&parent, &child, 0, 0, rng, xo)
		if child.geno[0] != child.geno[1] {
			recombinant++
		}
	}
	if got := float64(recombinant) / draws; math.Abs(got-0.5) > 0.012 {
		t.Fatalf("unlinked markers: recombination fraction %.4f, want 0.5", got)
	}
}

func TestFoundersHaveZeroIBDInbreeding(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	gm := buildSyntheticGenomeMap(300, 5, 1.0)
	pop := makeInitialPopulation(50, 300, rng)
	initFounders(pop, gm, rng)
	if f := ibdCoancestry(pop); f != 0 {
		t.Fatalf("founder coancestry should be 0, got %v", f)
	}
}

// Under no selection with N parents, IBD inbreeding should follow drift:
// F_t ≈ 1 − (1 − 1/2N)^t.
func TestNeutralDriftInbreedingMatchesTheory(t *testing.T) {
	const n, gens, reps = 100, 20, 12
	gm := buildSyntheticGenomeMap(400, 10, 1.0)
	var sum float64
	for r := 0; r < reps; r++ {
		rng := rand.New(rand.NewSource(int64(100 + r)))
		pop := makeInitialPopulation(n, 400, rng)
		initFounders(pop, gm, rng)
		parents := make([]int, n)
		for i := range parents {
			parents[i] = i
		}
		for g := 0; g < gens; g++ {
			pop = makeNextGeneration(pop, parents, gm, 0, rng, "random")
		}
		sum += ibdCoancestry(pop)
	}
	got := sum / reps
	want := 1 - math.Pow(1-1.0/(2*n), gens)
	if math.Abs(got-want)/want > 0.2 {
		t.Fatalf("neutral F after %d generations with N=%d: got %.4f, want ≈ %.4f", gens, n, got, want)
	}
}

// Regression for the v0.7.x artifact: selecting on marker novelty pushed
// heterozygosity above its starting value, so the old 1 − He/He₀
// "inbreeding" read 0 for the diversity-preserving strategy. IBD
// inbreeding cannot be lowered that way: it must stay near the drift
// expectation for the strategy's parent count.
func TestDiversityStrategyCannotGameInbreeding(t *testing.T) {
	req := SimRequest{Seed: 42, PopulationSize: 200, Markers: 600, QTLCount: 40, Generations: 20, SelectionPercent: 20, Heritability: 0.5, StrategySet: "core", Replicates: 3}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	div := findByCode(resp.Strategies, "diversity")
	if div == nil {
		t.Fatal("diversity strategy missing")
	}
	parents := float64(div.Final.EffectiveParents)
	drift := 1 - math.Pow(1-1/(2*parents), float64(req.Generations))
	if div.Final.Inbreeding < 0.5*drift {
		t.Fatalf("diversity strategy F = %.4f with %v parents; drift alone predicts ≈ %.4f", div.Final.Inbreeding, parents, drift)
	}
	if div.Final.MeanDeltaF <= 0 || div.Final.Ne <= 0 || div.Final.Ne >= maxEffectiveNe {
		t.Fatalf("expected a finite Ne from ΔF, got ΔF=%v Ne=%v", div.Final.MeanDeltaF, div.Final.Ne)
	}
}

func TestGainSigmaIsGainOverFounderSigmaA(t *testing.T) {
	req := SimRequest{Seed: 5, PopulationSize: 120, Markers: 300, QTLCount: 30, Generations: 8, SelectionPercent: 20, Heritability: 0.4, StrategySet: "core", Replicates: 1}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range resp.Strategies {
		f := s.Final
		if f.GeneticGain == 0 {
			continue
		}
		ratio := f.GeneticGain / f.GeneticGainSigma
		first := s.Metrics[len(s.Metrics)-1]
		if math.Abs(first.GeneticGain/first.GainSigma-ratio) > 1e-2*math.Abs(ratio) {
			t.Fatalf("%s: gain/gain_sigma ratio differs between final and metrics", s.Code)
		}
		if ratio <= 0 {
			t.Fatalf("%s: σA0 implied by gain/gain_sigma must be positive, got %v", s.Code, ratio)
		}
	}
}

func TestEnvironmentalVarianceFixedFromFounders(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	pop := makeInitialPopulation(200, 300, rng)
	effects := makeEffects(300, 30, rng)
	req := SimRequest{Heritability: 0.25}
	m := newRunModel(req, buildSyntheticGenomeMap(300, 10, 1), pop, effects)
	_, vg := meanVar(geneticValues(pop, effects))
	if math.Abs(m.varEnv-3*vg) > 1e-9*vg {
		t.Fatalf("h²=0.25 → VE should be 3·VG0 = %v, got %v", 3*vg, m.varEnv)
	}
	// Default GS accuracy √h² → error variance VG0(1 − h²)/h² = VE.
	if math.Abs(m.gsErrVar-m.varEnv) > 1e-9*vg {
		t.Fatalf("default GS error variance should equal VE, got %v vs %v", m.gsErrVar, m.varEnv)
	}
	req.GenomicAccuracy = 0.8
	m = newRunModel(req, buildSyntheticGenomeMap(300, 10, 1), pop, effects)
	if want := vg * (1 - 0.64) / 0.64; math.Abs(m.gsErrVar-want) > 1e-9*vg {
		t.Fatalf("accuracy 0.8 → error variance %v, got %v", want, m.gsErrVar)
	}
}

func TestGenomeMapFromDatasetPositions(t *testing.T) {
	raw := "# Sites (chrom:pos), in order matching the m1..mN columns:\n" +
		"# 1:100, 1:2000, 1:4100, 2:50, 2:1050\n" +
		"accession_id,m1,m2,m3,m4,m5\n" +
		"a,0,1,2,1,0\nb,2,1,0,1,2\n"
	ds, err := parseDatasetCSV([]byte(raw), "sites.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.sites) != 5 {
		t.Fatalf("expected 5 parsed sites, got %d", len(ds.sites))
	}
	for _, n := range ds.sourceNotes {
		if strings.Contains(n, "1:100") {
			t.Fatal("site list should not be copied into sourceNotes")
		}
	}
	gm := buildGenomeMap(SimRequest{Markers: 5, ChromosomeLengthM: 1}, ds)
	if len(gm.lengthM) != 2 {
		t.Fatalf("expected 2 chromosomes from positions, got %d", len(gm.lengthM))
	}
	// Spans 4001 bp and 1001 bp at one genome-wide rate averaging 1 M.
	if r := gm.lengthM[0] / gm.lengthM[1]; math.Abs(r-4001.0/1001.0) > 1e-9 {
		t.Fatalf("chromosome lengths should scale with physical span, ratio %v", r)
	}
	if math.Abs(gm.lengthM[0]+gm.lengthM[1]-2) > 1e-9 {
		t.Fatalf("total map length should be 2 M, got %v", gm.lengthM[0]+gm.lengthM[1])
	}
}

func TestHeaderSitesAndUnsortedFallback(t *testing.T) {
	ds, err := parseDatasetCSV([]byte("id,chr1:10,chr1:20,chr2:5\nx,0,1,2\n"), "h.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.sites) != 3 || ds.sites[2].chrom != "chr2" {
		t.Fatalf("header chrom:pos labels should parse, got %+v", ds.sites)
	}
	if gm := buildGenomeMapFromSites([]markerSite{{"1", 50}, {"1", 10}}, 1); gm != nil {
		t.Fatal("unsorted positions must fall back to the synthetic map")
	}
	if gm := buildGenomeMapFromSites([]markerSite{{"1", 1}, {"2", 1}, {"1", 5}}, 1); gm != nil {
		t.Fatal("a chromosome split into two blocks must fall back to the synthetic map")
	}
}

func TestSubsampleCoversAllColumns(t *testing.T) {
	raw := []byte("accession_id,m1,m2,m3,m4,m5,m6,m7,m8,m9,m10\nA,0,0,0,0,0,0,0,0,0,2\nB,0,0,0,0,0,0,0,0,0,2\n")
	ds, err := parseDatasetCSV(raw, "stride.csv")
	if err != nil {
		t.Fatal(err)
	}
	sub := subsampleDataset(ds, 2, 5, rand.New(rand.NewSource(1)))
	// Stride 2 over 10 columns picks m1, m3, m5, m7, m9 — never only the first five.
	if sub.markerCount != 5 {
		t.Fatalf("expected 5 markers, got %d", sub.markerCount)
	}
}

// Issue 34 — selection_percent unit ambiguity.
func TestSelectionPercentFractionRejectedWithUnitHint(t *testing.T) {
	req := SimRequest{PopulationSize: 200, Markers: 100, QTLCount: 10, Generations: 5, SelectionPercent: 0.2, Heritability: 0.5, StrategySet: "core", Replicates: 1, InbreedingLimit: 0.25, DiversityLossLimit: 0.3}
	err := validateRequest(req, 4)
	if err == nil || !strings.Contains(err.Error(), "0–100 scale") || !strings.Contains(err.Error(), "0.4 of 200") || !strings.Contains(err.Error(), "send 20") {
		t.Fatalf("0.2 should be rejected with the worked unit hint, got %v", err)
	}
	req.SelectionPercent = 120
	if err := validateRequest(req, 4); err == nil {
		t.Fatal("120 should be rejected")
	}
	req.SelectionPercent = 20
	if err := validateRequest(req, 4); err != nil {
		t.Fatalf("20 should be accepted, got %v", err)
	}
}

func TestExplicitZeroSelectionPercentRejected(t *testing.T) {
	if _, err := decodeSimRequest(strings.NewReader(`{"selection_percent": 0}`)); err == nil {
		t.Fatal("explicit selection_percent: 0 should be rejected")
	}
	req, err := decodeSimRequest(strings.NewReader(`{"population_size": 100}`))
	if err != nil {
		t.Fatalf("omitted selection_percent should decode, got %v", err)
	}
	normalizeRequest(&req)
	if req.SelectionPercent != 10 {
		t.Fatalf("omitted selection_percent should default to 10, got %v", req.SelectionPercent)
	}
}

func TestFlooredParentPoolMarksDegenerateRun(t *testing.T) {
	req := SimRequest{Seed: 3, PopulationSize: 10, Markers: 60, QTLCount: 6, Generations: 3, SelectionPercent: 5, Heritability: 0.5, StrategySet: "core", Replicates: 1}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.DegenerateRun {
		t.Fatal("5% of 10 floors the parent pool to 2 — degenerate_run should be true")
	}
	if len(resp.Notes) == 0 || !strings.HasPrefix(resp.Notes[0], "⚠ DEGENERATE RUN") {
		t.Fatalf("degenerate warning must be note #0, got %q", resp.Notes[0])
	}
	b, _ := json.Marshal(resp)
	if !strings.Contains(string(b), `"degenerate_run":true`) {
		t.Fatal("degenerate_run must be serialised")
	}
	req.PopulationSize, req.SelectionPercent = 100, 20
	resp, err = runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.DegenerateRun || strings.HasPrefix(resp.Notes[0], "⚠") {
		t.Fatal("a normal run must not be flagged degenerate")
	}
}

func TestBaselinesNeverRecommended(t *testing.T) {
	// Harsh selection: every selecting strategy burns diversity, which used
	// to let the neutral baseline win the risk-adjusted ranking.
	req := SimRequest{Seed: 7, PopulationSize: 100, Markers: 400, QTLCount: 30, Generations: 30, SelectionPercent: 5, Heritability: 0.25, StrategySet: "advanced", Replicates: 3}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	d := resp.Decision
	for _, code := range []string{d.BestRiskAdjustedCode, d.LowestRiskCode} {
		if isBaselineStrategy(code) {
			t.Fatalf("baseline %q must not be recommended", code)
		}
	}
	for _, s := range resp.Strategies {
		if isBaselineStrategy(s.Code) && s.Final.DecisionRank <= len(resp.Strategies)-2 {
			t.Fatalf("baseline %q ranked #%d; baselines rank after selecting strategies", s.Code, s.Final.DecisionRank)
		}
	}
}

func TestDairyRunsCarryEducationalLimitation(t *testing.T) {
	req := SimRequest{Seed: 2, Dataset: "holstein_synthetic", PopulationSize: 100, Markers: 300, QTLCount: 20, Generations: 3, SelectionPercent: 12, Heritability: 0.36, StrategySet: "core", Replicates: 1}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range resp.Decision.Limitations {
		if strings.HasPrefix(l, "Dairy runs are educational") {
			found = true
		}
	}
	if !found {
		t.Fatal("holstein_synthetic runs must state the educational limitation")
	}
}
