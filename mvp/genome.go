package main

// v0.8.0 — linked genome, phased haplotypes, identity-by-descent tracking.
//
// Up to v0.7.x every marker was inherited independently (free
// recombination between all loci) and "inbreeding" was 1 − He_t/He_0
// computed on the same markers the novelty score selects on, so a
// strategy could lower it by pushing marker frequencies toward 0.5.
// v0.8.0 replaces both:
//
//   - Markers sit on chromosomes with a genetic map in Morgans. Each
//     meiosis draws Poisson(L) crossovers per chromosome at uniform
//     positions (Haldane map function, no interference), so linkage
//     disequilibrium, hitchhiking and linkage drag emerge from the model.
//   - Every founder haplotype carries a unique label at a set of evenly
//     spaced tracker loci. Inbreeding is the probability that two allele
//     copies drawn from the population are identical by descent (IBD)
//     relative to the founder generation — the quantity pedigree F and
//     optimum-contribution selection reason about. Selection on observed
//     marker state cannot lower it.

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultChromosomes       = 10
	defaultChromosomeLengthM = 1.0
	maxChromosomes           = 100
	maxIBDTrackers           = 512
)

// organism is one diploid individual.
//
// geno is the allele dosage (0/1/2 copies of the counted allele) per
// marker; trait, frequency and similarity code read only this. phase and
// ibd carry what meiosis needs: phase bit m set means that at a
// heterozygous marker the counted allele sits on strand B (strand A
// otherwise); ibd holds founder-origin labels at the genome map's tracker
// loci, strand A in [0, T) and strand B in [T, 2T).
//
// v0.8.11 — autotetraploids. Polyploid individuals carry one allele bitset
// per homologue in strands (nil for diploids, which keep phase); geno is
// then the dosage 0..4 and ibd holds ploidy·T labels, homologue s at
// [s·T, (s+1)·T).
type organism struct {
	geno    []uint8
	phase   []uint64
	ibd     []uint16
	strands [][]uint64
}

// ploidyOf is the ploidy of a population (all members share it).
func ploidyOf(pop []organism) int {
	if len(pop) > 0 && pop[0].strands != nil {
		return len(pop[0].strands)
	}
	return 2
}

// markerSite is a marker's physical position when the dataset provides it.
type markerSite struct {
	chrom string
	pos   int64
}

type genomeMap struct {
	markers    int
	chromStart []int     // len C+1; chromosome c holds markers [chromStart[c], chromStart[c+1])
	posM       []float64 // marker position in Morgans from its chromosome's start
	lengthM    []float64 // genetic length of each chromosome in Morgans
	trackers   []int     // marker indices carrying IBD labels, ascending
	trackerOf  []int32   // per-marker tracker slot, -1 when the marker is not tracked
	source     string    // human-readable origin of the map, surfaced in the run notes
	// v0.8.9 — multi-allelic loci (ssr.go): indicator markers share their
	// locus' position and are never mutated.
	layout *locusLayout
	// v0.8.11 — ploidy (0 or 2 = diploid, 4 = autotetraploid).
	ploidyN int
	// v0.8.12 — linked loci (genes selected on a marker some cM away).
	// They sit after every chromosome range, at index chromStart[C]+j,
	// and are inherited with the crossovers of their chromosome.
	linked []linkedLocus
}

type linkedLocus struct {
	chrom int
	posM  float64
}

// chromOf is the chromosome holding marker m (chromosome ranges only).
func (gm *genomeMap) chromOf(m int) int {
	c := sort.SearchInts(gm.chromStart, m+1) - 1
	if c < 0 {
		c = 0
	}
	if last := len(gm.chromStart) - 2; c > last {
		c = last
	}
	return c
}

// addLinkedLoci appends one locus per marker, cM away on the marker's
// chromosome (on the far side if the chromosome ends first), and returns
// their indices. Linked loci carry no IBD label, are never mutated, and in
// a multi-allelic layout form single-allele loci named "<marker> (gene)".
func (gm *genomeMap) addLinkedLoci(markers []int, names []string, cM float64) []int {
	out := make([]int, len(markers))
	if gm.layout != nil {
		gm.layout = gm.layout.clone()
	}
	for k, m := range markers {
		c := gm.chromOf(m)
		d := cM / 100
		pos := gm.posM[m] + d
		if pos > gm.lengthM[c] {
			pos = gm.posM[m] - d
		}
		if pos < 0 {
			pos = 0
			if gm.lengthM[c]-gm.posM[m] > gm.posM[m] {
				pos = gm.lengthM[c]
			}
		}
		out[k] = gm.markers
		gm.linked = append(gm.linked, linkedLocus{chrom: c, posM: pos})
		gm.posM = append(gm.posM, pos)
		gm.trackerOf = append(gm.trackerOf, -1)
		if gm.layout != nil {
			name := names[k] + " (gene)"
			gm.layout.names = append(gm.layout.names, name)
			gm.layout.start = append(gm.layout.start, gm.markers)
			gm.layout.alleles = append(gm.layout.alleles, []string{name})
			gm.layout.locusOf = append(gm.layout.locusOf, len(gm.layout.names)-1)
		}
		gm.markers++
	}
	return out
}

// linkedIndex is the marker index of the j-th linked locus.
func (gm *genomeMap) linkedIndex(j int) int { return gm.chromStart[len(gm.chromStart)-1] + j }

// extendLinked gives every individual the linked loci, in coupling phase
// with their markers: each homologue carries at the gene the allele it
// carries at the marker.
func extendLinked(pop []organism, markers, genes []int, total int) {
	for i := range pop {
		o := &pop[i]
		geno := make([]uint8, total)
		copy(geno, o.geno)
		o.geno = geno
		ploidy := 2
		if o.strands != nil {
			ploidy = len(o.strands)
			for s := range o.strands {
				ns := make([]uint64, phaseWords(total))
				copy(ns, o.strands[s])
				o.strands[s] = ns
			}
		} else {
			np := make([]uint64, phaseWords(total))
			copy(np, o.phase)
			o.phase = np
		}
		for k, m := range markers {
			for s := 0; s < ploidy; s++ {
				o.setAllele(genes[k], s, o.allele(m, s))
			}
		}
	}
}

func phaseWords(markers int) int { return (markers + 63) / 64 }

func (o *organism) phaseBit(m int) uint8 {
	if o.phase == nil {
		return 0
	}
	return uint8(o.phase[m>>6] >> (uint(m) & 63) & 1)
}

func (o *organism) setPhaseBit(m int) { o.phase[m>>6] |= 1 << (uint(m) & 63) }

// allele returns the allele (0/1) carried on strand s (0 = A, 1 = B; or
// homologue s of a polyploid) at marker m.
func (o *organism) allele(m, s int) uint8 {
	if o.strands != nil {
		return uint8(o.strands[s][m>>6] >> (uint(m) & 63) & 1)
	}
	switch o.geno[m] {
	case 0:
		return 0
	case 2:
		return 1
	}
	b := o.phaseBit(m)
	if s == 1 {
		return b
	}
	return 1 - b
}

func buildSyntheticGenomeMap(markers, chromosomes int, lengthM float64) *genomeMap {
	if chromosomes < 1 {
		chromosomes = 1
	}
	if chromosomes > markers {
		chromosomes = markers
	}
	gm := &genomeMap{
		markers:    markers,
		chromStart: make([]int, chromosomes+1),
		posM:       make([]float64, markers),
		lengthM:    make([]float64, chromosomes),
		source:     fmt.Sprintf("synthetic map: %d chromosome(s) × %.2f M, markers evenly spaced", chromosomes, lengthM),
	}
	for c := 0; c <= chromosomes; c++ {
		gm.chromStart[c] = c * markers / chromosomes
	}
	for c := 0; c < chromosomes; c++ {
		gm.lengthM[c] = lengthM
		lo, hi := gm.chromStart[c], gm.chromStart[c+1]
		for m := lo; m < hi; m++ {
			gm.posM[m] = (float64(m-lo) + 0.5) / float64(hi-lo) * lengthM
		}
	}
	gm.placeTrackers()
	return gm
}

// buildGenomeMapFromSites derives a genetic map from physical positions.
// Physical distance is converted at one genome-wide rate chosen so the
// average chromosome is lengthM Morgans long; longer chromosomes get
// proportionally more crossovers. Returns nil when sites are missing,
// unsorted within a chromosome, or a chromosome appears in two blocks —
// callers then fall back to a synthetic map.
func buildGenomeMapFromSites(sites []markerSite, lengthM float64) *genomeMap {
	if len(sites) == 0 {
		return nil
	}
	starts := []int{0}
	seen := map[string]bool{sites[0].chrom: true}
	for m := 1; m < len(sites); m++ {
		if sites[m].chrom != sites[m-1].chrom {
			if seen[sites[m].chrom] {
				return nil
			}
			seen[sites[m].chrom] = true
			starts = append(starts, m)
			continue
		}
		if sites[m].pos < sites[m-1].pos {
			return nil
		}
	}
	starts = append(starts, len(sites))
	chromosomes := len(starts) - 1
	var totalSpan float64
	spans := make([]float64, chromosomes)
	for c := 0; c < chromosomes; c++ {
		spans[c] = float64(sites[starts[c+1]-1].pos-sites[starts[c]].pos) + 1
		totalSpan += spans[c]
	}
	rate := lengthM * float64(chromosomes) / totalSpan // Morgans per base pair
	cover := make([]string, 0, chromosomes)
	for c := 0; c < chromosomes && c < 6; c++ {
		cover = append(cover, fmt.Sprintf("%s:%.2f–%.2f Mb", sites[starts[c]].chrom, float64(sites[starts[c]].pos)/1e6, float64(sites[starts[c+1]-1].pos)/1e6))
	}
	if chromosomes > 6 {
		cover = append(cover, "…")
	}
	gm := &genomeMap{
		markers:    len(sites),
		chromStart: starts,
		posM:       make([]float64, len(sites)),
		lengthM:    make([]float64, chromosomes),
		source: fmt.Sprintf("map from dataset positions — %d chromosome(s) covering %s; physical distance scaled to %.2f M per chromosome on average (set chromosome_length_morgans to your species' map)",
			chromosomes, strings.Join(cover, ", "), lengthM),
	}
	for c := 0; c < chromosomes; c++ {
		gm.lengthM[c] = spans[c] * rate
		base := sites[starts[c]].pos
		for m := starts[c]; m < starts[c+1]; m++ {
			gm.posM[m] = float64(sites[m].pos-base) * rate
		}
	}
	gm.placeTrackers()
	return gm
}

// buildGenomeMap picks the dataset's physical map when it has one and the
// synthetic map otherwise.
func buildGenomeMap(req SimRequest, ds *loadedDataset) *genomeMap {
	chromosomes, lengthM := req.Chromosomes, req.ChromosomeLengthM
	if chromosomes <= 0 {
		chromosomes = defaultChromosomes
	}
	if lengthM <= 0 {
		lengthM = defaultChromosomeLengthM
	}
	if ds != nil && ds.layout != nil && ds.layout.markers() == req.Markers {
		return buildLocusGenomeMap(ds.layout, chromosomes, lengthM)
	}
	if ds != nil && len(ds.sites) == req.Markers {
		if gm := buildGenomeMapFromSites(ds.sites, lengthM); gm != nil {
			return gm
		}
	}
	return buildSyntheticGenomeMap(req.Markers, chromosomes, lengthM)
}

// buildLocusGenomeMap is the synthetic map for multi-allelic data: loci
// are spread evenly over the chromosomes and every indicator of a locus
// sits at the locus' position, so meiosis never separates its alleles.
func buildLocusGenomeMap(l *locusLayout, chromosomes int, lengthM float64) *genomeMap {
	loci := l.loci()
	if chromosomes > loci {
		chromosomes = loci
	}
	if chromosomes < 1 {
		chromosomes = 1
	}
	markers := l.markers()
	gm := &genomeMap{
		markers:    markers,
		chromStart: make([]int, chromosomes+1),
		posM:       make([]float64, markers),
		lengthM:    make([]float64, chromosomes),
		layout:     l,
		source:     fmt.Sprintf("synthetic map for %d multi-allelic loci on %d chromosome(s) × %.2f M (no positions in the file; loci spread evenly, alleles of a locus at one position)", loci, chromosomes, lengthM),
	}
	for c := 0; c <= chromosomes; c++ {
		firstLocus := c * loci / chromosomes
		if firstLocus < loci {
			gm.chromStart[c] = l.start[firstLocus]
		} else {
			gm.chromStart[c] = markers
		}
	}
	for c := 0; c < chromosomes; c++ {
		gm.lengthM[c] = lengthM
		lo, hi := c*loci/chromosomes, (c+1)*loci/chromosomes
		for li := lo; li < hi; li++ {
			pos := (float64(li-lo) + 0.5) / float64(hi-lo) * lengthM
			for a := range l.alleles[li] {
				gm.posM[l.start[li]+a] = pos
			}
		}
	}
	gm.placeTrackers()
	return gm
}

func (gm *genomeMap) placeTrackers() {
	t := gm.markers
	if t > maxIBDTrackers {
		t = maxIBDTrackers
	}
	gm.trackers = make([]int, t)
	gm.trackerOf = make([]int32, gm.markers)
	for m := range gm.trackerOf {
		gm.trackerOf[m] = -1
	}
	for k := 0; k < t; k++ {
		m := int((float64(k) + 0.5) * float64(gm.markers) / float64(t))
		gm.trackers[k] = m
		gm.trackerOf[m] = int32(k)
	}
}

// setAllele writes allele a (0/1) on strand s of a freshly allocated
// individual at marker m. Diploid strands must be written A before B.
func (o *organism) setAllele(m, s int, a uint8) {
	if a == 0 {
		return
	}
	o.geno[m]++
	if o.strands != nil {
		o.strands[s][m>>6] |= 1 << (uint(m) & 63)
	} else if s == 1 {
		o.setPhaseBit(m)
	}
}

// newOrganismPloidy allocates an empty individual of the given ploidy.
func newOrganismPloidy(markers, trackers, ploidy int) organism {
	if ploidy <= 2 {
		return organism{geno: make([]uint8, markers), phase: make([]uint64, phaseWords(markers)), ibd: make([]uint16, 2*trackers)}
	}
	o := organism{geno: make([]uint8, markers), ibd: make([]uint16, ploidy*trackers), strands: make([][]uint64, ploidy)}
	for s := range o.strands {
		o.strands[s] = make([]uint64, phaseWords(markers))
	}
	return o
}

// newOrganism allocates an individual with room for phase and IBD labels.
func (gm *genomeMap) newOrganism() organism {
	return newOrganismPloidy(gm.markers, len(gm.trackers), gm.ploidy())
}

// ploidy of the population this map was built for (2 unless set).
func (gm *genomeMap) ploidy() int {
	if gm.ploidyN > 2 {
		return gm.ploidyN
	}
	return 2
}

// initFounders phases unphased founders at random (input genotypes are
// unphased dosages) and gives every founder haplotype a unique IBD label.
// Founders are the IBD reference: their inbreeding is 0 by definition.
func initFounders(pop []organism, gm *genomeMap, rng *rand.Rand) {
	T := len(gm.trackers)
	if ploidy := gm.ploidy(); ploidy > 2 {
		// Polyploid founders: dosage d at a marker puts the counted
		// allele on d randomly chosen homologues.
		for i := range pop {
			o := &pop[i]
			if o.strands == nil {
				o.strands = make([][]uint64, ploidy)
				for s := range o.strands {
					o.strands[s] = make([]uint64, phaseWords(gm.markers))
				}
				for m, g := range o.geno {
					for _, s := range rng.Perm(ploidy)[:int(g)] {
						o.strands[s][m>>6] |= 1 << (uint(m) & 63)
					}
				}
				o.phase = nil
			}
			o.ibd = make([]uint16, ploidy*T)
			for s := 0; s < ploidy; s++ {
				for t := 0; t < T; t++ {
					o.ibd[s*T+t] = uint16(ploidy*i + s)
				}
			}
		}
		return
	}
	for i := range pop {
		o := &pop[i]
		if o.phase == nil {
			o.phase = make([]uint64, phaseWords(gm.markers))
			for m, g := range o.geno {
				if g == 1 && rng.Intn(2) == 1 {
					o.setPhaseBit(m)
				}
			}
		}
		if len(o.ibd) != 2*T {
			o.ibd = make([]uint16, 2*T)
		}
		for t := 0; t < T; t++ {
			o.ibd[t] = uint16(2 * i)
			o.ibd[T+t] = uint16(2*i + 1)
		}
	}
}

// transmit writes one recombinant gamete from parent into strand s of child.
// Crossovers per chromosome ~ Poisson(length in Morgans), positions uniform.
func (gm *genomeMap) transmit(parent, child *organism, s int, mutationRate float64, rng *rand.Rand, xo []float64) {
	T := len(gm.trackers)
	for c := 0; c+1 < len(gm.chromStart); c++ {
		strand := rng.Intn(2)
		strand0 := strand
		k := poisson(gm.lengthM[c], rng)
		xo = xo[:0]
		for j := 0; j < k; j++ {
			xo = append(xo, rng.Float64()*gm.lengthM[c])
		}
		sort.Float64s(xo)
		next := 0
		for j, l := range gm.linked {
			if l.chrom != c {
				continue
			}
			m := gm.linkedIndex(j)
			a := parent.allele(m, strand0^(sort.SearchFloat64s(xo, l.posM)&1))
			child.geno[m] += a
			if s == 1 && a == 1 {
				child.setPhaseBit(m)
			}
		}
		for m := gm.chromStart[c]; m < gm.chromStart[c+1]; m++ {
			for next < len(xo) && xo[next] < gm.posM[m] {
				strand ^= 1
				next++
			}
			a := parent.allele(m, strand)
			if mutationRate > 0 && !gm.layout.isIndicator(m) && rng.Float64() < mutationRate {
				a = 1 - a
			}
			child.geno[m] += a
			if s == 1 && a == 1 {
				child.setPhaseBit(m)
			}
			if slot := gm.trackerOf[m]; slot >= 0 && len(parent.ibd) == 2*T {
				child.ibd[s*T+int(slot)] = parent.ibd[strand*T+int(slot)]
			}
		}
	}
}

// transmitPolyploid writes one diploid gamete of an autotetraploid parent
// into homologues first and first+1 of child. Per chromosome the four
// homologues pair at random into two bivalents (random chromosome
// segregation); within each bivalent crossovers fall as in a diploid
// meiosis and the gamete takes one recombinant chromatid from each. Double
// reduction (both sister chromatids of one homologue in a gamete) is not
// modelled.
func (gm *genomeMap) transmitPolyploid(parent, child *organism, first int, mutationRate float64, rng *rand.Rand, xo []float64) {
	ploidy := len(parent.strands)
	T := len(gm.trackers)
	for c := 0; c+1 < len(gm.chromStart); c++ {
		perm := rng.Perm(ploidy)
		for b := 0; b < 2; b++ {
			pair := [2]int{perm[2*b], perm[2*b+1]}
			side := rng.Intn(2)
			side0 := side
			k := poisson(gm.lengthM[c], rng)
			xo = xo[:0]
			for j := 0; j < k; j++ {
				xo = append(xo, rng.Float64()*gm.lengthM[c])
			}
			sort.Float64s(xo)
			next := 0
			target := first + b
			for j, l := range gm.linked {
				if l.chrom != c {
					continue
				}
				m := gm.linkedIndex(j)
				child.setAllele(m, target, parent.allele(m, pair[side0^(sort.SearchFloat64s(xo, l.posM)&1)]))
			}
			for m := gm.chromStart[c]; m < gm.chromStart[c+1]; m++ {
				for next < len(xo) && xo[next] < gm.posM[m] {
					side ^= 1
					next++
				}
				h := pair[side]
				a := parent.allele(m, h)
				if mutationRate > 0 && !gm.layout.isIndicator(m) && rng.Float64() < mutationRate {
					a = 1 - a
				}
				child.setAllele(m, target, a)
				if slot := gm.trackerOf[m]; slot >= 0 && len(parent.ibd) == ploidy*T {
					child.ibd[target*T+int(slot)] = parent.ibd[h*T+int(slot)]
				}
			}
		}
	}
}

// poisson draws from Poisson(lambda) by Knuth's method; lambda here is a
// chromosome length in Morgans (≈ 0.1–5), where the method is exact and cheap.
func poisson(lambda float64, rng *rand.Rand) int {
	if lambda <= 0 {
		return 0
	}
	limit := math.Exp(-lambda)
	k := 0
	p := rng.Float64()
	for p > limit {
		k++
		p *= rng.Float64()
	}
	return k
}

// ibdCoancestry is the probability that two distinct allele copies drawn
// at random from the population (within or between individuals) are
// identical by descent from the same founder haplotype, averaged over
// tracker loci. Under random mating it is the expected inbreeding of the
// next generation; it is 0 for the founder generation.
func ibdCoancestry(pop []organism) float64 {
	return ibdCoancestryPairs(pop, false)
}

// ibdCoancestryPairs is ibdCoancestry; with betweenOnly it ignores the two
// copies inside one individual — coancestry among lines, the risk measure
// for line breeding, where within-line homozygosity is the goal (v0.8.8).
func ibdCoancestryPairs(pop []organism, betweenOnly bool) float64 {
	if len(pop) == 0 || len(pop[0].ibd) == 0 {
		return 0
	}
	ploidy := ploidyOf(pop)
	T := len(pop[0].ibd) / ploidy
	copies := ploidy * len(pop)
	counts := make([]int32, copies)
	touched := make([]uint16, 0, copies)
	var sum float64
	for t := 0; t < T; t++ {
		for i := range pop {
			for s := 0; s < ploidy; s++ {
				lab := pop[i].ibd[s*T+t]
				if int(lab) >= len(counts) {
					grown := make([]int32, int(lab)+1)
					copy(grown, counts)
					counts = grown
				}
				if counts[lab] == 0 {
					touched = append(touched, lab)
				}
				counts[lab]++
			}
		}
		var pairs float64
		for _, lab := range touched {
			c := float64(counts[lab])
			pairs += c * (c - 1)
			counts[lab] = 0
		}
		touched = touched[:0]
		if betweenOnly {
			var within float64
			for i := range pop {
				for s := 0; s < ploidy; s++ {
					for u := 0; u < ploidy; u++ {
						if s != u && pop[i].ibd[s*T+t] == pop[i].ibd[u*T+t] {
							within++
						}
					}
				}
			}
			sum += (pairs - within) / (float64(copies)*float64(copies-1) - float64(len(pop)*ploidy*(ploidy-1)))
			continue
		}
		sum += pairs / (float64(copies) * float64(copies-1))
	}
	return sum / float64(T)
}

// parseSiteList parses "chrom:pos, chrom:pos, ..." (the comment line the
// tools/data fetch scripts write) or a slice of "chrom:pos" header labels.
// Returns nil if any entry does not parse.
func parseSiteList(items []string) []markerSite {
	out := make([]markerSite, 0, len(items))
	for _, it := range items {
		it = strings.TrimSpace(it)
		i := strings.LastIndex(it, ":")
		if i <= 0 || i == len(it)-1 {
			return nil
		}
		pos, err := strconv.ParseInt(it[i+1:], 10, 64)
		if err != nil || pos < 0 {
			return nil
		}
		out = append(out, markerSite{chrom: it[:i], pos: pos})
	}
	return out
}

// v0.8.8 — Issue 39. Inbred-line derivation for self-pollinating crops.

const (
	matingOutcross        = "random"
	matingSSD             = "ssd"
	matingDoubledHaploid  = "dh"
	defaultSSDGenerations = 4
)

// selfed returns one offspring of o selfed: two independent gametes of o.
func (gm *genomeMap) selfed(o *organism, mutationRate float64, rng *rand.Rand, xo []float64) organism {
	child := gm.newOrganism()
	gm.transmit(o, &child, 0, mutationRate, rng, xo)
	gm.transmit(o, &child, 1, mutationRate, rng, xo)
	return child
}

// doubledHaploid returns a fully homozygous line from one gamete of o; both
// strands carry the same alleles and the same IBD labels.
func (gm *genomeMap) doubledHaploid(o *organism, mutationRate float64, rng *rand.Rand, xo []float64) organism {
	child := gm.newOrganism()
	gm.transmit(o, &child, 0, mutationRate, rng, xo)
	T := len(gm.trackers)
	for m := range child.geno {
		child.geno[m] *= 2
	}
	copy(child.ibd[T:], child.ibd[:T])
	return child
}

// deriveLines turns each F1 in pop into an inbred line in place: one
// doubled-haploid step, or ssdGenerations rounds of single-seed descent.
// Outcrossing leaves pop unchanged.
func deriveLines(pop []organism, system string, ssdGenerations int, gm *genomeMap, mutationRate float64, rng *rand.Rand) {
	xo := make([]float64, 0, 16)
	switch system {
	case matingDoubledHaploid:
		for i := range pop {
			pop[i] = gm.doubledHaploid(&pop[i], mutationRate, rng, xo)
		}
	case matingSSD:
		for i := range pop {
			for k := 0; k < ssdGenerations; k++ {
				pop[i] = gm.selfed(&pop[i], mutationRate, rng, xo)
			}
		}
	}
}

// homozygousFounders doubles one gamete of every founder whose observed
// heterozygosity exceeds maxHet, so a line-breeding run starts from inbred
// lines. Real inbred accessions (heterozygosity ≤ maxHet) are kept as they
// are. Returns how many founders were converted.
func homozygousFounders(pop []organism, gm *genomeMap, maxHet float64, rng *rand.Rand) int {
	converted := 0
	xo := make([]float64, 0, 16)
	for i := range pop {
		het := 0
		for _, g := range pop[i].geno {
			if g == 1 {
				het++
			}
		}
		if float64(het) <= maxHet*float64(len(pop[i].geno)) {
			continue
		}
		labels := append([]uint16(nil), pop[i].ibd...)
		pop[i] = gm.doubledHaploid(&pop[i], 0, rng, xo)
		// Founders stay the IBD base: keep their two distinct labels.
		copy(pop[i].ibd, labels)
		converted++
	}
	return converted
}
