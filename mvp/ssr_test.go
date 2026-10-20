package main

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

// v0.8.9 — multi-allelic (SSR) loci.

func TestParseSSRSlashFormat(t *testing.T) {
	raw := "id,RM1,RM21\nL1,103/107,125\nL2,107,125/131\nL3,0,131\nL4,103,129\n"
	ds, err := parseDatasetCSV([]byte(raw), "slash.csv")
	if err != nil {
		t.Fatal(err)
	}
	l := ds.layout
	if l == nil || l.loci() != 2 || ds.markerCount != 2+3 {
		t.Fatalf("expected 2 loci with 2 + 3 alleles, got layout %+v markers %d", l, ds.markerCount)
	}
	if strings.Join(l.alleles[0], ",") != "103,107" || strings.Join(l.alleles[1], ",") != "125,129,131" {
		t.Fatalf("allele labels: %v", l.alleles)
	}
	if ds.ssr.ImputedCalls != 1 || ds.ssr.TotalCalls != 8 || ds.ssr.Format != "one column per locus" {
		t.Fatalf("summary: %+v", ds.ssr)
	}
	// L4 is homozygous 103 at RM1: dosage 2 on the first indicator.
	if ds.individuals[3].geno[0] != 2 || ds.individuals[3].geno[1] != 0 {
		t.Fatalf("homozygous call should be dosage 2, got %v", ds.individuals[3].geno[:2])
	}
}

func TestParseSSRTwoColumnAndGenAlEx(t *testing.T) {
	cases := map[string]string{
		"two columns per locus": "id;RM1_1;RM1_2;RM5_1;RM5_2\nA;103;107;150;150\nB;107;107;150;152\n",
		"GenAlEx":               "2,2,1,2\nexample,,,pop1\nSample,Pop,RM1,,RM5,\nA,pop1,103,107,150,150\nB,pop1,107,107,150,152\n",
	}
	for want, raw := range cases {
		ds, err := parseDatasetCSV([]byte(raw), want)
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if ds.ssr.Format != want || ds.layout.loci() != 2 || strings.Join(ds.layout.names, ",") != "RM1,RM5" {
			t.Fatalf("%s: format %q loci %v", want, ds.ssr.Format, ds.layout.names)
		}
		if ds.accessionIDs[1] != "B" {
			t.Fatalf("%s: ids %v", want, ds.accessionIDs)
		}
	}
}

// Loci named RM1, RM2 must stay two loci, not be read as the two columns
// of a locus "RM".
func TestSSRLociWithDigitsAreNotPaired(t *testing.T) {
	ds, err := parseDatasetCSV([]byte("id,RM1,RM2\nA,103/107,150\nB,107,152/154\n"), "rm.csv")
	if err != nil {
		t.Fatal(err)
	}
	if ds.layout.loci() != 2 || ds.ssr.Format != "one column per locus" {
		t.Fatalf("RM1,RM2 misread: %v %q", ds.layout.names, ds.ssr.Format)
	}
}

func TestSSRPolyploidRejected(t *testing.T) {
	_, err := parseDatasetCSV([]byte("id,STM1\nA,150/152/158\nB,150\n"), "potato.csv")
	if err == nil || !strings.Contains(err.Error(), "polyploid") {
		t.Fatalf("tetraploid profile must be rejected with a clear message, got %v", err)
	}
}

func TestDosageTableWithTypoIsNotReadAsSSR(t *testing.T) {
	_, err := parseDatasetCSV([]byte("id,m1,m2,m3,m4\nA,0,1,2,1\nB,2,1,3,0\n"), "typo.csv")
	if err == nil || !strings.Contains(err.Error(), "outside 0..2") {
		t.Fatalf("a typo in a 0/1/2 table must report the dosage error, got %v", err)
	}
}

func ssrFixture(t *testing.T) *loadedDataset {
	t.Helper()
	ds, err := parseDatasetCSV(mustRead(t, "fixtures/upload-toy/genotype_ssr.csv"), "genotype_ssr.csv")
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

// Every strand must carry exactly one allele of every locus after many
// meioses — with mutation switched on, which must not touch indicators.
func TestSSRLociInheritAsUnits(t *testing.T) {
	ds := ssrFixture(t)
	gm := buildLocusGenomeMap(ds.layout, 10, 1)
	rng := rand.New(rand.NewSource(5))
	pop := clonePopulation(ds.individuals)
	initFounders(pop, gm, rng)
	parents := make([]int, len(pop))
	for i := range parents {
		parents[i] = i
	}
	for g := 0; g < 15; g++ {
		pop = makeNextGeneration(pop, parents, gm, 0.01, rng, "random")
	}
	l := ds.layout
	for i := range pop {
		for li, labels := range l.alleles {
			for s := 0; s < 2; s++ {
				n := 0
				for a := range labels {
					n += int(pop[i].allele(l.start[li]+a, s))
				}
				if n != 1 {
					t.Fatalf("individual %d locus %s strand %d carries %d alleles", i, l.names[li], s, n)
				}
			}
		}
	}
}

func TestNeiDiversityAndAllelicRichness(t *testing.T) {
	l := &locusLayout{names: []string{"L"}, start: []int{0}, alleles: [][]string{{"a", "b", "c"}}, locusOf: []int{0, 0, 0}}
	freq := []float64{0.5, 0.3, 0.2}
	if got, want := diversityOf(freq, l), 1-(0.25+0.09+0.04); math.Abs(got-want) > 1e-12 {
		t.Fatalf("Nei He %v, want %v", got, want)
	}
	if n := allelesPresent([]float64{0.7, 0.3, 0}, l); n != 2 {
		t.Fatalf("alleles present %d, want 2", n)
	}
}

func TestSubsampleKeepsWholeLoci(t *testing.T) {
	ds := ssrFixture(t)
	sub := subsampleDataset(ds, 30, ds.markerCount/2, rand.New(rand.NewSource(1)))
	if sub.layout == nil || sub.layout.markers() != sub.markerCount || sub.layout.loci() >= ds.layout.loci() {
		t.Fatalf("subsample must keep whole loci: %d markers, layout %d markers / %d loci", sub.markerCount, sub.layout.markers(), sub.layout.loci())
	}
	for i, o := range sub.individuals {
		for li, labels := range sub.layout.alleles {
			sum := 0
			for a := range labels {
				sum += int(o.geno[sub.layout.start[li]+a])
			}
			if sum != 2 {
				t.Fatalf("subsampled individual %d locus %d has dosage sum %d", i, li, sum)
			}
		}
	}
}

func TestSSRUploadRunsAndReportsAlleleRetention(t *testing.T) {
	resetUploadCache()
	up, err := parseUploadGenotype(mustRead(t, "fixtures/upload-toy/genotype_ssr.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if up.Loci == nil || up.Loci.Loci != 10 {
		t.Fatalf("upload summary should report 10 SSR loci, got %+v", up.Loci)
	}
	id := putUpload(&UploadedDataset{Genotype: up})
	resp, err := runSimulation(SimRequest{Seed: 3, Upload: id, PopulationSize: 48, Markers: 900, QTLCount: 45, Generations: 20, SelectionPercent: 20, Heritability: 0.4, StrategySet: "core", Replicates: 4, CrisprEnabled: true, CrisprEdits: 3, MatingSystem: matingSSD})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Loci == nil || len(resp.CandidateEdits) != 0 {
		t.Fatalf("SSR run: loci %+v, edit candidates %d (SSR alleles are not edit targets)", resp.Loci, len(resp.CandidateEdits))
	}
	if findByCode(resp.Strategies, "balanced_crispr") != nil {
		t.Fatal("CRISPR strategies must be dropped when there is nothing to edit")
	}
	agg, div := findByCode(resp.Strategies, "aggressive"), findByCode(resp.Strategies, "diversity")
	if div.Final.AllelesAtStart == 0 || div.Final.AllelesRetained < agg.Final.AllelesRetained {
		t.Fatalf("diversity-preserving selection should keep at least as many alleles as aggressive: %d vs %d of %d", div.Final.AllelesRetained, agg.Final.AllelesRetained, div.Final.AllelesAtStart)
	}
	if !strings.Contains(strings.Join(resp.Decision.Interpretation, " "), "SSR allelic richness") {
		t.Fatal("the decision report should summarise allele retention")
	}
	if resp.Request.QTLCount > resp.Request.Markers {
		t.Fatalf("qtl_count %d exceeds the %d markers", resp.Request.QTLCount, resp.Request.Markers)
	}
}

// A dataset with fewer markers than qtl_count used to loop forever in
// makeEffects; now the count is reduced and the notes say so.
func TestQTLCountClampedOnSmallData(t *testing.T) {
	resetUploadCache()
	up, err := parseUploadGenotype([]byte("id,RM1,RM21\nL1,103/107,125\nL2,107,125/131\nL3,103,131\nL4,103,129\nL5,107,125\nL6,103/107,129\n"))
	if err != nil {
		t.Fatal(err)
	}
	id := putUpload(&UploadedDataset{Genotype: up})
	resp, err := runSimulation(SimRequest{Seed: 1, Upload: id, PopulationSize: 6, Markers: 900, QTLCount: 45, Generations: 3, SelectionPercent: 50, Heritability: 0.5, StrategySet: "core", Replicates: 1})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Request.QTLCount != 1 || !strings.Contains(strings.Join(resp.Notes, " "), "qtl_count reduced from 45 to 1") {
		t.Fatalf("expected qtl_count 1 with a note, got %d", resp.Request.QTLCount)
	}
}

func TestResampleMultiallelicKeepsOneHot(t *testing.T) {
	ds := ssrFixture(t)
	o := clonePopulation(ds.individuals[:1])[0]
	for m := range o.geno {
		o.geno[m] = 1 // a per-marker re-roll that broke every locus
	}
	resampleMultiallelic(&o, ds.layout, alleleFreq(ds.individuals, ds.markerCount), rand.New(rand.NewSource(2)))
	for li, labels := range ds.layout.alleles {
		sum := 0
		for a := range labels {
			sum += int(o.geno[ds.layout.start[li]+a])
		}
		if sum != 2 {
			t.Fatalf("locus %d dosage sum %d after resampling", li, sum)
		}
	}
}

func TestSSRFragmentSizesRoundedToBasePairs(t *testing.T) {
	ds, err := parseDatasetCSV([]byte("id;RM1\nA;152,34/158,1\nB;151,8\nC;158\n"), "genemapper.csv")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ds.layout.alleles[0], ","); got != "152,158" {
		t.Fatalf("fragment sizes should round to 152 and 158, got %s", got)
	}
}

func TestSSRWithPhenotypesEstimatesAlleleEffects(t *testing.T) {
	resetUploadCache()
	up, err := parseUploadGenotype(mustRead(t, "fixtures/upload-toy/genotype_ssr.csv"))
	if err != nil {
		t.Fatal(err)
	}
	// Phenotype driven by the allele at RM21 plus noise.
	rng := rand.New(rand.NewSource(4))
	ph := &uploadedPhenotype{TraitName: "blast_score", values: map[string]float64{}}
	l := up.dataset.layout
	for i, id := range up.dataset.accessionIDs {
		v := rng.NormFloat64()
		v += 2 * float64(up.dataset.individuals[i].geno[l.start[2]])
		ph.values[id] = v
		ph.Rows++
	}
	ph.MatchedGenotype = ph.Rows
	id := putUpload(&UploadedDataset{Genotype: up, Phenotype: ph})
	resp, err := runSimulation(SimRequest{Seed: 2, Upload: id, PopulationSize: 48, Markers: 900, QTLCount: 10, Generations: 5, SelectionPercent: 25, Heritability: 0.5, StrategySet: "core", Replicates: 2})
	if err != nil {
		t.Fatal(err)
	}
	if resp.EffectsSource != "estimated_from_phenotype" || resp.EffectEstimate.Markers != up.Markers {
		t.Fatalf("SSR + phenotype should estimate allele effects on all %d alleles, got %q %+v", up.Markers, resp.EffectsSource, resp.EffectEstimate)
	}
}

func TestDosageTableWithSemicolons(t *testing.T) {
	ds, err := parseDatasetCSV([]byte("id;m1;m2\nA;0;2\nB;1;1\n"), "excel-ru.csv")
	if err != nil || ds.markerCount != 2 || ds.layout != nil {
		t.Fatalf("semicolon 0/1/2 table: %v %+v", err, ds)
	}
}
