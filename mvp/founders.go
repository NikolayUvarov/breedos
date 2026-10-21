package main

// v0.8.12 — founder set from a collection.
//
// A marker lab's collection table (genebank accessions × SSR) answers a
// breeding question before any selection: if a programme starts from n
// accessions, which n keep the most alleles? The greedy maximum-coverage
// pick (each step adds the accession with most alleles not yet covered)
// is within 1 − 1/e of the optimum and is what core-collection tools do
// (e.g. the M strategy of Schoen & Brown 1993). The result is compared
// with random sets of the same size.

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
)

const (
	founderSetDefault   = 20
	founderSetRandomSet = 200 // random subsets drawn for the comparison
)

type founderSetRequest struct {
	Upload string `json:"upload"`
	Size   int    `json:"size"`
}

type founderSetResponse struct {
	Size          int      `json:"size"`
	Individuals   int      `json:"individuals"`
	IDs           []string `json:"ids"`
	AllelesKept   int      `json:"alleles_kept"`
	AllelesTotal  int      `json:"alleles_total"`
	Curve         []int    `json:"curve"`          // alleles kept by the first k picks
	RandomAlleles float64  `json:"random_alleles"` // mean over random sets of the same size
	AllForKept    int      `json:"all_for_kept"`   // smallest greedy set holding every allele (0 = never)
	HeSubset      float64  `json:"he_subset"`
	HeAll         float64  `json:"he_all"`
	Note          string   `json:"note"`
}

// alleleSets lists, per individual, the alleles it carries: indicator
// markers with the allele present in a multi-allelic layout, or both
// alleles of every biallelic marker otherwise.
func alleleSets(ds *loadedDataset) ([][]int, int) {
	n := len(ds.individuals)
	sets := make([][]int, n)
	if ds.layout != nil {
		var universe []int
		for m := range ds.layout.locusOf {
			if ds.layout.isIndicator(m) {
				universe = append(universe, m)
			}
		}
		for i := range ds.individuals {
			for _, m := range universe {
				if ds.individuals[i].geno[m] > 0 {
					sets[i] = append(sets[i], m)
				}
			}
		}
		return sets, len(universe)
	}
	ploidy := ploidyOf(ds.individuals)
	for i := range ds.individuals {
		for m, g := range ds.individuals[i].geno {
			if g > 0 {
				sets[i] = append(sets[i], 2*m+1)
			}
			if int(g) < ploidy {
				sets[i] = append(sets[i], 2*m)
			}
		}
	}
	return sets, 2 * ds.markerCount
}

// greedyFounderSet picks size individuals covering the most alleles, and
// returns the picks in order with the cumulative coverage.
func greedyFounderSet(sets [][]int, universe, size int) (picks []int, curve []int) {
	covered := make([]bool, universe)
	used := make([]bool, len(sets))
	total := 0
	for len(picks) < size && len(picks) < len(sets) {
		best, bestGain := -1, -1
		for i, s := range sets {
			if used[i] {
				continue
			}
			gain := 0
			for _, a := range s {
				if !covered[a] {
					gain++
				}
			}
			if gain > bestGain {
				best, bestGain = i, gain
			}
		}
		used[best] = true
		for _, a := range sets[best] {
			if !covered[a] {
				covered[a] = true
				total++
			}
		}
		picks = append(picks, best)
		curve = append(curve, total)
	}
	return picks, curve
}

func coverage(sets [][]int, universe int, idx []int) int {
	covered := make([]bool, universe)
	n := 0
	for _, i := range idx {
		for _, a := range sets[i] {
			if !covered[a] {
				covered[a] = true
				n++
			}
		}
	}
	return n
}

func founderSet(ds *loadedDataset, size int) founderSetResponse {
	n := len(ds.individuals)
	if size <= 0 {
		size = founderSetDefault
	}
	if size > n {
		size = n
	}
	sets, universe := alleleSets(ds)
	picks, curve := greedyFounderSet(sets, universe, size)
	allAlleles := coverage(sets, universe, seq(n))
	rng := rand.New(rand.NewSource(0x5e7))
	randSum := 0
	for r := 0; r < founderSetRandomSet; r++ {
		randSum += coverage(sets, universe, rng.Perm(n)[:size])
	}
	allFor := 0
	if size == n || curve[len(curve)-1] == allAlleles {
		for k, c := range curve {
			if c == allAlleles {
				allFor = k + 1
				break
			}
		}
	}
	subset := make([]organism, len(picks))
	ids := make([]string, len(picks))
	for k, i := range picks {
		subset[k] = ds.individuals[i]
		ids[k] = ds.accessionIDs[i]
	}
	out := founderSetResponse{
		Size: size, Individuals: n, IDs: ids,
		AllelesKept: curve[len(curve)-1], AllelesTotal: allAlleles, Curve: curve,
		RandomAlleles: round4(float64(randSum) / founderSetRandomSet), AllForKept: allFor,
		HeSubset: round4(diversityOf(alleleFreq(subset, ds.markerCount), ds.layout)),
		HeAll:    round4(diversityOf(alleleFreq(ds.individuals, ds.markerCount), ds.layout)),
	}
	kind := "alleles"
	if ds.layout == nil {
		kind = "marker alleles (both alleles of each biallelic marker)"
	}
	out.Note = fmt.Sprintf("Greedy maximum coverage: each pick adds the accession with the most %s not yet in the set (within 1 − 1/e of the best possible set). Random sets of %d accessions keep %.0f of %d on average.", kind, size, out.RandomAlleles, allAlleles)
	return out
}

func seq(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

// founderSetHandler: POST /api/founder-set {"upload": id, "size": n}.
func founderSetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	var req founderSetRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	up, ok := getUpload(req.Upload)
	if !ok || up == nil || up.Genotype == nil || up.Genotype.dataset == nil {
		http.Error(w, "founder set needs an uploaded genotype table (upload id)", http.StatusBadRequest)
		return
	}
	if req.Size < 0 || req.Size > 5000 {
		http.Error(w, "size must be between 1 and the number of accessions", http.StatusBadRequest)
		return
	}
	writeJSON(w, founderSet(up.Genotype.dataset, req.Size))
}

// sortedCopy is a small helper for tests.
func sortedCopy(v []int) []int {
	out := append([]int(nil), v...)
	sort.Ints(out)
	return out
}
