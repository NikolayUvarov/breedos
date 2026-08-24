package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed static/*
var embeddedStatic embed.FS

//go:embed datasets-manifest.json
var embeddedDatasetsManifest []byte

// v0.7.26 — Issue 05. Toy fixtures for the demo upload form.
//
//go:embed fixtures/upload-toy/*.csv
var uploadFixturesFS embed.FS

type SimRequest struct {
	Seed               int64   `json:"seed"`
	PopulationSize     int     `json:"population_size"`
	Markers            int     `json:"markers"`
	QTLCount           int     `json:"qtl_count"`
	Generations        int     `json:"generations"`
	SelectionPercent   float64 `json:"selection_percent"`
	Heritability       float64 `json:"heritability"`
	MutationRate       float64 `json:"mutation_rate"`
	CrisprEnabled      bool    `json:"crispr_enabled"`
	CrisprEdits        int     `json:"crispr_edits"`
	CrisprIntroPercent float64 `json:"crispr_intro_percent"`
	StrategySet        string  `json:"strategy_set"`
	Replicates         int     `json:"replicates"`
	WorkerCount        int     `json:"worker_count"`
	InbreedingLimit    float64 `json:"inbreeding_limit"`
	DiversityLossLimit float64 `json:"diversity_loss_limit"`
	// v0.7.3 constraint engine (Issue 03). All zero = no constraint applied.
	MaxInbreeding       float64 `json:"max_inbreeding"`
	MaxDiversityLoss    float64 `json:"max_diversity_loss"`
	MaxRareUsefulLoss   int     `json:"max_rare_useful_loss"`
	MinGeneticGain      float64 `json:"min_genetic_gain"`
	MinEffectiveParents int     `json:"min_effective_parents"`
	MaxCombinedRisk     float64 `json:"max_combined_risk"`
	// v0.7.4 dataset loader (Issue 04). "" or "synthetic" = generated population;
	// "arabidopsis1001" = load real Arabidopsis 1001 Genomes founders from embedded CSV.
	Dataset string `json:"dataset"`
	// v0.7.8 strategy picker for live AFS histogram. "" or "auto" = prefer "balanced",
	// else first configured strategy. A non-empty value is taken as a strategy code
	// (e.g. "aggressive"); if not present in the run's strategy set, falls back to auto.
	TrackedStrategy string `json:"tracked_strategy"`
	// v0.7.18 — Issue 13 EU NGT regulatory context for candidate-edit classification.
	// Optional: when zero-valued, the resulting classification is "unclassifiable".
	NGT NGTContext `json:"ngt,omitempty"`
	// v0.7.21 — Issue 18. Multi-trait selection. Empty/nil triggers the
	// existing single-trait code path (backward compat). When non-empty,
	// runMultiTraitSimulation is used and selection runs on a weighted
	// index of per-trait standardised phenotypes.
	Traits              []TraitConfig `json:"traits,omitempty"`
	GeneticCorrelations [][]float64   `json:"genetic_correlations,omitempty"`
	// v0.7.24 — Issue 28. Climate stress scenario applied to phenotype.
	// Nil = no climate stress (default; bit-identical to v0.7.23 behaviour).
	// When set, TraitMean and GeneticGain on every MetricPoint are scaled
	// by (1 − penalty) where penalty = climateModePenalty[mode] × severity
	// clamped to [0, 0.95].
	Climate *ClimateScenario `json:"climate,omitempty"`
	// v0.7.26 — Issue 05. Minimal upload workflow. When non-empty,
	// the simulator looks up the upload by id (in-memory cache,
	// uploadTTL=1h) and uses its genotype as the founder population.
	// Takes precedence over Dataset. Phenotype / pedigree / edit
	// tables on the same upload are parsed and surfaced but not
	// (yet) consumed by the engine — see notes in upload.go.
	Upload string `json:"upload,omitempty"`
	// v0.7.28 — Issue 30. Ancestral-allele introgression strategy.
	// AncestralIntroPercent is the % of founder population replaced by
	// synthetic "ancestral" / landrace lines at gen 0. Default 0 (the
	// strategy is opt-in via the advanced strategy set). Hard cap 25%
	// enforced in validateRequest. AncestralStressTolerance is the
	// fraction of climate penalty borne by ancestral lines (0.5 = half
	// the modern penalty); when 0 → falls back to the package default
	// `AncestralStressToleranceDefault` (also 0.5).
	AncestralIntroPercent    float64 `json:"ancestral_intro_percent,omitempty"`
	AncestralStressTolerance float64 `json:"ancestral_stress_tolerance,omitempty"`
}

// v0.7.21 — Issue 18. Single trait's architecture within a multi-trait run.
// Heritability, QTLCount, and EffectScale are independent per trait;
// SelectionWeight is the trait's weight in the selection index.
type TraitConfig struct {
	Name            string  `json:"name"`
	Heritability    float64 `json:"heritability"`
	QTLCount        int     `json:"qtl_count"`
	EffectScale     float64 `json:"effect_scale"`
	SelectionWeight float64 `json:"selection_weight"`
}

type SimResponse struct {
	Request        SimRequest       `json:"request"`
	Decision       DecisionSummary  `json:"decision"`
	Strategies     []StrategyResult `json:"strategies"`
	CandidateEdits []EditCandidate  `json:"candidate_edits"`
	Notes          []string         `json:"notes"`
}

type DecisionSummary struct {
	BestRiskAdjustedCode string       `json:"best_risk_adjusted_code"`
	BestRiskAdjustedName string       `json:"best_risk_adjusted_name"`
	BestGainCode         string       `json:"best_gain_code"`
	LowestRiskCode       string       `json:"lowest_risk_code"`
	BestFeasibleCode     string       `json:"best_feasible_code"`
	BestFeasibleName     string       `json:"best_feasible_name"`
	FeasibilityNote      string       `json:"feasibility_note"`
	ConstraintsApplied   []string     `json:"constraints_applied"`
	ParetoCodes          []string     `json:"pareto_codes"`
	Interpretation       []string     `json:"interpretation"`
	Tradeoffs            []Tradeoff   `json:"tradeoffs"`
	AvoidStrategies      []AvoidEntry `json:"avoid_strategies"`
	KeyAssumptions       []string     `json:"key_assumptions"`
	MissingDataWarnings  []string     `json:"missing_data_warnings"`
	NextAnalysis         string       `json:"next_analysis"`
	SummaryText          string       `json:"summary_text"`
	HonestyBanner        string       `json:"honesty_banner"`
	Limitations          []string     `json:"limitations"`
	WhatCouldBeWrong     []string     `json:"what_could_be_wrong"`
	// v0.7.18 — Issue 13/15. NGT regulatory classification of the planned edit
	// set. Populated only when at least one edit is planned (CrisprEnabled and
	// CrisprEdits > 0). Carries the verdict + reasons + disqualifiers +
	// "Not legal advice" disclaimer.
	NGT *NGTClassification `json:"ngt,omitempty"`
	// v0.7.27 — Issue 07. Edit-vs-cross-vs-wait aggregate across all
	// candidate edits in this run. Headline + per-class counts. Always
	// present when candidates were ranked (otherwise headline explains
	// why the table is empty).
	EditDecisions EditDecisionSummary `json:"edit_decisions"`
	// v0.7.21 — Issue 18. Headline per-trait gain for the recommended
	// (best risk-adjusted) strategy, keyed by trait name. Populated only on
	// multi-trait runs.
	PerTraitGain map[string]float64 `json:"per_trait_gain,omitempty"`
}

type Tradeoff struct {
	A     string `json:"a"`
	B     string `json:"b"`
	Theme string `json:"theme"`
	Note  string `json:"note"`
}

type AvoidEntry struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type StrategyResult struct {
	Name          string        `json:"name"`
	Code          string        `json:"code"`
	Summary       string        `json:"summary"`
	Replicates    int           `json:"replicates"`
	ParetoOptimal bool          `json:"pareto_optimal"`
	Metrics       []MetricPoint `json:"metrics"`
	Final         FinalStats    `json:"final"`
	// v0.7.21 — Issue 18. Populated only by multi-trait runs.
	// PerTraitMetrics[t] holds the per-generation trajectory for trait t
	// (length == len(req.Traits)); SelectionIndex is the per-generation
	// weighted standardised index used for selection.
	PerTraitMetrics [][]MetricPoint `json:"per_trait_metrics,omitempty"`
	SelectionIndex  []float64       `json:"selection_index,omitempty"`
}

type MetricPoint struct {
	Generation       int     `json:"generation"`
	TraitMean        float64 `json:"trait_mean"`
	GeneticGain      float64 `json:"genetic_gain"`
	Diversity        float64 `json:"diversity"`
	Inbreeding       float64 `json:"inbreeding"`
	AlleleDrift      float64 `json:"allele_drift"`
	RareUsefulLost   int     `json:"rare_useful_lost"`
	FixedLoci        int     `json:"fixed_loci"`
	FavorableFixed   int     `json:"favorable_fixed"`
	UnfavorableFixed int     `json:"unfavorable_fixed"`
	EffectiveParents int     `json:"effective_parents"`
	// v0.7.20 — Issue 20. Effective population size derived from the
	// per-generation increase in inbreeding F:
	//   Ne = 1 / (2 ΔF),  ΔF = F[t] - F[t-1]
	// (Falconer & Mackay ch. 5.) Capped at maxEffectiveNe when ΔF ≤ 0 or
	// numerically tiny to avoid divide-by-zero on log-scale chart. Generation
	// 0 has no preceding generation; its Ne is set to the cap.
	Ne float64 `json:"ne"`
}

type FinalStats struct {
	TraitMean                    float64 `json:"trait_mean"`
	GeneticGain                  float64 `json:"genetic_gain"`
	Diversity                    float64 `json:"diversity"`
	Inbreeding                   float64 `json:"inbreeding"`
	AlleleDrift                  float64 `json:"allele_drift"`
	RareUsefulLost               int     `json:"rare_useful_lost"`
	FixedLoci                    int     `json:"fixed_loci"`
	FavorableFixed               int     `json:"favorable_fixed"`
	UnfavorableFixed             int     `json:"unfavorable_fixed"`
	EffectiveParents             int     `json:"effective_parents"`
	Replicates                   int     `json:"replicates"`
	GainStd                      float64 `json:"gain_std"`
	DiversityStd                 float64 `json:"diversity_std"`
	InbreedingStd                float64 `json:"inbreeding_std"`
	ProbabilityInbreedingBreach  float64 `json:"probability_inbreeding_breach"`
	ProbabilityDiversityCollapse float64 `json:"probability_diversity_collapse"`
	ProbabilityRareUsefulLoss    float64 `json:"probability_rare_useful_loss"`
	RiskAdjustedScore            float64 `json:"risk_adjusted_score"`
	DecisionRank                 int     `json:"decision_rank"`
	ParetoOptimal                bool    `json:"pareto_optimal"`
	RecommendedNext              string  `json:"recommended_next"`
	Feasible                     bool    `json:"feasible"`
	FailedConstraints            []string `json:"failed_constraints"`
}

type EditCandidate struct {
	Rank              int     `json:"rank"`
	Locus             int     `json:"locus"`
	Effect            float64 `json:"effect"`
	AlleleFrequency   float64 `json:"allele_frequency"`
	ExpectedGainScore float64 `json:"expected_gain_score"`
	DiversityRisk     string  `json:"diversity_risk"`
	Decision          string  `json:"decision"`
	// v0.7.27 — Issue 07. Structured edit-vs-cross-vs-wait
	// classification. Backward-compat: the legacy `decision` string
	// above is still emitted; clients that only know about that field
	// keep working. Nil-friendly for paths that bypass the classifier.
	Classification *EditDecision `json:"classification,omitempty"`
}

type organism struct{ geno []uint8 }

type scoredIndividual struct {
	idx     int
	trait   float64
	genomic float64
	novelty float64
	score   float64
}

type strategyConfig struct {
	Name              string
	Code              string
	Summary           string
	Rule              string
	MatingRule        string
	TraitWeight       float64
	NoveltyWeight     float64
	SimilarityPenalty float64
	ParentMultiplier  float64
	UseCrisprSeed     bool
	ConservativeEdits bool
	// v0.7.28 — Issue 30. Strategy-level flag for ancestral-allele
	// introgression. When set, the worker loop calls seedAncestralLines
	// against the strategy's local population copy at gen 0 (before any
	// metrics are computed), and aggregateReplicates discounts the
	// climate penalty by climateDiscountForStrategy.
	UseAncestralSeed bool
	// v0.7.29 — Issue 08. UseImportedGEBV: when true AND the run
	// supplies an `Upload` with a `predictions` table, the strategy
	// scores gen-0 selection by the imported GEBV instead of the
	// simulator's internal genomic signal. After gen 0 the strategy
	// falls back to internal genomic scoring (offspring identities
	// are not tracked through reproduction, so the prediction can no
	// longer be looked up).
	UseImportedGEBV bool
	// v0.7.29 — Issue 08. Per-organism imported GEBVs aligned with the
	// gen-0 population order (NaN means "no prediction for this index;
	// fall back to internal scoring"). Populated by runSimulation
	// after the upload is loaded; the worker loop passes the cfg by
	// value so each task carries its own copy of the pointer.
	Gen0GEBV []float64
}

type progressFunc func(percent int, message string)

// v0.7.6 live histogram: per-generation allele-frequency spectrum snapshot.
// Emitted from inside the simulation for ONE tracked strategy (one replicate)
// so the async job handler can expose AFS progression while the run is live.
// Bins are 10 fixed-width buckets over [0, 1]: bin[k] counts markers with
// allele frequency in [k/10, (k+1)/10), with the last bin closed at 1.0.
type AFSSnapshot struct {
	Generation       int     `json:"generation"`
	TotalGenerations int     `json:"total_generations"`
	StrategyCode     string  `json:"strategy_code"`
	StrategyName     string  `json:"strategy_name"`
	Bins             [10]int `json:"bins"`
}

type snapshotFunc func(AFSSnapshot)

type SimJobStartResponse struct {
	JobID string `json:"job_id"`
}

type SimJobStatus struct {
	JobID          string        `json:"job_id"`
	Percent        int           `json:"percent"`
	Message        string        `json:"message"`
	Done           bool          `json:"done"`
	Error          string        `json:"error,omitempty"`
	Result         *SimResponse  `json:"result,omitempty"`
	LatestSnapshot *AFSSnapshot  `json:"latest_snapshot,omitempty"`
	// v0.7.13: snapshot queue. The status endpoint accepts ?since=<n> and
	// returns Snapshots[since:] plus SnapshotSeq = len(internal store). The
	// client polls fast (cheap), accumulates snapshots, and plays them back
	// on an independent UI timer so the chart is not capped by network rate
	// and stays smooth even after the simulation finishes.
	Snapshots   []AFSSnapshot `json:"snapshots,omitempty"`
	SnapshotSeq int           `json:"snapshot_seq"`
}

type simJob struct {
	ID             string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Percent        int
	Message        string
	Done           bool
	Error          string
	Result         *SimResponse
	LatestSnapshot *AFSSnapshot
	Snapshots      []AFSSnapshot // v0.7.13: full history of emitted snapshots for ?since= replay.
}

var simJobStore = struct {
	sync.Mutex
	NextID uint64
	Jobs   map[string]*simJob
}{Jobs: make(map[string]*simJob)}

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address, for example :8080 or 127.0.0.1:8080")
	selfCheck := flag.Bool("self-check", false, "print OK and exit (used by the self-update contract)")
	flag.Parse()
	if *selfCheck {
		fmt.Println("OK")
		os.Exit(0)
	}
	staticFS, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		log.Fatalf("static fs error: %v", err)
	}
	startSelfUpdateWatcher(selfUpdateInterval)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/simulate", simulateHandler)
	mux.HandleFunc("/api/simulate/start", startSimulationJobHandler)
	mux.HandleFunc("/api/simulate/status", simulationJobStatusHandler)
	mux.HandleFunc("/api/datasets", datasetsAPIHandler)
	mux.HandleFunc("/api/sensitivity/start", sensitivityStartHandler)
	mux.HandleFunc("/api/sensitivity/status", sensitivityStatusHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	// v0.7.23 — Issue C. /api/version returns the build metadata so deploy
	// verification doesn't have to grep kicker HTML. The constants are
	// updated in lockstep with the version-bump touchpoints in this file.
	mux.HandleFunc("/api/version", versionHandler)
	// v0.7.26 — Issue 05. Minimal upload workflow.
	mux.HandleFunc("/api/upload", uploadHandler)
	// v0.7.26 — Issue 05. Static fixture files for the example links on
	// the demo upload form. Embedded; only the four toy CSVs are served.
	mux.HandleFunc("/upload-fixture/", uploadFixtureHandler)
	// v0.7.31 — Promptogenesis v0.1 Prompt Genome Mapper.
	// See ingest-done/handoff-dna-prompt.md.done Section 6.
	mux.HandleFunc("/api/promptbio/map", promptbioMapHandler)
	// v0.7.32 — Promptogenesis v0.2 Prompt Genome Diff.
	// See ingest-done/06-prompt-dna.md.done + handoff Section 6.4.
	mux.HandleFunc("/api/promptbio/diff", promptbioDiffHandler)
	// v0.7.33 — Issue 07 substrate abstraction: promptbio simulate.
	// Runs the same five core engine moves over the prompt substrate.
	mux.HandleFunc("/api/promptbio/simulate", promptbioSimulateHandler)
	// v0.7.34 — Issue 03 Prompt Evolution Loop. Population × niches
	// → lineage tree + niche specialists + Pareto front.
	mux.HandleFunc("/api/promptbio/evolve", promptbioEvolveHandler)
	// v0.7.35 — Issue 30 Epistemology & Truth Maintenance.
	mux.HandleFunc("/api/promptbio/epistemology/plan", promptbioEpistemologyPlanHandler)
	mux.HandleFunc("/api/promptbio/epistemology/gate", promptbioEpistemologyGateHandler)
	mux.HandleFunc("/api/promptbio/epistemology/update", promptbioEpistemologyUpdateHandler)
	// v0.7.36 — Issue 16 v1.3 Prompt Evaluation Lab.
	mux.HandleFunc("/api/promptbio/eval/run", promptbioEvalRunHandler)
	mux.HandleFunc("/api/promptbio/eval/regression", promptbioEvalRegressionHandler)
	// v0.7.37 — Issue 33 v3.0 Unified Prompt Organism Architecture.
	mux.HandleFunc("/api/promptbio/organism/build", promptbioOrganismBuildHandler)
	mux.HandleFunc("/api/promptbio/organism/validate", promptbioOrganismValidateHandler)
	mux.HandleFunc("/api/promptbio/organism/catalogues", promptbioOrganismCataloguesHandler)
	// v0.7.38 — Issue 34 v3.1 Prompt Organism Design Patterns.
	mux.HandleFunc("/api/promptbio/pattern-select", promptbioPatternSelectHandler)
	mux.HandleFunc("/api/promptbio/pattern-compose", promptbioPatternComposeHandler)
	mux.HandleFunc("/api/promptbio/anti-patterns", promptbioAntiPatternsHandler)
	mux.HandleFunc("/api/promptbio/patterns", promptbioPatternsListHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		switch path {
		case "":
			path = "index.html"
		case "demo":
			path = "demo.html"
		case "ru":
			path = "index-ru.html"
		case "es":
			path = "index-es.html"
		case "uz":
			path = "index-uz.html"
		case "datasets":
			path = "datasets.html"
		case "theory":
			path = "theory.html"
		case "promptbio":
			path = "promptbio.html"
		}
		if strings.Contains(path, "..") {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		data, err := fs.ReadFile(staticFS, path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		setContentType(w, path)
		_, _ = w.Write(data)
	})
	server := &http.Server{Addr: *listen, Handler: loggingMiddleware(mux), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("BreedOS MVP server listening on http://%s", normalizeListenForLog(*listen))
	log.Fatal(server.ListenAndServe())
}

func simulateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var req SimRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := runSimulation(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(resp); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func startSimulationJobHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var req SimRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	normalizeRequest(&req)
	strategies := buildStrategyConfigs(req)
	if err := validateRequest(req, len(strategies)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := createSimulationJob()
	go func(jobID string, jobReq SimRequest) {
		resp, err := runSimulationWithCallbacks(jobReq,
			func(percent int, message string) { updateSimulationJob(jobID, percent, message, nil, "", false) },
			func(snap AFSSnapshot) { updateSimulationJobSnapshot(jobID, snap) })
		if err != nil {
			updateSimulationJob(jobID, 100, "failed", nil, err.Error(), true)
			return
		}
		updateSimulationJob(jobID, 100, "complete", &resp, "", true)
	}(id, req)
	writeJSON(w, SimJobStartResponse{JobID: id})
}

func simulationJobStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	id := q.Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	since := 0
	if s := q.Get("since"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			since = n
		}
	}
	status, ok := getSimulationJobStatus(id, since)
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	writeJSON(w, status)
}

func createSimulationJob() string {
	now := time.Now()
	simJobStore.Lock()
	defer simJobStore.Unlock()
	cleanupSimulationJobsLocked(now)
	simJobStore.NextID++
	id := fmt.Sprintf("job-%d-%d", now.UnixNano(), simJobStore.NextID)
	simJobStore.Jobs[id] = &simJob{ID: id, CreatedAt: now, UpdatedAt: now, Percent: 0, Message: "queued"}
	return id
}

func updateSimulationJob(id string, percent int, message string, result *SimResponse, errText string, done bool) {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	now := time.Now()
	simJobStore.Lock()
	defer simJobStore.Unlock()
	job := simJobStore.Jobs[id]
	if job == nil {
		return
	}
	if percent < job.Percent && !done {
		percent = job.Percent
	}
	job.Percent = percent
	if message != "" {
		job.Message = message
	}
	job.Result = result
	job.Error = errText
	job.Done = done
	job.UpdatedAt = now
}

func getSimulationJobStatus(id string, since int) (SimJobStatus, bool) {
	now := time.Now()
	simJobStore.Lock()
	defer simJobStore.Unlock()
	cleanupSimulationJobsLocked(now)
	job := simJobStore.Jobs[id]
	if job == nil {
		return SimJobStatus{}, false
	}
	var snapCopy *AFSSnapshot
	if job.LatestSnapshot != nil {
		s := *job.LatestSnapshot
		snapCopy = &s
	}
	// v0.7.13: return only snapshots emitted after the client's `since` index.
	// Total count is always exposed as SnapshotSeq so the client can advance.
	totalSeq := len(job.Snapshots)
	var newSnaps []AFSSnapshot
	if since < totalSeq {
		newSnaps = make([]AFSSnapshot, totalSeq-since)
		copy(newSnaps, job.Snapshots[since:])
	}
	return SimJobStatus{
		JobID:          job.ID,
		Percent:        job.Percent,
		Message:        job.Message,
		Done:           job.Done,
		Error:          job.Error,
		Result:         job.Result,
		LatestSnapshot: snapCopy,
		Snapshots:      newSnaps,
		SnapshotSeq:    totalSeq,
	}, true
}

// updateSimulationJobSnapshot stores the latest per-generation AFS snapshot
// for the tracked strategy. Called concurrently from the worker pool, so it
// acquires the same mutex as updateSimulationJob. The pointer holds a copy
// to avoid aliasing with the caller's stack-allocated snapshot.
// v0.7.6 live histogram.
func updateSimulationJobSnapshot(id string, snap AFSSnapshot) {
	simJobStore.Lock()
	defer simJobStore.Unlock()
	job := simJobStore.Jobs[id]
	if job == nil {
		return
	}
	s := snap
	job.LatestSnapshot = &s
	// v0.7.13: append to history so /api/simulate/status?since= can replay
	// missed frames. Maximum generations is 200 (UI cap), so the slice stays
	// small.
	job.Snapshots = append(job.Snapshots, s)
	job.UpdatedAt = time.Now()
}

func cleanupSimulationJobsLocked(now time.Time) {
	for id, job := range simJobStore.Jobs {
		if now.Sub(job.UpdatedAt) > 30*time.Minute {
			delete(simJobStore.Jobs, id)
		}
	}
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func runSimulation(req SimRequest) (SimResponse, error) {
	return runSimulationWithCallbacks(req, nil, nil)
}

func runSimulationWithProgress(req SimRequest, progress progressFunc) (SimResponse, error) {
	return runSimulationWithCallbacks(req, progress, nil)
}

// runSimulationWithCallbacks is the v0.7.6 entry point that adds an optional
// AFS snapshot callback alongside the existing progress callback. The snapshot
// callback is invoked at most once per generation, from a worker goroutine,
// for ONE chosen strategy (preferring "balanced") and ONE replicate (index 0).
// Both callbacks may be nil. The sync /api/simulate path passes nil for both.
func runSimulationWithCallbacks(req SimRequest, progress progressFunc, snapshot snapshotFunc) (SimResponse, error) {
	reportProgress(progress, 1, "normalizing request")
	normalizeRequest(&req)
	// v0.7.21 — Issue 18. Multi-trait branch. When req.Traits is set, the
	// run uses runMultiTraitSimulation; the single-trait path below is
	// untouched (bit-identical backward compatibility for existing payloads).
	if len(req.Traits) > 0 {
		return runMultiTraitSimulation(req, progress, snapshot)
	}
	strategies := buildStrategyConfigs(req)
	if err := validateRequest(req, len(strategies)); err != nil {
		return SimResponse{}, err
	}
	rng := rand.New(rand.NewSource(req.Seed))

	var initial []organism
	var datasetMeta *loadedDataset
	switch {
	case req.Upload != "":
		// v0.7.26 — Issue 05. Upload path: founder population comes
		// from an in-memory upload. Takes precedence over Dataset.
		up, ok := getUpload(req.Upload)
		if !ok {
			return SimResponse{}, fmt.Errorf("upload %q not found or expired (uploads live for %s)", req.Upload, uploadTTL)
		}
		if up.Genotype == nil || up.Genotype.dataset == nil {
			return SimResponse{}, fmt.Errorf("upload %q has no genotype", req.Upload)
		}
		ds := subsampleDataset(up.Genotype.dataset, req.PopulationSize, req.Markers, rng)
		req.PopulationSize = len(ds.individuals)
		req.Markers = ds.markerCount
		datasetMeta = ds
		initial = clonePopulation(ds.individuals)
		reportProgress(progress, 3, fmt.Sprintf("loaded upload %s: N=%d, markers=%d (ephemeral, in-memory)",
			req.Upload, req.PopulationSize, req.Markers))
	case datasetSelected(req.Dataset):
		ds, err := loadDataset(req.Dataset)
		if err != nil {
			return SimResponse{}, fmt.Errorf("load dataset %q: %w", req.Dataset, err)
		}
		ds = subsampleDataset(ds, req.PopulationSize, req.Markers, rng)
		req.PopulationSize = len(ds.individuals)
		req.Markers = ds.markerCount
		datasetMeta = ds
		initial = clonePopulation(ds.individuals)
		reportProgress(progress, 3, fmt.Sprintf("loaded dataset %s: N=%d, markers=%d%s",
			req.Dataset, req.PopulationSize, req.Markers, placeholderTag(ds)))
	default:
		initial = makeInitialPopulation(req.PopulationSize, req.Markers, rng)
	}

	effects := makeEffects(req.Markers, req.QTLCount, rng)
	baseFreq := alleleFreq(initial, req.Markers)
	baseDiversity := diversityFromFreq(baseFreq)
	baseMean := meanGeneticValue(initial, effects)
	rareUsefulAtStart := rareUsefulLoci(baseFreq, effects)
	candidates := rankEditCandidates(baseFreq, effects, req.CrisprEdits, baseDiversity)
	// v0.7.29 — Issue 08. Patch the imported-GEBV slice onto the
	// imported_gebv strategy entry (if present). Computed once from
	// the upload's accession IDs; aligned with the gen-0 population.
	if req.Upload != "" && datasetMeta != nil && len(datasetMeta.accessionIDs) > 0 {
		gen0 := gen0GEBVOverride(req.Upload, datasetMeta.accessionIDs)
		if gen0 != nil {
			for i := range strategies {
				if strategies[i].UseImportedGEBV {
					strategies[i].Gen0GEBV = gen0
				}
			}
		}
	}
	reportProgress(progress, 5, "initial population, candidate edits, and strategy set ready")
	results := simulateStrategiesDecisionEngine(req, strategies, initial, effects, baseFreq, baseDiversity, baseMean, rareUsefulAtStart, candidates, progress, snapshot)
	annotateDecisionScores(results)
	annotateFeasibility(req, results, baseDiversity)
	decision := buildDecisionSummary(req, results, baseDiversity)
	// v0.7.18 — Issue 13/15. Classify the planned edit set under the EU NGT
	// regulation. Only attach when the user actually planned edits; otherwise
	// the field is omitted from the JSON (omitempty).
	if req.CrisprEnabled && req.CrisprEdits > 0 && len(candidates) > 0 {
		c := ClassifyEditSet(candidates, req.NGT)
		decision.NGT = &c
	}
	// v0.7.27 — Issue 07. Aggregate edit-vs-cross-vs-wait counts over
	// the per-candidate classifications produced by rankEditCandidates.
	decision.EditDecisions = summarizeEditDecisions(candidates)
	reportProgress(progress, 98, "building response")
	return SimResponse{Request: req, Decision: decision, Strategies: results, CandidateEdits: candidates, Notes: buildNotes(req, len(strategies), baseDiversity, datasetMeta)}, nil
}

func datasetSelected(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "synthetic":
		return false
	}
	return true
}

func placeholderTag(ds *loadedDataset) string {
	if ds != nil && ds.isPlaceholder {
		return " [PLACEHOLDER fixture]"
	}
	return ""
}

func buildNotes(req SimRequest, strategyCount int, baseDiversity float64, datasetMeta *loadedDataset) []string {
	workers := effectiveWorkerCount(req.WorkerCount, strategyCount*req.Replicates)
	notes := []string{
		"This MVP is a decision-layer simulator, not a wet-lab protocol and not a CRISPR guide/off-target design tool.",
		"The CRISPR part is intentionally minimal: it shows how candidate edits can be prioritized and injected into strategy simulation without providing laboratory instructions.",
		fmt.Sprintf("The engine runs %d strategies × %d replicates = %d simulation jobs through a worker pool of %d workers.", strategyCount, req.Replicates, strategyCount*req.Replicates, workers),
		fmt.Sprintf("Risk thresholds: inbreeding breach ≥ %.2f; diversity collapse means diversity loss ≥ %.2f relative to baseline diversity %.4f.", req.InbreedingLimit, req.DiversityLossLimit, baseDiversity),
	}
	if req.Upload != "" {
		notes = append(notes, fmt.Sprintf("Founder population came from upload %s (ephemeral, in-memory; not persisted). EARLY IMPORT path — not production integration. Phenotype, pedigree, and candidate-edit tables, if uploaded, are surfaced in the upload summary but NOT consumed by this run.", req.Upload))
	}
	if datasetMeta != nil {
		switch {
		case datasetMeta.isPlaceholder:
			notes = append(notes, fmt.Sprintf("⚠ Dataset '%s' is the embedded PLACEHOLDER fixture (%s) — a synthetic random matrix in the BreedOS founder-CSV format. Run tools/data/fetch_arabidopsis_1001.py to fetch real data; deploy_breedos.sh will upload it to the server alongside the binary.", req.Dataset, datasetMeta.sourceFile))
		case datasetMeta.external:
			notes = append(notes, fmt.Sprintf("Founder population loaded from external file %s (%d accessions × %d markers). Selection, recombination, and mutation remain synthetic for this run.", datasetMeta.sourceFile, len(datasetMeta.individuals), datasetMeta.markerCount))
		default:
			notes = append(notes, fmt.Sprintf("Founder population loaded from embedded dataset %s (%d accessions × %d markers). Selection, recombination, and mutation remain synthetic for this run.", datasetMeta.sourceFile, len(datasetMeta.individuals), datasetMeta.markerCount))
		}
	}
	if req.StrategySet == "advanced" {
		notes = append(notes, "Advanced strategy set enabled: includes neutral/random baselines, phenotype/genomic selection mockups, OCS-like diversity constraint, cross planning, and edit-aware introgression.")
	} else {
		notes = append(notes, "Core strategy set enabled: includes a neutral drift baseline plus aggressive, diversity-preserving, balanced, and CRISPR-aware balanced strategies when CRISPR is enabled.")
	}
	// v0.7.27 — Issue 07. Edit-vs-cross-vs-wait headline. Only added
	// when the operator actually planned edits — otherwise the
	// summary headline is "no candidates ranked" which is noisy.
	if req.CrisprEnabled && req.CrisprEdits > 0 {
		notes = append(notes, "Edit-vs-cross-vs-wait classifier (Issue 07): each candidate edit is classified by effect size, current allele frequency, and population diversity. EDIT = large-effect rare alleles where selection is too slow; CROSS = allele already segregating; WAIT = marginal effect, near fixation, or bottleneck risk. See `decision.edit_decisions` for the run's mix.")
	}
	if req.PopulationSize < 50 {
		notes = append(notes, "Small-population mode is enabled: N < 50 is intentionally allowed to expose stochastic drift, rapid fixation, and founder effects.")
	}
	if req.PopulationSize < 10 {
		notes = append(notes, "N < 10 is a stress-test regime, not a realistic breeding program. It is useful for showing why population size and diversity constraints matter.")
	}
	baseParents := int(math.Round(float64(req.PopulationSize) * req.SelectionPercent / 100.0))
	if baseParents < 2 {
		notes = append(notes, "Selection intensity would yield fewer than two parents; the simulator floors the selected parent pool to two when possible.")
	}
	expectedMutations := float64(req.PopulationSize) * float64(req.Markers) * 2 * float64(req.Generations) * req.MutationRate
	if req.MutationRate == 0 {
		notes = append(notes, "Mutation rate is 0: no de novo mutations are injected; changes come only from segregation, drift, and selection.")
	} else {
		notes = append(notes, fmt.Sprintf("Mutation rate %.8g means roughly %.1f expected allele flips per strategy replicate across the whole run before selection effects.", req.MutationRate, expectedMutations))
		if expectedMutations < 1 {
			notes = append(notes, "Mutation effect may be visually invisible: expected mutation count is below one per strategy replicate. Increase generations, markers, population size, or mutation rate to see it.")
		}
	}
	if simulationBudget(req, strategyCount) > 300000000 {
		notes = append(notes, "Large simulation: v0.7.16 caps the budget at 1.5B cells (per run AND sum-over-sweep) and uses a worker pool. Production BreedOS should move heavy runs to durable queued workers.")
	}
	return notes
}
func reportProgress(progress progressFunc, percent int, message string) {
	if progress != nil {
		progress(percent, message)
	}
}

func buildStrategyConfigs(req SimRequest) []strategyConfig {
	strategies := []strategyConfig{
		{Name: "Neutral drift baseline", Code: "neutral", Summary: "No intentional selection. This baseline isolates drift, fixation, and mutation effects.", Rule: "no_selection", MatingRule: "random", ParentMultiplier: 1.0},
		{Name: "Aggressive selection", Code: "aggressive", Summary: "Maximizes short-term trait gain. Useful as a warning baseline: fast progress can consume diversity and future optionality.", Rule: "phenotype", MatingRule: "random", TraitWeight: 1.0, ParentMultiplier: 0.75},
		{Name: "Diversity-preserving selection", Code: "diversity", Summary: "Keeps more genetically unusual candidates in the parent pool. Slower gain, lower bottleneck risk.", Rule: "score", MatingRule: "random", TraitWeight: 0.55, NoveltyWeight: 1.25, ParentMultiplier: 2.25},
		{Name: "Balanced strategy", Code: "balanced", Summary: "Trades off near-term gain and long-term genetic optionality. This is the default BreedOS posture.", Rule: "score", MatingRule: "random", TraitWeight: 0.85, NoveltyWeight: 0.50, ParentMultiplier: 1.60},
	}
	if req.CrisprEnabled && req.CrisprEdits > 0 {
		strategies = append(strategies, strategyConfig{Name: "Balanced + CRISPR seed", Code: "balanced_crispr", Summary: "Demonstrates edit-aware integration: prioritize beneficial loci, seed them into part of the founding population, then run balanced selection.", Rule: "score", MatingRule: "random", TraitWeight: 0.85, NoveltyWeight: 0.50, ParentMultiplier: 1.60, UseCrisprSeed: true})
	}
	if req.StrategySet != "advanced" {
		return strategies
	}
	strategies = append(strategies,
		strategyConfig{Name: "Random parent baseline", Code: "random", Summary: "Randomly selects the configured parent fraction. Useful to separate selection value from drift noise.", Rule: "random", MatingRule: "random", ParentMultiplier: 1.0},
		strategyConfig{Name: "Phenotype truncation selection", Code: "phenotype", Summary: "Classic truncation selection on noisy phenotype. Strong baseline for practical breeding programs.", Rule: "phenotype", MatingRule: "random", TraitWeight: 1.0, ParentMultiplier: 1.0},
		strategyConfig{Name: "Genomic selection mock", Code: "genomic", Summary: "Selects by a simplified predicted breeding value. In production this is where GBLUP/Bayesian/ML models plug in.", Rule: "genomic", MatingRule: "random", TraitWeight: 1.0, ParentMultiplier: 1.0},
		strategyConfig{Name: "OCS-like constrained selection", Code: "ocs_like", Summary: "Approximates optimum contribution selection: gain is pursued under a similarity/diversity penalty.", Rule: "ocs", MatingRule: "random", TraitWeight: 0.90, NoveltyWeight: 0.25, SimilarityPenalty: 0.85, ParentMultiplier: 1.80},
		strategyConfig{Name: "Cross planner", Code: "cross_planner", Summary: "Not only who to keep, but who to cross. Uses balanced parent choice plus more distant mating pairs.", Rule: "score", MatingRule: "diverse_pairs", TraitWeight: 0.82, NoveltyWeight: 0.60, ParentMultiplier: 1.80},
	)
	if req.CrisprEnabled && req.CrisprEdits > 0 {
		strategies = append(strategies, strategyConfig{Name: "Edit-aware introgression planner", Code: "edit_introgression", Summary: "Seeds lower-risk candidate edits, then spreads them through diversity-aware parent choice and diverse mate allocation.", Rule: "ocs", MatingRule: "diverse_pairs", TraitWeight: 0.88, NoveltyWeight: 0.35, SimilarityPenalty: 0.75, ParentMultiplier: 1.90, UseCrisprSeed: true, ConservativeEdits: true})
	}
	// v0.7.29 — Issue 08. Imported-GEBV selection strategy.
	// Available only when the operator uploaded a predictions table
	// in `req.Upload`. Surfaced in the advanced set only.
	if uploadHasPredictions(req.Upload) {
		strategies = append(strategies, strategyConfig{
			Name:             "Imported GEBV selection",
			Code:             "imported_gebv",
			Summary:          "Selects gen-0 parents by externally-imported breeding values (rrBLUP / BGLR / sommer / AlphaSimR). From gen 1 onwards reverts to internal genomic scoring because offspring identities are not tracked through reproduction.",
			Rule:             "imported_gebv",
			MatingRule:       "random",
			TraitWeight:      1.0,
			ParentMultiplier: 1.0,
			UseImportedGEBV:  true,
		})
	}
	// v0.7.28 — Issue 30. Ancestral-allele introgression strategy.
	// Available only when the operator enables the advanced strategy
	// set AND sets ancestral_intro_percent > 0. Lower trait baseline
	// (slower under no-stress) traded for reduced climate penalty
	// (better under stress).
	if req.AncestralIntroPercent > 0 {
		strategies = append(strategies, strategyConfig{
			Name:             "Ancestral introgression",
			Code:             "ancestral_introgression",
			Summary:          "Seeds landrace / wild-relative lines into the founder population at generation 0 and runs diversity-aware balanced selection. Ancestral lines carry better climate-stress tolerance but worse mean trait performance.",
			Rule:             "score",
			MatingRule:       "diverse_pairs",
			TraitWeight:      0.80,
			NoveltyWeight:    0.60,
			ParentMultiplier: 1.80,
			UseAncestralSeed: true,
		})
	}
	return strategies
}

type strategyTask struct {
	StrategyIndex int
	Replicate     int
	Config        strategyConfig
}
type strategyTaskResult struct {
	StrategyIndex int
	Replicate     int
	Config        strategyConfig
	Result        StrategyResult
}

func simulateStrategiesDecisionEngine(req SimRequest, strategies []strategyConfig, initial []organism, effects []float64, baseFreq []float64, baseDiversity, baseMean float64, rareUsefulAtStart []int, candidates []EditCandidate, progress progressFunc, snapshot snapshotFunc) []StrategyResult {
	strategyCount := len(strategies)
	jobCount := strategyCount * req.Replicates
	resultsByStrategy := make([][]StrategyResult, strategyCount)
	totalSteps := jobCount * req.Generations
	if totalSteps < 1 {
		totalSteps = 1
	}
	workers := effectiveWorkerCount(req.WorkerCount, jobCount)
	reportProgress(progress, 6, fmt.Sprintf("parallel decision engine: %d strategies × %d replicates, %d workers", strategyCount, req.Replicates, workers))
	// v0.7.6 live histogram: track AFS snapshots from ONE strategy / one replicate
	// to avoid concurrent writes from multiple workers. v0.7.8 honors an explicit
	// TrackedStrategy if it matches one of the configured strategies; otherwise
	// falls back to "balanced", otherwise to the first configured strategy.
	trackIdx := -1
	if req.TrackedStrategy != "" && strings.ToLower(req.TrackedStrategy) != "auto" {
		for i, cfg := range strategies {
			if cfg.Code == req.TrackedStrategy {
				trackIdx = i
				break
			}
		}
	}
	if trackIdx == -1 {
		trackIdx = 0
		for i, cfg := range strategies {
			if cfg.Code == "balanced" {
				trackIdx = i
				break
			}
		}
	}
	// v0.7.8: emit a generation-0 snapshot of the founder population so the
	// live histogram has data to render the instant the client starts polling
	// — no perceived startup delay before the first generation completes.
	if snapshot != nil && trackIdx >= 0 && trackIdx < len(strategies) {
		trackedCfg := strategies[trackIdx]
		snapshot(AFSSnapshot{
			Generation:       0,
			TotalGenerations: req.Generations,
			StrategyCode:     trackedCfg.Code,
			StrategyName:     trackedCfg.Name,
			Bins:             afsBinsFromPop(initial, req.Markers),
		})
	}
	tasks := make(chan strategyTask)
	out := make(chan strategyTaskResult, jobCount)
	var completedSteps int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				strategyPop := clonePopulation(initial)
				editSet := candidates
				if task.Config.ConservativeEdits {
					editSet = conservativeEditCandidates(candidates)
				}
				if task.Config.UseCrisprSeed {
					applyCrisprSeed(strategyPop, editSet, req.CrisprIntroPercent, rand.New(rand.NewSource(req.Seed+int64(900000+task.StrategyIndex*1009+task.Replicate*97))))
				}
				// v0.7.28 — Issue 30. Ancestral-allele seed at gen 0.
				// Re-rolls the last K founders with a low-favourable-
				// allele Bernoulli draw so the base trait mean is lower
				// (matching the issue's "lower mean trait performance"
				// premise). Per-strategy seed is independent of the
				// CRISPR seed so the two can co-exist (a hybrid strategy
				// could use both — out of scope for v0.9).
				if task.Config.UseAncestralSeed {
					seedAncestralLines(strategyPop, req.AncestralIntroPercent, req.Markers, rand.New(rand.NewSource(req.Seed+int64(700000+task.StrategyIndex*1013+task.Replicate*103))))
				}
				strategyRNG := rand.New(rand.NewSource(req.Seed + int64(1000+task.StrategyIndex*31337+task.Replicate*7919)))
				emitSnapshot := snapshot != nil && task.StrategyIndex == trackIdx && task.Replicate == 0
				res := simulateStrategy(req, task.Config, strategyPop, effects, baseFreq, baseDiversity, baseMean, rareUsefulAtStart, strategyRNG, func(gen int, currentPop []organism) {
					done := atomic.AddInt64(&completedSteps, 1)
					percent := 6 + int(math.Round(float64(done)*91.0/float64(totalSteps)))
					reportProgress(progress, percent, fmt.Sprintf("parallel run: %d/%d strategy-generations complete; %s replicate %d/%d generation %d/%d", done, totalSteps, task.Config.Name, task.Replicate+1, req.Replicates, gen, req.Generations))
					if emitSnapshot {
						snapshot(AFSSnapshot{
							Generation:       gen,
							TotalGenerations: req.Generations,
							StrategyCode:     task.Config.Code,
							StrategyName:     task.Config.Name,
							Bins:             afsBinsFromPop(currentPop, req.Markers),
						})
					}
				})
				out <- strategyTaskResult{StrategyIndex: task.StrategyIndex, Replicate: task.Replicate, Config: task.Config, Result: res}
			}
		}()
	}
	go func() {
		// v0.7.14: enqueue the tracked (strategy=trackIdx, replicate=0) task
		// FIRST so a worker picks it up immediately and snapshots start
		// flowing on poll #1. Without this, the tracked task is at queue
		// position (trackIdx * req.Replicates) and can sit unstarted for
		// 10+ seconds while the other strategies run — leaving the live
		// histogram stuck on the gen-0 snapshot until then.
		if trackIdx >= 0 && trackIdx < len(strategies) {
			tasks <- strategyTask{StrategyIndex: trackIdx, Replicate: 0, Config: strategies[trackIdx]}
		}
		for i, cfg := range strategies {
			for rep := 0; rep < req.Replicates; rep++ {
				if i == trackIdx && rep == 0 {
					continue
				}
				tasks <- strategyTask{StrategyIndex: i, Replicate: rep, Config: cfg}
			}
		}
		close(tasks)
		wg.Wait()
		close(out)
	}()
	for item := range out {
		resultsByStrategy[item.StrategyIndex] = append(resultsByStrategy[item.StrategyIndex], item.Result)
	}
	aggregated := make([]StrategyResult, strategyCount)
	for i, cfg := range strategies {
		aggregated[i] = aggregateReplicates(req, cfg, resultsByStrategy[i], baseDiversity)
	}
	return aggregated
}

func effectiveWorkerCount(requested, jobCount int) int {
	if jobCount < 1 {
		return 1
	}
	workers := requested
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers < 1 {
		workers = 1
	}
	if workers > 64 {
		workers = 64
	}
	if workers > jobCount {
		workers = jobCount
	}
	return workers
}
func conservativeEditCandidates(edits []EditCandidate) []EditCandidate {
	out := make([]EditCandidate, 0, len(edits))
	for _, e := range edits {
		if strings.HasPrefix(e.DiversityRisk, "low") || strings.HasPrefix(e.DiversityRisk, "medium:") {
			out = append(out, e)
		}
		if len(out) >= 3 {
			break
		}
	}
	if len(out) == 0 && len(edits) > 0 {
		out = append(out, edits[0])
	}
	return out
}

func aggregateReplicates(req SimRequest, cfg strategyConfig, reps []StrategyResult, baseDiversity float64) StrategyResult {
	if len(reps) == 0 {
		return StrategyResult{Name: cfg.Name, Code: cfg.Code, Summary: cfg.Summary}
	}
	metricCount := len(reps[0].Metrics)
	metrics := make([]MetricPoint, metricCount)
	for genIdx := 0; genIdx < metricCount; genIdx++ {
		var trait, gain, diversity, inbreeding, drift, lost, fixed, favFixed, unfavFixed, parents float64
		for _, rep := range reps {
			p := rep.Metrics[genIdx]
			trait += p.TraitMean
			gain += p.GeneticGain
			diversity += p.Diversity
			inbreeding += p.Inbreeding
			drift += p.AlleleDrift
			lost += float64(p.RareUsefulLost)
			fixed += float64(p.FixedLoci)
			favFixed += float64(p.FavorableFixed)
			unfavFixed += float64(p.UnfavorableFixed)
			parents += float64(p.EffectiveParents)
		}
		den := float64(len(reps))
		metrics[genIdx] = MetricPoint{Generation: reps[0].Metrics[genIdx].Generation, TraitMean: round4(trait / den), GeneticGain: round4(gain / den), Diversity: round4(diversity / den), Inbreeding: round4(inbreeding / den), AlleleDrift: round4(drift / den), RareUsefulLost: int(math.Round(lost / den)), FixedLoci: int(math.Round(fixed / den)), FavorableFixed: int(math.Round(favFixed / den)), UnfavorableFixed: int(math.Round(unfavFixed / den)), EffectiveParents: int(math.Round(parents / den))}
	}
	// v0.7.20 — Issue 20. Fill in Ne[t] = 1 / (2 ΔF) over the metrics slice.
	populateNeTrajectory(metrics)
	// v0.7.24 — Issue 28. Climate penalty applied uniformly across the
	// population. Scales TraitMean / GeneticGain on every MetricPoint by
	// (1 − penalty). No-op when req.Climate == nil (single-trait runs
	// without climate stress are bit-identical to v0.7.23).
	// v0.7.28 — Issue 30. Per-strategy climate-penalty discount.
	// Default 1.0 (no discount) reproduces v0.7.24 behaviour.
	applyClimatePenaltyToMetricsWithDiscount(metrics, req.Climate, climateDiscountForStrategy(cfg, req))
	gains, divs, inbr := make([]float64, 0, len(reps)), make([]float64, 0, len(reps)), make([]float64, 0, len(reps))
	rareLossEvents, inbreedingBreaches, diversityCollapses := 0, 0, 0
	for _, rep := range reps {
		f := rep.Final
		gains = append(gains, f.GeneticGain)
		divs = append(divs, f.Diversity)
		inbr = append(inbr, f.Inbreeding)
		if f.RareUsefulLost > 0 {
			rareLossEvents++
		}
		if f.Inbreeding >= req.InbreedingLimit {
			inbreedingBreaches++
		}
		diversityLoss := 0.0
		if baseDiversity > 0 {
			diversityLoss = (baseDiversity - f.Diversity) / baseDiversity
		}
		if diversityLoss >= req.DiversityLossLimit {
			diversityCollapses++
		}
	}
	finalMetric := metrics[len(metrics)-1]
	final := FinalStats{TraitMean: finalMetric.TraitMean, GeneticGain: finalMetric.GeneticGain, Diversity: finalMetric.Diversity, Inbreeding: finalMetric.Inbreeding, AlleleDrift: finalMetric.AlleleDrift, RareUsefulLost: finalMetric.RareUsefulLost, FixedLoci: finalMetric.FixedLoci, FavorableFixed: finalMetric.FavorableFixed, UnfavorableFixed: finalMetric.UnfavorableFixed, EffectiveParents: finalMetric.EffectiveParents, Replicates: len(reps), GainStd: round4(stddev(gains)), DiversityStd: round4(stddev(divs)), InbreedingStd: round4(stddev(inbr)), ProbabilityInbreedingBreach: round4(float64(inbreedingBreaches) / float64(len(reps))), ProbabilityDiversityCollapse: round4(float64(diversityCollapses) / float64(len(reps))), ProbabilityRareUsefulLoss: round4(float64(rareLossEvents) / float64(len(reps)))}
	final.RecommendedNext = recommendationFor(cfg.Code, final)
	return StrategyResult{Name: cfg.Name, Code: cfg.Code, Summary: cfg.Summary, Replicates: len(reps), Metrics: metrics, Final: final}
}

func annotateDecisionScores(results []StrategyResult) {
	if len(results) == 0 {
		return
	}
	gainMin, gainMax := minMaxStrategy(results, func(s StrategyResult) float64 { return s.Final.GeneticGain })
	divMin, divMax := minMaxStrategy(results, func(s StrategyResult) float64 { return s.Final.Diversity })
	inbMin, inbMax := minMaxStrategy(results, func(s StrategyResult) float64 { return s.Final.Inbreeding })
	riskMin, riskMax := minMaxStrategy(results, func(s StrategyResult) float64 { return combinedRisk(s.Final) })
	lostMin, lostMax := minMaxStrategy(results, func(s StrategyResult) float64 { return float64(s.Final.RareUsefulLost) })
	for i := range results {
		f := results[i].Final
		gain := norm(f.GeneticGain, gainMin, gainMax)
		div := norm(f.Diversity, divMin, divMax)
		lowInb := 1.0 - norm(f.Inbreeding, inbMin, inbMax)
		lowRisk := 1.0 - norm(combinedRisk(f), riskMin, riskMax)
		lowLost := 1.0 - norm(float64(f.RareUsefulLost), lostMin, lostMax)
		results[i].Final.RiskAdjustedScore = round4(0.40*gain + 0.22*div + 0.18*lowInb + 0.15*lowRisk + 0.05*lowLost)
	}
	for i := range results {
		pareto := true
		for j := range results {
			if i != j && dominates(results[j].Final, results[i].Final) {
				pareto = false
				break
			}
		}
		results[i].ParetoOptimal = pareto
		results[i].Final.ParetoOptimal = pareto
	}
	type ranked struct {
		idx   int
		score float64
	}
	ranks := make([]ranked, len(results))
	for i := range results {
		ranks[i] = ranked{i, results[i].Final.RiskAdjustedScore}
	}
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].score == ranks[j].score {
			return results[ranks[i].idx].Final.GeneticGain > results[ranks[j].idx].Final.GeneticGain
		}
		return ranks[i].score > ranks[j].score
	})
	for rank, item := range ranks {
		results[item.idx].Final.DecisionRank = rank + 1
	}
}
func dominates(a, b FinalStats) bool {
	betterOrEqual := a.GeneticGain >= b.GeneticGain && a.Diversity >= b.Diversity && a.Inbreeding <= b.Inbreeding && a.ProbabilityDiversityCollapse <= b.ProbabilityDiversityCollapse && a.ProbabilityRareUsefulLoss <= b.ProbabilityRareUsefulLoss
	strictlyBetter := a.GeneticGain > b.GeneticGain || a.Diversity > b.Diversity || a.Inbreeding < b.Inbreeding || a.ProbabilityDiversityCollapse < b.ProbabilityDiversityCollapse || a.ProbabilityRareUsefulLoss < b.ProbabilityRareUsefulLoss
	return betterOrEqual && strictlyBetter
}
func combinedRisk(f FinalStats) float64 {
	return 0.45*f.ProbabilityInbreedingBreach + 0.35*f.ProbabilityDiversityCollapse + 0.20*f.ProbabilityRareUsefulLoss
}

// v0.7.3 constraint engine (Issue 03).
// Each constraint field on SimRequest is "off" when zero. Evaluation compares
// the mean final outcome (FinalStats) against the user-supplied caps/floors.
// The probability-breach metrics on FinalStats remain a separate risk signal —
// they are not used as hard constraints here unless MaxCombinedRisk is set.
func evaluateConstraints(req SimRequest, f FinalStats, baseDiversity float64) (bool, []string) {
	failed := make([]string, 0)
	if req.MaxInbreeding > 0 && f.Inbreeding > req.MaxInbreeding {
		failed = append(failed, fmt.Sprintf("inbreeding %.4f > max %.4f", f.Inbreeding, req.MaxInbreeding))
	}
	if req.MaxDiversityLoss > 0 && baseDiversity > 0 {
		loss := (baseDiversity - f.Diversity) / baseDiversity
		if loss > req.MaxDiversityLoss {
			failed = append(failed, fmt.Sprintf("diversity loss %.4f > max %.4f (fraction of baseline)", loss, req.MaxDiversityLoss))
		}
	}
	if req.MaxRareUsefulLoss > 0 && f.RareUsefulLost > req.MaxRareUsefulLoss {
		failed = append(failed, fmt.Sprintf("rare-useful loci lost %d > max %d", f.RareUsefulLost, req.MaxRareUsefulLoss))
	}
	if req.MinGeneticGain > 0 && f.GeneticGain < req.MinGeneticGain {
		failed = append(failed, fmt.Sprintf("genetic gain %.4f < min %.4f", f.GeneticGain, req.MinGeneticGain))
	}
	if req.MinEffectiveParents > 0 && f.EffectiveParents < req.MinEffectiveParents {
		failed = append(failed, fmt.Sprintf("effective parents %d < min %d", f.EffectiveParents, req.MinEffectiveParents))
	}
	if req.MaxCombinedRisk > 0 && combinedRisk(f) > req.MaxCombinedRisk {
		failed = append(failed, fmt.Sprintf("combined risk %.4f > max %.4f", combinedRisk(f), req.MaxCombinedRisk))
	}
	return len(failed) == 0, failed
}

func annotateFeasibility(req SimRequest, results []StrategyResult, baseDiversity float64) {
	for i := range results {
		feasible, failed := evaluateConstraints(req, results[i].Final, baseDiversity)
		if failed == nil {
			failed = []string{}
		}
		results[i].Final.Feasible = feasible
		results[i].Final.FailedConstraints = failed
	}
}

func anyConstraintActive(req SimRequest) bool {
	return req.MaxInbreeding > 0 || req.MaxDiversityLoss > 0 || req.MaxRareUsefulLoss > 0 ||
		req.MinGeneticGain > 0 || req.MinEffectiveParents > 0 || req.MaxCombinedRisk > 0
}

func constraintsAppliedList(req SimRequest) []string {
	out := make([]string, 0, 6)
	if req.MaxInbreeding > 0 {
		out = append(out, fmt.Sprintf("max inbreeding ≤ %.4f", req.MaxInbreeding))
	}
	if req.MaxDiversityLoss > 0 {
		out = append(out, fmt.Sprintf("max diversity loss ≤ %.4f (fraction of baseline)", req.MaxDiversityLoss))
	}
	if req.MaxRareUsefulLoss > 0 {
		out = append(out, fmt.Sprintf("max rare-useful loci lost ≤ %d", req.MaxRareUsefulLoss))
	}
	if req.MinGeneticGain > 0 {
		out = append(out, fmt.Sprintf("min genetic gain ≥ %.4f", req.MinGeneticGain))
	}
	if req.MinEffectiveParents > 0 {
		out = append(out, fmt.Sprintf("min effective parents ≥ %d", req.MinEffectiveParents))
	}
	if req.MaxCombinedRisk > 0 {
		out = append(out, fmt.Sprintf("max combined risk ≤ %.4f", req.MaxCombinedRisk))
	}
	return out
}

func buildDecisionSummary(req SimRequest, results []StrategyResult, baseDiversity float64) DecisionSummary {
	if len(results) == 0 {
		return DecisionSummary{}
	}
	bestRiskIdx, bestGainIdx, lowestRiskIdx := 0, 0, 0
	for i := range results {
		if results[i].Final.RiskAdjustedScore > results[bestRiskIdx].Final.RiskAdjustedScore {
			bestRiskIdx = i
		}
		if results[i].Final.GeneticGain > results[bestGainIdx].Final.GeneticGain {
			bestGainIdx = i
		}
		if combinedRisk(results[i].Final) < combinedRisk(results[lowestRiskIdx].Final) {
			lowestRiskIdx = i
		}
	}
	pareto := make([]string, 0)
	for _, s := range results {
		if s.ParetoOptimal {
			pareto = append(pareto, s.Code)
		}
	}
	best, bestGain, lowest := results[bestRiskIdx], results[bestGainIdx], results[lowestRiskIdx]
	// v0.7.3: best feasible — top risk-adjusted score among strategies that pass user constraints.
	bestFeasibleIdx := -1
	for i := range results {
		if !results[i].Final.Feasible {
			continue
		}
		if bestFeasibleIdx == -1 || results[i].Final.RiskAdjustedScore > results[bestFeasibleIdx].Final.RiskAdjustedScore {
			bestFeasibleIdx = i
		}
	}
	constraintsApplied := constraintsAppliedList(req)
	feasibilityNote, bestFeasibleCode, bestFeasibleName := buildFeasibilityNote(req, results, bestFeasibleIdx, constraintsApplied)
	interpretation := []string{
		fmt.Sprintf("Recommended risk-adjusted strategy: %s (score %.4f, rank #%d).", best.Name, best.Final.RiskAdjustedScore, best.Final.DecisionRank),
		fmt.Sprintf("Maximum final gain is produced by %s, but compare its risk probabilities before treating it as deployable.", bestGain.Name),
		fmt.Sprintf("Lowest combined risk is produced by %s.", lowest.Name),
	}
	if len(constraintsApplied) > 0 {
		if bestFeasibleIdx >= 0 {
			interpretation = append(interpretation, fmt.Sprintf("Best strategy that satisfies your %d constraint(s): %s (risk-adjusted score %.4f).", len(constraintsApplied), bestFeasibleName, results[bestFeasibleIdx].Final.RiskAdjustedScore))
		} else {
			interpretation = append(interpretation, fmt.Sprintf("No strategy satisfies your %d constraint(s) — relax limits or expand the strategy set.", len(constraintsApplied)))
		}
	} else {
		interpretation = append(interpretation, "No hard constraints supplied — the ranking is purely risk-adjusted. Add constraints (max inbreeding, min gain, etc.) to filter to feasible strategies only.")
	}
	interpretation = append(interpretation, "Use the Pareto chart to choose a trade-off, not a single metric. Real BreedOS should optimize under explicit constraints supplied by the breeding program.")
	// v0.7.20 — Issue 21. Inbreeding-cost note. Only meaningful when the
	// recommended strategy ended with a non-trivial F. The default
	// coefficient is the Holstein-literature median (~45 kg per 1% F);
	// range 20–65 kg/F reflects measure-specific differences (FPED vs
	// FROH vs FGRM). Single-trait MVP — assumes the modelled trait IS
	// milk yield; Issue 18 (multi-trait engine) will let multi-trait runs
	// pick the right trait automatically.
	if best.Final.Inbreeding > 0.01 {
		costLo := inbreedingDepressionCostMilkKg(best.Final.Inbreeding, holsteinDepressionMilkLowKgPerF)
		costHi := inbreedingDepressionCostMilkKg(best.Final.Inbreeding, holsteinDepressionMilkHighKgPerF)
		costDefault := inbreedingDepressionCostMilkKg(best.Final.Inbreeding, holsteinDepressionMilkDefaultKgPerF)
		interpretation = append(interpretation, fmt.Sprintf(
			"Inbreeding cost (if treating the modelled trait as milk yield): the recommended strategy ended with F ≈ %.3f. Under published Holstein inbreeding-depression coefficients (range %g–%g kg of milk per 1%% F, default ≈ %g kg/F), this would drag yield by roughly %.0f kg (range %.0f–%.0f kg). Net forward gain = simulated genetic gain − inbreeding cost.",
			best.Final.Inbreeding,
			holsteinDepressionMilkLowKgPerF, holsteinDepressionMilkHighKgPerF, holsteinDepressionMilkDefaultKgPerF,
			costDefault, costLo, costHi,
		))
	}
	d := DecisionSummary{
		BestRiskAdjustedCode: best.Code,
		BestRiskAdjustedName: best.Name,
		BestGainCode:         bestGain.Code,
		LowestRiskCode:       lowest.Code,
		BestFeasibleCode:     bestFeasibleCode,
		BestFeasibleName:     bestFeasibleName,
		FeasibilityNote:      feasibilityNote,
		ConstraintsApplied:   constraintsApplied,
		ParetoCodes:          pareto,
		Interpretation:       interpretation,
	}
	d.Tradeoffs = buildTradeoffs(results, best, bestGain, lowest, pareto)
	d.AvoidStrategies = buildAvoidList(results)
	d.KeyAssumptions = buildKeyAssumptions(req)
	d.MissingDataWarnings = buildMissingDataWarnings(req, baseDiversity)
	d.NextAnalysis = buildNextAnalysis(req, results, best, bestGain, pareto)
	d.HonestyBanner = buildHonestyBanner(req)
	d.Limitations = buildLimitations(req)
	d.WhatCouldBeWrong = buildWhatCouldBeWrong(req, results, best, bestGain)
	d.SummaryText = buildSummaryText(req, d, best, bestGain, lowest)
	return d
}

func buildTradeoffs(results []StrategyResult, best, bestGain, lowest StrategyResult, pareto []string) []Tradeoff {
	out := make([]Tradeoff, 0, 3)
	if bestGain.Code != best.Code {
		out = append(out, Tradeoff{
			A: bestGain.Code, B: best.Code, Theme: "gain_vs_risk_adjusted",
			Note: fmt.Sprintf("%s delivers higher final gain (%.4f) but %s wins on risk-adjusted score (%.4f vs %.4f) — the %.4f gain difference costs %.4f extra combined risk.",
				bestGain.Name, bestGain.Final.GeneticGain, best.Name,
				best.Final.RiskAdjustedScore, bestGain.Final.RiskAdjustedScore,
				bestGain.Final.GeneticGain-best.Final.GeneticGain,
				combinedRisk(bestGain.Final)-combinedRisk(best.Final)),
		})
	}
	if lowest.Code != best.Code {
		out = append(out, Tradeoff{
			A: best.Code, B: lowest.Code, Theme: "risk_adjusted_vs_min_risk",
			Note: fmt.Sprintf("%s balances gain and risk (combined risk %.4f) whereas %s prioritises minimum risk (combined risk %.4f) at a cost of %.4f gain.",
				best.Name, combinedRisk(best.Final), lowest.Name,
				combinedRisk(lowest.Final), best.Final.GeneticGain-lowest.Final.GeneticGain),
		})
	}
	if len(pareto) >= 2 {
		a, b := findByCode(results, pareto[0]), findByCode(results, pareto[1])
		if a != nil && b != nil {
			out = append(out, Tradeoff{
				A: a.Code, B: b.Code, Theme: "pareto_pair",
				Note: fmt.Sprintf("%s and %s are both Pareto-optimal — neither dominates. Pick by priority: %s gives gain %.4f / risk %.4f, %s gives gain %.4f / risk %.4f.",
					a.Name, b.Name, a.Name, a.Final.GeneticGain, combinedRisk(a.Final),
					b.Name, b.Final.GeneticGain, combinedRisk(b.Final)),
			})
		}
	} else {
		aggr, divers := findByCode(results, "aggressive"), findByCode(results, "diversity")
		if aggr != nil && divers != nil {
			out = append(out, Tradeoff{
				A: aggr.Code, B: divers.Code, Theme: "aggressive_vs_diversity",
				Note: fmt.Sprintf("%s pursues fast gain (%.4f) but burns diversity (collapse probability %.4f); %s preserves diversity (collapse probability %.4f) at slower gain (%.4f).",
					aggr.Name, aggr.Final.GeneticGain, aggr.Final.ProbabilityDiversityCollapse,
					divers.Name, divers.Final.ProbabilityDiversityCollapse, divers.Final.GeneticGain),
			})
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func buildAvoidList(results []StrategyResult) []AvoidEntry {
	out := make([]AvoidEntry, 0)
	for _, s := range results {
		risk := combinedRisk(s.Final)
		if risk < 0.5 || s.ParetoOptimal {
			continue
		}
		reasons := make([]string, 0, 3)
		if s.Final.ProbabilityInbreedingBreach >= 0.5 {
			reasons = append(reasons, fmt.Sprintf("inbreeding-breach probability %.2f", s.Final.ProbabilityInbreedingBreach))
		}
		if s.Final.ProbabilityDiversityCollapse >= 0.5 {
			reasons = append(reasons, fmt.Sprintf("diversity-collapse probability %.2f", s.Final.ProbabilityDiversityCollapse))
		}
		if s.Final.ProbabilityRareUsefulLoss >= 0.5 {
			reasons = append(reasons, fmt.Sprintf("rare-allele-loss probability %.2f", s.Final.ProbabilityRareUsefulLoss))
		}
		if len(reasons) == 0 {
			reasons = append(reasons, fmt.Sprintf("combined risk %.2f", risk))
		}
		out = append(out, AvoidEntry{
			Code: s.Code, Name: s.Name,
			Reason: fmt.Sprintf("Not Pareto-optimal and %s.", strings.Join(reasons, "; ")),
		})
	}
	return out
}

func buildKeyAssumptions(req SimRequest) []string {
	founderStmt := "Synthetic population; no real genotype or phenotype data ingested."
	if datasetSelected(req.Dataset) {
		founderStmt = fmt.Sprintf("Founder genotypes loaded from real-data dataset '%s'; subsequent generations (selection, recombination, mutation) are still simulated.", req.Dataset)
	}
	out := []string{
		founderStmt,
		"Mock genomic-selection signal; production genomic prediction (GBLUP/Bayesian/ML) not yet integrated.",
		"Additive trait architecture; no dominance or epistasis modelled.",
		fmt.Sprintf("Heritability h² = %.2f and selection percent = %g%% applied uniformly across generations.", req.Heritability, req.SelectionPercent),
		fmt.Sprintf("Risk thresholds: inbreeding ≥ %.2f flagged as breach; diversity loss ≥ %.2f relative to baseline flagged as collapse.", req.InbreedingLimit, req.DiversityLossLimit),
		"Discrete non-overlapping generations; cohort-based selection.",
	}
	if req.CrisprEnabled && req.CrisprEdits > 0 {
		out = append(out, fmt.Sprintf("CRISPR seed: %d candidate edits introduced into %g%% of the founding population.", req.CrisprEdits, req.CrisprIntroPercent))
	}
	return out
}

func buildMissingDataWarnings(req SimRequest, baseDiversity float64) []string {
	out := make([]string, 0)
	if req.Replicates < 5 {
		out = append(out, fmt.Sprintf("Only %d Monte Carlo replicates — uncertainty is wide; consider rerunning with ≥10 for deployable decisions.", req.Replicates))
	}
	if req.PopulationSize < 50 {
		out = append(out, fmt.Sprintf("Small population (N=%d) amplifies drift; outcomes may be dominated by stochastic sampling rather than selection.", req.PopulationSize))
	}
	if req.PopulationSize < 10 {
		out = append(out, "N<10 is a stress-test regime, not a realistic breeding program.")
	}
	if req.Heritability < 0.1 {
		out = append(out, fmt.Sprintf("Very low heritability (h²=%.2f) — selection response will be small and noisy.", req.Heritability))
	}
	if req.Heritability > 0.9 {
		out = append(out, fmt.Sprintf("Very high heritability (h²=%.2f) — uncommon for complex agricultural traits.", req.Heritability))
	}
	if req.MutationRate == 0 {
		out = append(out, "Mutation rate is 0 — no new variation introduced; long-term response may plateau due to fixation.")
	}
	if req.Generations < 5 {
		out = append(out, fmt.Sprintf("Short horizon (%d generations); long-term diversity loss may not have surfaced yet.", req.Generations))
	}
	if baseDiversity < 0.1 {
		out = append(out, fmt.Sprintf("Starting diversity is low (%.4f); founder population may be near a bottleneck before any selection is applied.", baseDiversity))
	}
	return out
}

func buildFeasibilityNote(req SimRequest, results []StrategyResult, bestFeasibleIdx int, constraintsApplied []string) (note, code, name string) {
	if len(constraintsApplied) == 0 {
		return "No hard constraints were applied. All strategies treated as feasible; ranking is risk-adjusted only.", "", ""
	}
	feasibleCount := 0
	for _, r := range results {
		if r.Final.Feasible {
			feasibleCount++
		}
	}
	if bestFeasibleIdx >= 0 && feasibleCount > 0 {
		best := results[bestFeasibleIdx]
		return fmt.Sprintf("%d of %d strategies satisfy your constraints (%s). Best feasible: %s (risk-adjusted score %.4f, rank #%d).",
			feasibleCount, len(results), strings.Join(constraintsApplied, "; "), best.Name, best.Final.RiskAdjustedScore, best.Final.DecisionRank), best.Code, best.Name
	}
	// No feasible strategies — explain binding constraints by counting which fail most often.
	failCount := make(map[string]int)
	for _, r := range results {
		for _, fc := range r.Final.FailedConstraints {
			// FailedConstraints contain values; classify by the leading metric keyword.
			label := classifyFailure(fc)
			failCount[label]++
		}
	}
	binding := mostBinding(failCount, len(results))
	if binding == "" {
		return fmt.Sprintf("No strategy satisfies your %d constraint(s) — consider relaxing limits, expanding the strategy set, or increasing replicates to reduce noise.", len(constraintsApplied)), "", ""
	}
	return fmt.Sprintf("No strategy satisfies your %d constraint(s). Binding limit appears to be %s — relax that first, or expand the strategy set / increase replicates.", len(constraintsApplied), binding), "", ""
}

func classifyFailure(msg string) string {
	switch {
	case strings.HasPrefix(msg, "inbreeding "):
		return "max inbreeding"
	case strings.HasPrefix(msg, "diversity loss "):
		return "max diversity loss"
	case strings.HasPrefix(msg, "rare-useful "):
		return "max rare-useful loci lost"
	case strings.HasPrefix(msg, "genetic gain "):
		return "min genetic gain"
	case strings.HasPrefix(msg, "effective parents "):
		return "min effective parents"
	case strings.HasPrefix(msg, "combined risk "):
		return "max combined risk"
	}
	return msg
}

func mostBinding(counts map[string]int, total int) string {
	bestK, bestV := "", 0
	for k, v := range counts {
		if v > bestV {
			bestK, bestV = k, v
		}
	}
	if total == 0 || bestV == 0 {
		return ""
	}
	return bestK
}

func buildHonestyBanner(req SimRequest) string {
	first := "Decision-layer simulator on synthetic data"
	if datasetSelected(req.Dataset) {
		first = fmt.Sprintf("Decision-layer simulator on real founder genotypes (%s) with synthetic selection/recombination/mutation", req.Dataset)
	}
	parts := []string{first}
	if req.CrisprEnabled {
		parts = append(parts, "minimal CRISPR demo (not guide design, not wet-lab protocol)")
	}
	parts = append(parts, "not a deployable recommendation without your own genotype/phenotype data and domain review")
	return strings.Join(parts, " — ") + "."
}

func buildLimitations(req SimRequest) []string {
	germplasmStmt := "No real germplasm, pedigree, or field-trial data ingested — every run uses a generated synthetic population."
	if datasetSelected(req.Dataset) {
		germplasmStmt = "Real founder genotypes are loaded for the starting generation, but selection / recombination / mutation in subsequent generations are still simulated — there is no real field-trial or pedigree data driving the trajectory."
	}
	out := []string{
		"Diploid biallelic markers; no copy-number variation, no structural variants.",
		"Simplified Mendelian inheritance; recombination map is uniform, no chromosome structure.",
		"Additive trait architecture only; no dominance, no epistasis, no pleiotropy.",
		"No genotype-by-environment (GxE) interaction; trait expression treated as environment-invariant.",
		"No production genomic-prediction model (GBLUP / Bayesian / ML); the predictor is a mock signal for demonstration only.",
		germplasmStmt,
		"Risk thresholds are user-set, not learned from program history.",
	}
	if req.CrisprEnabled {
		out = append(out,
			"CRISPR layer ranks candidate loci by expected gain / allele frequency / diversity risk — it does not design guide RNAs, score off-targets, or model regulatory feasibility.",
			"Edits are propagated through simulation; biological validation is out of scope.",
		)
	}
	return out
}

func buildWhatCouldBeWrong(req SimRequest, results []StrategyResult, best, bestGain StrategyResult) []string {
	out := make([]string, 0, 6)
	if best.Code != bestGain.Code {
		gap := bestGain.Final.GeneticGain - best.Final.GeneticGain
		out = append(out, fmt.Sprintf("Risk-adjusted leader '%s' is not the max-gain strategy. If your program tolerates higher inbreeding-breach probability than the %.2f threshold used here, '%s' (+%.4f gain) may be the right call instead.",
			best.Name, req.InbreedingLimit, bestGain.Name, gap))
	}
	if req.Replicates < 10 {
		out = append(out, fmt.Sprintf("With only %d Monte Carlo replicates, the strategy ranking may flip under a different random seed. Re-run with ≥10 replicates before treating this as a stable recommendation.", req.Replicates))
	}
	if req.PopulationSize < 100 {
		out = append(out, fmt.Sprintf("Small population (N=%d) means drift dominates many outcomes — real programs with N≥500 will see lower variance and the ranking may shift.", req.PopulationSize))
	}
	out = append(out,
		"If real heritability differs substantially from the assumed h²="+fmt.Sprintf("%.2f", req.Heritability)+", selection response and risk both scale — re-run with the heritability you actually estimate for the target trait.",
		"If the trait has dominance, epistasis, or pleiotropy (this MVP assumes additivity), gains may not stack as predicted and edit-driven changes may carry correlated trait effects.",
		"If population substructure (subpopulations, family clusters) is present and not modelled, effective population size is lower than nominal and diversity-loss risk is underestimated.",
		"If your selection intensity cannot be maintained in practice (logistics, budget, field capacity), the realised gain trajectory will be slower than simulated.",
	)
	if req.CrisprEnabled {
		out = append(out,
			"If candidate edits have off-target effects or regulatory hurdles (not modelled here), the CRISPR-enabled strategy may not be deployable even when it dominates in simulation.",
		)
	}
	return out
}

func buildNextAnalysis(req SimRequest, results []StrategyResult, best, bestGain StrategyResult, pareto []string) string {
	if best.Code == bestGain.Code {
		return fmt.Sprintf("Strategy '%s' wins on both maximum gain and risk-adjusted score in this run. Confirm robustness by varying random seed across 3-5 runs before treating as deployable.", best.Name)
	}
	if len(pareto) >= 3 {
		return fmt.Sprintf("%d strategies are Pareto-optimal — the recommendation is sensitive to weighting. Consider tighter constraints (lower inbreeding limit, lower diversity-loss limit) to narrow the set.", len(pareto))
	}
	if combinedRisk(bestGain.Final) >= 0.5 {
		return fmt.Sprintf("Best-gain strategy '%s' has combined risk %.2f. Either accept the risk-adjusted leader as the deployable choice or explore narrower selection intensity / added diversity constraints.", bestGain.Name, combinedRisk(bestGain.Final))
	}
	if req.Replicates < 10 {
		return fmt.Sprintf("Recommendation is consistent in this run; confirm by re-running with replicates=%d (≥10) to tighten uncertainty intervals.", req.Replicates+5)
	}
	return "Confirm by re-running with a different random seed and verify strategy ranking remains stable. If stable, sweep selection_percent ±5% to inspect sensitivity."
}

func buildSummaryText(req SimRequest, d DecisionSummary, best, bestGain, lowest StrategyResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "BreedOS decision report (synthetic; N=%d, replicates=%d, generations=%d). ",
		req.PopulationSize, req.Replicates, req.Generations)
	fmt.Fprintf(&b, "Recommended (risk-adjusted): %s (score %.4f, rank #%d). ", best.Name, best.Final.RiskAdjustedScore, best.Final.DecisionRank)
	fmt.Fprintf(&b, "Max gain: %s (%.4f). Lowest risk: %s (combined risk %.4f). ", bestGain.Name, bestGain.Final.GeneticGain, lowest.Name, combinedRisk(lowest.Final))
	if len(d.ConstraintsApplied) > 0 {
		if d.BestFeasibleCode != "" {
			fmt.Fprintf(&b, "Best feasible (under %d constraint(s)): %s. ", len(d.ConstraintsApplied), d.BestFeasibleName)
		} else {
			fmt.Fprintf(&b, "No strategy satisfies the %d user constraint(s). ", len(d.ConstraintsApplied))
		}
	}
	if len(d.ParetoCodes) > 0 {
		fmt.Fprintf(&b, "Pareto-optimal: %s. ", strings.Join(d.ParetoCodes, ", "))
	}
	if len(d.Tradeoffs) > 0 {
		fmt.Fprintf(&b, "Key trade-off — %s. ", d.Tradeoffs[0].Note)
	}
	if len(d.AvoidStrategies) > 0 {
		names := make([]string, 0, len(d.AvoidStrategies))
		for _, a := range d.AvoidStrategies {
			names = append(names, a.Name)
		}
		fmt.Fprintf(&b, "Avoid: %s. ", strings.Join(names, ", "))
	}
	if len(d.MissingDataWarnings) > 0 {
		fmt.Fprintf(&b, "Caveats: %s. ", d.MissingDataWarnings[0])
	}
	fmt.Fprintf(&b, "Next: %s", d.NextAnalysis)
	return b.String()
}

func findByCode(results []StrategyResult, code string) *StrategyResult {
	for i := range results {
		if results[i].Code == code {
			return &results[i]
		}
	}
	return nil
}
func minMaxStrategy(results []StrategyResult, fn func(StrategyResult) float64) (float64, float64) {
	minV, maxV := math.Inf(1), math.Inf(-1)
	for _, s := range results {
		v := fn(s)
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	return minV, maxV
}
func norm(v, minV, maxV float64) float64 {
	if math.Abs(maxV-minV) < 1e-12 {
		return 0.5
	}
	x := (v - minV) / (maxV - minV)
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func normalizeRequest(req *SimRequest) {
	if req.Seed == 0 {
		req.Seed = time.Now().UnixNano() % 1000000000
	}
	if req.PopulationSize == 0 {
		req.PopulationSize = 400
	}
	if req.Markers == 0 {
		req.Markers = 900
	}
	if req.QTLCount == 0 {
		req.QTLCount = 45
	}
	if req.Generations == 0 {
		req.Generations = 30
	}
	if req.SelectionPercent == 0 {
		req.SelectionPercent = 10
	}
	if req.Heritability == 0 {
		req.Heritability = 0.4
	}
	if req.StrategySet == "" {
		req.StrategySet = "core"
	}
	req.StrategySet = strings.ToLower(strings.TrimSpace(req.StrategySet))
	if req.Replicates == 0 {
		req.Replicates = 3
	}
	if req.InbreedingLimit == 0 {
		req.InbreedingLimit = 0.25
	}
	if req.DiversityLossLimit == 0 {
		req.DiversityLossLimit = 0.30
	}
}
func validateRequest(req SimRequest, strategyCount int) error {
	if req.PopulationSize < 2 || req.PopulationSize > 5000 {
		return errors.New("population_size must be between 2 and 5000")
	}
	if req.Markers < 10 || req.Markers > 5000 {
		return errors.New("markers must be between 10 and 5000")
	}
	if req.QTLCount < 1 || req.QTLCount > req.Markers {
		return errors.New("qtl_count must be between 1 and markers")
	}
	if req.Generations < 1 || req.Generations > 200 {
		return errors.New("generations must be between 1 and 200")
	}
	if req.SelectionPercent <= 0 || req.SelectionPercent > 100 {
		return errors.New("selection_percent must be in (0, 100]")
	}
	if req.Heritability <= 0 || req.Heritability > 1 {
		return errors.New("heritability must be in (0, 1]")
	}
	if req.MutationRate < 0 || req.MutationRate > 0.01 {
		return errors.New("mutation_rate must be between 0 and 0.01")
	}
	if req.CrisprEdits < 0 || req.CrisprEdits > 20 {
		return errors.New("crispr_edits must be between 0 and 20")
	}
	if req.CrisprIntroPercent < 0 || req.CrisprIntroPercent > 100 {
		return errors.New("crispr_intro_percent must be between 0 and 100")
	}
	// v0.7.28 — Issue 30. Hard cap on ancestral intro percent. The
	// cap mirrors the issue's "max 25%" — the model would not be
	// honest beyond that point because the strategy-level discount is
	// a population-mean approximation, not per-individual tracking.
	if req.AncestralIntroPercent < 0 || req.AncestralIntroPercent > AncestralIntroPercentMax {
		return fmt.Errorf("ancestral_intro_percent must be between 0 and %.0f", AncestralIntroPercentMax)
	}
	if req.AncestralStressTolerance < 0 || req.AncestralStressTolerance > 1 {
		return errors.New("ancestral_stress_tolerance must be between 0 and 1")
	}
	if req.StrategySet != "core" && req.StrategySet != "advanced" {
		return errors.New("strategy_set must be core or advanced")
	}
	if req.Replicates < 1 || req.Replicates > 100 {
		return errors.New("replicates must be between 1 and 100")
	}
	if req.WorkerCount < 0 || req.WorkerCount > 64 {
		return errors.New("worker_count must be between 0 and 64; 0 means auto")
	}
	if req.InbreedingLimit <= 0 || req.InbreedingLimit > 1 {
		return errors.New("inbreeding_limit must be in (0, 1]")
	}
	if req.DiversityLossLimit <= 0 || req.DiversityLossLimit > 1 {
		return errors.New("diversity_loss_limit must be in (0, 1]")
	}
	budget := simulationBudget(req, strategyCount)
	if budget > 1500000000 {
		return fmt.Errorf("simulation budget too high: population_size * markers * (generations + 1) * strategies * replicates must be <= 1,500,000,000 for the MVP; got %d", budget)
	}
	// v0.7.24 — Issue 28. Climate scenario, if set, must reference a known
	// mode and have non-negative severity. nil = no climate stress (default).
	if req.Climate != nil {
		if err := ValidateClimateScenario(req.Climate); err != nil {
			return fmt.Errorf("invalid climate scenario: %v", err)
		}
	}
	return nil
}
func simulationBudget(req SimRequest, strategyCount int) int64 {
	return int64(req.PopulationSize) * int64(req.Markers) * int64(req.Generations+1) * int64(strategyCount) * int64(req.Replicates)
}

// v0.7.20 — Issue 20. Cap on effective population size used to keep the
// log-scale Ne chart well-behaved when the per-generation inbreeding
// increment is numerically zero (e.g. very early generations before drift
// has registered). FAO vulnerable threshold is 100; long-term-viability
// threshold is 50.
const maxEffectiveNe = 10000.0

// v0.7.20 — Issue 21. Inbreeding-depression coefficients on milk yield (kg
// per 1% F). Range and default were widened during the 2026-05-28 freshness
// audit to reflect Bjelland 2013 (NA), Doekes / Italian Holstein, and
// Canadian Holstein literature. Operator-facing copy reports the range AND
// the default so the breeder sees the uncertainty explicitly.
const (
	holsteinDepressionMilkLowKgPerF     = 20.0
	holsteinDepressionMilkHighKgPerF    = 65.0
	holsteinDepressionMilkDefaultKgPerF = 45.0
)

// inbreedingDepressionCostMilkKg returns the expected milk-yield drag in kg
// associated with a given inbreeding coefficient F (range 0..1) under the
// supplied coefficient (kg per 1% F). Always non-negative. Used by Issue 21
// to compose the inbreeding-cost note in the Decision Report.
func inbreedingDepressionCostMilkKg(F, coefficientKgPerF float64) float64 {
	if F <= 0 || coefficientKgPerF <= 0 {
		return 0
	}
	return F * 100.0 * coefficientKgPerF
}

// effectiveNeFromDeltaF computes Ne = 1 / (2 ΔF) with a sane cap when ΔF
// is non-positive or numerically tiny. Falconer & Mackay ch. 5.
func effectiveNeFromDeltaF(deltaF float64) float64 {
	if deltaF <= 1e-9 {
		return maxEffectiveNe
	}
	ne := 1.0 / (2.0 * deltaF)
	if ne > maxEffectiveNe {
		return maxEffectiveNe
	}
	return ne
}

// populateNeTrajectory fills in MetricPoint.Ne from the per-generation
// inbreeding values. Generation 0 has no preceding generation, so its Ne
// is set to maxEffectiveNe (chart entry point).
func populateNeTrajectory(metrics []MetricPoint) {
	if len(metrics) == 0 {
		return
	}
	metrics[0].Ne = maxEffectiveNe
	for i := 1; i < len(metrics); i++ {
		dF := metrics[i].Inbreeding - metrics[i-1].Inbreeding
		metrics[i].Ne = round4(effectiveNeFromDeltaF(dF))
	}
}

func makeEffects(markers, qtl int, rng *rand.Rand) []float64 {
	effects := make([]float64, markers)
	used := make(map[int]bool, qtl)
	for len(used) < qtl {
		idx := rng.Intn(markers)
		if used[idx] {
			continue
		}
		used[idx] = true
		v := rng.NormFloat64()
		if math.Abs(v) < 0.15 {
			if v < 0 {
				v = -0.15
			} else {
				v = 0.15
			}
		}
		effects[idx] = v
	}
	return effects
}
func makeInitialPopulation(n, markers int, rng *rand.Rand) []organism {
	freq := make([]float64, markers)
	for m := 0; m < markers; m++ {
		freq[m] = 0.03 + rng.Float64()*0.94
	}
	pop := make([]organism, n)
	for i := range pop {
		g := make([]uint8, markers)
		for m := 0; m < markers; m++ {
			var x uint8
			if rng.Float64() < freq[m] {
				x++
			}
			if rng.Float64() < freq[m] {
				x++
			}
			g[m] = x
		}
		pop[i] = organism{geno: g}
	}
	return pop
}
func clonePopulation(pop []organism) []organism {
	out := make([]organism, len(pop))
	for i := range pop {
		g := make([]uint8, len(pop[i].geno))
		copy(g, pop[i].geno)
		out[i] = organism{geno: g}
	}
	return out
}

func simulateStrategy(req SimRequest, cfg strategyConfig, pop []organism, effects []float64, baseFreq []float64, baseDiversity, baseMean float64, rareUsefulAtStart []int, rng *rand.Rand, progress func(gen int, pop []organism)) StrategyResult {
	metrics := make([]MetricPoint, 0, req.Generations+1)
	metrics = append(metrics, computeMetrics(0, pop, effects, baseFreq, baseDiversity, baseMean, rareUsefulAtStart, 0))
	for gen := 1; gen <= req.Generations; gen++ {
		parents := selectParents(pop, effects, req, cfg, rng, gen)
		pop = makeNextGeneration(pop, parents, req.Markers, req.MutationRate, rng, cfg.MatingRule)
		metrics = append(metrics, computeMetrics(gen, pop, effects, baseFreq, baseDiversity, baseMean, rareUsefulAtStart, len(parents)))
		if progress != nil {
			progress(gen, pop)
		}
	}
	finalMetric := metrics[len(metrics)-1]
	final := finalMetricToFinal(finalMetric)
	final.Replicates = 1
	final.RecommendedNext = recommendationFor(cfg.Code, final)
	return StrategyResult{Name: cfg.Name, Code: cfg.Code, Summary: cfg.Summary, Replicates: 1, Metrics: metrics, Final: final}
}
func finalMetricToFinal(m MetricPoint) FinalStats {
	return FinalStats{TraitMean: m.TraitMean, GeneticGain: m.GeneticGain, Diversity: m.Diversity, Inbreeding: m.Inbreeding, AlleleDrift: m.AlleleDrift, RareUsefulLost: m.RareUsefulLost, FixedLoci: m.FixedLoci, FavorableFixed: m.FavorableFixed, UnfavorableFixed: m.UnfavorableFixed, EffectiveParents: m.EffectiveParents}
}

// v0.7.29 — Issue 08. `gen` is the generation being entered (1-indexed
// for the first selection cycle). Used by the imported_gebv strategy
// to decide whether the gen-0 GEBV override is still meaningful
// (only at the first call; later calls operate on offspring whose
// ids are not in the predictions map).
func selectParents(pop []organism, effects []float64, req SimRequest, cfg strategyConfig, rng *rand.Rand, gen int) []int {
	n := len(pop)
	if cfg.Rule == "no_selection" {
		parents := make([]int, n)
		for i := range parents {
			parents[i] = i
		}
		return parents
	}
	baseCount := int(math.Round(float64(n) * req.SelectionPercent / 100.0))
	parentCount := int(math.Round(float64(baseCount) * cfg.ParentMultiplier))
	if parentCount < 2 {
		parentCount = 2
	}
	if parentCount > n {
		parentCount = n
	}
	if cfg.Rule == "random" {
		perm := rng.Perm(n)
		return append([]int(nil), perm[:parentCount]...)
	}
	freq := alleleFreq(pop, req.Markers)
	gvals := geneticValues(pop, effects)
	meanG, varG := meanVar(gvals)
	stdG := math.Sqrt(math.Max(varG, 1e-9))
	varEnv := 0.0
	if req.Heritability < 1.0 {
		varEnv = varG * (1.0 - req.Heritability) / req.Heritability
	}
	stdEnv := math.Sqrt(math.Max(varEnv, 1e-9))
	novelties := make([]float64, n)
	for i := range pop {
		var novelty float64
		for m, gv := range pop[i].geno {
			p := freq[m]
			x := float64(gv) / 2.0
			d := x - p
			novelty += d * d
		}
		novelties[i] = novelty / float64(req.Markers)
	}
	meanN, varN := meanVar(novelties)
	stdN := math.Sqrt(math.Max(varN, 1e-9))
	scored := make([]scoredIndividual, n)
	for i := range pop {
		phenotype := gvals[i] + rng.NormFloat64()*stdEnv
		zTrait := (phenotype - meanG) / stdG
		zNovelty := (novelties[i] - meanN) / stdN
		genomicPred := gvals[i]
		if cfg.Rule == "genomic" {
			accuracy := math.Sqrt(math.Max(0.01, req.Heritability))
			genomicPred = meanG + accuracy*(gvals[i]-meanG) + rng.NormFloat64()*stdG*(1.0-accuracy+0.10)
		}
		// v0.7.29 — Issue 08. Imported-GEBV selection. Only at gen 1
		// (the gen-0 selection cycle); later generations have lost
		// the id ↔ index correspondence and fall back to internal
		// genomic scoring. NaN entries also fall back per-individual.
		var importedGEBV float64
		importedAvailable := false
		if cfg.Rule == "imported_gebv" && gen == 1 && cfg.Gen0GEBV != nil && i < len(cfg.Gen0GEBV) {
			v := cfg.Gen0GEBV[i]
			if !math.IsNaN(v) {
				importedGEBV = v
				importedAvailable = true
			}
		}
		var score float64
		switch cfg.Rule {
		case "phenotype":
			score = zTrait
		case "genomic":
			score = (genomicPred - meanG) / stdG
		case "imported_gebv":
			if importedAvailable {
				// Standardise the imported GEBV against the simulator's
				// population statistics so scores live on the same scale
				// as zTrait / zNovelty. Reuses meanG/stdG (the
				// simulator's true-trait moments) as the reference
				// frame; the imported predictor is assumed to estimate
				// the same underlying breeding value.
				score = (importedGEBV - meanG) / stdG
			} else {
				// Gen ≥ 1 OR missing prediction for this individual:
				// fall back to internal genomic scoring (NOT phenotype,
				// to keep the strategy genuinely "genomic selection"
				// across the whole run).
				accuracy := math.Sqrt(math.Max(0.01, req.Heritability))
				fb := meanG + accuracy*(gvals[i]-meanG) + rng.NormFloat64()*stdG*(1.0-accuracy+0.10)
				score = (fb - meanG) / stdG
			}
		default:
			score = cfg.TraitWeight*zTrait + cfg.NoveltyWeight*zNovelty
		}
		scored[i] = scoredIndividual{idx: i, trait: phenotype, genomic: genomicPred, novelty: novelties[i], score: score}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	if cfg.Rule == "ocs" {
		return selectOCSLike(pop, scored, parentCount, cfg.SimilarityPenalty)
	}
	parents := make([]int, parentCount)
	for i := 0; i < parentCount; i++ {
		parents[i] = scored[i].idx
	}
	return parents
}
func selectOCSLike(pop []organism, scored []scoredIndividual, parentCount int, penalty float64) []int {
	if parentCount >= len(scored) {
		parents := make([]int, len(scored))
		for i := range scored {
			parents[i] = scored[i].idx
		}
		return parents
	}
	poolCount := parentCount * 5
	if poolCount < parentCount {
		poolCount = parentCount
	}
	if poolCount > len(scored) {
		poolCount = len(scored)
	}
	if poolCount > 600 {
		poolCount = 600
	}
	selected := []int{scored[0].idx}
	used := map[int]bool{scored[0].idx: true}
	for len(selected) < parentCount {
		bestIdx, bestScore := -1, math.Inf(-1)
		for i := 0; i < poolCount; i++ {
			candidate := scored[i].idx
			if used[candidate] {
				continue
			}
			value := scored[i].score - penalty*avgSimilarityToSelected(pop, candidate, selected)
			if value > bestScore {
				bestScore = value
				bestIdx = candidate
			}
		}
		if bestIdx < 0 {
			break
		}
		selected = append(selected, bestIdx)
		used[bestIdx] = true
	}
	return selected
}
func avgSimilarityToSelected(pop []organism, candidate int, selected []int) float64 {
	if len(selected) == 0 {
		return 0
	}
	var sum float64
	for _, idx := range selected {
		sum += genotypeSimilarity(pop[candidate].geno, pop[idx].geno)
	}
	return sum / float64(len(selected))
}
func genotypeSimilarity(a, b []uint8) float64 {
	if len(a) == 0 {
		return 0
	}
	var sim float64
	for i := range a {
		sim += 1.0 - math.Abs(float64(a[i])-float64(b[i]))/2.0
	}
	return sim / float64(len(a))
}
func genotypeDistance(a, b []uint8) float64 { return 1.0 - genotypeSimilarity(a, b) }
func makeNextGeneration(pop []organism, parents []int, markers int, mutationRate float64, rng *rand.Rand, matingRule string) []organism {
	n := len(pop)
	next := make([]organism, n)
	for i := 0; i < n; i++ {
		p1 := parents[rng.Intn(len(parents))]
		p2 := chooseSecondParent(pop, parents, p1, rng, matingRule)
		g := make([]uint8, markers)
		for m := 0; m < markers; m++ {
			a := drawGamete(pop[p1].geno[m], rng)
			b := drawGamete(pop[p2].geno[m], rng)
			if mutationRate > 0 && rng.Float64() < mutationRate {
				a = 1 - a
			}
			if mutationRate > 0 && rng.Float64() < mutationRate {
				b = 1 - b
			}
			g[m] = a + b
		}
		next[i] = organism{geno: g}
	}
	return next
}
func chooseSecondParent(pop []organism, parents []int, p1 int, rng *rand.Rand, matingRule string) int {
	if len(parents) == 1 {
		return p1
	}
	if matingRule != "diverse_pairs" {
		p2 := parents[rng.Intn(len(parents))]
		for p2 == p1 {
			p2 = parents[rng.Intn(len(parents))]
		}
		return p2
	}
	best, bestDist := -1, math.Inf(-1)
	samples := 12
	if samples > len(parents)-1 {
		samples = len(parents) - 1
	}
	for s := 0; s < samples; s++ {
		p2 := parents[rng.Intn(len(parents))]
		for p2 == p1 {
			p2 = parents[rng.Intn(len(parents))]
		}
		d := genotypeDistance(pop[p1].geno, pop[p2].geno)
		if d > bestDist {
			bestDist = d
			best = p2
		}
	}
	if best >= 0 {
		return best
	}
	return parents[0]
}
func drawGamete(genotype uint8, rng *rand.Rand) uint8 {
	switch genotype {
	case 0:
		return 0
	case 2:
		return 1
	default:
		if rng.Intn(2) == 0 {
			return 0
		}
		return 1
	}
}

func computeMetrics(gen int, pop []organism, effects []float64, baseFreq []float64, baseDiversity, baseMean float64, rareUsefulAtStart []int, effectiveParents int) MetricPoint {
	freq := alleleFreq(pop, len(effects))
	div := diversityFromFreq(freq)
	trait := meanGeneticValue(pop, effects)
	drift := meanAbsDiff(freq, baseFreq)
	fixed, favorableFixed, unfavorableFixed := fixationStats(freq, effects)
	inbreeding := 0.0
	if baseDiversity > 0 {
		inbreeding = 1.0 - div/baseDiversity
	}
	if inbreeding < 0 {
		inbreeding = 0
	}
	lost := countRareUsefulLost(freq, rareUsefulAtStart)
	return MetricPoint{Generation: gen, TraitMean: round4(trait), GeneticGain: round4(trait - baseMean), Diversity: round4(div), Inbreeding: round4(inbreeding), AlleleDrift: round4(drift), RareUsefulLost: lost, FixedLoci: fixed, FavorableFixed: favorableFixed, UnfavorableFixed: unfavorableFixed, EffectiveParents: effectiveParents}
}
func fixationStats(freq []float64, effects []float64) (fixed, favorableFixed, unfavorableFixed int) {
	for i, p := range freq {
		fixedToZero, fixedToOne := p <= 0.001, p >= 0.999
		if !fixedToZero && !fixedToOne {
			continue
		}
		fixed++
		if effects[i] == 0 {
			continue
		}
		if effects[i] > 0 {
			if fixedToOne {
				favorableFixed++
			} else {
				unfavorableFixed++
			}
			continue
		}
		if fixedToZero {
			favorableFixed++
		} else {
			unfavorableFixed++
		}
	}
	return fixed, favorableFixed, unfavorableFixed
}
func geneticValues(pop []organism, effects []float64) []float64 {
	out := make([]float64, len(pop))
	for i := range pop {
		var v float64
		for m, g := range pop[i].geno {
			if effects[m] != 0 {
				v += float64(g) * effects[m]
			}
		}
		out[i] = v
	}
	return out
}
func meanGeneticValue(pop []organism, effects []float64) float64 {
	vals := geneticValues(pop, effects)
	mean, _ := meanVar(vals)
	return mean
}
func alleleFreq(pop []organism, markers int) []float64 {
	freq := make([]float64, markers)
	denom := float64(len(pop) * 2)
	for i := range pop {
		for m, g := range pop[i].geno {
			freq[m] += float64(g)
		}
	}
	for m := range freq {
		freq[m] /= denom
	}
	return freq
}

// afsBinsFromPop builds the 10-bin allele-frequency spectrum for live
// visualisation. bins[k] counts markers with frequency in [k/10, (k+1)/10);
// the last bin is closed at 1.0. v0.7.6 live histogram.
func afsBinsFromPop(pop []organism, markers int) [10]int {
	var bins [10]int
	if markers <= 0 || len(pop) == 0 {
		return bins
	}
	freq := alleleFreq(pop, markers)
	for _, p := range freq {
		idx := int(p * 10)
		if idx < 0 {
			idx = 0
		}
		if idx > 9 {
			idx = 9
		}
		bins[idx]++
	}
	return bins
}
func diversityFromFreq(freq []float64) float64 {
	var s float64
	for _, p := range freq {
		s += 2.0 * p * (1.0 - p)
	}
	return s / float64(len(freq))
}
func meanAbsDiff(a, b []float64) float64 {
	var s float64
	for i := range a {
		s += math.Abs(a[i] - b[i])
	}
	return s / float64(len(a))
}
func meanVar(vals []float64) (float64, float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))
	var ss float64
	for _, v := range vals {
		d := v - mean
		ss += d * d
	}
	return mean, ss / float64(len(vals))
}
func stddev(vals []float64) float64 {
	_, variance := meanVar(vals)
	return math.Sqrt(math.Max(0, variance))
}
func rareUsefulLoci(freq []float64, effects []float64) []int {
	out := make([]int, 0)
	for i := range freq {
		if effects[i] > 0 && freq[i] > 0.02 && freq[i] < 0.25 {
			out = append(out, i)
		}
	}
	return out
}
func countRareUsefulLost(freq []float64, loci []int) int {
	lost := 0
	for _, idx := range loci {
		if idx >= 0 && idx < len(freq) && freq[idx] < 0.01 {
			lost++
		}
	}
	return lost
}
// v0.7.27 — Issue 07. baseDiversity is consumed by the classifier so
// the "wait — bottleneck risk" rule has access to it. Callers all have
// baseDiversity in scope.
func rankEditCandidates(freq []float64, effects []float64, maxEdits int, baseDiversity float64) []EditCandidate {
	if maxEdits <= 0 {
		return nil
	}
	type item struct {
		locus int
		score float64
	}
	items := make([]item, 0)
	for i := range effects {
		if effects[i] <= 0 {
			continue
		}
		p := freq[i]
		score := effects[i] * (1.0 - p)
		if score > 0 {
			items = append(items, item{i, score})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].score > items[j].score })
	if len(items) > maxEdits {
		items = items[:maxEdits]
	}
	out := make([]EditCandidate, 0, len(items))
	for rank, it := range items {
		p := freq[it.locus]
		risk := "low"
		if p < 0.05 {
			risk = "medium: avoid single-founder bottleneck"
		}
		if effects[it.locus] > 1.4 && p < 0.10 {
			risk = "medium-high: validate pleiotropy and background effects"
		}
		decision := "seed edit into limited founders, then test under balanced selection"
		if p > 0.70 {
			decision = "prefer selection/crossing; edit adds limited marginal value"
		}
		// v0.7.27 — Issue 07. Structured class derived from the rule
		// engine. The legacy `decision` string above is kept aligned
		// so clients that only render it still get a sane summary.
		cls := classifyEditCandidate(effects[it.locus], p, baseDiversity)
		switch cls.Class {
		case "edit":
			decision = "seed edit into limited founders, then test under balanced selection"
		case "cross":
			decision = "prefer selection/crossing; edit adds limited marginal value"
		case "wait":
			decision = "defer edit — validate effect, restore diversity, or let selection complete the lift"
		}
		out = append(out, EditCandidate{Rank: rank + 1, Locus: it.locus, Effect: round4(effects[it.locus]), AlleleFrequency: round4(p), ExpectedGainScore: round4(it.score), DiversityRisk: risk, Decision: decision, Classification: &cls})
	}
	return out
}
func applyCrisprSeed(pop []organism, edits []EditCandidate, introPercent float64, rng *rand.Rand) {
	if introPercent <= 0 || len(edits) == 0 {
		return
	}
	count := int(math.Round(float64(len(pop)) * introPercent / 100.0))
	if count < 1 {
		count = 1
	}
	if count > len(pop) {
		count = len(pop)
	}
	perm := rng.Perm(len(pop))
	for i := 0; i < count; i++ {
		ind := perm[i]
		for _, edit := range edits {
			if edit.Locus >= 0 && edit.Locus < len(pop[ind].geno) {
				pop[ind].geno[edit.Locus] = 2
			}
		}
	}
}

func recommendationFor(code string, f FinalStats) string {
	riskText := ""
	if f.ProbabilityInbreedingBreach >= 0.5 || f.ProbabilityDiversityCollapse >= 0.5 {
		riskText = " High risk probability: treat as non-deployable without stronger constraints."
	}
	switch code {
	case "neutral":
		return "Use as a baseline only; it reveals drift without intentional gain." + riskText
	case "random":
		return "Use as a stochastic baseline; a real strategy should beat it on risk-adjusted score." + riskText
	case "phenotype":
		return "Good practical baseline; compare against genomic and OCS-like strategies under noise." + riskText
	case "genomic":
		return "Promising when marker/phenotype data support prediction; replace mock predictor with real GEBV models." + riskText
	case "ocs_like":
		return "Strong early BreedOS candidate: gain is pursued under explicit diversity pressure." + riskText
	case "cross_planner":
		return "Use when mating allocation matters; next step is real pedigree and kinship-aware cross optimization." + riskText
	case "edit_introgression":
		return "Use as the CRISPR-aware decision demo: edit candidates still require biological validation and partner lab work." + riskText
	case "aggressive":
		if f.Inbreeding > 0.35 || f.Diversity < 0.20 {
			return "Use only as a short-term upper-bound baseline; add diversity constraints before real deployment." + riskText
		}
		return "Good short-term gain; still run a diversity-constrained scenario before choosing parents." + riskText
	case "diversity":
		return "Use when the program is bottlenecked, rare alleles are strategic, or future environments are uncertain." + riskText
	case "balanced_crispr":
		return "Use as a CRISPR-integration demo: validate candidate edits, seed narrowly, then propagate through balanced selection." + riskText
	default:
		return "Recommended default: preserve enough diversity while still producing visible trait gain." + riskText
	}
}
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
func setContentType(w http.ResponseWriter, path string) {
	if strings.HasSuffix(path, ".html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}
	if strings.HasSuffix(path, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		return
	}
	if strings.HasSuffix(path, ".js") {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		return
	}
	if strings.HasSuffix(path, ".ico") {
		w.Header().Set("Content-Type", "image/x-icon")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
}
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Truncate(time.Millisecond))
	})
}
func normalizeListenForLog(s string) string {
	if strings.HasPrefix(s, ":") {
		return "127.0.0.1" + s
	}
	return s
}
func _unusedFmtGuard() { fmt.Print("") }
