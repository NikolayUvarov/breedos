package main

// v0.7.26 — Issue 05. Minimal upload workflow.
//
// Honest scope: this is the narrow, ephemeral CSV path the issue calls
// for. Uploads are held in memory, evicted after uploadTTL, and never
// persisted. Only the genotype CSV is consumed by the simulator; the
// phenotype / pedigree / edit tables are parsed and surfaced in the
// import summary with explicit "loaded but not yet used by the engine"
// notes (the simulator generates phenotypes from genotypes via its QTL
// model — wiring uploaded phenotypes into the engine is a separate
// follow-up).
//
// Non-goals (per the issue): BrAPI, large-file handling beyond
// uploadMaxBytes, multi-user storage, imputation/QC.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

func nanFloat() float64 { return math.NaN() }

const (
	uploadMaxBytes  = 16 * 1024 * 1024 // 16 MB hard cap per file. Anything larger is rejected with a clear error.
	uploadTTL       = 1 * time.Hour    // ephemeral; uploads evicted after this idle period.
	uploadCacheMax  = 32               // most-recently-used cap. Older uploads evicted FIFO.
)

// UploadedDataset is the in-memory snapshot of one upload. Only the
// genotype field is consumed by the simulator today; the rest are
// surfaced to the operator but not (yet) wired into runSimulation.
type UploadedDataset struct {
	ID         string             `json:"id"`
	CreatedAt  time.Time          `json:"created_at"`
	Genotype   *uploadedGenotype  `json:"genotype,omitempty"`
	Phenotype  *uploadedPhenotype `json:"phenotype,omitempty"`
	Pedigree   *uploadedPedigree  `json:"pedigree,omitempty"`
	Edits      *uploadedEdits     `json:"edits,omitempty"`
	// v0.7.29 — Issue 08. External GEBV / breeding-value predictions
	// from rrBLUP / BGLR / sommer / AlphaSimR or any other prediction
	// pipeline. Consumed by the imported_gebv strategy at gen 0.
	Predictions *uploadedPredictions `json:"predictions,omitempty"`
	UsedByEngine []string         `json:"used_by_engine"`         // human-readable list of which tables actually feed the simulator.
	IgnoredByEngine []string      `json:"ignored_by_engine"`      // counterpart: parsed but unused.
}

type uploadedGenotype struct {
	Individuals int      `json:"individuals"`
	Markers     int      `json:"markers"`
	SampleIDs   []string `json:"sample_ids"`                  // first up to 5 ids for the import summary.
	dataset     *loadedDataset                                 // not JSON-serialised; the simulator entry point.
}

type uploadedPhenotype struct {
	Rows       int      `json:"rows"`
	TraitName  string   `json:"trait_name"`
	Min        float64  `json:"min"`
	Max        float64  `json:"max"`
	Mean       float64  `json:"mean"`
	SampleIDs  []string `json:"sample_ids"`                   // first up to 5 ids; used to flag id-mismatch against genotype.
	// v0.8.6 — Issue 37. Ids also present in the genotype CSV; the engine
	// estimates marker effects from these when there are enough of them.
	MatchedGenotype int                `json:"matched_genotype"`
	values          map[string]float64 // id → trait value, for effect estimation.
}

type uploadedPedigree struct {
	Rows         int      `json:"rows"`
	UniqueSires  int      `json:"unique_sires"`
	UniqueDams   int      `json:"unique_dams"`
}

type uploadedEdits struct {
	Rows          int                 `json:"rows"`
	Entries       []uploadedEditEntry `json:"entries"`         // small enough to round-trip whole.
}

// v0.7.29 — Issue 08. External genomic-prediction outputs (rrBLUP /
// BGLR / sommer / AlphaSimR / internal pipelines). Accepted as a CSV
// "id, gebv[, uncertainty]". Used at gen 0 selection by the
// `imported_gebv` strategy (and any future GEBV-aware strategies);
// gen ≥ 1 falls back to the simulator's internal genomic signal
// because offspring identities are not tracked through reproduction.
type uploadedPredictions struct {
	Rows        int                       `json:"rows"`
	TraitName   string                    `json:"trait_name,omitempty"`
	Min         float64                   `json:"min"`
	Max         float64                   `json:"max"`
	Mean        float64                   `json:"mean"`
	HasUncertainty bool                   `json:"has_uncertainty"`
	SampleIDs   []string                  `json:"sample_ids"`         // first up to 5 ids.
	values      map[string]predictionRow  // id → {gebv, uncertainty}; not JSON-serialised; the simulator-consumption surface.
}

type predictionRow struct {
	GEBV        float64
	Uncertainty float64 // 0 when not provided.
}

type uploadedEditEntry struct {
	MarkerID       string  `json:"marker_id"`
	TargetAllele   int     `json:"target_allele"`
	ExpectedEffect float64 `json:"expected_effect"`
	Note           string  `json:"note,omitempty"`
}

var uploadCache = struct {
	sync.Mutex
	items map[string]*UploadedDataset
}{items: make(map[string]*UploadedDataset)}

// newUploadID produces a 16-char hex id stable enough for the cache.
// Uses crypto/rand so collisions are negligible at this scale.
func newUploadID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback: timestamp-based id. Still unique enough for a single
		// in-memory cache that holds tens of items at most.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// putUpload stores the upload under a freshly-minted id and evicts the
// oldest item if the cache is over capacity. Idle TTL is enforced by
// getUpload on read.
func putUpload(d *UploadedDataset) string {
	uploadCache.Lock()
	defer uploadCache.Unlock()
	d.ID = newUploadID()
	d.CreatedAt = time.Now()
	uploadCache.items[d.ID] = d
	if len(uploadCache.items) > uploadCacheMax {
		var oldestID string
		var oldestT time.Time
		for k, v := range uploadCache.items {
			if oldestID == "" || v.CreatedAt.Before(oldestT) {
				oldestID = k
				oldestT = v.CreatedAt
			}
		}
		delete(uploadCache.items, oldestID)
	}
	return d.ID
}

// getUpload returns the upload by id or (nil, false) if missing/expired.
func getUpload(id string) (*UploadedDataset, bool) {
	uploadCache.Lock()
	defer uploadCache.Unlock()
	d, ok := uploadCache.items[id]
	if !ok {
		return nil, false
	}
	if time.Since(d.CreatedAt) > uploadTTL {
		delete(uploadCache.items, id)
		return nil, false
	}
	return d, true
}

// parseUploadGenotype reuses parseDatasetCSV — the upload genotype
// format intentionally matches the existing dataset CSV format so the
// engine path is shared.
func parseUploadGenotype(raw []byte) (*uploadedGenotype, error) {
	ds, err := parseDatasetCSV(raw, "uploaded:genotype.csv")
	if err != nil {
		return nil, err
	}
	sample := ds.accessionIDs
	if len(sample) > 5 {
		sample = sample[:5]
	}
	return &uploadedGenotype{
		Individuals: len(ds.individuals),
		Markers:     ds.markerCount,
		SampleIDs:   sample,
		dataset:     ds,
	}, nil
}

// parseUploadPhenotype expects a CSV with header `id,trait_value` (case
// insensitive). The second column may be named anything; the column
// label becomes the trait name in the summary.
func parseUploadPhenotype(raw []byte) (*uploadedPhenotype, error) {
	lines := splitCSVLines(raw)
	if len(lines) < 2 {
		return nil, fmt.Errorf("phenotype CSV: need header + at least one data row")
	}
	headers := splitFields(lines[0])
	if len(headers) < 2 {
		return nil, fmt.Errorf("phenotype CSV: header must have at least 2 columns (id, trait)")
	}
	if !strings.EqualFold(strings.TrimSpace(headers[0]), "id") {
		return nil, fmt.Errorf("phenotype CSV: first column must be 'id', got %q", headers[0])
	}
	trait := strings.TrimSpace(headers[1])

	out := &uploadedPhenotype{TraitName: trait, values: make(map[string]float64)}
	out.SampleIDs = make([]string, 0, 5)
	first := true
	for i, raw := range lines[1:] {
		fields := splitFields(raw)
		if len(fields) < 2 {
			return nil, fmt.Errorf("phenotype CSV: line %d has %d columns, expected ≥2", i+2, len(fields))
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("phenotype CSV: line %d col 2: %v", i+2, err)
		}
		if first {
			out.Min, out.Max, out.Mean = v, v, v
			first = false
		} else {
			if v < out.Min {
				out.Min = v
			}
			if v > out.Max {
				out.Max = v
			}
			out.Mean += v
		}
		if len(out.SampleIDs) < 5 {
			out.SampleIDs = append(out.SampleIDs, strings.TrimSpace(fields[0]))
		}
		out.values[strings.TrimSpace(fields[0])] = v
		out.Rows++
	}
	if out.Rows == 0 {
		return nil, fmt.Errorf("phenotype CSV: no data rows")
	}
	out.Mean = out.Mean / float64(out.Rows)
	return out, nil
}

// parseUploadPedigree expects a CSV with header `id,sire,dam`.
// Unknown sires/dams are not flagged here — the simulator does not
// consume the table, and the issue's non-goals exclude QC/imputation.
func parseUploadPedigree(raw []byte) (*uploadedPedigree, error) {
	lines := splitCSVLines(raw)
	if len(lines) < 2 {
		return nil, fmt.Errorf("pedigree CSV: need header + at least one data row")
	}
	headers := splitFields(lines[0])
	if len(headers) < 3 {
		return nil, fmt.Errorf("pedigree CSV: header must have at least 3 columns (id, sire, dam)")
	}
	if !strings.EqualFold(strings.TrimSpace(headers[0]), "id") {
		return nil, fmt.Errorf("pedigree CSV: first column must be 'id', got %q", headers[0])
	}
	sires := map[string]struct{}{}
	dams := map[string]struct{}{}
	out := &uploadedPedigree{}
	for i, raw := range lines[1:] {
		fields := splitFields(raw)
		if len(fields) < 3 {
			return nil, fmt.Errorf("pedigree CSV: line %d has %d columns, expected ≥3", i+2, len(fields))
		}
		sire := strings.TrimSpace(fields[1])
		dam := strings.TrimSpace(fields[2])
		if sire != "" {
			sires[sire] = struct{}{}
		}
		if dam != "" {
			dams[dam] = struct{}{}
		}
		out.Rows++
	}
	out.UniqueSires = len(sires)
	out.UniqueDams = len(dams)
	return out, nil
}

// parseUploadEdits expects header `marker_id,target_allele,expected_effect`
// with an optional 4th `note` column.
func parseUploadEdits(raw []byte) (*uploadedEdits, error) {
	lines := splitCSVLines(raw)
	if len(lines) < 2 {
		return nil, fmt.Errorf("edits CSV: need header + at least one data row")
	}
	headers := splitFields(lines[0])
	if len(headers) < 3 {
		return nil, fmt.Errorf("edits CSV: header must have at least 3 columns (marker_id, target_allele, expected_effect)")
	}

	out := &uploadedEdits{}
	for i, raw := range lines[1:] {
		fields := splitFields(raw)
		if len(fields) < 3 {
			return nil, fmt.Errorf("edits CSV: line %d has %d columns, expected ≥3", i+2, len(fields))
		}
		target, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil {
			return nil, fmt.Errorf("edits CSV: line %d col 2 (target_allele): %v", i+2, err)
		}
		if target < 0 || target > 2 {
			return nil, fmt.Errorf("edits CSV: line %d target_allele=%d outside 0..2", i+2, target)
		}
		effect, err := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("edits CSV: line %d col 3 (expected_effect): %v", i+2, err)
		}
		note := ""
		if len(fields) >= 4 {
			note = strings.TrimSpace(fields[3])
		}
		out.Entries = append(out.Entries, uploadedEditEntry{
			MarkerID:       strings.TrimSpace(fields[0]),
			TargetAllele:   target,
			ExpectedEffect: effect,
			Note:           note,
		})
		out.Rows++
	}
	return out, nil
}

// parseUploadPredictions expects header `id, gebv[, uncertainty[, trait]]`
// (case-insensitive column names). The id column must match exactly
// the genotype CSV's id column at the row level; ID lookup happens
// later, in the simulator's gen-0 selection path.
//
// Output `values` maps id → {gebv, uncertainty}. Uncertainty is 0
// when the column is absent. The trait column (if present) is
// captured in TraitName for display.
func parseUploadPredictions(raw []byte) (*uploadedPredictions, error) {
	lines := splitCSVLines(raw)
	if len(lines) < 2 {
		return nil, fmt.Errorf("predictions CSV: need header + at least one data row")
	}
	headers := splitFields(lines[0])
	if len(headers) < 2 {
		return nil, fmt.Errorf("predictions CSV: header must have at least 2 columns (id, gebv)")
	}
	if !strings.EqualFold(strings.TrimSpace(headers[0]), "id") {
		return nil, fmt.Errorf("predictions CSV: first column must be 'id', got %q", headers[0])
	}
	if !strings.EqualFold(strings.TrimSpace(headers[1]), "gebv") {
		return nil, fmt.Errorf("predictions CSV: second column must be 'gebv', got %q", headers[1])
	}
	hasUncertainty := len(headers) >= 3 && strings.EqualFold(strings.TrimSpace(headers[2]), "uncertainty")
	traitName := ""
	if len(headers) >= 4 && strings.EqualFold(strings.TrimSpace(headers[3]), "trait") {
		// Trait column may carry a single trait name repeated per row;
		// captured below on the first non-empty row.
	} else if len(headers) >= 3 && !hasUncertainty {
		// A third column that is not 'uncertainty' is treated as trait.
		traitName = strings.TrimSpace(headers[2])
	}

	out := &uploadedPredictions{
		HasUncertainty: hasUncertainty,
		values:         make(map[string]predictionRow),
	}
	out.SampleIDs = make([]string, 0, 5)
	first := true
	for i, raw := range lines[1:] {
		fields := splitFields(raw)
		if len(fields) < 2 {
			return nil, fmt.Errorf("predictions CSV: line %d has %d columns, expected ≥2", i+2, len(fields))
		}
		id := strings.TrimSpace(fields[0])
		if id == "" {
			return nil, fmt.Errorf("predictions CSV: line %d has empty id", i+2)
		}
		gebv, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("predictions CSV: line %d col 2 (gebv): %v", i+2, err)
		}
		row := predictionRow{GEBV: gebv}
		if hasUncertainty && len(fields) >= 3 {
			u, err := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64)
			if err != nil {
				return nil, fmt.Errorf("predictions CSV: line %d col 3 (uncertainty): %v", i+2, err)
			}
			row.Uncertainty = u
		}
		if _, dup := out.values[id]; dup {
			return nil, fmt.Errorf("predictions CSV: line %d duplicate id %q", i+2, id)
		}
		out.values[id] = row
		if first {
			out.Min, out.Max, out.Mean = gebv, gebv, gebv
			out.TraitName = traitName
			first = false
		} else {
			if gebv < out.Min {
				out.Min = gebv
			}
			if gebv > out.Max {
				out.Max = gebv
			}
			out.Mean += gebv
		}
		if len(out.SampleIDs) < 5 {
			out.SampleIDs = append(out.SampleIDs, id)
		}
		out.Rows++
	}
	if out.Rows == 0 {
		return nil, fmt.Errorf("predictions CSV: no data rows")
	}
	out.Mean = out.Mean / float64(out.Rows)
	return out, nil
}

// splitCSVLines skips blank and #-comment lines (same convention as
// parseDatasetCSV).
func splitCSVLines(raw []byte) []string {
	all := strings.Split(string(raw), "\n")
	out := make([]string, 0, len(all))
	for _, l := range all {
		t := strings.TrimRight(l, "\r")
		s := strings.TrimSpace(t)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		out = append(out, t)
	}
	return out
}

func splitFields(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// uploadHandler accepts a multipart/form-data POST with fields:
//   genotype  (required) — CSV in the parseDatasetCSV format.
//   phenotype (optional) — CSV "id,trait_value".
//   pedigree  (optional) — CSV "id,sire,dam".
//   edits     (optional) — CSV "marker_id,target_allele,expected_effect[,note]".
// Returns 200 + UploadedDataset on success, 4xx with a plain-text error
// describing exactly which file failed validation.
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	// Per-request body cap: 4 × per-file cap to allow all four files.
	r.Body = http.MaxBytesReader(w, r.Body, 4*uploadMaxBytes)
	if err := r.ParseMultipartForm(uploadMaxBytes); err != nil {
		http.Error(w, "upload: parse multipart: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	out := &UploadedDataset{}

	geno, err := readFormFile(r, "genotype")
	if err != nil {
		http.Error(w, "upload: genotype: "+err.Error(), http.StatusBadRequest)
		return
	}
	if geno == nil {
		http.Error(w, "upload: genotype file is required", http.StatusBadRequest)
		return
	}
	g, err := parseUploadGenotype(geno)
	if err != nil {
		http.Error(w, "upload: genotype: "+err.Error(), http.StatusBadRequest)
		return
	}
	out.Genotype = g
	out.UsedByEngine = append(out.UsedByEngine, "genotype (as founder population)")

	if pheno, err := readFormFile(r, "phenotype"); err != nil {
		http.Error(w, "upload: phenotype: "+err.Error(), http.StatusBadRequest)
		return
	} else if pheno != nil {
		p, err := parseUploadPhenotype(pheno)
		if err != nil {
			http.Error(w, "upload: phenotype: "+err.Error(), http.StatusBadRequest)
			return
		}
		// v0.8.6 — Issue 37. Phenotypes now drive the trait architecture:
		// marker effects are estimated from the genotyped lines that have
		// a phenotype (effects.go).
		for _, id := range g.dataset.accessionIDs {
			if _, ok := p.values[id]; ok {
				p.MatchedGenotype++
			}
		}
		if p.MatchedGenotype == 0 {
			http.Error(w, fmt.Sprintf("upload: phenotype: none of the %d phenotype ids match a genotype id (phenotype starts %q, genotype starts %q). The id columns of both files must use the same identifiers.", p.Rows, firstOr(p.SampleIDs), firstOr(g.SampleIDs)), http.StatusBadRequest)
			return
		}
		out.Phenotype = p
		if p.MatchedGenotype < minPhenotypedForEffects {
			out.IgnoredByEngine = append(out.IgnoredByEngine, fmt.Sprintf("phenotype (only %d genotyped lines have a phenotype; at least %d are needed to estimate marker effects)", p.MatchedGenotype, minPhenotypedForEffects))
		} else {
			out.UsedByEngine = append(out.UsedByEngine, fmt.Sprintf("phenotype (marker effects estimated by rrBLUP from %d phenotyped lines; they replace the synthetic QTL)", p.MatchedGenotype))
		}
	}

	if ped, err := readFormFile(r, "pedigree"); err != nil {
		http.Error(w, "upload: pedigree: "+err.Error(), http.StatusBadRequest)
		return
	} else if ped != nil {
		p, err := parseUploadPedigree(ped)
		if err != nil {
			http.Error(w, "upload: pedigree: "+err.Error(), http.StatusBadRequest)
			return
		}
		out.Pedigree = p
		out.IgnoredByEngine = append(out.IgnoredByEngine, "pedigree (simulator builds its own pedigree from random mating; uploaded table is shown but not consumed)")
	}

	if edits, err := readFormFile(r, "edits"); err != nil {
		http.Error(w, "upload: edits: "+err.Error(), http.StatusBadRequest)
		return
	} else if edits != nil {
		e, err := parseUploadEdits(edits)
		if err != nil {
			http.Error(w, "upload: edits: "+err.Error(), http.StatusBadRequest)
			return
		}
		out.Edits = e
		out.IgnoredByEngine = append(out.IgnoredByEngine, "edits (simulator uses crispr_edits count, not uploaded marker-level edits; uploaded table is shown but not consumed)")
	}

	// v0.7.29 — Issue 08. Predictions (id, gebv[, uncertainty]).
	if predFile, err := readFormFile(r, "predictions"); err != nil {
		http.Error(w, "upload: predictions: "+err.Error(), http.StatusBadRequest)
		return
	} else if predFile != nil {
		p, err := parseUploadPredictions(predFile)
		if err != nil {
			http.Error(w, "upload: predictions: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Cross-validation: every predictions.id must be present in
		// genotype's accession IDs. Catches the most common silent
		// failure (wrong file pair).
		if out.Genotype != nil && out.Genotype.dataset != nil {
			genoIDs := make(map[string]struct{}, len(out.Genotype.dataset.accessionIDs))
			for _, id := range out.Genotype.dataset.accessionIDs {
				genoIDs[id] = struct{}{}
			}
			missing := 0
			var missingExample string
			for id := range p.values {
				if _, ok := genoIDs[id]; !ok {
					missing++
					if missingExample == "" {
						missingExample = id
					}
				}
			}
			if missing > 0 {
				http.Error(w, fmt.Sprintf("upload: predictions: %d id(s) not present in genotype CSV (first: %q). Predictions id column must match genotype id column.", missing, missingExample), http.StatusBadRequest)
				return
			}
		}
		out.Predictions = p
		out.UsedByEngine = append(out.UsedByEngine, "predictions (as gen-0 GEBV-aware selection signal in the imported_gebv strategy)")
	}

	id := putUpload(out)
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	resp := map[string]interface{}{
		"upload_id": id,
		"summary":   out,
		"note":      "Uploaded files are held in memory for up to 1 hour and never persisted. Reference upload_id in /api/simulate to use this genotype.",
	}
	_ = enc.Encode(resp)
}

func firstOr(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// readFormFile reads at most uploadMaxBytes from a named form file.
// Returns (nil, nil) when the field is absent.
func readFormFile(r *http.Request, field string) ([]byte, error) {
	f, _, err := r.FormFile(field)
	if err == http.ErrMissingFile {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, uploadMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > uploadMaxBytes {
		return nil, fmt.Errorf("file exceeds %d MB limit", uploadMaxBytes/(1024*1024))
	}
	return raw, nil
}

// uploadFixtureHandler serves the four embedded toy CSVs at
// /upload-fixture/<name>.csv. Used by the example links on the demo
// upload form. Only the allowlisted names are served.
func uploadFixtureHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/upload-fixture/")
	switch name {
	case "genotype.csv", "phenotype.csv", "pedigree.csv", "edits.csv", "predictions.csv":
		// ok — v0.7.29 added predictions.csv to the allowlist.
	default:
		http.NotFound(w, r)
		return
	}
	data, err := uploadFixturesFS.ReadFile("fixtures/upload-toy/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline; filename=\""+name+"\"")
	_, _ = w.Write(data)
}

// v0.7.29 — Issue 08. uploadHasPredictions returns true when the
// upload id resolves to a cached upload that carries a predictions
// table. Used by buildStrategyConfigs to decide whether to surface
// the imported_gebv strategy.
func uploadHasPredictions(uploadID string) bool {
	if uploadID == "" {
		return false
	}
	d, ok := getUpload(uploadID)
	if !ok || d == nil || d.Predictions == nil {
		return false
	}
	return d.Predictions.Rows > 0
}

// v0.7.29 — Issue 08. gen0GEBVOverride returns a length-N slice of
// imported GEBVs indexed by population position, matched via
// accession IDs from the upload's genotype. Positions whose accession
// id has no prediction get math.NaN — the caller treats NaN as
// "fall back to internal scoring for that individual". Returns nil
// when no upload is set or the upload carries no predictions.
//
// The slice is meaningful at gen 0 only; later generations are
// offspring whose ids are not in the predictions map.
func gen0GEBVOverride(uploadID string, accessionIDs []string) []float64 {
	if uploadID == "" || len(accessionIDs) == 0 {
		return nil
	}
	d, ok := getUpload(uploadID)
	if !ok || d == nil || d.Predictions == nil {
		return nil
	}
	out := make([]float64, len(accessionIDs))
	for i, id := range accessionIDs {
		row, ok := d.Predictions.values[id]
		if ok {
			out[i] = row.GEBV
		} else {
			out[i] = nanFloat()
		}
	}
	return out
}

// resetUploadCache is exposed for tests.
func resetUploadCache() {
	uploadCache.Lock()
	defer uploadCache.Unlock()
	uploadCache.items = make(map[string]*UploadedDataset)
}
