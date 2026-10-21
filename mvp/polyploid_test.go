package main

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// v0.8.11 — autotetraploids.

// A duplex parent (AAaa) under random chromosome segregation gives gametes
// AA : Aa : aa = 1 : 4 : 1.
func TestTetraploidDuplexGameteRatio(t *testing.T) {
	gm := buildSyntheticGenomeMap(1, 1, 1)
	gm.ploidyN = 4
	parent := gm.newOrganism()
	parent.setAllele(0, 0, 1)
	parent.setAllele(0, 1, 1)
	rng := rand.New(rand.NewSource(7))
	var count [3]int
	const n = 60000
	for i := 0; i < n; i++ {
		child := gm.newOrganism()
		gm.transmitPolyploid(&parent, &child, 0, 0, rng, nil)
		count[child.geno[0]]++
	}
	for d, want := range []float64{1.0 / 6, 4.0 / 6, 1.0 / 6} {
		if got := float64(count[d]) / n; math.Abs(got-want) > 0.01 {
			t.Errorf("gamete dosage %d: %.4f, want %.4f", d, got, want)
		}
	}
}

// Neutral drift in a tetraploid: 4N gene copies, F_t ≈ 1 − (1 − 1/4N)^t.
func TestTetraploidNeutralDrift(t *testing.T) {
	const n, gens, reps = 60, 20, 10
	var sum float64
	for r := 0; r < reps; r++ {
		rng := rand.New(rand.NewSource(int64(40 + r)))
		gm := buildSyntheticGenomeMap(300, 5, 1)
		gm.ploidyN = 4
		pop := makeInitialPopulationPloidy(n, 300, 4, rng)
		initFounders(pop, gm, rng)
		parents := make([]int, n)
		for i := range parents {
			parents[i] = i
		}
		for g := 0; g < gens; g++ {
			pop = makeNextGeneration(pop, parents, gm, 0, rng, "random")
		}
		sum += ibdCoancestry(pop)
	}
	got, want := sum/reps, 1-math.Pow(1-1.0/(4*n), gens)
	if math.Abs(got-want)/want > 0.25 {
		t.Fatalf("tetraploid neutral F %.4f, want ≈ %.4f", got, want)
	}
}

func TestTetraploidSSRKeepsOneAllelePerHomologue(t *testing.T) {
	raw := "id,STM1,STM2\nA,150/152/158,200/204\nB,150,204/210/212/214\nC,152/158,0\nD,150/150/152/158,200\n"
	ds, err := parseDatasetCSV([]byte(raw), "potato.csv")
	if err != nil {
		t.Fatal(err)
	}
	if ds.ssr.DosageImputed == 0 || ds.ssr.ImputedCalls != 1 {
		t.Fatalf("expected unknown-dosage and missing calls to be counted: %+v", ds.ssr)
	}
	gm := buildLocusGenomeMap(ds.layout, 2, 1)
	gm.ploidyN = 4
	rng := rand.New(rand.NewSource(3))
	pop := clonePopulation(ds.individuals)
	initFounders(pop, gm, rng)
	parents := []int{0, 1, 2, 3}
	for g := 0; g < 10; g++ {
		pop = makeNextGeneration(pop, parents, gm, 0.01, rng, "random")
	}
	l := ds.layout
	for i := range pop {
		for li, labels := range l.alleles {
			for h := 0; h < 4; h++ {
				n := 0
				for a := range labels {
					n += int(pop[i].allele(l.start[li]+a, h))
				}
				if n != 1 {
					t.Fatalf("individual %d locus %s homologue %d carries %d alleles", i, l.names[li], h, n)
				}
			}
		}
	}
}

func TestTetraploidRunAndGuards(t *testing.T) {
	resetUploadCache()
	raw := "id,STM1,STM2,STM3\n"
	rng := rand.New(rand.NewSource(1))
	sizes := []int{150, 152, 158, 160, 164}
	for i := 0; i < 40; i++ {
		raw += fmt.Sprintf("P%02d", i)
		for l := 0; l < 3; l++ {
			raw += ","
			for k := 0; k < 4; k++ {
				if k > 0 {
					raw += "/"
				}
				raw += itoa(sizes[rng.Intn(len(sizes))] + 20*l)
			}
		}
		raw += "\n"
	}
	up, err := parseUploadGenotype([]byte(raw))
	if err != nil || up.Ploidy != 4 {
		t.Fatalf("tetraploid upload: %v ploidy %d", err, up.Ploidy)
	}
	id := putUpload(&UploadedDataset{Genotype: up})
	resp, err := runSimulation(SimRequest{Seed: 2, Upload: id, PopulationSize: 40, Markers: 900, QTLCount: 4, Generations: 10, SelectionPercent: 25, Heritability: 0.5, StrategySet: "advanced", Replicates: 2})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Request.Ploidy != 4 || !strings.Contains(strings.Join(resp.Notes, " "), "Autotetraploid") {
		t.Fatalf("run should be tetraploid with a note, ploidy %d", resp.Request.Ploidy)
	}
	if _, err := runSimulation(SimRequest{Seed: 2, Upload: id, PopulationSize: 40, Generations: 3, SelectionPercent: 25, Heritability: 0.5, Replicates: 1, Ploidy: 2}); err == nil {
		t.Fatal("ploidy 2 on tetraploid data must be rejected")
	}
	if _, err := runSimulation(SimRequest{Seed: 2, Upload: id, PopulationSize: 40, Generations: 3, SelectionPercent: 25, Heritability: 0.5, Replicates: 1, MatingSystem: matingSSD}); err == nil {
		t.Fatal("SSD on tetraploid data must be rejected")
	}
	// Synthetic tetraploid population.
	if resp, err := runSimulation(SimRequest{Seed: 3, PopulationSize: 80, Markers: 300, QTLCount: 20, Generations: 8, SelectionPercent: 20, Heritability: 0.4, Replicates: 2, Ploidy: 4}); err != nil || resp.Request.Ploidy != 4 {
		t.Fatalf("synthetic tetraploid run: %v", err)
	}
}

func TestTetraploidDosageTable(t *testing.T) {
	if _, err := parseDatasetCSV([]byte("id,m1,m2\nA,3,0\nB,4,1\n"), "d.csv"); err == nil || !strings.Contains(err.Error(), "ploidy 4") {
		t.Fatalf("dosage 3 without a ploidy declaration must point to ploidy 4, got %v", err)
	}
	ds, err := parseDatasetCSV([]byte("# ploidy: 4\nid,m1,m2\nA,3,0\nB,4,1\n"), "d.csv")
	if err != nil || ds.ploidy != 4 {
		t.Fatalf("declared tetraploid dosage table: %v", err)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
