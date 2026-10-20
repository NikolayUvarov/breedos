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
}

var ssrMissing = map[string]bool{"": true, "0": true, "na": true, "n/a": true, "-": true, "?": true, ".": true, "nan": true, "-9": true}

// A two-column locus needs an explicit separator before the suffix, so
// loci named RM1, RM2 are never mistaken for the two columns of "RM".
var pairSuffix = regexp.MustCompile(`^(.+?)[_.\-]([12abAB])$`)

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

// looksMultiallelic is a cheap check that a table that failed the 0/1/2
// parser is SSR-like rather than broken dosage data: an allele separator,
// an integer above 2 (allele sizes), or a GenAlEx counts line.
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
			} else if v, err := strconv.Atoi(c); err == nil && v > 2 {
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

func parseSSRCell(cell string) (allelePair, error) {
	if ssrMissing[strings.ToLower(cell)] {
		return allelePair{missing: true}, nil
	}
	fields := strings.FieldsFunc(cell, func(r rune) bool { return r == '/' || r == '|' })
	switch len(fields) {
	case 1:
		a := normaliseAllele(fields[0])
		return allelePair{a: a, b: a}, nil
	case 2:
		a, b := normaliseAllele(fields[0]), normaliseAllele(fields[1])
		if ssrMissing[strings.ToLower(a)] || ssrMissing[strings.ToLower(b)] {
			return allelePair{missing: true}, nil
		}
		return allelePair{a: a, b: b}, nil
	}
	return allelePair{}, fmt.Errorf("cell %q has %d alleles: BreedOS models diploid inheritance, so polyploid profiles with unknown dosage (e.g. tetraploid potato SSR) are not supported yet", cell, len(fields))
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
	var lines []string
	var notes []string
	for _, l := range splitCSVLines(raw) {
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
	locusCols := header[first:]
	for len(locusCols) > 0 && !genalex && locusCols[len(locusCols)-1] == "" {
		locusCols = locusCols[:len(locusCols)-1]
	}
	names, paired := pairColumns(locusCols, genalex)
	if paired && !genalex {
		// Paired columns hold single alleles; a separator in any cell
		// means the header pattern was a coincidence.
		for _, l := range lines[1:] {
			if cells := splitRow(l, delim); len(cells) > first && strings.ContainsAny(strings.Join(cells[first:], ""), "/|") {
				names, paired = locusCols, false
				break
			}
		}
	}
	if paired && !genalex {
		format = "two columns per locus"
	}
	type row struct {
		id    string
		pairs []allelePair
	}
	rows := make([]row, 0, len(lines)-1)
	for i, l := range lines[1:] {
		cells := splitRow(l, delim)
		if len(cells) < first+len(locusCols) {
			cells = append(cells, make([]string, first+len(locusCols)-len(cells))...)
		}
		r := row{id: cells[0], pairs: make([]allelePair, len(names))}
		if r.id == "" {
			return nil, fmt.Errorf("SSR table %s: line %d has an empty id", sourceFile, i+2)
		}
		for k := range names {
			var p allelePair
			var err error
			if paired {
				// A half-missing call ("152", "0") is treated as missing.
				a, b := cells[first+2*k], cells[first+2*k+1]
				if ssrMissing[strings.ToLower(a)] || ssrMissing[strings.ToLower(b)] {
					p = allelePair{missing: true}
				} else {
					p = allelePair{a: normaliseAllele(a), b: normaliseAllele(b)}
				}
			} else {
				p, err = parseSSRCell(cells[first+k])
			}
			if err != nil {
				return nil, fmt.Errorf("SSR table %s: line %d, locus %s: %w", sourceFile, i+2, names[k], err)
			}
			r.pairs[k] = p
		}
		rows = append(rows, r)
	}

	// Allele sets and counts per locus; drop monomorphic loci.
	info := &ssrInfo{Format: format}
	layout := &locusLayout{}
	type kept struct {
		col     int
		index   map[string]int
		weights []float64
	}
	var keep []kept
	markers := 0
	for k, name := range names {
		count := map[string]int{}
		for _, r := range rows {
			p := r.pairs[k]
			if p.missing {
				continue
			}
			count[p.a]++
			count[p.b]++
		}
		if len(count) < 2 {
			if len(count) == 1 {
				info.Monomorphic = append(info.Monomorphic, name)
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
		}
		layout.names = append(layout.names, name)
		layout.start = append(layout.start, markers)
		layout.alleles = append(layout.alleles, labels)
		for range labels {
			layout.locusOf = append(layout.locusOf, len(layout.names)-1)
		}
		markers += len(labels)
		keep = append(keep, kept{col: k, index: index, weights: weights})
	}
	if len(keep) == 0 {
		return nil, errors.New("SSR table: no polymorphic locus (every locus has a single allele or no calls)")
	}

	// Build individuals: dosage per indicator plus a phase that keeps one
	// allele per strand; missing calls drawn from allele frequencies.
	rng := rand.New(rand.NewSource(0x55_8090))
	out := &loadedDataset{sourceFile: sourceFile, sourceNotes: notes, markerCount: markers, layout: layout, ssr: info}
	var het, called int
	for _, r := range rows {
		o := organism{geno: make([]uint8, markers), phase: make([]uint64, phaseWords(markers))}
		for li, kp := range keep {
			p := r.pairs[kp.col]
			info.TotalCalls++
			var ia, ib int
			if p.missing {
				info.ImputedCalls++
				ia, ib = drawWeighted(kp.weights, rng), drawWeighted(kp.weights, rng)
			} else {
				ia, ib = kp.index[p.a], kp.index[p.b]
				called++
				if ia != ib {
					het++
				}
			}
			if rng.Intn(2) == 1 {
				ia, ib = ib, ia
			}
			base := layout.start[li]
			o.geno[base+ia]++
			o.geno[base+ib]++
			if ia != ib {
				o.setPhaseBit(base + ib) // allele ib sits on strand B
			}
		}
		out.individuals = append(out.individuals, o)
		out.accessionIDs = append(out.accessionIDs, r.id)
	}
	info.Loci = layout.loci()
	info.Alleles = markers
	info.MinAlleles, info.MaxAlleles = markers, 0
	for _, a := range layout.alleles {
		if len(a) < info.MinAlleles {
			info.MinAlleles = len(a)
		}
		if len(a) > info.MaxAlleles {
			info.MaxAlleles = len(a)
		}
	}
	if called > 0 {
		info.ObservedHet = round4(float64(het) / float64(called))
	}
	return out, nil
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
	if o.phase == nil {
		o.phase = make([]uint64, phaseWords(len(o.geno)))
	}
	for li, labels := range l.alleles {
		if len(labels) <= 1 {
			continue
		}
		s := l.start[li]
		w := make([]float64, len(labels))
		for a := range labels {
			o.geno[s+a] = 0
			o.phase[(s+a)>>6] &^= 1 << (uint(s+a) & 63)
			w[a] = freq[s+a] + 1e-9
		}
		ia, ib := drawWeighted(w, rng), drawWeighted(w, rng)
		o.geno[s+ia]++
		o.geno[s+ib]++
		if ia != ib {
			o.setPhaseBit(s + ib)
		}
	}
}
