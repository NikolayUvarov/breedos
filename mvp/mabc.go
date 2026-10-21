package main

// v0.8.13 — marker-assisted backcrossing (MABC).
//
// The standard way to move resistance genes into an elite variety: cross a
// donor to the recurrent parent, then backcross the carriers to the
// recurrent parent for n generations, selecting each time on the markers
// (foreground) and, among carriers, on the share of the recurrent
// parent's genome (background, from the IBD labels at the tracker loci).
// After the backcrosses the carriers are intercrossed / selfed under the
// run's mating system to fix the genes. Without background selection the
// expected recurrent genome at BCn is 1 − (1/2)^(n+1).

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
)

const (
	maxBackcrossGenerations = 8
	mabcForegroundWeight    = 1000.0 // target alleles: a hard filter ahead of the background (one heterozygous gene of eight outranks any background z-score)
	mabcBackgroundWeight    = 3.0    // standardised recurrent-genome share in the MABC score
	mabcTraitWeight         = 0.3    // phenotype breaks the remaining ties
)

// chooseRecurrentParent returns the founder to backcross to: the named
// accession, or the founder with the highest genetic value among those
// carrying no target allele (among all founders if every one carries
// some).
func chooseRecurrentParent(pop []organism, effects []float64, targets []int, ids []string, name string) (int, error) {
	if name = strings.TrimSpace(name); name != "" {
		for i, id := range ids {
			if strings.EqualFold(id, name) {
				return i, nil
			}
		}
		return 0, fmt.Errorf("recurrent_parent %q is not an id of the founder table (first ids: %s)", name, strings.Join(firstN(ids, 5), ", "))
	}
	g := geneticValues(pop, effects)
	best := -1
	for pass := 0; pass < 2 && best < 0; pass++ {
		for i := range pop {
			if pass == 0 {
				carries := false
				for _, t := range targets {
					if pop[i].geno[t] > 0 {
						carries = true
						break
					}
				}
				if carries {
					continue
				}
			}
			if best < 0 || g[i] > g[best] {
				best = i
			}
		}
	}
	return best, nil
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// recurrentLabelSet is the IBD label set of founder i (initFounders gives
// homologue s of founder i the label ploidy·i + s).
func recurrentLabelSet(i, ploidy int) map[uint16]bool {
	set := make(map[uint16]bool, ploidy)
	for s := 0; s < ploidy; s++ {
		set[uint16(ploidy*i+s)] = true
	}
	return set
}

// recurrentShareOf is the share of an individual's tracker labels that
// come from the recurrent parent.
func recurrentShareOf(o *organism, set map[uint16]bool) float64 {
	if len(o.ibd) == 0 || len(set) == 0 {
		return 0
	}
	n := 0
	for _, l := range o.ibd {
		if set[l] {
			n++
		}
	}
	return float64(n) / float64(len(o.ibd))
}

func recurrentShare(pop []organism, set map[uint16]bool) float64 {
	if len(pop) == 0 || len(set) == 0 {
		return 0
	}
	var s float64
	for i := range pop {
		s += recurrentShareOf(&pop[i], set)
	}
	return round4(s / float64(len(pop)))
}

// backcrossGeneration makes N offspring of the selected parents × the
// recurrent parent, cycling through the parents in random order.
func backcrossGeneration(pop []organism, parents []int, rp *organism, gm *genomeMap, mutationRate float64, rng *rand.Rand) []organism {
	n := len(pop)
	next := make([]organism, n)
	xo := make([]float64, 0, 16)
	order := rng.Perm(len(parents))
	for i := 0; i < n; i++ {
		p := &pop[parents[order[i%len(parents)]]]
		child := gm.newOrganism()
		if gm.ploidy() > 2 {
			gm.transmitPolyploid(p, &child, 0, mutationRate, rng, xo)
			gm.transmitPolyploid(rp, &child, gm.ploidy()/2, mutationRate, rng, xo)
		} else {
			gm.transmit(p, &child, 0, mutationRate, rng, xo)
			gm.transmit(rp, &child, 1, mutationRate, rng, xo)
		}
		next[i] = child
	}
	return next
}

// recurrentHow says how the recurrent parent was chosen.
func recurrentHow(req SimRequest) string {
	if strings.TrimSpace(req.RecurrentParent) != "" {
		return "named in recurrent_parent"
	}
	return "the founder with the highest genetic value that carries no target allele"
}

// balancedCarriers takes the best carriers of each target gene in equal
// quotas, then fills the rest of the parent set by score.
func balancedCarriers(pop []organism, scored []scoredIndividual, targets []int, parentCount int) []int {
	quota := parentCount / len(targets)
	if quota < 1 {
		quota = 1
	}
	chosen := make(map[int]bool, parentCount)
	parents := make([]int, 0, parentCount)
	for _, t := range targets {
		n := 0
		for _, s := range scored {
			if n == quota || len(parents) == parentCount {
				break
			}
			if !chosen[s.idx] && pop[s.idx].geno[t] > 0 {
				chosen[s.idx] = true
				parents = append(parents, s.idx)
				n++
			}
		}
	}
	for _, s := range scored {
		if len(parents) == parentCount {
			break
		}
		if !chosen[s.idx] {
			chosen[s.idx] = true
			parents = append(parents, s.idx)
		}
	}
	return parents
}

// expectedRecurrentGenome is the textbook recurrent-parent share at BCn
// without background selection.
func expectedRecurrentGenome(backcrosses int) float64 {
	return 1 - math.Pow(0.5, float64(backcrosses+1))
}

// mabcSummaryLine reports the backcross programme against plain MAS.
func mabcSummaryLine(results []StrategyResult, metricsAtBC func(StrategyResult) float64, req SimRequest, rpID string) string {
	var mabc, mas *StrategyResult
	for i := range results {
		switch results[i].Code {
		case "mabc":
			mabc = &results[i]
		case "mas_pyramid":
			mas = &results[i]
		}
	}
	if mabc == nil {
		return ""
	}
	when := "not within the horizon"
	if mabc.Final.PyramidGen90 > 0 {
		when = fmt.Sprintf("by generation %d", mabc.Final.PyramidGen90)
	}
	out := fmt.Sprintf("Marker-assisted backcrossing to %s: after %d backcrosses the carriers hold %.1f%% of the recurrent parent's genome (%.1f%% expected without background selection); at the end %.1f%%, with 90%% complete pyramids %s.", rpID, req.BackcrossGenerations, 100*metricsAtBC(*mabc), 100*expectedRecurrentGenome(req.BackcrossGenerations), 100*mabc.Final.RecurrentGenome, when)
	if mas != nil {
		out += fmt.Sprintf(" Plain MAS keeps %.0f%% of that parent's genome; its F (%.2f) and alleles are a background of many lines, MABC's F (%.2f) is convergence on one.", 100*mas.Final.RecurrentGenome, mas.Final.Inbreeding, mabc.Final.Inbreeding)
	}
	return out
}
