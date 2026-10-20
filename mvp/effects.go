package main

// v0.8.6 — Issue 37. Marker effects estimated from uploaded phenotypes.
//
// When an upload carries phenotypes for at least minPhenotypedForEffects
// genotyped lines, the trait architecture is no longer random synthetic
// QTL: additive marker effects are estimated by ridge regression (SNP-BLUP
// / rrBLUP) and used as the effects for forward simulation.
//
//	y = μ + Xβ + e,  β ~ N(0, σβ² I),  λ = σe² / σβ² = (1 − h²)/h² · Σ 2pq
//	β̂ = X'(XX' + λI)⁻¹ (y − ȳ)      (n×n dual form; X centred dosages)
//
// h² is the request's heritability, the same value that sets the
// environmental variance of the forward simulation. Prediction quality is
// reported by k-fold cross-validation: predictive ability r(ŷ, y) and the
// implied accuracy r / h.

import (
	"errors"
	"math"
	"math/rand"
)

const (
	minPhenotypedForEffects = 20
	maxPhenotypedForEffects = 1000 // bounds the n×n solve (n³/3 flops)
	effectCVFolds           = 5
)

// EffectEstimateSummary is reported in the response when effects come
// from phenotypes.
type EffectEstimateSummary struct {
	Trait                string  `json:"trait"`
	Phenotyped           int     `json:"phenotyped"`
	Markers              int     `json:"markers"`
	HeritabilityAssumed  float64 `json:"heritability_assumed"`
	Lambda               float64 `json:"lambda"`
	CVPredictiveAbility  float64 `json:"cv_predictive_ability"`
	CVAccuracyApprox     float64 `json:"cv_accuracy_approx"`
	Folds                int     `json:"folds"`
	SubsampledPhenotyped bool    `json:"subsampled_phenotyped,omitempty"`
}

// genomicKernel returns K = XX' for centred genotype rows X.
func genomicKernel(X [][]float64) [][]float64 {
	n := len(X)
	K := make([][]float64, n)
	for i := range K {
		K[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			var s float64
			xi, xj := X[i], X[j]
			for k := range xi {
				s += xi[k] * xj[k]
			}
			K[i][j], K[j][i] = s, s
		}
	}
	return K
}

// ridgeEffects fits β̂ = X_S'(K_SS + λI)⁻¹(y_S − ȳ_S) on the rows in idx,
// reusing the precomputed kernel K = XX'.
func ridgeEffects(X, K [][]float64, y []float64, idx []int, lambda float64) ([]float64, error) {
	n := len(idx)
	if n == 0 {
		return nil, errors.New("no phenotyped individuals")
	}
	var ybar float64
	for _, i := range idx {
		ybar += y[i]
	}
	ybar /= float64(n)
	sub := make([][]float64, n)
	rhs := make([]float64, n)
	for a, i := range idx {
		sub[a] = make([]float64, n)
		for b, j := range idx {
			sub[a][b] = K[i][j]
		}
		sub[a][a] += lambda
		rhs[a] = y[i] - ybar
	}
	L, err := choleskyDecomp(sub)
	if err != nil {
		return nil, err
	}
	alpha := choleskySolve(L, rhs)
	beta := make([]float64, len(X[idx[0]]))
	for a, i := range idx {
		for k, x := range X[i] {
			beta[k] += x * alpha[a]
		}
	}
	return beta, nil
}

func pearson(a, b []float64) float64 {
	ma, va := meanVar(a)
	mb, vb := meanVar(b)
	if va <= 0 || vb <= 0 {
		return 0
	}
	var c float64
	for i := range a {
		c += (a[i] - ma) * (b[i] - mb)
	}
	return c / float64(len(a)) / math.Sqrt(va*vb)
}

// estimateMarkerEffects centres the dosages of the phenotyped lines,
// fits SNP-BLUP and cross-validates it. geno rows are 0/1/2 dosages at the
// simulation's marker columns.
func estimateMarkerEffects(geno [][]uint8, y []float64, h2 float64, rng *rand.Rand) ([]float64, EffectEstimateSummary, error) {
	n := len(geno)
	sum := EffectEstimateSummary{Phenotyped: n, HeritabilityAssumed: h2, Folds: effectCVFolds}
	if n < minPhenotypedForEffects {
		return nil, sum, errors.New("too few phenotyped lines")
	}
	m := len(geno[0])
	sum.Markers = m
	means := make([]float64, m)
	for _, row := range geno {
		for k, g := range row {
			means[k] += float64(g)
		}
	}
	var sum2pq float64
	for k := range means {
		means[k] /= float64(n)
		p := means[k] / 2
		sum2pq += 2 * p * (1 - p)
	}
	X := make([][]float64, n)
	for i, row := range geno {
		X[i] = make([]float64, m)
		for k, g := range row {
			X[i][k] = float64(g) - means[k]
		}
	}
	if h2 <= 0 || h2 >= 1 {
		h2 = 0.5
	}
	lambda := (1 - h2) / h2 * math.Max(sum2pq, 1e-6)
	sum.Lambda = round4(lambda)

	// k-fold cross-validation on the same centring and kernel.
	K := genomicKernel(X)
	order := rng.Perm(n)
	preds := make([]float64, n)
	for f := 0; f < effectCVFolds; f++ {
		var train, test []int
		for pos, i := range order {
			if pos%effectCVFolds == f {
				test = append(test, i)
			} else {
				train = append(train, i)
			}
		}
		b, err := ridgeEffects(X, K, y, train, lambda)
		if err != nil {
			return nil, sum, err
		}
		for _, i := range test {
			var s float64
			for k := range b {
				s += X[i][k] * b[k]
			}
			preds[i] = s
		}
	}
	sum.CVPredictiveAbility = round4(pearson(preds, y))
	sum.CVAccuracyApprox = round4(math.Min(1, math.Max(0, sum.CVPredictiveAbility/math.Sqrt(h2))))

	all := make([]int, n)
	for i := range all {
		all[i] = i
	}
	beta, err := ridgeEffects(X, K, y, all, lambda)
	if err != nil {
		return nil, sum, err
	}
	return beta, sum, nil
}

// effectsFromUpload returns estimated effects for the simulation's marker
// columns when the upload has enough phenotyped genotyped lines, or nil.
func effectsFromUpload(up *UploadedDataset, cols []int, h2 float64, rng *rand.Rand) ([]float64, *EffectEstimateSummary, error) {
	if up == nil || up.Phenotype == nil || up.Genotype == nil || up.Genotype.dataset == nil {
		return nil, nil, nil
	}
	ds := up.Genotype.dataset
	var rows [][]uint8
	var y []float64
	for i, id := range ds.accessionIDs {
		v, ok := up.Phenotype.values[id]
		if !ok {
			continue
		}
		row := make([]uint8, len(cols))
		for k, c := range cols {
			row[k] = ds.individuals[i].geno[c]
		}
		rows = append(rows, row)
		y = append(y, v)
	}
	if len(rows) < minPhenotypedForEffects {
		return nil, nil, nil
	}
	subsampled := false
	if len(rows) > maxPhenotypedForEffects {
		perm := rng.Perm(len(rows))[:maxPhenotypedForEffects]
		r2, y2 := make([][]uint8, len(perm)), make([]float64, len(perm))
		for k, i := range perm {
			r2[k], y2[k] = rows[i], y[i]
		}
		rows, y, subsampled = r2, y2, true
	}
	beta, sum, err := estimateMarkerEffects(rows, y, h2, rng)
	if err != nil {
		return nil, nil, err
	}
	sum.Trait = up.Phenotype.TraitName
	sum.SubsampledPhenotyped = subsampled
	return beta, &sum, nil
}
