package main

// v0.8.11 — gene pyramiding with marker-assisted selection (MAS).
//
// Pyramiding combines several major resistance genes (e.g. rice blast
// Pi-1 + Pi-2 + Pi-33) in one variety. The genes do not raise the selected
// trait (yield), so ordinary phenotypic or genomic selection does not see
// them and they drift. The MAS strategy ranks candidates first by how
// many favourable target alleles they carry, then by phenotype.
//
// Targets come either from named columns of the uploaded genotype table
// (target_loci; in an SSR table, "gene:<name>" columns) or, for a
// synthetic demo, from pyramid_genes: K loci on different parts of the
// genome, each carried homozygously by its own small set of donor
// founders.
//
// Reported per generation: the share of individuals homozygous for every
// target gene (pyramid complete), the share carrying every gene at least
// once, and each gene's frequency; per strategy, the generations at which
// 50 % and 90 % of individuals carry the complete homozygous pyramid and
// how many genes were lost.

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
)

const (
	maxMarkerDistanceCM = 50.0 // linked marker: beyond this the marker says little about the gene
	pyramidTargetWeight = 10.0 // target alleles dominate the MAS score; phenotype breaks ties
	maxPyramidGenes     = 8
	pyramidDonorShare   = 0.02 // founders carrying each synthetic gene
	pyramidGoal         = 0.9  // share of plants with the complete pyramid that counts as assembled
)

func pyramidRequested(req SimRequest) bool { return len(req.TargetLoci) > 0 || req.PyramidGenes > 0 }

// pyramidStatus returns the share of individuals homozygous for every
// target allele, the share carrying every target at least once, and each
// target's allele frequency.
func pyramidStatus(pop []organism, targets []int) (complete, carriers float64, freq []float64) {
	if len(targets) == 0 || len(pop) == 0 {
		return 0, 0, nil
	}
	ploidy := ploidyOf(pop)
	freq = make([]float64, len(targets))
	var nComplete, nCarriers int
	for i := range pop {
		hom, carry := true, true
		for k, t := range targets {
			g := int(pop[i].geno[t])
			freq[k] += float64(g)
			if g != ploidy {
				hom = false
			}
			if g == 0 {
				carry = false
			}
		}
		if hom {
			nComplete++
		}
		if carry {
			nCarriers++
		}
	}
	n := float64(len(pop))
	for k := range freq {
		freq[k] = round4(freq[k] / (float64(ploidy) * n))
	}
	return round4(float64(nComplete) / n), round4(float64(nCarriers) / n), freq
}

// haldaneR is the recombination fraction at d cM, r = (1 − e^(−2d)) / 2.
func haldaneR(cM float64) float64 { return (1 - math.Exp(-2*cM/100)) / 2 }

// markerPyramidStatus is the share of individuals homozygous for every
// marker allele, and the share homozygous for every marker but not for
// every gene: recombinants that marker screening would select.
func markerPyramidStatus(pop []organism, markers, genes []int) (byMarker, markerOnly float64) {
	if len(markers) == 0 || len(pop) == 0 {
		return 0, 0
	}
	ploidy := ploidyOf(pop)
	var nMarker, nOnly int
	for i := range pop {
		hom := true
		for _, t := range markers {
			if int(pop[i].geno[t]) != ploidy {
				hom = false
				break
			}
		}
		if !hom {
			continue
		}
		nMarker++
		for _, g := range genes {
			if int(pop[i].geno[g]) != ploidy {
				nOnly++
				break
			}
		}
	}
	n := float64(len(pop))
	return round4(float64(nMarker) / n), round4(float64(nOnly) / n)
}

// targetWeights weights each target gene by 1/√p of its current
// frequency (floored at one copy), normalised to sum to 1 — the weighting
// of rare favourable alleles of Jannink (2010). With plain gene counting,
// plants carrying two common genes out-rank the only carriers of a third,
// and the third is lost; rarer genes now count more.
func targetWeights(freq []float64, targets []int, n, ploidy int) []float64 {
	w := make([]float64, len(targets))
	floor := 1 / float64(ploidy*n)
	var sum float64
	for k, t := range targets {
		w[k] = 1 / math.Sqrt(math.Max(freq[t], floor))
		sum += w[k]
	}
	for k := range w {
		w[k] /= sum
	}
	return w
}

// targetScore is the weighted share of favourable target alleles an
// individual carries, in [0, 1].
func targetScore(o *organism, targets []int, weights []float64, ploidy int) float64 {
	var s float64
	for k, t := range targets {
		s += weights[k] * float64(o.geno[t])
	}
	return s / float64(ploidy)
}

// setupSyntheticPyramid places k target loci evenly across the genome
// (so with k ≤ chromosomes they sit on different chromosomes), removes any
// trait effect there (resistance genes do not raise yield), and makes each
// gene present only in its own donor founders, homozygous.
func setupSyntheticPyramid(pop []organism, effects []float64, k, ploidy int, rng *rand.Rand) []int {
	markers := len(effects)
	targets := make([]int, k)
	for i := range targets {
		targets[i] = int((float64(i) + 0.5) * float64(markers) / float64(k))
		effects[targets[i]] = 0
	}
	n := len(pop)
	donors := int(math.Round(pyramidDonorShare * float64(n)))
	if donors < 1 {
		donors = 1
	}
	order := rng.Perm(n)
	for i := range pop {
		for _, t := range targets {
			pop[i].geno[t] = 0
		}
	}
	for g, t := range targets {
		for d := 0; d < donors; d++ {
			pop[order[(g*donors+d)%n]].geno[t] = uint8(ploidy)
		}
	}
	return targets
}

// resolveTargets maps target names to marker indices of the full
// dataset. Names match marker names case-insensitively; a "gene:" prefix
// is optional on either side.
func resolveTargets(names []string, ds *loadedDataset) ([]int, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if ds == nil || len(ds.markerNames) == 0 {
		return nil, fmt.Errorf("target_loci needs a genotype table with named marker columns (upload or dataset)")
	}
	key := func(s string) string {
		return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "gene:"))
	}
	index := make(map[string]int, len(ds.markerNames))
	for m, n := range ds.markerNames {
		index[key(n)] = m
	}
	out := make([]int, 0, len(names))
	seen := map[int]bool{}
	for _, n := range names {
		m, ok := index[key(n)]
		if !ok {
			return nil, fmt.Errorf("target locus %q is not a column of the genotype table (gene columns: %s)", n, strings.Join(geneColumns(ds, 12), ", "))
		}
		if ds.layout.isIndicator(m) {
			return nil, fmt.Errorf("target locus %q is an SSR allele; pyramiding targets must be biallelic gene columns (0/1/2 or gene:<name> in an SSR table)", n)
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(out) > maxPyramidGenes {
		return nil, fmt.Errorf("at most %d target loci", maxPyramidGenes)
	}
	return out, nil
}

// geneColumns lists candidate target names for an error message.
func geneColumns(ds *loadedDataset, limit int) []string {
	var out []string
	for m, n := range ds.markerNames {
		if ds.layout.isIndicator(m) {
			continue
		}
		out = append(out, n)
		if len(out) == limit {
			out = append(out, "…")
			break
		}
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

// pyramidMilestones returns the first generation at which at least 50 %
// and 90 % of individuals carry the complete homozygous pyramid (0 =
// not reached).
func pyramidMilestones(metrics []MetricPoint) (g50, g90 int) {
	for _, m := range metrics {
		if g50 == 0 && m.PyramidComplete >= 0.5 && m.Generation > 0 {
			g50 = m.Generation
		}
		if g90 == 0 && m.PyramidComplete >= 0.9 && m.Generation > 0 {
			g90 = m.Generation
		}
	}
	return g50, g90
}

// plantsFor95 is how many independent individuals must be genotyped to
// find at least one with probability p each, with 95 % confidence.
func plantsFor95(p float64) int {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	return int(math.Ceil(math.Log(0.05) / math.Log(1-p)))
}

// pyramidPlanningLine gives the textbook population sizes for unlinked
// target genes.
func pyramidPlanningLine(k int, req SimRequest) string {
	f2, lines := math.Pow(0.25, float64(k)), math.Pow(0.5, float64(k))
	if lineBreeding(req) {
		return fmt.Sprintf("Planning (unlinked genes): among inbred lines from a cross segregating for all %d genes, 1/%d are homozygous for all of them — derive at least %d lines to find one with 95%% probability (in an F2 it is 1/%d of plants: %d plants).", k, int(math.Round(1/lines)), plantsFor95(lines), int(math.Round(1/f2)), plantsFor95(f2))
	}
	return fmt.Sprintf("Planning (unlinked genes): in an F2 segregating for all %d genes, 1/%d of plants are homozygous for all of them — genotype at least %d plants to find one with 95%% probability.", k, int(math.Round(1/f2)), plantsFor95(f2))
}

// pyramidSummaryLine compares the MAS strategy with the best strategy
// that does not use the markers.
func pyramidSummaryLine(results []StrategyResult, names []string, cycleYears, linkedCM float64) string {
	var mas *StrategyResult
	best := -1
	minLost, maxLost := len(names), 0
	for i := range results {
		r := &results[i]
		if r.Code == "mas_pyramid" {
			mas = r
			continue
		}
		if r.Code == "mas_ocs" {
			continue
		}
		if isBaselineStrategy(r.Code) {
			continue
		}
		if best < 0 || r.Final.PyramidComplete > results[best].Final.PyramidComplete {
			best = i
		}
		if r.Final.TargetsLost < minLost {
			minLost = r.Final.TargetsLost
		}
		if r.Final.TargetsLost > maxLost {
			maxLost = r.Final.TargetsLost
		}
	}
	if mas == nil {
		return ""
	}
	when := "not within the horizon"
	if mas.Final.PyramidGen90 > 0 {
		when = fmt.Sprintf("by generation %d", mas.Final.PyramidGen90)
		if cycleYears > 0 {
			when += fmt.Sprintf(" (year %g)", float64(mas.Final.PyramidGen90)*cycleYears)
		}
	}
	out := fmt.Sprintf("Gene pyramid (%s): with marker-assisted selection, 90%% of plants are homozygous for all %d genes %s; at the end %.0f%% carry the full pyramid%s.", strings.Join(names, " + "), len(names), when, 100*mas.Final.PyramidComplete, lossText(mas.Final))
	if best >= 0 {
		b := results[best]
		lost := fmt.Sprintf("%d", minLost)
		if maxLost != minLost {
			lost = fmt.Sprintf("%d–%d", minLost, maxLost)
		}
		if b.Final.PyramidComplete == 0 {
			out += fmt.Sprintf(" Without markers no strategy ends with a full pyramid; they lose %s of the %d genes, because the genes do not raise the selected trait and selection does not see them.", lost, len(names))
		} else {
			out += fmt.Sprintf(" Without markers the best is %s with %.0f%% full pyramids; strategies lose %s of the %d genes, because the genes do not raise the selected trait.", b.Name, 100*b.Final.PyramidComplete, lost, len(names))
		}
	}
	if linkedCM > 0 {
		out += fmt.Sprintf(" The markers are %.1f cM from their genes (r = %.3f per meiosis): at the end %.0f%% of MAS plants are homozygous for all markers, %.0f%% for all genes, and %.0f%% carry every marker without every gene — recombinants that marker screening selects and cannot see.", linkedCM, haldaneR(linkedCM), 100*mas.Final.PyramidByMarker, 100*mas.Final.PyramidComplete, 100*mas.Final.PyramidMarkerOnly)
	}
	for i := range results {
		if r := results[i]; r.Code == "mas_ocs" {
			w := "not within the horizon"
			if r.Final.PyramidGen90 > 0 {
				w = fmt.Sprintf("by generation %d", r.Final.PyramidGen90)
			}
			out += fmt.Sprintf(" MAS + OCS reaches 90%% %s, %.0f%% at the end, at F %.3f (plain MAS: F %.3f)%s.", w, 100*r.Final.PyramidComplete, r.Final.Inbreeding, mas.Final.Inbreeding, lossText(r.Final))
		}
	}
	return out
}

// lossText says how often target genes were lost across replicates.
func lossText(f FinalStats) string {
	if f.ProbabilityTargetLoss == 0 {
		return " and no gene is lost"
	}
	return fmt.Sprintf(", but in %.0f%% of replicates at least one gene was lost", 100*f.ProbabilityTargetLoss)
}

func targetsLost(freq []float64) int {
	n := 0
	for _, f := range freq {
		if f == 0 {
			n++
		}
	}
	return n
}

// keepColumns merges forced columns into a sorted column list.
func keepColumns(cols, keep []int) []int {
	if len(keep) == 0 {
		return cols
	}
	set := make(map[int]bool, len(cols)+len(keep))
	for _, c := range cols {
		set[c] = true
	}
	for _, k := range keep {
		set[k] = true
	}
	out := make([]int, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Ints(out)
	return out
}
