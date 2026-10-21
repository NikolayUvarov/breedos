package main

// v0.8.9 — multi-allelic loci (SSR / microsatellites).
//
// A locus with k alleles is stored as k indicator markers, each carrying
// the dosage (0/1/2) of one allele. The indicators of a locus share one
// position on the genetic map, so no crossover can fall between them:
// every strand carries exactly one allele of the locus through meiosis,
// and the rest of the engine — per-allele trait effects, allele
// frequencies, rrBLUP on one-hot dosages, IBD tracking, SSD/DH line
// derivation — works unchanged. Mutation is disabled on indicator markers:
// flipping one indicator would break the one-allele-per-strand invariant.
//
// Diversity on these loci is Nei's expected heterozygosity 1 − Σ p_a² per
// locus, and allelic richness (alleles still present) is reported too.
//
// Accepted layouts, comma-, semicolon- or tab-separated:
//
//	id,RM1,RM21,...            one column per locus; cells "152/158",
//	line_01,152/158,...        "152|158", or "152" for a homozygous call
//
//	id,RM1_1,RM1_2,...         two columns per locus (suffixes _1/_2, .1/.2,
//	line_01,152,158,...        -1/-2, _a/_b; or an empty / repeated name)
//
//	GenAlEx export             counts line, title line, then
//	                           "Sample,Pop,Locus1,,Locus2,,..."
//
// Missing calls ("", 0, NA, -, ?, .) are imputed by drawing alleles from
// the locus' allele frequencies; the count is reported. Cells with three
// or more alleles (polyploid profiles with unknown dosage) are rejected.

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// locusLayout groups markers into loci. Biallelic dosage data has no
// layout (nil); a multi-allelic dataset has one entry per locus.
type locusLayout struct {
	names   []string   // locus names
	start   []int      // first marker of each locus
	alleles [][]string // allele labels in marker order
	locusOf []int      // locus index of every marker
}

func (l *locusLayout) loci() int { return len(l.names) }

func (l *locusLayout) markers() int { return len(l.locusOf) }

// isIndicator reports whether marker m belongs to a multi-allelic locus.
func (l *locusLayout) isIndicator(m int) bool {
	return l != nil && m < len(l.locusOf) && len(l.alleles[l.locusOf[m]]) > 1
}

// ssrInfo summarises a multi-allelic import for the upload summary and
// the run notes.
type ssrInfo struct {
	Format       string   `json:"format"`
	Loci         int      `json:"loci"`
	Alleles      int      `json:"alleles"`
	MinAlleles   int      `json:"min_alleles_per_locus"`
	MaxAlleles   int      `json:"max_alleles_per_locus"`
	ImputedCalls int      `json:"imputed_calls"`
	TotalCalls   int      `json:"total_calls"`
	Monomorphic  []string `json:"dropped_monomorphic,omitempty"`
	ObservedHet  float64  `json:"observed_heterozygosity"`
	// v0.8.11. Ploidy (2 or 4), tetraploid calls whose allele dosage was
	// unknown and had to be drawn, and gene columns (pyramiding targets).
	Ploidy        int      `json:"ploidy"`
	DosageImputed int      `json:"dosage_imputed_calls,omitempty"`
	GeneColumns   []string `json:"gene_columns,omitempty"`
	// Columns read as sample descriptions (species, site, variety…), not
	// loci, and warnings about a layout that may have been misread.
	IgnoredColumns []string `json:"ignored_columns,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
}

// Missing calls: empty, 0 / 000 / 000000, NA, -9 (STRUCTURE), 999 (a common
// spreadsheet code; no SSR fragment is 999 bp).
var ssrMissing = map[string]bool{"": true, "0": true, "na": true, "n/a": true, "-": true, "?": true, ".": true, "nan": true, "-9": true, "999": true}

func isMissingAllele(a string) bool {
	return ssrMissing[strings.ToLower(a)] || ssrMissing[normaliseAllele(a)]
}

// A two-column locus needs an explicit separator before the suffix, so
// loci named RM1, RM2 are never mistaken for the two columns of "RM".
// "RM1_1", "RM1.b", "gpsb223_allele 1" and "RM1 a2" all pair.
var pairSuffix = regexp.MustCompile(`^(.+?)[_.\- ]+(?:allele|all|a)?[ _.\-]?([12abAB])$`)

// metadataColumns are header names of sample descriptions that sit among
// the locus columns in collection tables.
var metadataColumns = map[string]bool{"species": true, "вид": true, "site": true, "farm": true, "farm_id": true, "village": true, "region": true, "country": true, "origin": true, "variety": true, "сорт": true, "race": true, "раса": true, "cluster": true, "name": true, "название": true, "accession": true, "catalogue": true, "year": true, "lat": true, "lon": true, "latitude": true, "longitude": true}

// unpackAlleles splits a diploid call packed into one integer with three
// digits per allele ("304304" → "304/304"; "96096", whose leading zero a
// spreadsheet dropped, → "96/96"). SSR fragments are shorter than 1000 bp,
// so any integer from 1000 up is a packed pair.
func unpackAlleles(cell string) (string, bool) {
	if len(cell) < 4 || len(cell) > 6 {
		return cell, false
	}
	v, err := strconv.Atoi(cell)
	if err != nil || v < 0 {
		return cell, false
	}
	if v == 0 {
		return "0", true
	}
	if v < 1000 {
		return cell, false
	}
	return strconv.Itoa(v/1000) + "/" + strconv.Itoa(v%1000), true
}

func splitRow(line string, delim string) []string {
	parts := strings.Split(line, delim)
	for i := range parts {
		parts[i] = strings.TrimSpace(strings.Trim(strings.TrimSpace(parts[i]), `"`))
	}
	return parts
}

func detectDelimiter(header string) string {
	best, n := ",", strings.Count(header, ",")
	for _, d := range []string{";", "\t"} {
		if c := strings.Count(header, d); c > n {
			best, n = d, c
		}
	}
	return best
}

// looksMultiallelic is a cheap check that a table that failed the dosage
// parser is SSR-like rather than broken dosage data: an allele separator,
// an integer above 4 (allele sizes), or a GenAlEx counts line.
func looksMultiallelic(raw []byte) bool {
	lines := splitCSVLines(raw)
	var data []string
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			data = append(data, t)
		}
	}
	if len(data) < 2 {
		return false
	}
	delim := detectDelimiter(data[0])
	if isGenAlExCounts(splitRow(data[0], delim)) {
		return true
	}
	// Allele separators or allele sizes (> 2) in at least half of the
	// non-missing cells; a stray typo in a 0/1/2 table does not qualify.
	var ssrLike, total int
	for _, l := range data[1:] {
		cells := splitRow(l, delim)
		for _, c := range cells[1:] {
			if ssrMissing[strings.ToLower(c)] {
				continue
			}
			total++
			if strings.ContainsAny(c, "/|") {
				ssrLike++
			} else if v, err := strconv.Atoi(c); err == nil && v > 4 {
				// Above any dosage (0..4 for tetraploids): an allele size.
				ssrLike++
			}
		}
	}
	return total > 0 && 2*ssrLike >= total
}

func isGenAlExCounts(cells []string) bool {
	numeric := 0
	for _, c := range cells {
		if c == "" {
			continue
		}
		if _, err := strconv.Atoi(c); err != nil {
			return false
		}
		numeric++
	}
	return numeric >= 3
}

// normaliseAllele maps numeric calls to whole base pairs: unbinned
// fragment sizes such as 152.34 (or 152,34 from a semicolon-separated
// export) are rounded, so one allele is not split into several labels.
// Non-numeric labels are kept as they are.
func normaliseAllele(a string) string {
	a = strings.TrimSpace(a)
	if f, err := strconv.ParseFloat(strings.Replace(a, ",", ".", 1), 64); err == nil {
		return strconv.Itoa(int(math.Round(f)))
	}
	return a
}

type allelePair struct {
	a, b    string
	missing bool
}

// parseSSRCell parses a one-column call into its allele labels: "152",
// "152/158", or up to four labels for a tetraploid profile
// ("150/152/158", "150/150/152/158"). Missing calls return nil.
func parseSSRCell(cell string, maxAlleles int) ([]string, error) {
	if ssrMissing[strings.ToLower(cell)] {
		return nil, nil
	}
	fields := strings.FieldsFunc(cell, func(r rune) bool { return r == '/' || r == '|' })
	if len(fields) == 0 {
		return nil, nil
	}
	if len(fields) > maxAlleles {
		if maxAlleles == 2 {
			return nil, fmt.Errorf("cell %q has %d alleles: choose ploidy 4 for tetraploid profiles (e.g. potato); diploid data has at most 2 per cell", cell, len(fields))
		}
		return nil, fmt.Errorf("cell %q has %d alleles: BreedOS supports diploid and tetraploid data (at most 4 alleles per cell)", cell, len(fields))
	}
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = normaliseAllele(f)
		if ssrMissing[strings.ToLower(out[i])] {
			return nil, nil
		}
	}
	return out, nil
}

// maxAllelesInCells is the largest number of alleles in any one cell.
func maxAllelesInCells(lines []string, delim string, first int) int {
	max := 0
	for _, l := range lines {
		for _, c := range splitRow(l, delim)[first:] {
			if n := len(strings.FieldsFunc(c, func(r rune) bool { return r == '/' || r == '|' })); n > max {
				max = n
			}
		}
	}
	return max
}

// pairColumns decides whether the locus columns come in pairs and returns
// the locus names.
func pairColumns(names []string, genalex bool) ([]string, bool) {
	if genalex {
		out := make([]string, 0, len(names)/2)
		for i := 0; i+1 < len(names); i += 2 {
			out = append(out, names[i])
		}
		return out, len(names)%2 == 0
	}
	if len(names)%2 != 0 || len(names) == 0 {
		return names, false
	}
	out := make([]string, 0, len(names)/2)
	for i := 0; i+1 < len(names); i += 2 {
		n1, n2 := names[i], names[i+1]
		switch {
		case n2 == "" || n2 == n1:
			out = append(out, n1)
		default:
			m1, m2 := pairSuffix.FindStringSubmatch(n1), pairSuffix.FindStringSubmatch(n2)
			if m1 == nil || m2 == nil || m1[1] != m2[1] || m1[1] == "" || m1[2] == m2[2] {
				return names, false
			}
			out = append(out, m1[1])
		}
	}
	return out, true
}

func sortAlleles(labels []string) {
	sort.Slice(labels, func(i, j int) bool {
		a, ea := strconv.Atoi(labels[i])
		b, eb := strconv.Atoi(labels[j])
		if ea == nil && eb == nil {
			return a < b
		}
		return labels[i] < labels[j]
	})
}

// parseMultiallelicCSV reads an SSR table into a loadedDataset of
// indicator markers with a locus layout and consistent phase.
func parseMultiallelicCSV(raw []byte, sourceFile string) (*loadedDataset, error) {
	return parseMultiallelicCSVPloidy(raw, sourceFile, 0)
}

// geneColumnPrefix marks a biallelic gene column inside an SSR table
// (e.g. "gene:Pi-ta" with 0/1/2 dosages, or +/− presence), used as a
// pyramiding target (v0.8.11).
const geneColumnPrefix = "gene:"

type ssrColumn struct {
	name   string
	colA   int
	colB   int // second column of a paired locus; -1 otherwise
	isGene bool
}

// parseMultiallelicCSVPloidy is parseMultiallelicCSV with a declared ploidy:
// 0 = detect (3–4 alleles in any cell means tetraploid), 2 or 4.
func parseMultiallelicCSVPloidy(raw []byte, sourceFile string, ploidy int) (*loadedDataset, error) {
	var lines []string
	var notes []string
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimRight(l, "\r")
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") {
			if n := strings.TrimSpace(strings.TrimPrefix(t, "#")); n != "" {
				notes = append(notes, n)
			}
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) < 2 {
		return nil, fmt.Errorf("SSR table %s: need a header and at least one sample", sourceFile)
	}
	delim := detectDelimiter(lines[0])
	format := "one column per locus"
	genalex := false
	if isGenAlExCounts(splitRow(lines[0], delim)) {
		if len(lines) < 4 {
			return nil, fmt.Errorf("SSR table %s: GenAlEx export needs the counts line, the title line, a header and samples", sourceFile)
		}
		genalex = true
		format = "GenAlEx"
		lines = lines[2:]
	}
	header := splitRow(lines[0], delim)
	first := 1 // first locus column
	if genalex || (len(header) > 1 && isPopColumn(header[1])) {
		first = 2
	}
	if len(header) <= first {
		return nil, fmt.Errorf("SSR table %s: no locus columns after the id column", sourceFile)
	}
	last := len(header)
	for last > first && !genalex && header[last-1] == "" {
		last--
	}
	// Ploidy: declared, or detected from the widest cell.
	widest := maxAllelesInCells(lines[1:], delim, first)
	switch {
	case ploidy == 0 && widest > 2:
		ploidy = 4
	case ploidy == 0:
		ploidy = 2
	}
	if ploidy != 2 && ploidy != 4 {
		return nil, fmt.Errorf("SSR table %s: ploidy %d is not supported (2 or 4)", sourceFile, ploidy)
	}
	if ploidy == 4 {
		format += ", tetraploid"
	}
	// Gene columns are set aside before pairing detection, so they never
	// break the two-columns-per-locus pattern.
	var geneCols []ssrColumn
	var ssrIdx []int
	var ssrNames []string
	var ignored []string
	for c := first; c < last; c++ {
		if strings.HasPrefix(strings.ToLower(header[c]), geneColumnPrefix) {
			geneCols = append(geneCols, ssrColumn{name: strings.TrimSpace(header[c][len(geneColumnPrefix):]), colA: c, colB: -1, isGene: true})
			continue
		}
		if isMetadataColumn(header[c], lines[1:], delim, c) {
			ignored = append(ignored, header[c])
			continue
		}
		ssrIdx = append(ssrIdx, c)
		ssrNames = append(ssrNames, header[c])
	}
	names, paired := pairColumns(ssrNames, genalex)
	if paired && !genalex {
		// Paired columns hold single alleles; a separator in any cell
		// means the header pattern was a coincidence.
		for _, l := range lines[1:] {
			cells := splitRow(l, delim)
			for _, c := range ssrIdx {
				if c < len(cells) && strings.ContainsAny(cells[c], "/|") {
					names, paired = ssrNames, false
					break
				}
			}
			if !paired {
				break
			}
		}
	}
	if paired && ploidy == 4 {
		return nil, fmt.Errorf("SSR table %s: tetraploid data must use one column per locus (\"150/152/158\")", sourceFile)
	}
	if paired && !genalex {
		format = "two columns per locus"
	}
	var columns []ssrColumn
	for k, n := range names {
		if paired {
			columns = append(columns, ssrColumn{name: n, colA: ssrIdx[2*k], colB: ssrIdx[2*k+1]})
		} else {
			columns = append(columns, ssrColumn{name: n, colA: ssrIdx[k], colB: -1})
		}
	}
	columns = append(columns, geneCols...)

	// Read every call: allele labels per SSR locus, dosage per gene column
	// (-1 = missing).
	type row struct {
		id     string
		calls  [][]string
		dosage []int
	}
	rows := make([]row, 0, len(lines)-1)
	packed := false
	for i, l := range lines[1:] {
		cells := splitRow(l, delim)
		if len(cells) < last {
			cells = append(cells, make([]string, last-len(cells))...)
		}
		r := row{id: cells[0], calls: make([][]string, len(columns)), dosage: make([]int, len(columns))}
		if r.id == "" {
			return nil, fmt.Errorf("SSR table %s: line %d has an empty id", sourceFile, i+2)
		}
		for k, col := range columns {
			cell := cells[col.colA]
			switch {
			case col.isGene:
				d, err := parseGeneDosage(cell, ploidy)
				if err != nil {
					return nil, fmt.Errorf("SSR table %s: line %d, gene %s: %w", sourceFile, i+2, col.name, err)
				}
				r.dosage[k] = d
			case col.colB >= 0:
				// A half-missing call ("152", "0") is treated as missing.
				a, b := cell, cells[col.colB]
				if !isMissingAllele(a) && !isMissingAllele(b) {
					r.calls[k] = []string{normaliseAllele(a), normaliseAllele(b)}
				}
			default:
				if ploidy == 2 {
					if u, ok := unpackAlleles(cell); ok {
						cell = u
						packed = true
					}
				}
				labels, err := parseSSRCell(cell, ploidy)
				if err != nil {
					return nil, fmt.Errorf("SSR table %s: line %d, locus %s: %w", sourceFile, i+2, col.name, err)
				}
				r.calls[k] = labels
			}
		}
		rows = append(rows, r)
	}

	// Allele sets per SSR locus (monomorphic loci dropped); gene columns
	// become single biallelic markers.
	if packed {
		format += ", alleles packed as 3+3 digits"
	}
	info := &ssrInfo{Format: format, Ploidy: ploidy, IgnoredColumns: ignored}
	layout := &locusLayout{}
	type kept struct {
		col     int
		gene    bool
		index   map[string]int
		weights []float64
		mean    float64 // gene columns: mean dosage over called rows
	}
	var keep []kept
	var markerNames []string
	markers := 0
	for k, col := range columns {
		if col.isGene {
			var sum, n float64
			for _, r := range rows {
				if r.dosage[k] >= 0 {
					sum += float64(r.dosage[k])
					n++
				}
			}
			mean := 0.0
			if n > 0 {
				mean = sum / n
			}
			layout.names = append(layout.names, col.name)
			layout.start = append(layout.start, markers)
			layout.alleles = append(layout.alleles, []string{col.name})
			layout.locusOf = append(layout.locusOf, len(layout.names)-1)
			markerNames = append(markerNames, col.name)
			info.GeneColumns = append(info.GeneColumns, col.name)
			markers++
			keep = append(keep, kept{col: k, gene: true, mean: mean})
			continue
		}
		count := map[string]int{}
		for _, r := range rows {
			for _, a := range r.calls[k] {
				count[a]++
			}
		}
		if len(count) < 2 {
			if len(count) == 1 {
				info.Monomorphic = append(info.Monomorphic, col.name)
			}
			continue
		}
		labels := make([]string, 0, len(count))
		for a := range count {
			labels = append(labels, a)
		}
		sortAlleles(labels)
		index := make(map[string]int, len(labels))
		weights := make([]float64, len(labels))
		for i, a := range labels {
			index[a] = i
			weights[i] = float64(count[a])
			markerNames = append(markerNames, col.name+":"+a)
		}
		layout.names = append(layout.names, col.name)
		layout.start = append(layout.start, markers)
		layout.alleles = append(layout.alleles, labels)
		for range labels {
			layout.locusOf = append(layout.locusOf, len(layout.names)-1)
		}
		markers += len(labels)
		keep = append(keep, kept{col: k, index: index, weights: weights})
	}
	if markers == 0 {
		return nil, errors.New("SSR table: no polymorphic locus (every locus has a single allele or no calls)")
	}

	// Build individuals. Each strand carries one allele per locus; missing
	// calls are drawn from allele frequencies, and a tetraploid profile
	// with fewer than four distinct alleles gets its unknown extra copies
	// drawn among the alleles it shows.
	rng := rand.New(rand.NewSource(0x55_8090))
	out := &loadedDataset{sourceFile: sourceFile, sourceNotes: notes, markerCount: markers, layout: layout, ssr: info, markerNames: markerNames, ploidy: ploidy}
	var het, called int
	for _, r := range rows {
		o := newOrganismPloidy(markers, 0, ploidy)
		o.ibd = nil
		for li, kp := range keep {
			base := layout.start[li]
			if kp.gene {
				d := r.dosage[kp.col]
				info.TotalCalls++
				if d < 0 {
					info.ImputedCalls++
					d = 0
					for c := 0; c < ploidy; c++ {
						if rng.Float64() < kp.mean/float64(ploidy) {
							d++
						}
					}
				}
				for c, s := range rng.Perm(ploidy) {
					if c < d {
						o.setAllele(base, s, 1)
					}
				}
				continue
			}
			call := r.calls[kp.col]
			info.TotalCalls++
			idx := make([]int, 0, ploidy)
			if call == nil {
				info.ImputedCalls++
				for c := 0; c < ploidy; c++ {
					idx = append(idx, drawWeighted(kp.weights, rng))
				}
			} else {
				called++
				distinct := map[int]bool{}
				for _, a := range call {
					idx = append(idx, kp.index[a])
					distinct[kp.index[a]] = true
				}
				if len(distinct) > 1 {
					het++
				}
				if len(idx) < ploidy {
					if ploidy > 2 {
						info.DosageImputed++
					}
					for len(idx) < ploidy {
						idx = append(idx, idx[rng.Intn(len(idx))])
					}
				}
			}
			rng.Shuffle(len(idx), func(a, b int) { idx[a], idx[b] = idx[b], idx[a] })
			for s, a := range idx {
				o.setAllele(base+a, s, 1)
			}
		}
		out.individuals = append(out.individuals, o)
		out.accessionIDs = append(out.accessionIDs, r.id)
	}
	info.Loci = 0
	info.MinAlleles, info.MaxAlleles = markers, 0
	for li, a := range layout.alleles {
		if len(a) <= 1 && layout.names[li] == a[0] {
			continue // gene column
		}
		info.Loci++
		info.Alleles += len(a)
		if len(a) < info.MinAlleles {
			info.MinAlleles = len(a)
		}
		if len(a) > info.MaxAlleles {
			info.MaxAlleles = len(a)
		}
	}
	if info.Loci == 0 {
		info.MinAlleles = 0
	}
	if called > 0 {
		info.ObservedHet = round4(float64(het) / float64(called))
	}
	if het == 0 && called > 0 && !paired && ploidy == 2 {
		info.Warnings = append(info.Warnings, "no heterozygous call in the whole table: fine for inbred lines, but if every locus has two columns, their headers were not recognised as a pair and each column was read as its own locus — name them LOCUS_1, LOCUS_2")
	}
	return out, nil
}

// isMetadataColumn reports a sample-description column among the loci: a
// known header name (species, site, variety…), or a column whose values
// contain no digit at all (no allele call does).
func isMetadataColumn(name string, rows []string, delim string, c int) bool {
	if metadataColumns[strings.ToLower(strings.TrimSpace(name))] || isPopColumn(name) {
		return true
	}
	seen := 0
	for _, l := range rows {
		cells := splitRow(l, delim)
		if c >= len(cells) || isMissingAllele(cells[c]) {
			continue
		}
		v := cells[c]
		if v == "+" || v == "−" || strings.EqualFold(v, "yes") || strings.EqualFold(v, "no") || v == "да" || v == "нет" {
			return false // presence calls of an unprefixed gene column
		}
		if strings.ContainsAny(v, "0123456789") {
			return false
		}
		seen++
	}
	return seen > 0
}

// parseGeneDosage reads a gene-column cell: 0..ploidy dosage, "+" (present;
// taken as homozygous, as in inbred lines) or "−"/"-" (absent); missing →
// -1.
func parseGeneDosage(cell string, ploidy int) (int, error) {
	c := strings.ToLower(strings.TrimSpace(cell))
	switch c {
	case "+", "yes", "да":
		return ploidy, nil
	case "-", "−", "no", "нет":
		return 0, nil
	case "", "na", "n/a", "?", ".", "nan":
		return -1, nil
	}
	v, err := strconv.Atoi(c)
	if err != nil || v < 0 || v > ploidy {
		return 0, fmt.Errorf("value %q: use 0..%d, + or −", cell, ploidy)
	}
	return v, nil
}

func isPopColumn(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "pop", "population", "популяция", "group", "группа":
		return true
	}
	return false
}

func drawWeighted(w []float64, rng *rand.Rand) int {
	var total float64
	for _, v := range w {
		total += v
	}
	x := rng.Float64() * total
	for i, v := range w {
		if x < v {
			return i
		}
		x -= v
	}
	return len(w) - 1
}

// subsetLayout keeps whole loci: it returns the marker columns of every
// chosen locus and the re-indexed layout.
func subsetLayout(l *locusLayout, loci []int) ([]int, *locusLayout) {
	out := &locusLayout{}
	var cols []int
	for _, li := range loci {
		out.names = append(out.names, l.names[li])
		out.start = append(out.start, len(cols))
		out.alleles = append(out.alleles, l.alleles[li])
		for a := range l.alleles[li] {
			cols = append(cols, l.start[li]+a)
			out.locusOf = append(out.locusOf, len(out.names)-1)
		}
	}
	return cols, out
}

// diversityOf is mean expected heterozygosity: 2p(1−p) per biallelic
// marker, or Nei's 1 − Σ p_a² per multi-allelic locus.
func diversityOf(freq []float64, l *locusLayout) float64 {
	if l == nil {
		return diversityFromFreq(freq)
	}
	var sum float64
	for li, labels := range l.alleles {
		s := l.start[li]
		if len(labels) <= 1 {
			p := freq[s]
			sum += 2 * p * (1 - p)
			continue
		}
		h := 1.0
		for a := range labels {
			h -= freq[s+a] * freq[s+a]
		}
		sum += h
	}
	return sum / float64(l.loci())
}

// allelesPresent counts alleles still segregating or fixed on
// multi-allelic loci (allelic richness summed over loci).
func allelesPresent(freq []float64, l *locusLayout) int {
	if l == nil {
		return 0
	}
	n := 0
	for m := range l.locusOf {
		if l.isIndicator(m) && freq[m] > 0 {
			n++
		}
	}
	return n
}

// withoutIndicators zeroes effects on multi-allelic indicator markers, so
// edit candidates are only drawn from biallelic markers (editing an SSR
// allele is not a breeding operation).
func withoutIndicators(effects []float64, l *locusLayout) []float64 {
	if l == nil {
		return effects
	}
	out := append([]float64(nil), effects...)
	for m := range out {
		if l.isIndicator(m) {
			out[m] = 0
		}
	}
	return out
}

// resampleMultiallelic redraws every multi-allelic locus of o from the
// given allele frequencies, keeping one allele per strand. Used after
// per-marker founder re-rolls (ancestral lines), which would otherwise
// break the one-hot coding.
func resampleMultiallelic(o *organism, l *locusLayout, freq []float64, rng *rand.Rand) {
	if l == nil {
		return
	}
	ploidy := 2
	if o.strands != nil {
		ploidy = len(o.strands)
	} else if o.phase == nil {
		o.phase = make([]uint64, phaseWords(len(o.geno)))
	}
	for li, labels := range l.alleles {
		if len(labels) <= 1 {
			continue
		}
		s := l.start[li]
		w := make([]float64, len(labels))
		for a := range labels {
			m := s + a
			o.geno[m] = 0
			if o.strands != nil {
				for h := range o.strands {
					o.strands[h][m>>6] &^= 1 << (uint(m) & 63)
				}
			} else {
				o.phase[m>>6] &^= 1 << (uint(m) & 63)
			}
			w[a] = freq[m] + 1e-9
		}
		for h := 0; h < ploidy; h++ {
			o.setAllele(s+drawWeighted(w, rng), h, 1)
		}
	}
}
