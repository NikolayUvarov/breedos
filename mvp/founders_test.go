package main

import (
	"math"
	"strings"
	"testing"
)

// v0.8.12 — per-locus statistics and founder set.

// He, Ho and PIC of a locus with allele frequencies 0.5 / 0.25 / 0.25:
// He = 0.625, PIC = 0.625 − 2(0.25·0.0625 + 0.25·0.0625 + 0.0625·0.0625)
// = 0.5547; Ho is the share of called individuals with two alleles. A
// homozygous call counts two copies of its allele.
func TestPerLocusStats(t *testing.T) {
	ds, err := parseDatasetCSV([]byte("id,RM1,RM5\nA,100/104,150\nB,100/108,150/152\nC,100,0\nD,104/108,152\n"), "x.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.ssr.PerLocus) != 2 {
		t.Fatalf("per-locus stats for 2 loci expected: %+v", ds.ssr.PerLocus)
	}
	rm1 := ds.ssr.PerLocus[0]
	if rm1.Name != "RM1" || rm1.Alleles != 3 || math.Abs(rm1.He-0.625) > 1e-4 || math.Abs(rm1.PIC-0.5547) > 1e-3 || rm1.Ho != 0.75 || rm1.Missing != 0 {
		t.Fatalf("RM1 stats: %+v", rm1)
	}
	if rm5 := ds.ssr.PerLocus[1]; rm5.Missing != 1 || rm5.Alleles != 2 {
		t.Fatalf("RM5 stats: %+v", rm5)
	}
}

// Three accessions hold every allele of a toy table; greedy coverage must
// find them in three picks and report the curve.
func TestFounderSetGreedy(t *testing.T) {
	raw := "id,RM1,RM5,RM9\n" +
		"A,100/104,150/152,200\n" + // 5 alleles
		"B,100,150,200\n" + // nothing new after A
		"C,108,154,200/202\n" + // 3 new
		"D,100,156,200\n" + // 1 new
		"E,104,152,202\n"
	ds, err := parseDatasetCSV([]byte(raw), "toy.csv")
	if err != nil {
		t.Fatal(err)
	}
	out := founderSet(ds, 3)
	if out.AllelesTotal != 9 || out.AllelesKept != 9 || out.AllForKept != 3 || strings.Join(out.IDs, ",") != "A,C,D" {
		t.Fatalf("greedy founder set: %+v", out)
	}
	if len(out.Curve) != 3 || out.Curve[0] != 5 || out.Curve[1] != 8 || out.Curve[2] != 9 {
		t.Fatalf("coverage curve: %v", out.Curve)
	}
	if out.RandomAlleles >= 9 || out.RandomAlleles <= 5 {
		t.Fatalf("random sets of 3 should keep between 5 and 9 alleles on average: %v", out.RandomAlleles)
	}
	if all := founderSet(ds, 50); all.Size != 5 || all.AllelesKept != 9 {
		t.Fatalf("size above N is clamped: %+v", all)
	}
	// Biallelic dosage tables: both alleles of every marker count.
	dosage, err := parseDatasetCSV([]byte("id,m1,m2\nA,0,2\nB,2,0\nC,1,1\n"), "d.csv")
	if err != nil {
		t.Fatal(err)
	}
	if d := founderSet(dosage, 1); d.AllelesTotal != 4 || d.AllelesKept != 4 || d.IDs[0] != "C" {
		t.Fatalf("dosage founder set: %+v", d)
	}
}
