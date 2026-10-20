package main

// v0.7.21 — Issue 18. Multi-trait selection engine. Implements a parallel
// simulator path that runs when req.Traits is set. Single-trait runs
// continue through the existing path in main.go untouched — this file is
// strictly additive.
//
// Approach:
//
//   1. Build per-trait effects via Cholesky decomposition of the genetic-
//      correlation matrix. For each marker, draw T independent standard
//      normals and transform: e[t][m] = scale[t] · Σ_k L[t,k] · z[k].
//      Marker is a QTL for trait t if its absolute effect ranks in the
//      top QTLCount[t] for that trait; otherwise zero.
//
//   2. Per-organism per-trait genetic value g_t = Σ_m geno[m] · e[t][m].
//      Phenotype p_t = g_t + N(0, σ_E²) where σ_E² is the environmental
//      variance derived from h²[t] and the founder variance of g_t, fixed
//      from generation 0 on (v0.8.0; it used to track the current VG).
//
//   3. Selection index I_i = Σ_t w_t · (p_{t,i} − mean_t) / sd_t.
//      Truncation select the top fraction by I.
//
//   4. Recombination, mutation, and mating use the existing helpers
//      (makeNextGeneration, alleleFreq, etc.). Per-trait metrics are
//      tracked alongside the existing per-generation MetricPoint.
//
// Strategy rules:
//
//   "no_selection" / "random" / "neutral" → unchanged behaviour.
//   All other rule codes use index-based truncation selection in this
//   MVP. OCS-like with similarity penalty is deferred to a follow-up.

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
)

// validateMultiTraitRequest checks the new schema fields before the
// simulator runs. Errors here surface to the API as 400 with a descriptive
// message; the operator can correct the form and re-submit.
func validateMultiTraitRequest(req SimRequest) error {
	T := len(req.Traits)
	if T == 0 {
		return errors.New("multi-trait branch requires len(traits) > 0")
	}
	for i, t := range req.Traits {
		if t.Name == "" {
			return fmt.Errorf("trait %d: name must be set", i)
		}
		if t.Heritability < 0 || t.Heritability > 1 {
			return fmt.Errorf("trait %q: heritability must be in [0, 1], got %v", t.Name, t.Heritability)
		}
		if t.QTLCount < 1 {
			return fmt.Errorf("trait %q: qtl_count must be ≥ 1, got %d", t.Name, t.QTLCount)
		}
		if t.QTLCount > req.Markers {
			return fmt.Errorf("trait %q: qtl_count (%d) exceeds markers (%d)", t.Name, t.QTLCount, req.Markers)
		}
		if t.EffectScale <= 0 {
			return fmt.Errorf("trait %q: effect_scale must be > 0, got %v", t.Name, t.EffectScale)
		}
	}
	// Trait names must be unique — they key DecisionSummary.PerTraitGain.
	seen := make(map[string]bool, T)
	for _, t := range req.Traits {
		if seen[t.Name] {
			return fmt.Errorf("duplicate trait name %q — names must be unique", t.Name)
		}
		seen[t.Name] = true
	}
	// Genetic-correlation matrix: square T×T, |r|≤1, diagonals == 1.
	C := req.GeneticCorrelations
	if len(C) != T {
		return fmt.Errorf("genetic_correlations rows = %d, expected %d (one per trait)", len(C), T)
	}
	for i, row := range C {
		if len(row) != T {
			return fmt.Errorf("genetic_correlations row %d has length %d, expected %d (square matrix)", i, len(row), T)
		}
		for j, v := range row {
			if i == j {
				if math.Abs(v-1.0) > 1e-9 {
					return fmt.Errorf("genetic_correlations[%d][%d] must be 1.0 (diagonal), got %v", i, j, v)
				}
				continue
			}
			if v < -1 || v > 1 {
				return fmt.Errorf("genetic_correlations[%d][%d] = %v out of [-1, 1]", i, j, v)
			}
			if math.Abs(v-C[j][i]) > 1e-9 {
				return fmt.Errorf("genetic_correlations is not symmetric at [%d][%d] = %v vs [%d][%d] = %v", i, j, v, j, i, C[j][i])
			}
		}
	}
	if req.Generations < 1 || req.Generations > 200 {
		return errors.New("generations must be between 1 and 200")
	}
	return nil
}

// choleskyDecomp computes the lower-triangular Cholesky factor L such that
// L · L^T = C. Returns an error if C is not positive-(semi-)definite to
// the required precision. General N×N Cholesky-Banachiewicz algorithm —
// O(N³) is fine; N is the trait count and stays in single digits.
func choleskyDecomp(C [][]float64) ([][]float64, error) {
	n := len(C)
	L := make([][]float64, n)
	for i := range L {
		L[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := C[i][j]
			for k := 0; k < j; k++ {
				sum -= L[i][k] * L[j][k]
			}
			if i == j {
				if sum < -1e-12 {
					return nil, fmt.Errorf("matrix not positive-definite at row %d (diagonal residual = %v)", i, sum)
				}
				if sum < 0 {
					sum = 0
				}
				L[i][j] = math.Sqrt(sum)
			} else {
				if L[j][j] < 1e-12 {
					return nil, fmt.Errorf("matrix not positive-definite at row %d (L[%d][%d] = %v near zero)", i, j, j, L[j][j])
				}
				L[i][j] = sum / L[j][j]
			}
		}
	}
	return L, nil
}

// makeMultiTraitEffects returns effects[trait][marker] with correlated
// effects across traits via Cholesky transform. For each marker, draws T
// independent standard normals, transforms by L. Then masks each trait's
// effects so only the top-QTLCount[t] markers (by |effect|) are non-zero
// for that trait.
func makeMultiTraitEffects(markers int, traits []TraitConfig, L [][]float64, rng *rand.Rand) [][]float64 {
	T := len(traits)
	// Stage 1: correlated raw effects at every marker.
	raw := make([][]float64, T)
	for t := 0; t < T; t++ {
		raw[t] = make([]float64, markers)
	}
	for m := 0; m < markers; m++ {
		z := make([]float64, T)
		for k := 0; k < T; k++ {
			z[k] = rng.NormFloat64()
		}
		for t := 0; t < T; t++ {
			var sum float64
			for k := 0; k <= t; k++ {
				sum += L[t][k] * z[k]
			}
			raw[t][m] = traits[t].EffectScale * sum
		}
	}
	// Stage 2: mask each trait to top-QTLCount markers by |effect|.
	for t := 0; t < T; t++ {
		idx := make([]int, markers)
		for i := range idx {
			idx[i] = i
		}
		abs := raw[t]
		sort.Slice(idx, func(a, b int) bool { return math.Abs(abs[idx[a]]) > math.Abs(abs[idx[b]]) })
		keep := make(map[int]bool, traits[t].QTLCount)
		for i := 0; i < traits[t].QTLCount && i < len(idx); i++ {
			keep[idx[i]] = true
		}
		for m := 0; m < markers; m++ {
			if !keep[m] {
				raw[t][m] = 0
			}
		}
	}
	return raw
}

// geneticValuesMultiTrait returns [trait][organism] genetic value matrix.
func geneticValuesMultiTrait(pop []organism, effects [][]float64) [][]float64 {
	T := len(effects)
	gv := make([][]float64, T)
	for t := 0; t < T; t++ {
		gv[t] = make([]float64, len(pop))
		for i, o := range pop {
			var sum float64
			for m, e := range effects[t] {
				if e == 0 {
					continue
				}
				sum += float64(o.geno[m]) * e
			}
			gv[t][i] = sum
		}
	}
	return gv
}

// selectionIndex computes per-individual weighted standardised index from
// per-trait phenotypes. Returns one float per individual; higher = better.
func selectionIndex(phen [][]float64, traits []TraitConfig) []float64 {
	T := len(phen)
	if T == 0 {
		return nil
	}
	N := len(phen[0])
	idx := make([]float64, N)
	for t := 0; t < T; t++ {
		mean, varT := meanVar(phen[t])
		sd := math.Sqrt(math.Max(varT, 1e-12))
		w := traits[t].SelectionWeight
		for i := 0; i < N; i++ {
			idx[i] += w * (phen[t][i] - mean) / sd
		}
	}
	return idx
}

// multiTraitModel is the multi-trait counterpart of runModel: one
// environmental variance and founder σA per trait (v0.8.0).
type multiTraitModel struct {
	genome  *genomeMap
	varEnv  []float64
	sigmaA0 []float64
}

func newMultiTraitModel(gm *genomeMap, founders []organism, effects [][]float64, traits []TraitConfig) multiTraitModel {
	mm := multiTraitModel{genome: gm, varEnv: make([]float64, len(traits)), sigmaA0: make([]float64, len(traits))}
	gv := geneticValuesMultiTrait(founders, effects)
	for t := range traits {
		_, varG0 := meanVar(gv[t])
		mm.varEnv[t] = environmentalVariance(varG0, traits[t].Heritability)
		mm.sigmaA0[t] = math.Sqrt(varG0)
	}
	return mm
}

// selectParentsMultiTrait selects parent indices by truncation on the
// weighted index. Honours strategy rule for the "no_selection" and
// "random" special cases (consistent with single-trait behaviour).
func selectParentsMultiTrait(pop []organism, effects [][]float64, traits []TraitConfig, req SimRequest, cfg strategyConfig, mm multiTraitModel, rng *rand.Rand) ([]int, []float64) {
	n := len(pop)
	if cfg.Rule == "no_selection" {
		parents := make([]int, n)
		for i := range parents {
			parents[i] = i
		}
		return parents, make([]float64, n)
	}
	baseCount := int(math.Round(float64(n) * req.SelectionPercent / 100.0))
	parentCount := int(math.Round(float64(baseCount) * cfg.ParentMultiplier))
	if parentCount < 2 {
		parentCount = 2
	}
	if parentCount > n {
		parentCount = n
	}
	if cfg.Rule == "random" {
		perm := rng.Perm(n)
		return append([]int(nil), perm[:parentCount]...), make([]float64, n)
	}
	// Per-trait phenotype = genetic value + environmental noise with the
	// variance fixed at generation 0.
	gvals := geneticValuesMultiTrait(pop, effects)
	T := len(traits)
	phen := make([][]float64, T)
	for t := 0; t < T; t++ {
		stdEnv := math.Sqrt(mm.varEnv[t])
		phen[t] = make([]float64, n)
		for i := 0; i < n; i++ {
			phen[t][i] = gvals[t][i] + rng.NormFloat64()*stdEnv
		}
	}
	idx := selectionIndex(phen, traits)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return idx[order[a]] > idx[order[b]] })
	parents := make([]int, parentCount)
	copy(parents, order[:parentCount])
	return parents, idx
}

// simulateMultiTraitStrategy runs one strategy × replicate trajectory.
// Per-trait metrics are computed alongside the aggregate MetricPoint.
func simulateMultiTraitStrategy(req SimRequest, cfg strategyConfig, mm multiTraitModel, pop []organism, effects [][]float64, traits []TraitConfig, baseFreq []float64, baseDiversity float64, baseMeansPerTrait []float64, rareUsefulAtStart []int, rng *rand.Rand) StrategyResult {
	T := len(traits)
	// Aggregate metric path uses the first trait as the "headline" trait
	// (consistent with how a single-trait run reports gain). Per-trait
	// trajectories are stored in PerTraitMetrics.
	primaryEffects := effects[0]
	primaryBaseMean := baseMeansPerTrait[0]

	metrics := make([]MetricPoint, 0, req.Generations+1)
	perTraitMetrics := make([][]MetricPoint, T)
	for t := 0; t < T; t++ {
		perTraitMetrics[t] = make([]MetricPoint, 0, req.Generations+1)
	}
	indexHistory := make([]float64, 0, req.Generations+1)

	appendGen := func(gen int, eff int) {
		// Aggregate metric on trait 0 (headline).
		metrics = append(metrics, computeMetrics(gen, pop, primaryEffects, baseFreq, baseDiversity, primaryBaseMean, mm.sigmaA0[0], rareUsefulAtStart, eff, lineBreeding(req)))
		// Per-trait MetricPoints — we only need a small subset for charts,
		// reuse computeMetrics with per-trait effects + baseline.
		for t := 0; t < T; t++ {
			perTraitMetrics[t] = append(perTraitMetrics[t], computeMetrics(gen, pop, effects[t], baseFreq, baseDiversity, baseMeansPerTrait[t], mm.sigmaA0[t], rareUsefulAtStart, eff, lineBreeding(req)))
		}
	}
	populateNeOnAggregate := func() {
		populateNeTrajectory(metrics)
		for t := 0; t < T; t++ {
			populateNeTrajectory(perTraitMetrics[t])
		}
	}

	appendGen(0, 0)
	cycle := cycleYearsFor(req, cfg)
	for gen := 1; gen <= generationsFor(req, cfg); gen++ {
		parents, idx := selectParentsMultiTrait(pop, effects, traits, req, cfg, mm, rng)
		var meanIdx float64
		if len(parents) > 0 && len(idx) > 0 {
			for _, p := range parents {
				meanIdx += idx[p]
			}
			meanIdx /= float64(len(parents))
		}
		indexHistory = append(indexHistory, round4(meanIdx))
		pop = makeNextGeneration(pop, parents, mm.genome, req.MutationRate, rng, cfg.MatingRule)
		deriveLines(pop, req.MatingSystem, req.SSDGenerations, mm.genome, req.MutationRate, rng)
		appendGen(gen, len(parents))
		metrics[len(metrics)-1].Year = round4(float64(gen) * cycle)
	}
	populateNeOnAggregate()

	finalMetric := metrics[len(metrics)-1]
	final := finalMetricToFinal(finalMetric)
	final.Replicates = 1
	final.RecommendedNext = recommendationFor(cfg.Code, final)
	return StrategyResult{
		Name:            cfg.Name,
		Code:            cfg.Code,
		Summary:         cfg.Summary,
		Replicates:      1,
		Metrics:         metrics,
		Final:           final,
		PerTraitMetrics: perTraitMetrics,
		SelectionIndex:  indexHistory,
	}
}

// v0.7.22 — Issue 23. N-D Pareto dominance. Generalises the single-trait
// dominates() (5 dimensions on FinalStats) by adding one dimension per
// trait gain. Bigger trait gain = better. Single-trait gain on `Final`
// is left alone; this function looks at PerTraitMetrics directly.
//
// "a dominates b" iff: a is ≥ on every dimension AND strictly > on at
// least one. Tolerance 1e-9 for floating-point equality.
func multiTraitDominates(a, b StrategyResult, traitCount int) bool {
	const tol = 1e-9
	betterOrEqual := true
	strictlyBetter := false
	gainAt := func(s StrategyResult, t int) float64 {
		if t >= len(s.PerTraitMetrics) || len(s.PerTraitMetrics[t]) == 0 {
			return 0
		}
		return s.PerTraitMetrics[t][len(s.PerTraitMetrics[t])-1].GeneticGain
	}
	for t := 0; t < traitCount; t++ {
		aT, bT := gainAt(a, t), gainAt(b, t)
		if aT < bT-tol {
			betterOrEqual = false
			break
		}
		if aT > bT+tol {
			strictlyBetter = true
		}
	}
	if !betterOrEqual {
		return false
	}
	// Non-trait dimensions: higher diversity better; lower inbreeding /
	// Pdiv / Prare better.
	if a.Final.Diversity < b.Final.Diversity-tol {
		return false
	}
	if a.Final.Inbreeding > b.Final.Inbreeding+tol {
		return false
	}
	if a.Final.ProbabilityDiversityCollapse > b.Final.ProbabilityDiversityCollapse+tol {
		return false
	}
	if a.Final.ProbabilityRareUsefulLoss > b.Final.ProbabilityRareUsefulLoss+tol {
		return false
	}
	if a.Final.Diversity > b.Final.Diversity+tol ||
		a.Final.Inbreeding < b.Final.Inbreeding-tol ||
		a.Final.ProbabilityDiversityCollapse < b.Final.ProbabilityDiversityCollapse-tol ||
		a.Final.ProbabilityRareUsefulLoss < b.Final.ProbabilityRareUsefulLoss-tol {
		strictlyBetter = true
	}
	return strictlyBetter
}

// annotateMultiTraitPareto overwrites ParetoOptimal flags using N-D
// dominance — replaces the single-trait result of annotateDecisionScores.
func annotateMultiTraitPareto(results []StrategyResult, traitCount int) {
	for i := range results {
		pareto := true
		for j := range results {
			if i == j {
				continue
			}
			if multiTraitDominates(results[j], results[i], traitCount) {
				pareto = false
				break
			}
		}
		results[i].ParetoOptimal = pareto
		results[i].Final.ParetoOptimal = pareto
	}
}

// runMultiTraitSimulation is the multi-trait entry point. Mirrors
// runSimulationWithCallbacks but uses the multi-trait simulator throughout.
func runMultiTraitSimulation(req SimRequest, progress progressFunc, snapshot snapshotFunc) (SimResponse, error) {
	reportProgress(progress, 2, "multi-trait branch")
	if err := validateMultiTraitRequest(req); err != nil {
		return SimResponse{}, err
	}
	// OCS (Issue 36) needs per-candidate coancestry and single-trait
	// scores; the multi-trait path does not run it.
	strategies := make([]strategyConfig, 0)
	for _, cfg := range buildStrategyConfigs(req) {
		if cfg.Rule != "ocs_target" && !cfg.SelectInTargetEnv {
			strategies = append(strategies, cfg)
		}
	}
	if err := validateRequest(req, len(strategies)); err != nil {
		return SimResponse{}, err
	}
	rng := rand.New(rand.NewSource(req.Seed))

	var initial []organism
	var datasetMeta *loadedDataset
	if datasetSelected(req.Dataset) {
		ds, err := loadDataset(req.Dataset)
		if err != nil {
			return SimResponse{}, fmt.Errorf("load dataset %q: %w", req.Dataset, err)
		}
		ds = subsampleDataset(ds, req.PopulationSize, req.Markers, rng)
		req.PopulationSize = len(ds.individuals)
		req.Markers = ds.markerCount
		datasetMeta = ds
		initial = clonePopulation(ds.individuals)
	} else {
		initial = makeInitialPopulation(req.PopulationSize, req.Markers, rng)
	}

	L, err := choleskyDecomp(req.GeneticCorrelations)
	if err != nil {
		return SimResponse{}, fmt.Errorf("genetic_correlations Cholesky: %w", err)
	}
	effects := makeMultiTraitEffects(req.Markers, req.Traits, L, rng)
	gm := buildGenomeMap(req, datasetMeta)
	initFounders(initial, gm, rand.New(rand.NewSource(req.Seed+founderPhaseSeedOffset)))
	if lineBreeding(req) {
		homozygousFounders(initial, gm, 0.05, rand.New(rand.NewSource(req.Seed+founderPhaseSeedOffset+1)))
	}
	mm := newMultiTraitModel(gm, initial, effects, req.Traits)

	baseFreq := alleleFreq(initial, req.Markers)
	baseDiversity := diversityFromFreq(baseFreq)
	T := len(req.Traits)
	baseMeans := make([]float64, T)
	for t := 0; t < T; t++ {
		baseMeans[t] = meanGeneticValue(initial, effects[t])
	}
	rareUsefulAtStart := rareUsefulLoci(baseFreq, effects[0])
	candidates := rankEditCandidates(baseFreq, effects[0], req.CrisprEdits, baseDiversity)

	reportProgress(progress, 5, fmt.Sprintf("multi-trait population + effects ready (T=%d traits)", T))

	results := make([]StrategyResult, 0, len(strategies)*req.Replicates)
	reps := make(map[string][]StrategyResult, len(strategies))
	totalJobs := len(strategies) * req.Replicates
	done := 0
	for si, cfg := range strategies {
		for r := 0; r < req.Replicates; r++ {
			pop := clonePopulation(initial)
			subRng := rand.New(rand.NewSource(req.Seed + int64(si*req.Replicates+r+1)))
			res := simulateMultiTraitStrategy(req, cfg, mm, pop, effects, req.Traits, baseFreq, baseDiversity, baseMeans, rareUsefulAtStart, subRng)
			reps[cfg.Code] = append(reps[cfg.Code], res)
			done++
			if done%5 == 0 || done == totalJobs {
				reportProgress(progress, 5+90*done/totalJobs, fmt.Sprintf("multi-trait: %d / %d strategy-replicates complete", done, totalJobs))
			}
		}
	}
	for _, cfg := range strategies {
		agg := aggregateReplicates(req, cfg, reps[cfg.Code], baseDiversity)
		// aggregateReplicates only sees the aggregate metrics; copy the
		// per-trait metrics from replicate 0 (the metrics are per-organism
		// averages — replicate 0 is representative enough for the chart).
		if len(reps[cfg.Code]) > 0 {
			agg.PerTraitMetrics = reps[cfg.Code][0].PerTraitMetrics
			agg.SelectionIndex = reps[cfg.Code][0].SelectionIndex
			// v0.7.24 — Issue 28. Apply climate penalty to each per-trait
			// trajectory so PerTraitGain (computed from the last point of
			// each trajectory below) and chart values reflect the penalty.
			// v0.7.28 — Issue 30. Per-strategy climate-penalty discount,
			// applied to per-trait trajectories in multi-trait runs too.
			discount := climateDiscountForStrategy(cfg, req)
			for t := range agg.PerTraitMetrics {
				applyClimatePenaltyToMetricsWithDiscount(agg.PerTraitMetrics[t], req.Climate, discount)
			}
		}
		results = append(results, agg)
	}
	annotateDecisionScores(results)
	// v0.7.22 — Issue 23. Overwrite the single-trait Pareto annotation
	// from annotateDecisionScores with N-D dominance that includes per-
	// trait gains. The frontend chart projects onto a 2D plane chosen by
	// the user, but the non-dominance outline reflects full N-D dominance.
	annotateMultiTraitPareto(results, T)
	annotateFeasibility(req, results, baseDiversity)
	decision := buildDecisionSummary(req, results, baseDiversity)
	if req.CrisprEnabled && req.CrisprEdits > 0 && len(candidates) > 0 {
		c := ClassifyEditSet(candidates, req.NGT)
		decision.NGT = &c
	}
	// v0.7.27 — Issue 07. Aggregate edit-vs-cross-vs-wait counts in
	// the multi-trait path too.
	decision.EditDecisions = summarizeEditDecisions(candidates)
	// Headline per-trait gain map for the recommended (best risk-adjusted)
	// strategy. Indexed by trait name so a downstream UI doesn't need to
	// rely on ordering.
	decision.PerTraitGain = make(map[string]float64, T)
	bestCode := decision.BestRiskAdjustedCode
	for _, r := range results {
		if r.Code != bestCode {
			continue
		}
		for t, tr := range req.Traits {
			if t < len(r.PerTraitMetrics) && len(r.PerTraitMetrics[t]) > 0 {
				lastIdx := len(r.PerTraitMetrics[t]) - 1
				decision.PerTraitGain[tr.Name] = round4(r.PerTraitMetrics[t][lastIdx].GeneticGain)
			}
		}
		break
	}
	// v0.7.22 — Issue 26. Append a plain-English multi-trait trade-off
	// paragraph to the interpretation. Conditional: only when ≥ 2 traits
	// are active. The text walks through per-trait gains and notes the
	// additive-only modelling caveat so the operator doesn't over-trust the
	// number.
	if len(req.Traits) >= 2 {
		var sb []string
		sb = append(sb, fmt.Sprintf("Multi-trait trade-off (recommended strategy %q):", decision.BestRiskAdjustedName))
		for _, tr := range req.Traits {
			gain := decision.PerTraitGain[tr.Name]
			arrow := "+"
			if gain < 0 {
				arrow = ""
			}
			sb = append(sb, fmt.Sprintf("• %s: %s%.4f (selection weight %+.2f)", tr.Name, arrow, gain, tr.SelectionWeight))
		}
		sb = append(sb, "Cross-trait responses are simulated under additive-only multi-trait selection (Issue 18 MVP); non-additive components (dominance, epistasis) are not modelled. Use the sensitivity sweep panel above to test robustness across selection-weight ranges.")
		decision.Interpretation = append(decision.Interpretation, strings.Join(sb, "  "))
	}

	reportProgress(progress, 98, "building response")
	notes := buildNotes(req, len(strategies), baseDiversity, datasetMeta, gm)
	notes = append(notes, fmt.Sprintf("Multi-trait MVP (Issue 18): %d traits with weighted selection-index truncation. OCS-like with similarity penalty is not implemented in the multi-trait path yet — strategies map to index-based truncation regardless of Rule (except no_selection and random).", T))
	return SimResponse{Request: req, Decision: decision, Strategies: results, CandidateEdits: candidates, Notes: notes, DegenerateRun: degenerateParentPool(req)}, nil
}
