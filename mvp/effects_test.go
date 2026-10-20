package main

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Issue 37 — marker effects estimated from phenotypes.

// knownArchitecture builds n lines × m markers with sparse true effects and
// phenotypes at the given heritability.
func knownArchitecture(n, m, qtl int, h2 float64, seed int64) (geno [][]uint8, trueEffects, g, y []float64) {
	rng := rand.New(rand.NewSource(seed))
	pop := makeInitialPopulation(n, m, rng)
	trueEffects = makeEffects(m, qtl, rng)
	g = geneticValues(pop, trueEffects)
	_, vg := meanVar(g)
	sdE := math.Sqrt(vg * (1 - h2) / h2)
	geno = make([][]uint8, n)
	y = make([]float64, n)
	for i := range pop {
		geno[i] = pop[i].geno
		y[i] = g[i] + rng.NormFloat64()*sdE
	}
	return
}

func TestSNPBLUPRecoversKnownArchitecture(t *testing.T) {
	const n, m, h2 = 400, 500, 0.5
	geno, trueEff, g, y := knownArchitecture(n, m, 50, h2, 21)
	beta, sum, err := estimateMarkerEffects(geno, y, h2, rand.New(rand.NewSource(3)))
	if err != nil {
		t.Fatal(err)
	}
	// Daetwyler et al. (2008): r = √(N h² / (N h² + Me)); with unlinked
	// markers Me ≈ m, so r ≈ √(200/700) ≈ 0.53 and r(ŷ, y) ≈ r·h ≈ 0.38.
	if sum.CVPredictiveAbility < 0.2 || sum.CVPredictiveAbility > 0.7 {
		t.Fatalf("CV predictive ability %.3f outside the expected range for n=%d, m=%d, h²=%.1f", sum.CVPredictiveAbility, n, m, h2)
	}
	t.Logf("CV predictive ability %.3f, accuracy ≈ %.3f, r(β̂, β) = %.3f", sum.CVPredictiveAbility, sum.CVAccuracyApprox, pearson(beta, trueEff))
	if r := pearson(beta, trueEff); r < 0.3 {
		t.Fatalf("estimated effects correlate %.3f with the true effects", r)
	}
	pop := make([]organism, n)
	for i := range geno {
		pop[i] = organism{geno: geno[i]}
	}
	if r := pearson(geneticValues(pop, beta), g); r < 0.6 {
		t.Fatalf("in-sample genetic values correlate %.3f with the truth", r)
	}
}

func uploadCSVs(n, m int, seed int64, idPrefix string) (string, string) {
	geno, _, _, y := knownArchitecture(n, m, 30, 0.5, seed)
	var gb, pb strings.Builder
	gb.WriteString("id")
	for k := 0; k < m; k++ {
		fmt.Fprintf(&gb, ",chr%d:%d", 1+k*3/m, 1000*(k+1))
	}
	gb.WriteString("\n")
	pb.WriteString("id,yield\n")
	for i := range geno {
		fmt.Fprintf(&gb, "line_%03d", i)
		for _, v := range geno[i] {
			fmt.Fprintf(&gb, ",%d", v)
		}
		gb.WriteString("\n")
		fmt.Fprintf(&pb, "%s%03d,%.4f\n", idPrefix, i, y[i])
	}
	return gb.String(), pb.String()
}

func postUpload(t *testing.T, geno, pheno string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("genotype", "genotype.csv")
	fw.Write([]byte(geno))
	fw, _ = mw.CreateFormFile("phenotype", "phenotype.csv")
	fw.Write([]byte(pheno))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	uploadHandler(rr, req)
	return rr
}

func TestUploadedPhenotypeDrivesTraitArchitecture(t *testing.T) {
	resetUploadCache()
	geno, pheno := uploadCSVs(120, 150, 8, "line_")
	rr := postUpload(t, geno, pheno)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "marker effects estimated by rrBLUP") {
		t.Fatalf("upload should accept and mark the phenotype as used: %d %s", rr.Code, rr.Body.String())
	}
	var id string
	uploadCache.Lock()
	for k := range uploadCache.items {
		id = k
	}
	uploadCache.Unlock()
	resp, err := runSimulation(SimRequest{Seed: 1, Upload: id, PopulationSize: 100, Markers: 150, QTLCount: 10, Generations: 5, SelectionPercent: 20, Heritability: 0.5, StrategySet: "core", Replicates: 1})
	if err != nil {
		t.Fatal(err)
	}
	if resp.EffectsSource != "estimated_from_phenotype" || resp.EffectEstimate == nil || resp.EffectEstimate.Phenotyped != 120 {
		t.Fatalf("expected estimated effects from 120 lines, got %q %+v", resp.EffectsSource, resp.EffectEstimate)
	}
	found := false
	for _, n := range resp.Notes {
		if strings.HasPrefix(n, "Trait architecture from your data") {
			found = true
		}
	}
	if !found {
		t.Fatal("notes must state that the architecture comes from the uploaded phenotype")
	}
}

func TestPhenotypeIDMismatchRejected(t *testing.T) {
	resetUploadCache()
	geno, pheno := uploadCSVs(40, 50, 9, "plot_")
	rr := postUpload(t, geno, pheno)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "none of the 40 phenotype ids match") {
		t.Fatalf("mismatched ids must be a clear 400, got %d %s", rr.Code, rr.Body.String())
	}
}
