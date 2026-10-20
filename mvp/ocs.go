package main

// v0.8.5 — Issue 36. Optimum contribution selection with a ΔF target.
//
// Each generation the strategy chooses parent contributions c (Σc = 1,
// c ≥ 0) that maximise the expected selection response c'g subject to the
// group coancestry of the next generation c'Fc reaching a target set by
// the requested rate of inbreeding:
//
//	C_target = θ_t + ΔF · (1 − θ_t)
//
// where θ_t is the current population coancestry (IBD relative to the
// founders) and F is the coancestry matrix among candidates, computed from
// the founder-haplotype labels that genome.go tracks. The Lagrangian
// solution is Meuwissen (1997, Genet. Sel. Evol. 29:497):
//
//	c = F⁻¹(g − μ1) / λ,   λ = √((d − b²/a) / (C − 1/a)),   μ = (b − λ) / a
//
// with a = 1'F⁻¹1, b = 1'F⁻¹g, d = g'F⁻¹g. Candidates with negative
// contributions are dropped and the system is re-solved until every
// contribution is non-negative. Contributions are then turned into exact
// gamete counts, so family sizes do not add sampling drift on top of the
// target.

import (
	"math"
	"math/rand"
	"sort"
)

const (
	ocsMaxCandidates    = 200 // bounds the K×K coancestry system per generation
	ocsMaxTrackers      = 128 // tracker loci used for the coancestry matrix
	ocsRidge            = 1e-6
	defaultTargetDeltaF = 0.01
)

// coancestryMatrix returns f_ij for the given individuals: the probability
// that random alleles drawn from i and j are IBD, averaged over up to
// ocsMaxTrackers tracker loci. f_ii = (1 + F_i) / 2.
func coancestryMatrix(pop []organism, ids []int) [][]float64 {
	k := len(ids)
	F := make([][]float64, k)
	for i := range F {
		F[i] = make([]float64, k)
	}
	if k == 0 || len(pop[ids[0]].ibd) == 0 {
		return F
	}
	T := len(pop[ids[0]].ibd) / 2
	stride := 1
	if T > ocsMaxTrackers {
		stride = T / ocsMaxTrackers
	}
	var used float64
	for t := 0; t < T; t += stride {
		used++
	}
	for x := 0; x < k; x++ {
		a := pop[ids[x]].ibd
		for y := x; y < k; y++ {
			b := pop[ids[y]].ibd
			var same float64
			for t := 0; t < T; t += stride {
				aA, aB, bA, bB := a[t], a[T+t], b[t], b[T+t]
				if aA == bA {
					same++
				}
				if aA == bB {
					same++
				}
				if aB == bA {
					same++
				}
				if aB == bB {
					same++
				}
			}
			f := same / (4 * used)
			F[x][y], F[y][x] = f, f
		}
	}
	return F
}

// choleskySolve solves L L' x = b for lower-triangular L.
func choleskySolve(L [][]float64, b []float64) []float64 {
	n := len(b)
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		s := b[i]
		for k := 0; k < i; k++ {
			s -= L[i][k] * y[k]
		}
		y[i] = s / L[i][i]
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := y[i]
		for k := i + 1; k < n; k++ {
			s -= L[k][i] * x[k]
		}
		x[i] = s / L[i][i]
	}
	return x
}

// ocsContributions returns non-negative contributions summing to 1 that
// maximise c'g subject to c'Fc = target (Meuwissen 1997). When the target
// is below the minimum achievable coancestry, it returns the
// minimum-coancestry contributions.
func ocsContributions(g []float64, F [][]float64, target float64) []float64 {
	n := len(g)
	c := make([]float64, n)
	active := make([]int, n)
	for i := range active {
		active[i] = i
	}
	for len(active) > 0 {
		m := len(active)
		sub := make([][]float64, m)
		gs := make([]float64, m)
		ones := make([]float64, m)
		for x, i := range active {
			sub[x] = make([]float64, m)
			for y, j := range active {
				sub[x][y] = F[i][j]
			}
			sub[x][x] += ocsRidge
			gs[x] = g[i]
			ones[x] = 1
		}
		L, err := choleskyDecomp(sub)
		if err != nil {
			break
		}
		fInv1 := choleskySolve(L, ones)
		fInvG := choleskySolve(L, gs)
		var a, b, d float64
		for x := 0; x < m; x++ {
			a += fInv1[x]
			b += fInvG[x]
			d += gs[x] * fInvG[x]
		}
		cs := make([]float64, m)
		if target <= 1/a+1e-12 || d-b*b/a <= 1e-12 {
			for x := range cs {
				cs[x] = fInv1[x] / a
			}
		} else {
			lambda := math.Sqrt((d - b*b/a) / (target - 1/a))
			mu := (b - lambda) / a
			for x := range cs {
				cs[x] = (fInvG[x] - mu*fInv1[x]) / lambda
			}
		}
		next := active[:0:0]
		for x, i := range active {
			if cs[x] > 0 {
				next = append(next, i)
			}
		}
		if len(next) == m {
			for x, i := range active {
				c[i] = cs[x]
			}
			return c
		}
		active = next
	}
	// Degenerate system: fall back to the single best candidate's group.
	best := 0
	for i := range g {
		if g[i] > g[best] {
			best = i
		}
	}
	c[best] = 1
	return c
}

// allocateGametes turns contributions into exactly 2·nOffspring gametes by
// largest remainder, shuffles them, and pairs them into offspring, avoiding
// self-pairs where another pair allows a swap. The result is a gamete list
// consumed pairwise by makeNextGeneration's "allocated" mating rule.
func allocateGametes(ids []int, c []float64, nOffspring int, rng *rand.Rand) []int {
	total := 2 * nOffspring
	type share struct {
		id   int
		n    int
		frac float64
	}
	shares := make([]share, 0, len(ids))
	assigned := 0
	for x, id := range ids {
		if c[x] <= 0 {
			continue
		}
		exact := c[x] * float64(total)
		n := int(math.Floor(exact))
		shares = append(shares, share{id, n, exact - float64(n)})
		assigned += n
	}
	sort.SliceStable(shares, func(i, j int) bool { return shares[i].frac > shares[j].frac })
	for i := 0; assigned < total && len(shares) > 0; i = (i + 1) % len(shares) {
		shares[i].n++
		assigned++
	}
	gametes := make([]int, 0, total)
	for _, s := range shares {
		for k := 0; k < s.n; k++ {
			gametes = append(gametes, s.id)
		}
	}
	rng.Shuffle(len(gametes), func(i, j int) { gametes[i], gametes[j] = gametes[j], gametes[i] })
	// Break self-pairs: swap the second gamete with one from another pair
	// whose members both differ from this parent (j^1 is j's partner).
	for i := 0; i+1 < len(gametes); i += 2 {
		p := gametes[i]
		if gametes[i+1] != p {
			continue
		}
		for try := 0; try < 32; try++ {
			j := rng.Intn(len(gametes))
			if gametes[j] != p && gametes[j^1] != p {
				gametes[i+1], gametes[j] = gametes[j], gametes[i+1]
				break
			}
		}
	}
	return gametes
}

// selectOCSTarget runs one OCS generation on candidates already scored and
// sorted by descending score. It returns a gamete list of length 2·len(pop).
func selectOCSTarget(pop []organism, scored []scoredIndividual, targetDeltaF float64, rng *rand.Rand) []int {
	k := len(scored)
	if k > ocsMaxCandidates {
		k = ocsMaxCandidates
	}
	ids := make([]int, k)
	g := make([]float64, k)
	for x := 0; x < k; x++ {
		ids[x] = scored[x].idx
		g[x] = scored[x].score
	}
	theta := ibdCoancestry(pop)
	target := theta + targetDeltaF*(1-theta)
	c := ocsContributions(g, coancestryMatrix(pop, ids), target)
	return allocateGametes(ids, c, len(pop), rng)
}

func distinctParents(parents []int) int {
	seen := make(map[int]struct{}, len(parents))
	for _, p := range parents {
		seen[p] = struct{}{}
	}
	return len(seen)
}
