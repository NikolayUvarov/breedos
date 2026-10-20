package main

// v0.8.7 — Issue 38. Climate as genotype × environment interaction.
//
// Before v0.8.7 a climate scenario multiplied the recorded trait mean by
// (1 − penalty) after simulation. That preserves every ranking, so climate
// could not change which genotypes or strategies are best, and the
// robustness of ancestral introgression was an input (a hard-coded
// "ancestral lines bear half the penalty").
//
// Single-trait runs now model performance in normal and stress seasons as
// two additive traits with a genetic correlation that comes from shared
// QTL:
//
//   - a fraction rg of the normal-season QTL also act in stress with the
//     same effect; the rest are normal-only ("yield potential") loci;
//   - as many stress-only loci are drawn from markers without a
//     normal-season effect;
//   - so cor(g_normal, g_stress) ≈ rg in the founders (reported).
//
// The target is the mix of seasons the variety will meet:
//
//	v = (1 − f) · g_normal + f · (1 − loss) · g_stress
//
// with f the stress frequency and loss the mode's mean yield loss
// (climateModePenalty × severity). v is additive too, so it is simply
// another effect vector, and gain is reported on it. Strategies select on
// normal-season trials (breeding nurseries) unless they select in the
// target environments. Ancestral lines are enriched for stress-only
// favourable alleles and depleted at yield-potential loci, so their
// advantage under stress is a result, not an input.

import (
	"math"
	"math/rand"
)

// Genetic correlations between normal- and stress-season performance.
// Order-of-magnitude defaults from wheat and maize G×E studies, where rg
// for yield under managed stress vs optimal conditions is typically
// 0.3–0.7. Override per scenario with climate.rg.
var climateModeRG = map[string]float64{
	"heat_burst_anthesis":    0.5,
	"heat_burst_booting":     0.6,
	"heat_grain_filling":     0.5,
	"combined_postflowering": 0.4,
	"prolonged_heat":         0.5,
	"drought_terminal":       0.5,
	"salinity_chronic":       0.4,
}

const defaultStressFrequency = 0.5

type gxeModel struct {
	frequency, loss, rg float64
	stress, target      []float64 // effect vectors
	stressOnly          []bool
	normalOnly          []bool
	nShared, nStress    int
	nNormal             int
	founderRG           float64
}

// buildGxE returns nil when the scenario carries no stress (normal mode or
// zero loss and no rg override).
func buildGxE(effectsN []float64, sc *ClimateScenario, rng *rand.Rand) *gxeModel {
	if sc == nil || sc.Mode == "" || sc.Mode == "normal" {
		return nil
	}
	loss := climatePenaltyFor(sc)
	rg, ok := climateModeRG[sc.Mode]
	if !ok {
		rg = 0.5
	}
	if sc.RG > 0 {
		rg = sc.RG
	}
	f := defaultStressFrequency
	if sc.Frequency > 0 {
		f = sc.Frequency
	}
	m := len(effectsN)
	g := &gxeModel{frequency: f, loss: loss, rg: rg, stress: make([]float64, m), target: make([]float64, m), stressOnly: make([]bool, m), normalOnly: make([]bool, m)}
	var qtl, free []int
	for i, e := range effectsN {
		if e != 0 {
			qtl = append(qtl, i)
		} else {
			free = append(free, i)
		}
	}
	if len(free) >= len(qtl)/2 {
		// Sparse QTL: shared + normal-only + stress-only loci.
		rng.Shuffle(len(qtl), func(a, b int) { qtl[a], qtl[b] = qtl[b], qtl[a] })
		g.nShared = int(math.Round(rg * float64(len(qtl))))
		for k, i := range qtl {
			if k < g.nShared {
				g.stress[i] = effectsN[i]
			} else {
				g.normalOnly[i] = true
				g.nNormal++
			}
		}
		rng.Shuffle(len(free), func(a, b int) { free[a], free[b] = free[b], free[a] })
		want := len(qtl) - g.nShared
		if want > len(free) {
			want = len(free)
		}
		for _, i := range free[:want] {
			v := rng.NormFloat64()
			if math.Abs(v) < 0.15 {
				v = math.Copysign(0.15, v)
			}
			g.stress[i] = v
			g.stressOnly[i] = true
			g.nStress++
		}
	} else {
		// Dense effects (estimated from phenotypes): correlated effects.
		sd := stddev(effectsN)
		for i, e := range effectsN {
			g.stress[i] = rg*e + math.Sqrt(1-rg*rg)*sd*rng.NormFloat64()
			if math.Abs(g.stress[i]) > 2*math.Abs(e) {
				g.stressOnly[i] = true
				g.nStress++
			} else if math.Abs(e) > 2*math.Abs(g.stress[i]) {
				g.normalOnly[i] = true
				g.nNormal++
			}
		}
		g.nShared = m - g.nStress - g.nNormal
	}
	for i := range g.target {
		g.target[i] = (1-f)*effectsN[i] + f*(1-loss)*g.stress[i]
	}
	return g
}

// seedAncestralLinesGxE replaces the last K founders with landrace-like
// lines: favourable at stress-only loci with probability 0.5 + 0.45·t
// (t = ancestral_stress_tolerance), favourable at yield-potential
// (normal-only) loci with probability AncestralAlleleBias, and drawn from
// the founder allele frequency elsewhere. With t = 0 they are no better
// under stress than modern founders.
func seedAncestralLinesGxE(pop []organism, introPercent float64, effectsN []float64, g *gxeModel, freq []float64, tolerance float64, rng *rand.Rand) {
	if introPercent <= 0 || len(pop) == 0 {
		return
	}
	if introPercent > AncestralIntroPercentMax {
		introPercent = AncestralIntroPercentMax
	}
	count := int(math.Round(float64(len(pop)) * introPercent / 100.0))
	if count > len(pop) {
		count = len(pop)
	}
	markers := len(effectsN)
	for i := len(pop) - count; i < len(pop); i++ {
		o := organism{geno: make([]uint8, markers), phase: make([]uint64, phaseWords(markers)), ibd: pop[i].ibd}
		for m := 0; m < markers; m++ {
			p1 := freq[m] // P(allele 1)
			switch {
			case g.stressOnly[m]:
				fav := 0.5 + 0.45*tolerance
				if g.stress[m] > 0 {
					p1 = fav
				} else {
					p1 = 1 - fav
				}
			case g.normalOnly[m]:
				if effectsN[m] > 0 {
					p1 = AncestralAlleleBias
				} else {
					p1 = 1 - AncestralAlleleBias
				}
			}
			a := rng.Float64() < p1
			b := rng.Float64() < p1
			if a {
				o.geno[m]++
			}
			if b {
				o.geno[m]++
				if !a {
					o.setPhaseBit(m)
				}
			}
		}
		pop[i] = o
	}
}
