package main

import (
	"math"
	"math/rand"
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

// v0.8.12 — linked markers.

// A linked locus 50 cM from its marker recombines with Haldane
// r = (1 − e^−1)/2 ≈ 0.316 per meiosis, in diploids and tetraploids.
func TestLinkedLocusRecombination(t *testing.T) {
	const gametes = 20000
	for _, ploidy := range []int{2, 4} {
		gm := buildSyntheticGenomeMap(100, 1, 1.0)
		gm.ploidyN = ploidy
		genes := gm.addLinkedLoci([]int{20}, []string{"R1"}, 50)
		if gm.markers != 101 || genes[0] != 100 || gm.posM[100] != gm.posM[20]+0.5 {
			t.Fatalf("ploidy %d: linked locus placed wrongly: markers %d genes %v pos %v", ploidy, gm.markers, genes, gm.posM[100])
		}
		parent := newOrganismPloidy(100, 0, ploidy)
		parent.setAllele(20, 0, 1) // marker and gene on homologue 0 only
		pop := []organism{parent}
		extendLinked(pop, []int{20}, genes, 101)
		parent = pop[0]
		if parent.geno[100] != 1 || parent.allele(100, 0) != 1 {
			t.Fatalf("ploidy %d: gene not in coupling with the marker: %+v", ploidy, parent.geno[98:])
		}
		rng := rand.New(rand.NewSource(3))
		xo := make([]float64, 0, 8)
		recombinant := 0
		for i := 0; i < gametes; i++ {
			child := newOrganismPloidy(101, 0, ploidy)
			if ploidy == 2 {
				gm.transmit(&parent, &child, 0, 0, rng, xo)
			} else {
				gm.transmitPolyploid(&parent, &child, 0, 0, rng, xo)
			}
			if child.geno[20] != child.geno[100] {
				recombinant++
			}
		}
		r := float64(recombinant) / gametes
		if math.Abs(r-haldaneR(50)) > 0.015 {
			t.Fatalf("ploidy %d: recombination fraction %.3f, want %.3f", ploidy, r, haldaneR(50))
		}
	}
}

// With markers 20 cM from the genes, marker screening selects plants that
// lack a gene; the pyramid by marker runs ahead of the pyramid by gene.
func TestLinkedMarkerPyramidReportsRecombinants(t *testing.T) {
	base := SimRequest{Seed: 11, PopulationSize: 200, Markers: 600, QTLCount: 40, Generations: 10, SelectionPercent: 15, Heritability: 0.4, StrategySet: "core", Replicates: 2, PyramidGenes: 2}
	perfect, err := runSimulation(base)
	if err != nil {
		t.Fatal(err)
	}
	if p := findByCode(perfect.Strategies, "mas_pyramid"); p.Final.PyramidByMarker != 0 || perfect.Request.Markers != 600 {
		t.Fatalf("without a distance the marker is the gene: %+v markers %d", p.Final, perfect.Request.Markers)
	}
	base.MarkerDistanceCM = 20
	linked, err := runSimulation(base)
	if err != nil {
		t.Fatal(err)
	}
	mas := findByCode(linked.Strategies, "mas_pyramid")
	if linked.Request.Markers != 602 {
		t.Fatalf("two linked gene loci expected, markers %d", linked.Request.Markers)
	}
	if mas.Final.PyramidByMarker <= mas.Final.PyramidComplete || mas.Final.PyramidMarkerOnly <= 0 {
		t.Fatalf("marker screening should out-run the genes: by marker %.2f, by gene %.2f, marker only %.2f", mas.Final.PyramidByMarker, mas.Final.PyramidComplete, mas.Final.PyramidMarkerOnly)
	}
	found := false
	for _, l := range linked.Decision.Interpretation {
		if strings.Contains(l, "20.0 cM") {
			found = true
		}
	}
	if !found {
		t.Fatalf("interpretation should state the marker distance: %v", linked.Decision.Interpretation)
	}
	if _, err := runSimulation(SimRequest{Seed: 1, PopulationSize: 50, Markers: 100, QTLCount: 10, Generations: 2, SelectionPercent: 20, Heritability: 0.4, StrategySet: "core", Replicates: 1, MarkerDistanceCM: 5}); err == nil {
		t.Fatal("marker_distance_cm without targets must be rejected")
	}
}

// v0.8.13 — marker-assisted backcrossing.

// Three backcrosses with background selection recover at least the
// textbook 93.75 % of the recurrent parent's genome, more than plain MAS
// keeps, and the pyramid is fixed afterwards.
func TestMABCRecoversRecurrentGenome(t *testing.T) {
	req := SimRequest{Seed: 11, PopulationSize: 200, Markers: 600, QTLCount: 40, Generations: 10, SelectionPercent: 15, Heritability: 0.4, StrategySet: "core", Replicates: 2, PyramidGenes: 2, BackcrossGenerations: 3, MatingSystem: matingSSD, SSDGenerations: 3}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	mabc, mas := findByCode(resp.Strategies, "mabc"), findByCode(resp.Strategies, "mas_pyramid")
	if mabc == nil {
		t.Fatal("mabc strategy expected with backcross_generations")
	}
	atBC := mabc.Metrics[4].RecurrentGenome // F1 + 3 backcrosses
	if atBC < expectedRecurrentGenome(3) {
		t.Fatalf("recurrent genome after BC3 %.3f, want ≥ %.4f with background selection", atBC, expectedRecurrentGenome(3))
	}
	if mabc.Final.RecurrentGenome <= mas.Final.RecurrentGenome {
		t.Fatalf("MABC should keep more of the recurrent parent than MAS: %.3f vs %.3f", mabc.Final.RecurrentGenome, mas.Final.RecurrentGenome)
	}
	if mabc.Final.PyramidComplete < 0.8 {
		t.Fatalf("MABC should fix the pyramid after the backcrosses: %+v", mabc.Final)
	}
	found := false
	for _, l := range resp.Decision.Interpretation {
		if strings.HasPrefix(l, "Marker-assisted backcrossing to") {
			found = true
		}
	}
	if !found {
		t.Fatalf("interpretation should report the backcross programme: %v", resp.Decision.Interpretation)
	}
	req.BackcrossGenerations = 0
	if plain, err := runSimulation(req); err != nil || findByCode(plain.Strategies, "mabc") != nil {
		t.Fatalf("no MABC strategy without backcrosses: %v", err)
	}
	req.BackcrossGenerations = 9
	if _, err := runSimulation(req); err == nil {
		t.Fatal("F1 + backcrosses ≥ generations must be rejected")
	}
}

// A named recurrent parent of an uploaded table is honoured; the name
// must exist.
func TestMABCNamedRecurrentParent(t *testing.T) {
	raw := mustRead(t, "fixtures/upload-toy/genotype_ssr_pi.csv")
	up, err := parseUploadGenotype(raw)
	if err != nil {
		t.Fatal(err)
	}
	id := putUpload(&UploadedDataset{Genotype: up})
	req := SimRequest{Seed: 3, Upload: id, QTLCount: 10, Generations: 8, SelectionPercent: 20, Heritability: 0.4, StrategySet: "core", Replicates: 1, TargetLoci: []string{"Pi-1", "Pi-2", "Pi-33"}, BackcrossGenerations: 2, RecurrentParent: "line_10", MatingSystem: matingSSD, SSDGenerations: 2}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, n := range resp.Notes {
		if strings.Contains(n, "recurrent parent line_10") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("notes should name the recurrent parent: %v", resp.Notes)
	}
	req.RecurrentParent = "no_such_line"
	if _, err := runSimulation(req); err == nil || !strings.Contains(err.Error(), "no_such_line") {
		t.Fatalf("unknown recurrent parent must be rejected by name, got %v", err)
	}
}
