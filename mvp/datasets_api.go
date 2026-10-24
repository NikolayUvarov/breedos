package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// v0.7.12: dataset metadata page. Reads the embedded
// datasets/MANIFEST.json (curated list of public wheat panels, etc.) and
// merges it with the actual file sizes present on the server in
// <bindir>/data/datasets/. The /api/datasets endpoint returns the combined
// JSON; static/datasets.html renders it as a table.

type datasetManifest struct {
	Version          int                    `json:"version"`
	Description      string                 `json:"description"`
	DeployTruncateMb int                    `json:"deploy_truncate_mb"`
	Datasets         []datasetManifestEntry `json:"datasets"`
}

type datasetManifestEntry struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Filename       string `json:"filename"`
	Format         string `json:"format"`
	SourceURL      string `json:"source_url"`
	LandingURL     string `json:"landing_url"`
	License        string `json:"license"`
	Accessions     *int   `json:"accessions"`
	Markers        *int   `json:"markers"`
	Ploidy         string `json:"ploidy,omitempty"`
	SizeBytes      *int64 `json:"size_bytes"`
	Category       string `json:"category"`
	DeployStrategy string `json:"deploy_strategy"`
	ManualReason   string `json:"manual_reason,omitempty"`
	Content        string `json:"content"`
}

type datasetAPIEntry struct {
	datasetManifestEntry
	DeployedBytes *int64 `json:"deployed_bytes"`
	Truncated     bool   `json:"truncated"`
	Status        string `json:"status"` // "missing", "full", "truncated", "manual", "stale"
}

type datasetAPIResponse struct {
	Version           int               `json:"version"`
	Description       string            `json:"description"`
	DeployTruncateMb  int               `json:"deploy_truncate_mb"`
	ServerDatasetsDir string            `json:"server_datasets_dir"`
	Datasets          []datasetAPIEntry `json:"datasets"`
	// v0.7.23 — Issue E. In-memory generated datasets that the simulator
	// can use via `req.Dataset = "<id>"`. Not downloadable; not file-backed.
	// Listed separately so the operator sees they exist alongside the
	// public-file datasets above.
	GeneratedDatasets []generatedDatasetEntry `json:"generated_datasets"`
	EmbeddedDatasets  []embeddedDatasetEntry  `json:"embedded_datasets"`
}

// generatedDatasetEntry describes a synthetic / in-memory dataset that the
// simulator generates on demand. ExternalReferences point operators at the
// real-data sources that the synthetic one mimics (so they can graduate
// from synthetic to real when they have operator-side data acquisition).
type generatedDatasetEntry struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	HonestyNote        string   `json:"honesty_note"`
	ExternalReferences []string `json:"external_references"`
}

func datasetsAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	var manifest datasetManifest
	if err := json.Unmarshal(embeddedDatasetsManifest, &manifest); err != nil {
		http.Error(w, "manifest parse error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	datasetsDir := serverDatasetsDir()
	resp := datasetAPIResponse{
		Version:           manifest.Version,
		Description:       manifest.Description,
		DeployTruncateMb:  manifest.DeployTruncateMb,
		ServerDatasetsDir: datasetsDir,
		Datasets:          make([]datasetAPIEntry, 0, len(manifest.Datasets)),
	}
	for _, m := range manifest.Datasets {
		entry := datasetAPIEntry{datasetManifestEntry: m, Status: "missing"}
		path := filepath.Join(datasetsDir, m.Filename)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			n := info.Size()
			entry.DeployedBytes = &n
			full := m.SizeBytes
			switch {
			case m.DeployStrategy == "manual":
				if full != nil && n == *full {
					entry.Status = "full"
				} else {
					entry.Status = "manual"
				}
			case full != nil && n == *full:
				entry.Status = "full"
			case full != nil && n < *full:
				entry.Status = "truncated"
				entry.Truncated = true
			default:
				entry.Status = "stale"
			}
		} else if m.DeployStrategy == "manual" {
			entry.Status = "manual"
		}
		resp.Datasets = append(resp.Datasets, entry)
	}
	// v0.7.23 — Issue E. Populate the generated-datasets section.
	resp.GeneratedDatasets = generatedDatasetsCatalog()
	resp.EmbeddedDatasets = embeddedDatasetsCatalog()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

// generatedDatasetsCatalog returns the static list of in-memory dataset
// generators the simulator supports. Hardcoded for now; if new generators
// land they get an entry here.
// v0.8.19. Real datasets small enough to ship inside the binary.
type embeddedDatasetEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Citation    string `json:"citation"`
	License     string `json:"license"`
	SourceURL   string `json:"source_url"`
	LandingURL  string `json:"landing_url"`
	Accessions  int    `json:"accessions"`
	Loci        int    `json:"loci"`
	Alleles     int    `json:"alleles"`
}

func embeddedDatasetsCatalog() []embeddedDatasetEntry {
	return []embeddedDatasetEntry{
		{
			ID:          "sorghum_vir200",
			Name:        "Sorghum, VIR collection — 200 accessions × 12 SSR",
			Description: "Genetic passports of 200 sorghum accessions of the Vavilov Institute (VIR) collection by the 12-locus multiplex SSR panel; 230 alleles (10–31 per locus), 2.8 % missing calls, species group per accession. Transcribed from the paper's appendix, every row checked against it. Select it under Founder population in the demo (preset \"Sorghum VIR (SSR)\").",
			Citation:    "Aniskina Yu. V., Malinovskaya E. V., Mitsurova V. S., Velishaeva N. S., Kolobova O. S., Shilov I. A. (2019). Researching the genetic diversity of sorghum using the multiplex microsatellite analysis. Plant Biotechnology and Breeding, 2(3). doi:10.30901/2658-6266-2019-3-o1",
			License:     "CC BY 4.0",
			SourceURL:   "https://biosel.elpub.ru/jour/article/downloadSuppFile/39/26",
			LandingURL:  "https://doi.org/10.30901/2658-6266-2019-3-o1",
			Accessions:  200, Loci: 12, Alleles: 230,
		},
	}
}

func generatedDatasetsCatalog() []generatedDatasetEntry {
	return []generatedDatasetEntry{
		{
			ID:          "holstein_synthetic",
			Name:        "Synthetic Holstein-flavoured founders",
			Description: "500 diploid individuals × 2000 markers, generated in memory from a Beta(0.5, 0.5) MAF (U-shaped, reflecting Holstein bottleneck history) with Hardy-Weinberg dosage sampling. Subsampled to req.PopulationSize × req.Markers at run time.",
			HonestyNote: "NOT real bovine genotypes. Statistically Holstein-shaped (MAF distribution mimics published Holstein data) but does not represent any specific bull or herd. Use only for decision-layer modelling, not for breeding decisions on real animals.",
			ExternalReferences: []string{
				"1000 Bull Genomes Run 8 (public, NCBI BioProject PRJEB42783)",
				"1000 Bull Genomes Run 9 (controlled access, CNGB PRJEB56689)",
			},
		},
	}
}

// serverDatasetsDir resolves to <bindir>/data/datasets/ at runtime. Mirrors
// the lookup in dataset.go for external CSVs.
func serverDatasetsDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "data/datasets"
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err == nil {
		exe = resolved
	}
	return filepath.Join(filepath.Dir(exe), "data", "datasets")
}
