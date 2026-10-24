package main

import "testing"

// v0.8.19 — the VIR sorghum collection ships inside the binary.
func TestEmbeddedSorghumDataset(t *testing.T) {
	ds, err := loadDataset("sorghum_vir200")
	if err != nil {
		t.Fatal(err)
	}
	if ds.isPlaceholder || len(ds.individuals) != 200 || ds.ssr == nil || ds.ssr.Loci != 12 || ds.ssr.Alleles != 230 {
		t.Fatalf("sorghum dataset: placeholder=%v n=%d ssr=%+v", ds.isPlaceholder, len(ds.individuals), ds.ssr)
	}
	resp, err := runSimulation(SimRequest{Seed: 1, Dataset: "sorghum_vir200", PopulationSize: 400, Markers: 900, QTLCount: 15, Generations: 3, SelectionPercent: 10, Heritability: 0.4, StrategySet: "core", Replicates: 1, MatingSystem: matingSSD, SSDGenerations: 2})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Request.PopulationSize != 200 || resp.Loci == nil || resp.Loci.Loci != 12 {
		t.Fatalf("run on the sorghum dataset: N=%d loci=%+v", resp.Request.PopulationSize, resp.Loci)
	}
}
