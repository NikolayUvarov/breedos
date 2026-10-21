package main

import (
	"strings"
	"testing"
)

// v0.8.11 — gene pyramiding.

func TestPlantsFor95(t *testing.T) {
	// Three unlinked genes in an F2: 1/64 homozygous for all → 191 plants.
	if n := plantsFor95(1.0 / 64); n != 191 {
		t.Fatalf("plants for 95%% at p = 1/64: %d, want 191", n)
	}
}

func TestPyramidStatus(t *testing.T) {
	pop := []organism{{geno: []uint8{2, 2, 0}}, {geno: []uint8{2, 1, 0}}, {geno: []uint8{0, 2, 0}}, {geno: []uint8{2, 2, 1}}}
	complete, carriers, freq := pyramidStatus(pop, []int{0, 1})
	if complete != 0.5 || carriers != 0.75 || freq[0] != 0.75 || freq[1] != 0.875 {
		t.Fatalf("pyramid status: complete %v carriers %v freq %v", complete, carriers, freq)
	}
}

// A plant with the only copy of a rare gene must out-rank one with two
// common genes, or greedy gene counting loses the rare gene.
func TestTargetWeightsFavourRareGene(t *testing.T) {
	freq := []float64{0.5, 0.5, 0.01}
	targets := []int{0, 1, 2}
	w := targetWeights(freq, targets, 50, 2)
	if s := w[0] + w[1] + w[2]; s < 0.9999 || s > 1.0001 || w[2] <= w[0] {
		t.Fatalf("weights %v: want sum 1 and the rare gene weighted most", w)
	}
	common := organism{geno: []uint8{2, 2, 0}}
	rare := organism{geno: []uint8{0, 0, 1}}
	if targetScore(&rare, targets, w, 2) <= targetScore(&common, targets, w, 2) {
		t.Fatalf("rare-gene carrier %.3f should out-rank two-common-gene plant %.3f", targetScore(&rare, targets, w, 2), targetScore(&common, targets, w, 2))
	}
}

// Naming targets adds the complete-pyramid constraint; strategies blind
// to the genes cannot satisfy it, and MAS + OCS is offered.
func TestPyramidConstraintAndMASOCS(t *testing.T) {
	resp, err := runSimulation(SimRequest{Seed: 11, PopulationSize: 200, Markers: 600, QTLCount: 40, Generations: 12, SelectionPercent: 15, Heritability: 0.4, StrategySet: "core", Replicates: 3, PyramidGenes: 3, MatingSystem: matingSSD})
	if err != nil {
		t.Fatal(err)
	}
	if findByCode(resp.Strategies, "mas_ocs") == nil {
		t.Fatal("mas_ocs strategy expected with pyramid_genes")
	}
	if b := findByCode(resp.Strategies, "balanced"); b.Final.Feasible {
		t.Fatalf("balanced selection should fail the complete-pyramid constraint: %+v", b.Final)
	}
	if code := resp.Decision.BestFeasibleCode; code != "mas_pyramid" && code != "mas_ocs" {
		t.Fatalf("best feasible %q, want a MAS strategy", code)
	}
}

// MAS must assemble a 3-gene pyramid that phenotypic selection — blind to
// genes without an effect on the selected trait — does not.
func TestMASAssemblesPyramid(t *testing.T) {
	resp, err := runSimulation(SimRequest{Seed: 11, PopulationSize: 200, Markers: 600, QTLCount: 40, Generations: 12, SelectionPercent: 15, Heritability: 0.4, StrategySet: "advanced", Replicates: 3, PyramidGenes: 3, MatingSystem: matingSSD})
	if err != nil {
		t.Fatal(err)
	}
	mas, ph := findByCode(resp.Strategies, "mas_pyramid"), findByCode(resp.Strategies, "phenotype")
	if mas == nil {
		t.Fatal("mas_pyramid strategy expected with pyramid_genes")
	}
	if mas.Final.PyramidComplete < 0.8 || mas.Final.PyramidGen90 == 0 || mas.Final.TargetsLost != 0 {
		t.Fatalf("MAS should complete the pyramid: %+v", mas.Final)
	}
	if ph.Final.PyramidComplete >= mas.Final.PyramidComplete {
		t.Fatalf("phenotype selection (%.2f) should not match MAS (%.2f)", ph.Final.PyramidComplete, mas.Final.PyramidComplete)
	}
	joined := strings.Join(resp.Decision.Interpretation, " ")
	if !strings.Contains(joined, "Gene pyramid (R1 + R2 + R3)") || !strings.Contains(joined, "Planning (unlinked genes)") {
		t.Fatalf("report should summarise the pyramid and planning: %s", joined)
	}
}

func TestPyramidTargetsFromGeneColumns(t *testing.T) {
	resetUploadCache()
	raw := "id,RM1,RM21,gene:Pi-ta,gene:Pi-b\n"
	rows := []string{"103/107,125,2,0", "107,125/131,0,2", "103,131,0,0", "107,129,2,2", "103,125,0,0", "107,131,2,0", "103,129,0,2", "103/107,125,0,0"}
	for i := 0; i < 40; i++ {
		raw += "L" + itoa(i) + "," + rows[i%len(rows)] + "\n"
	}
	up, err := parseUploadGenotype([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if up.Loci == nil || strings.Join(up.Loci.GeneColumns, ",") != "Pi-ta,Pi-b" || up.Loci.Loci != 2 {
		t.Fatalf("gene columns should be listed separately from SSR loci: %+v", up.Loci)
	}
	id := putUpload(&UploadedDataset{Genotype: up})
	resp, err := runSimulation(SimRequest{Seed: 4, Upload: id, PopulationSize: 40, Markers: 900, QTLCount: 3, Generations: 8, SelectionPercent: 25, Heritability: 0.5, StrategySet: "core", Replicates: 2, TargetLoci: []string{"Pi-ta", "gene:Pi-b"}, MatingSystem: matingSSD})
	if err != nil {
		t.Fatal(err)
	}
	if mas := findByCode(resp.Strategies, "mas_pyramid"); mas == nil || len(mas.Metrics[0].TargetFreq) != 2 {
		t.Fatal("target_loci should drive the MAS strategy with two tracked genes")
	}
	if _, err := runSimulation(SimRequest{Seed: 4, Upload: id, PopulationSize: 40, Generations: 3, SelectionPercent: 25, Heritability: 0.5, Replicates: 1, TargetLoci: []string{"Pi-9"}}); err == nil || !strings.Contains(err.Error(), "Pi-ta") {
		t.Fatalf("unknown target should list the gene columns, got %v", err)
	}
}
