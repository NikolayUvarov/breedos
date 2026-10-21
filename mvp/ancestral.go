package main

// v0.7.28 — Issue 30. Ancestral-allele introgression strategy.
//
// Models the deliberate introduction of a small pool of landrace /
// wild-relative individuals into the founder population at generation
// 0. The dynamic the issue asks us to capture:
//
//   * Ancestral lines carry better climate-stress tolerance — modelled
//     as a strategy-level multiplier on the climate penalty applied
//     in aggregateReplicates.
//   * Ancestral lines have lower mean trait value — modelled by
//     re-rolling K founders with a Bernoulli-biased genotype draw at
//     gen 0 (lower expected favourable-allele count → lower base trait
//     mean), so the strategy starts behind on a normal-weather run.
//
// This is intentionally a population-level effect, not a per-individual
// tag threaded through selection / mating. Per-individual lineage
// tracking is out of scope for v0.9 and would require touching every
// reproduce-offspring site. The strategy-level multiplier captures the
// headline dynamic (climate-robust under stress, slower under normal
// conditions) without that invasion.

import (
	"math"
	"math/rand"
)

// Defaults if the operator does not set the corresponding SimRequest
// fields. AncestralStressToleranceDefault = 0.5 means ancestral lines
// suffer half the climate penalty of modern lines (the issue's calibration).
const (
	AncestralStressToleranceDefault = 0.5
	AncestralIntroPercentMax        = 25.0 // hard cap; validation rejects requests above this.
	AncestralAlleleBias             = 0.30 // P(carrying favourable allele) for re-rolled ancestral genotypes; lower than the assumed-0.5 modern baseline → lower trait mean.
)

// seedAncestralLines re-rolls the last K = round(len(pop) × pct/100)
// individuals' genotypes with a low-allele-frequency Bernoulli draw, so
// the founder population's base trait mean is dragged down by the
// ancestral fraction. The choice of "last K" rather than "random K" is
// deliberate: keeps the modern individuals' indices stable so any
// debugging trace stays aligned.
func seedAncestralLines(pop []organism, introPercent float64, markers int, rng *rand.Rand) {
	if introPercent <= 0 || len(pop) == 0 {
		return
	}
	if introPercent > AncestralIntroPercentMax {
		introPercent = AncestralIntroPercentMax
	}
	count := int(math.Round(float64(len(pop)) * introPercent / 100.0))
	if count <= 0 {
		return
	}
	if count > len(pop) {
		count = len(pop)
	}
	startIdx := len(pop) - count
	ploidy := ploidyOf(pop)
	for i := startIdx; i < len(pop); i++ {
		// v0.8.0: the line keeps the slot's founder IBD labels and gets
		// a phase from its draws (one per strand, in strand order).
		o := newOrganismPloidy(markers, 0, ploidy)
		o.ibd = pop[i].ibd
		for m := 0; m < markers; m++ {
			// One Bernoulli draw per strand with P(carry favourable
			// allele) = AncestralAlleleBias.
			for h := 0; h < ploidy; h++ {
				if rng.Float64() < AncestralAlleleBias {
					o.setAllele(m, h, 1)
				}
			}
		}
		pop[i] = o
	}
}

// ancestralCount is how many founders the ancestral seeding replaces (the
// last K of the population), matching seedAncestralLines.
func ancestralCount(n int, introPercent float64) int {
	if introPercent <= 0 || n == 0 {
		return 0
	}
	if introPercent > AncestralIntroPercentMax {
		introPercent = AncestralIntroPercentMax
	}
	count := int(math.Round(float64(n) * introPercent / 100.0))
	if count > n {
		count = n
	}
	return count
}

// climateDiscountForStrategy returns the per-strategy multiplier
// applied to the climate penalty in aggregateReplicates. For strategies
// without ancestral seeding this is 1.0 (no discount, climate penalty
// applied as v0.7.24). For ancestral_introgression it is
//
//   1 − introPct × stressTolerance
//
// derived from the population-weighted per-individual penalty:
//
//   E[penalty] = (1 − f) × penalty + f × stressTolerance × penalty
//              = penalty × (1 − f × (1 − stressTolerance))
//
// where f = AncestralIntroPercent / 100. With defaults (15%, 0.5) this
// yields a 7.5% reduction in the recorded climate penalty; with the
// cap (25%, 0.5) it yields 12.5%. The multiplier is then applied INSIDE
// applyClimatePenaltyToMetrics so the strategy's reported gain trajectory
// shows the partial protection.
func climateDiscountForStrategy(cfg strategyConfig, req SimRequest) float64 {
	if !cfg.UseAncestralSeed {
		return 1.0
	}
	pct := req.AncestralIntroPercent
	if pct <= 0 {
		return 1.0
	}
	if pct > AncestralIntroPercentMax {
		pct = AncestralIntroPercentMax
	}
	st := req.AncestralStressTolerance
	if st <= 0 {
		st = AncestralStressToleranceDefault
	}
	if st > 1 {
		st = 1
	}
	f := pct / 100.0
	return 1.0 - f*(1.0-st)
}
