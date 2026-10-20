package main

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

// Issue 39 — line breeding for self-pollinating crops.

// fullyHeterozygousF1s returns n F1s of two complementary homozygous lines,
// heterozygous at every marker, with founder IBD labels.
func fullyHeterozygousF1s(n, markers int, gm *genomeMap, rng *rand.Rand) []organism {
	a, b := gm.newOrganism(), gm.newOrganism()
	for m := 0; m < markers; m++ {
		a.geno[m] = 2
	}
	pop := []organism{a, b}
	initFounders(pop, gm, rng)
	parents := make([]int, 2*n)
	for i := 0; i < n; i++ {
		parents[2*i], parents[2*i+1] = 0, 1
	}
	return makeNextGeneration(append(pop, make([]organism, n-2)...)[:n], parents, gm, 0, rng, "allocated")
}

func TestDoubledHaploidLinesAreFullyHomozygous(t *testing.T) {
	gm := buildSyntheticGenomeMap(300, 5, 1)
	rng := rand.New(rand.NewSource(1))
	pop := fullyHeterozygousF1s(50, 300, gm, rng)
	deriveLines(pop, matingDoubledHaploid, 0, gm, 0, rng)
	for i, o := range pop {
		for m, g := range o.geno {
			if g == 1 {
				t.Fatalf("DH line %d is heterozygous at marker %d", i, m)
			}
		}
	}
}

func TestSSDResidualHeterozygosity(t *testing.T) {
	gm := buildSyntheticGenomeMap(400, 10, 1)
	rng := rand.New(rand.NewSource(2))
	for _, k := range []int{1, 3} {
		pop := fullyHeterozygousF1s(200, 400, gm, rng)
		deriveLines(pop, matingSSD, k, gm, 0, rng)
		het := 0
		for _, o := range pop {
			for _, g := range o.geno {
				if g == 1 {
					het++
				}
			}
		}
		got := float64(het) / float64(200*400)
		if want := math.Pow(0.5, float64(k)); math.Abs(got-want) > 0.15*want {
			t.Errorf("SSD %d generations: heterozygosity %.4f, want ≈ %.4f", k, got, want)
		}
	}
}

func TestBetweenLineCoancestryIgnoresWithinLineIdentity(t *testing.T) {
	gm := buildSyntheticGenomeMap(100, 2, 1)
	rng := rand.New(rand.NewSource(3))
	pop := makeInitialPopulation(40, 100, rng)
	initFounders(pop, gm, rng)
	for i := range pop {
		pop[i] = gm.doubledHaploid(&pop[i], 0, rng, nil)
	}
	// Every DH line is IBD within itself, but no two lines share a label.
	if f := ibdCoancestryPairs(pop, true); f != 0 {
		t.Fatalf("between-line coancestry of unrelated DH lines must be 0, got %v", f)
	}
	if f := ibdCoancestryPairs(pop, false); f <= 0 {
		t.Fatal("with within-line pairs counted the coancestry must be positive")
	}
}

func TestLineBreedingRunsEndToEnd(t *testing.T) {
	for _, system := range []string{matingDoubledHaploid, matingSSD} {
		resp, err := runSimulation(SimRequest{Seed: 4, PopulationSize: 200, Markers: 500, QTLCount: 40, Generations: 6, SelectionPercent: 20, Heritability: 0.5, StrategySet: "core", Replicates: 2, MatingSystem: system})
		if err != nil {
			t.Fatal(err)
		}
		bal := findByCode(resp.Strategies, "balanced")
		if bal.Final.GeneticGain <= 0 {
			t.Fatalf("%s: line breeding should make progress, gain %v", system, bal.Final.GeneticGain)
		}
		if bal.Final.Inbreeding > 0.6 {
			t.Fatalf("%s: coancestry among lines %.3f looks like within-line homozygosity leaking into the risk metric", system, bal.Final.Inbreeding)
		}
		found := false
		for _, n := range resp.Notes {
			if strings.HasPrefix(n, "Line breeding:") {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: notes must describe the line-breeding scheme", system)
		}
	}
	if _, err := runSimulation(SimRequest{MatingSystem: "clonal"}); err == nil {
		t.Fatal("unknown mating_system must be rejected")
	}
}
