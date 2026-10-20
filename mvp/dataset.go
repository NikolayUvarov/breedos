package main

import (
	"bufio"
	"embed"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// v0.7.4 dataset loader (Issue 04 — public-data proof).
// v0.7.5 — external file preferred over embedded; embedded fallback only ships
// the placeholder so the binary stays small.
//
// On-disk founder-CSV format:
//
//   # comments and metadata may appear as lines starting with '#'
//   # placeholder: true               <-- if present, marks the file as a non-real placeholder
//   accession_id,m1,m2,m3,...,mN
//   1001,0,1,2,0,...,1
//   1002,2,1,0,2,...,0
//
// Values are 0/1/2 (allele dose per locus, diploid biallelic).
// Rows are individual accessions; columns after the ID are SNP markers.
//
// v0.8.0 — marker positions. When the file carries them, the simulator
// builds its genetic map from them instead of a synthetic one. Two forms
// are recognised: the comment line the tools/data fetch scripts write
// right after "# Sites (chrom:pos), ...", or marker header labels that are
// themselves "chrom:pos" (e.g. "chr1:12345"). Markers must be ordered by
// chromosome and position.
//
// Lookup order for a request like dataset=arabidopsis1001:
//   1. <bindir>/data/arabidopsis1001.csv          (external — operator-deployed)
//   2. embedded data/arabidopsis1001.csv           (only if explicitly included via //go:embed)
//   3. embedded data/example_founders.csv          (placeholder fallback so the dropdown still works)
//
// Only the placeholder is included in the //go:embed directive so the binary
// stays small. Large real-data CSVs (Arabidopsis 1001, future maize panels)
// are gitignored in the breedos repo, fetched locally with tools/data/fetch_*.py,
// and uploaded to the server by deploy_breedos.sh as separate files alongside
// the binary.

//go:embed data/example_founders.csv
var embeddedDatasets embed.FS

type loadedDataset struct {
	individuals   []organism
	markerCount   int
	accessionIDs  []string
	isPlaceholder bool
	sourceFile    string
	sourceNotes   []string
	external      bool
	sites         []markerSite // v0.8.0: physical positions, nil when the file has none.
	// v0.8.9 — multi-allelic (SSR) data: markers are allele indicators
	// grouped into loci (ssr.go); nil for 0/1/2 dosage data.
	layout *locusLayout
	ssr    *ssrInfo
}

var datasetCache = struct {
	sync.Mutex
	items map[string]*loadedDataset
}{items: make(map[string]*loadedDataset)}

func loadDataset(name string) (*loadedDataset, error) {
	datasetCache.Lock()
	defer datasetCache.Unlock()
	if ds, ok := datasetCache.items[name]; ok {
		return ds, nil
	}
	ds, err := loadDatasetUncached(name)
	if err != nil {
		return nil, err
	}
	datasetCache.items[name] = ds
	return ds, nil
}

func loadDatasetUncached(name string) (*loadedDataset, error) {
	// v0.7.22 — Issue 19. The Holstein-synthetic dataset is generated in
	// memory from a fixed-seed RNG (the request's seed is applied later
	// during subsampleDataset). Reasonable defaults: 500 founders × 2000
	// markers, then subsampled to the request's N × M.
	if name == "holstein_synthetic" {
		return synthHolsteinFounders(500, 2000, holsteinSynthGenSeed()), nil
	}
	// 1) External file next to the running binary, in ./data/<name>.csv.
	//    This is the production layout: deploy_breedos.sh uploads large CSVs
	//    here instead of embedding them in the binary.
	if path, ok := externalDatasetPath(name); ok {
		data, err := os.ReadFile(path)
		if err == nil {
			ds, err := parseDatasetCSV(data, path)
			if err == nil {
				ds.external = true
				return ds, nil
			}
			return nil, fmt.Errorf("external dataset %q parse failed: %w", path, err)
		}
	}

	// 2) Embedded dataset matching the requested name (rare — only when a
	//    small dataset is explicitly added to the //go:embed directive).
	candidate := "data/" + name + ".csv"
	if data, err := embeddedDatasets.ReadFile(candidate); err == nil {
		return parseDatasetCSV(data, candidate)
	}

	// 3) Fall back to the embedded placeholder so the dropdown still works on
	//    a fresh install without external data.
	data, err := embeddedDatasets.ReadFile("data/example_founders.csv")
	if err != nil {
		return nil, fmt.Errorf("no external file for %q and no embedded placeholder available: %w", name, err)
	}
	return parseDatasetCSV(data, "data/example_founders.csv")
}

// externalDatasetPath returns the path BreedOS looks at for an external
// dataset, alongside the running binary in ./data/<name>.csv. Returns
// (path, true) if the file exists, (path, false) otherwise.
func externalDatasetPath(name string) (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err == nil {
		exe = resolved
	}
	path := filepath.Join(filepath.Dir(exe), "data", name+".csv")
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return path, false
	}
	return path, true
}

// resetDatasetCache is exposed for tests; it should not be called from
// production code paths since dataset files are not expected to change while
// the binary is running.
func resetDatasetCache() {
	datasetCache.Lock()
	defer datasetCache.Unlock()
	datasetCache.items = make(map[string]*loadedDataset)
}

// parseDatasetCSV reads a 0/1/2 dosage table, or — v0.8.9 — an SSR table of
// allele sizes when the dosage parse fails and the content looks
// multi-allelic (ssr.go).
func parseDatasetCSV(raw []byte, sourceFile string) (*loadedDataset, error) {
	ds, err := parseDosageCSV(raw, sourceFile)
	if err != nil && looksMultiallelic(raw) {
		ssr, ssrErr := parseMultiallelicCSV(raw, sourceFile)
		if ssrErr != nil {
			return nil, fmt.Errorf("%v; read as an SSR allele table instead: %v", err, ssrErr)
		}
		return ssr, nil
	}
	return ds, err
}

func parseDosageCSV(raw []byte, sourceFile string) (*loadedDataset, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)
	out := &loadedDataset{sourceFile: sourceFile}
	headerSeen := false
	var headerCols int
	lineNum := 0
	expectSites := false
	delim := "," // v0.8.9: ";" or tab when the header uses them (Excel exports)
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if strings.Contains(strings.ToLower(line), "placeholder: true") {
				out.isPlaceholder = true
			}
			note := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if expectSites {
				// The site list can hold tens of thousands of entries; it
				// feeds the genetic map and is kept out of sourceNotes.
				expectSites = false
				out.sites = parseSiteList(strings.Split(note, ","))
				continue
			}
			if strings.HasPrefix(note, "Sites (chrom:pos)") {
				expectSites = true
			}
			if note != "" {
				out.sourceNotes = append(out.sourceNotes, note)
			}
			continue
		}
		if !headerSeen {
			delim = detectDelimiter(line)
		}
		fields := strings.Split(line, delim)
		if !headerSeen {
			headerSeen = true
			headerCols = len(fields)
			if headerCols < 2 {
				return nil, fmt.Errorf("dataset %s: header has fewer than 2 columns", sourceFile)
			}
			out.markerCount = headerCols - 1
			if out.sites == nil {
				out.sites = parseSiteList(fields[1:])
			}
			continue
		}
		if len(fields) != headerCols {
			return nil, fmt.Errorf("dataset %s: line %d has %d cols, expected %d", sourceFile, lineNum, len(fields), headerCols)
		}
		geno := make([]uint8, out.markerCount)
		for i := 0; i < out.markerCount; i++ {
			v, err := strconv.Atoi(strings.TrimSpace(fields[i+1]))
			if err != nil {
				return nil, fmt.Errorf("dataset %s: line %d col %d: %w", sourceFile, lineNum, i+2, err)
			}
			if v < 0 || v > 2 {
				return nil, fmt.Errorf("dataset %s: line %d col %d value %d outside 0..2", sourceFile, lineNum, i+2, v)
			}
			geno[i] = uint8(v)
		}
		out.accessionIDs = append(out.accessionIDs, strings.TrimSpace(fields[0]))
		out.individuals = append(out.individuals, organism{geno: geno})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("dataset %s scan: %w", sourceFile, err)
	}
	if len(out.individuals) == 0 {
		return nil, fmt.Errorf("dataset %s: no individuals after header", sourceFile)
	}
	if len(out.sites) != out.markerCount {
		out.sites = nil
	}
	return out, nil
}

// subsampleDataset samples up to n accessions without replacement and up to m
// markers. v0.8.0: markers are taken at an even stride across all columns
// (order preserved) instead of the first m, so a run on a genome-ordered
// file covers every chromosome rather than the start of the first one. The
// returned dataset is a deep copy; the input is not mutated and can stay in
// the package-level cache.
func subsampleDataset(ds *loadedDataset, n, m int, rng *rand.Rand) *loadedDataset {
	if ds == nil {
		return nil
	}
	if n <= 0 || n > len(ds.individuals) {
		n = len(ds.individuals)
	}
	if m <= 0 || m > ds.markerCount {
		m = ds.markerCount
	}
	idx := rng.Perm(len(ds.individuals))[:n]
	out := &loadedDataset{
		markerCount:   m,
		isPlaceholder: ds.isPlaceholder,
		sourceFile:    ds.sourceFile,
		sourceNotes:   ds.sourceNotes,
		external:      ds.external,
		individuals:   make([]organism, n),
		accessionIDs:  make([]string, n),
	}
	cols, layout := subsampleColumns(ds, m)
	if layout != nil {
		out.layout, out.ssr = layout, ds.ssr
		out.markerCount = len(cols)
		m = len(cols)
	}
	if ds.sites != nil {
		out.sites = make([]markerSite, m)
		for k, c := range cols {
			out.sites[k] = ds.sites[c]
		}
	}
	for i, src := range idx {
		geno := make([]uint8, m)
		from := &ds.individuals[src]
		o := organism{geno: geno}
		if from.phase != nil {
			o.phase = make([]uint64, phaseWords(m))
		}
		for k, c := range cols {
			geno[k] = from.geno[c]
			if from.phase != nil && from.phaseBit(c) == 1 {
				o.setPhaseBit(k)
			}
		}
		out.individuals[i] = o
		out.accessionIDs[i] = ds.accessionIDs[src]
	}
	return out
}

// strideColumns picks m of total marker columns at an even stride, in
// order. subsampleDataset and the effect estimator (effects.go) must agree.
func strideColumns(total, m int) []int {
	if m <= 0 || m > total {
		m = total
	}
	cols := make([]int, m)
	for k := range cols {
		cols[k] = k * total / m
	}
	return cols
}

// subsampleColumns returns the marker columns subsampleDataset keeps for m
// requested markers. v0.8.9: multi-allelic data is subsampled by whole
// loci, so the indicators of a locus are never split; the re-indexed
// layout is returned with them (nil for dosage data).
func subsampleColumns(ds *loadedDataset, m int) ([]int, *locusLayout) {
	if m <= 0 || m > ds.markerCount {
		m = ds.markerCount
	}
	if ds.layout == nil {
		return strideColumns(ds.markerCount, m), nil
	}
	nLoci := ds.layout.loci()
	want := int(math.Round(float64(nLoci) * float64(m) / float64(ds.markerCount)))
	if want < 1 {
		want = 1
	}
	return subsetLayout(ds.layout, strideColumns(nLoci, want))
}
