package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestMutationRateZeroIsPreserved(t *testing.T) {
	req := SimRequest{Seed: 123, PopulationSize: 12, Markers: 40, QTLCount: 8, Generations: 8, SelectionPercent: 25, Heritability: 0.5, MutationRate: 0, CrisprEnabled: true, CrisprEdits: 2, Replicates: 1}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation failed: %v", err)
	}
	if resp.Request.MutationRate != 0 {
		t.Fatalf("mutation rate was normalized incorrectly: got %g, want 0", resp.Request.MutationRate)
	}
	joined := strings.Join(resp.Notes, "\n")
	if !strings.Contains(joined, "Mutation rate is 0") {
		t.Fatalf("expected zero-mutation note, got: %s", joined)
	}
}

func TestCrisprEditsZeroDisablesCandidatesAndCrisprStrategy(t *testing.T) {
	req := SimRequest{Seed: 123, PopulationSize: 40, Markers: 80, QTLCount: 10, Generations: 5, SelectionPercent: 20, Heritability: 0.5, MutationRate: 0.0001, CrisprEnabled: true, CrisprEdits: 0, CrisprIntroPercent: 0, Replicates: 1}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation failed: %v", err)
	}
	if len(resp.CandidateEdits) != 0 {
		t.Fatalf("expected no edit candidates when crispr_edits=0, got %d", len(resp.CandidateEdits))
	}
	for _, s := range resp.Strategies {
		if strings.Contains(s.Code, "crispr") || strings.Contains(s.Code, "edit_introgression") {
			t.Fatalf("CRISPR/edit strategy should be disabled when crispr_edits=0, got %s", s.Code)
		}
	}
}

func TestMutationRateChangeIsAcceptedOnManualRun(t *testing.T) {
	base := SimRequest{Seed: 98765, PopulationSize: 40, Markers: 120, QTLCount: 20, Generations: 30, SelectionPercent: 20, Heritability: 0.5, MutationRate: 0, Replicates: 1}
	withoutMutations, err := runSimulation(base)
	if err != nil {
		t.Fatalf("runSimulation without mutations failed: %v", err)
	}
	withMutationReq := base
	withMutationReq.MutationRate = 0.01
	withMutations, err := runSimulation(withMutationReq)
	if err != nil {
		t.Fatalf("runSimulation with mutations failed: %v", err)
	}
	if withoutMutations.Request.MutationRate != 0 {
		t.Fatalf("expected first run mutation rate echo 0, got %g", withoutMutations.Request.MutationRate)
	}
	if withMutations.Request.MutationRate != 0.01 {
		t.Fatalf("expected second run mutation rate echo 0.01, got %g", withMutations.Request.MutationRate)
	}
	if len(withoutMutations.Strategies) == 0 || len(withMutations.Strategies) == 0 {
		t.Fatalf("expected strategies in both runs")
	}
	before := withoutMutations.Strategies[0].Final
	after := withMutations.Strategies[0].Final
	if before.GeneticGain == after.GeneticGain && before.Diversity == after.Diversity && before.FixedLoci == after.FixedLoci {
		t.Fatalf("mutation rate change was accepted but produced effectively identical final stats; before=%+v after=%+v", before, after)
	}
}

func TestProgressCallbackAdvances(t *testing.T) {
	req := SimRequest{Seed: 4321, PopulationSize: 20, Markers: 60, QTLCount: 10, Generations: 8, SelectionPercent: 25, Heritability: 0.5, MutationRate: 0.001, Replicates: 2}
	var mu sync.Mutex
	maxPercent, calls := 0, 0
	emptyMessage, parallelMessage := false, false
	_, err := runSimulationWithProgress(req, func(percent int, message string) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if percent > maxPercent {
			maxPercent = percent
		}
		if message == "" {
			emptyMessage = true
		}
		if strings.Contains(strings.ToLower(message), "parallel") || strings.Contains(strings.ToLower(message), "worker") {
			parallelMessage = true
		}
	})
	if err != nil {
		t.Fatalf("runSimulationWithProgress failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if emptyMessage {
		t.Fatalf("progress message must not be empty")
	}
	if !parallelMessage {
		t.Fatalf("expected at least one progress message to mention parallel/worker execution")
	}
	if calls < 3 {
		t.Fatalf("expected multiple progress calls, got %d", calls)
	}
	if maxPercent < 98 {
		t.Fatalf("expected progress to reach at least 98 before completion, got %d", maxPercent)
	}
}

func TestCoreStrategyOrderAndCount(t *testing.T) {
	req := SimRequest{Seed: 555, PopulationSize: 30, Markers: 80, QTLCount: 12, Generations: 6, SelectionPercent: 20, Heritability: 0.5, MutationRate: 0.0001, CrisprEnabled: true, CrisprEdits: 2, CrisprIntroPercent: 10, Replicates: 1}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation failed: %v", err)
	}
	want := []string{"neutral", "aggressive", "diversity", "balanced", "balanced_crispr"}
	if len(resp.Strategies) != len(want) {
		t.Fatalf("strategy count mismatch: got %d want %d", len(resp.Strategies), len(want))
	}
	for i, code := range want {
		if resp.Strategies[i].Code != code {
			t.Fatalf("strategy order mismatch at %d: got %q want %q", i, resp.Strategies[i].Code, code)
		}
	}
	joined := strings.Join(resp.Notes, "\n")
	if !strings.Contains(strings.ToLower(joined), "worker pool") && !strings.Contains(strings.ToLower(joined), "parallel") {
		t.Fatalf("expected notes to mention parallel/worker execution, got: %s", joined)
	}
}

func TestAdvancedDecisionEngineReturnsRanksAndRisks(t *testing.T) {
	req := SimRequest{Seed: 777, PopulationSize: 24, Markers: 70, QTLCount: 12, Generations: 7, SelectionPercent: 25, Heritability: 0.45, MutationRate: 0.0002, CrisprEnabled: true, CrisprEdits: 3, CrisprIntroPercent: 10, StrategySet: "advanced", Replicates: 2, WorkerCount: 2, InbreedingLimit: 0.25, DiversityLossLimit: 0.30}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation failed: %v", err)
	}
	codes := map[string]bool{}
	for _, s := range resp.Strategies {
		codes[s.Code] = true
		if s.Replicates != 2 || s.Final.Replicates != 2 {
			t.Fatalf("expected strategy %s to aggregate 2 replicates, got result=%d final=%d", s.Code, s.Replicates, s.Final.Replicates)
		}
		if s.Final.DecisionRank <= 0 {
			t.Fatalf("expected decision rank for %s", s.Code)
		}
		if s.Final.ProbabilityInbreedingBreach < 0 || s.Final.ProbabilityInbreedingBreach > 1 {
			t.Fatalf("risk probability out of range for %s", s.Code)
		}
	}
	for _, required := range []string{"genomic", "ocs_like", "cross_planner", "edit_introgression"} {
		if !codes[required] {
			t.Fatalf("advanced response missing strategy %s", required)
		}
	}
	if resp.Decision.BestRiskAdjustedCode == "" || len(resp.Decision.ParetoCodes) == 0 {
		t.Fatalf("expected decision summary and Pareto codes: %+v", resp.Decision)
	}
}

func TestDecisionReportPopulatesNewFields(t *testing.T) {
	req := SimRequest{Seed: 555, PopulationSize: 24, Markers: 60, QTLCount: 10, Generations: 8, SelectionPercent: 20, Heritability: 0.5, MutationRate: 0.0001, StrategySet: "core", Replicates: 3, InbreedingLimit: 0.25, DiversityLossLimit: 0.30}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation failed: %v", err)
	}
	d := resp.Decision
	if d.SummaryText == "" {
		t.Fatalf("SummaryText must not be empty")
	}
	if d.NextAnalysis == "" {
		t.Fatalf("NextAnalysis must not be empty")
	}
	if len(d.KeyAssumptions) == 0 {
		t.Fatalf("KeyAssumptions must not be empty")
	}
	joined := strings.Join(d.KeyAssumptions, " ")
	if !strings.Contains(joined, "Heritability") && !strings.Contains(joined, "h²") {
		t.Fatalf("KeyAssumptions should mention heritability: %v", d.KeyAssumptions)
	}
	if d.Tradeoffs == nil {
		t.Fatalf("Tradeoffs must be initialized (non-nil), even if empty")
	}
	if d.AvoidStrategies == nil {
		t.Fatalf("AvoidStrategies must be initialized (non-nil), even if empty")
	}
	if d.MissingDataWarnings == nil {
		t.Fatalf("MissingDataWarnings must be initialized (non-nil), even if empty")
	}
	if !strings.Contains(d.SummaryText, "Recommended") {
		t.Fatalf("SummaryText should mention Recommended: %q", d.SummaryText)
	}
}

func TestDecisionReportFlagsHighRiskAggressiveOrIncludesItInTradeoff(t *testing.T) {
	req := SimRequest{Seed: 31337, PopulationSize: 12, Markers: 60, QTLCount: 10, Generations: 25, SelectionPercent: 15, Heritability: 0.5, MutationRate: 0.0001, StrategySet: "core", Replicates: 3, InbreedingLimit: 0.25, DiversityLossLimit: 0.30}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation failed: %v", err)
	}
	flagged := false
	for _, a := range resp.Decision.AvoidStrategies {
		if a.Code == "aggressive" {
			flagged = true
			break
		}
	}
	for _, tr := range resp.Decision.Tradeoffs {
		if tr.A == "aggressive" || tr.B == "aggressive" {
			flagged = true
			break
		}
	}
	if !flagged {
		t.Fatalf("expected aggressive to appear in AvoidStrategies or Tradeoffs (high-pressure small-N scenario); got decision=%+v", resp.Decision)
	}
}

func TestDecisionReportSummaryReferencesRecommendedStrategy(t *testing.T) {
	req := SimRequest{Seed: 777, PopulationSize: 50, Markers: 80, QTLCount: 12, Generations: 15, SelectionPercent: 20, Heritability: 0.5, MutationRate: 0.0001, StrategySet: "advanced", Replicates: 3, InbreedingLimit: 0.25, DiversityLossLimit: 0.30}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation failed: %v", err)
	}
	d := resp.Decision
	bestName := ""
	for _, s := range resp.Strategies {
		if s.Code == d.BestRiskAdjustedCode {
			bestName = s.Name
			break
		}
	}
	if bestName == "" {
		t.Fatalf("best risk-adjusted strategy '%s' not found in results", d.BestRiskAdjustedCode)
	}
	if !strings.Contains(d.SummaryText, bestName) {
		t.Fatalf("SummaryText must reference recommended strategy name '%s'; got: %s", bestName, d.SummaryText)
	}
	keywords := []string{"replicates", "Pareto", "seed", "intensity", "constraints", "robustness", "robust", "narrow"}
	found := false
	for _, kw := range keywords {
		if strings.Contains(d.NextAnalysis, kw) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("NextAnalysis should contain at least one of %v; got: %s", keywords, d.NextAnalysis)
	}
}

func TestPerformSwapRenamesBinaryAndCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "breedos")
	updatePath := currentPath + ".UPDATE"

	if err := os.WriteFile(currentPath, []byte("OLD"), 0o755); err != nil {
		t.Fatalf("write current: %v", err)
	}
	if err := os.WriteFile(updatePath, []byte("NEW"), 0o755); err != nil {
		t.Fatalf("write update: %v", err)
	}

	if err := performSwap(currentPath, updatePath); err != nil {
		t.Fatalf("performSwap returned error: %v", err)
	}

	data, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("read current after swap: %v", err)
	}
	if string(data) != "NEW" {
		t.Fatalf("current path should now hold update content; got %q", string(data))
	}

	if _, err := os.Stat(updatePath); !os.IsNotExist(err) {
		t.Fatalf("update path should be gone after swap; stat err: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var backupName string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "breedos.bak.") {
			backupName = e.Name()
			break
		}
	}
	if backupName == "" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("no backup file matching 'breedos.bak.*' found; dir contains: %v", names)
	}
	backupData, err := os.ReadFile(filepath.Join(dir, backupName))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backupData) != "OLD" {
		t.Fatalf("backup should contain old binary content; got %q", string(backupData))
	}
}

func TestEnsureExecutableSetsExecBit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "candidate")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ensureExecutable(path); err != nil {
		t.Fatalf("ensureExecutable: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("owner exec bit should be set after ensureExecutable; got mode %o", info.Mode().Perm())
	}
}

// v0.7.3 constraint-engine tests (Issue 03).

func TestConstraintEngineFeasibleStrategyExists(t *testing.T) {
	req := SimRequest{
		Seed: 12345, PopulationSize: 60, Markers: 100, QTLCount: 12,
		Generations: 8, SelectionPercent: 20, Heritability: 0.5,
		MutationRate: 0.0001, StrategySet: "core", Replicates: 2,
		InbreedingLimit: 0.25, DiversityLossLimit: 0.30,
		// Permissive floor: 0.01 gain is trivially achievable by most strategies.
		MinGeneticGain: 0.01,
	}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation: %v", err)
	}
	feasibleCount := 0
	for _, s := range resp.Strategies {
		if s.Final.Feasible {
			feasibleCount++
		}
	}
	if feasibleCount == 0 {
		t.Fatalf("expected at least one feasible strategy under permissive constraint; got 0")
	}
	if resp.Decision.BestFeasibleCode == "" {
		t.Fatalf("expected BestFeasibleCode to be populated when feasible strategies exist")
	}
	if len(resp.Decision.ConstraintsApplied) == 0 {
		t.Fatalf("expected ConstraintsApplied to list the active constraint")
	}
}

func TestConstraintEngineNoFeasibleStrategy(t *testing.T) {
	req := SimRequest{
		Seed: 67890, PopulationSize: 40, Markers: 100, QTLCount: 10,
		Generations: 12, SelectionPercent: 15, Heritability: 0.5,
		MutationRate: 0.0001, StrategySet: "core", Replicates: 2,
		InbreedingLimit: 0.25, DiversityLossLimit: 0.30,
		// Impossibly tight: every selection scheme on this small N will exceed.
		MaxInbreeding: 0.001,
	}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation: %v", err)
	}
	for _, s := range resp.Strategies {
		if s.Final.Feasible {
			t.Fatalf("expected no feasible strategy under impossibly tight inbreeding cap; %s was feasible", s.Code)
		}
		if len(s.Final.FailedConstraints) == 0 {
			t.Fatalf("expected %s to list at least one failed constraint", s.Code)
		}
	}
	if resp.Decision.BestFeasibleCode != "" {
		t.Fatalf("expected BestFeasibleCode to be empty when nothing is feasible; got %q", resp.Decision.BestFeasibleCode)
	}
	if !strings.Contains(strings.ToLower(resp.Decision.FeasibilityNote), "inbreeding") {
		t.Fatalf("expected FeasibilityNote to reference inbreeding as the binding constraint; got %q", resp.Decision.FeasibilityNote)
	}
}

func TestConstraintEngineAggressiveRejectedByRiskCap(t *testing.T) {
	req := SimRequest{
		Seed: 24680, PopulationSize: 40, Markers: 100, QTLCount: 12,
		Generations: 15, SelectionPercent: 12, Heritability: 0.5,
		MutationRate: 0.0001, StrategySet: "core", Replicates: 3,
		InbreedingLimit: 0.25, DiversityLossLimit: 0.30,
		// Risk cap that aggressive selection (which drives inbreeding/diversity
		// risk up under heavy selection on small N) cannot pass.
		MaxCombinedRisk: 0.20,
	}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation: %v", err)
	}
	var aggressive *StrategyResult
	for i := range resp.Strategies {
		if resp.Strategies[i].Code == "aggressive" {
			aggressive = &resp.Strategies[i]
			break
		}
	}
	if aggressive == nil {
		t.Fatalf("expected 'aggressive' strategy in core set; got: %v", strategyCodes(resp.Strategies))
	}
	if aggressive.Final.Feasible {
		t.Fatalf("expected aggressive to be infeasible under tight risk cap; actual combined risk %.4f", combinedRisk(aggressive.Final))
	}
	hasRiskFailure := false
	for _, fc := range aggressive.Final.FailedConstraints {
		if strings.HasPrefix(fc, "combined risk ") {
			hasRiskFailure = true
			break
		}
	}
	if !hasRiskFailure {
		t.Fatalf("expected aggressive FailedConstraints to include 'combined risk ...'; got %v", aggressive.Final.FailedConstraints)
	}
}

func strategyCodes(s []StrategyResult) []string {
	out := make([]string, 0, len(s))
	for _, r := range s {
		out = append(out, r.Code)
	}
	return out
}

// v0.7.4 dataset loader tests (Issue 04).

func TestParseDatasetCSVRoundTrip(t *testing.T) {
	raw := []byte(`# tiny fixture
# placeholder: true
accession_id,m1,m2,m3
A001,0,1,2
A002,2,2,0
A003,1,0,1
`)
	ds, err := parseDatasetCSV(raw, "tiny.csv")
	if err != nil {
		t.Fatalf("parseDatasetCSV: %v", err)
	}
	if !ds.isPlaceholder {
		t.Fatalf("expected placeholder=true from `# placeholder: true` line")
	}
	if ds.markerCount != 3 {
		t.Fatalf("expected 3 markers, got %d", ds.markerCount)
	}
	if len(ds.individuals) != 3 {
		t.Fatalf("expected 3 individuals, got %d", len(ds.individuals))
	}
	want := []uint8{2, 2, 0}
	if string(ds.individuals[1].geno) != string(want) {
		t.Fatalf("row A002 geno mismatch: got %v want %v", ds.individuals[1].geno, want)
	}
	if ds.accessionIDs[0] != "A001" || ds.accessionIDs[2] != "A003" {
		t.Fatalf("accession IDs mismatch: %v", ds.accessionIDs)
	}
}

func TestParseDatasetCSVRejectsOutOfRange(t *testing.T) {
	raw := []byte("accession_id,m1\nX,3\n")
	if _, err := parseDatasetCSV(raw, "bad.csv"); err == nil {
		t.Fatalf("expected error on value 3 outside 0..2")
	}
}

func TestLoadDatasetFallsBackToPlaceholder(t *testing.T) {
	ds, err := loadDataset("definitely-not-a-real-dataset-name")
	if err != nil {
		t.Fatalf("loadDataset unexpected error: %v", err)
	}
	if !ds.isPlaceholder {
		t.Fatalf("expected fallback to placeholder example_founders.csv")
	}
	if len(ds.individuals) == 0 || ds.markerCount == 0 {
		t.Fatalf("expected non-empty placeholder dataset; got N=%d M=%d", len(ds.individuals), ds.markerCount)
	}
}

func TestSubsampleDatasetSize(t *testing.T) {
	raw := []byte("accession_id,m1,m2,m3,m4,m5\n")
	for i := 0; i < 20; i++ {
		raw = append(raw, []byte(fmt.Sprintf("R%02d,0,1,2,1,0\n", i))...)
	}
	ds, err := parseDatasetCSV(raw, "fixture.csv")
	if err != nil {
		t.Fatalf("parseDatasetCSV: %v", err)
	}
	rng := rand.New(rand.NewSource(1))
	sub := subsampleDataset(ds, 5, 3, rng)
	if len(sub.individuals) != 5 || sub.markerCount != 3 {
		t.Fatalf("expected N=5 M=3 after subsample; got N=%d M=%d", len(sub.individuals), sub.markerCount)
	}
	for _, ind := range sub.individuals {
		if len(ind.geno) != 3 {
			t.Fatalf("subsampled individual should have 3 markers; got %d", len(ind.geno))
		}
	}
}

func TestDatasetRoutedThroughSimulation(t *testing.T) {
	req := SimRequest{
		Seed: 7, PopulationSize: 30, Markers: 50, QTLCount: 10,
		Generations: 4, SelectionPercent: 25, Heritability: 0.5,
		MutationRate: 0.0001, StrategySet: "core", Replicates: 1,
		InbreedingLimit: 0.25, DiversityLossLimit: 0.30,
		Dataset: "arabidopsis1001", // will fall back to placeholder fixture
	}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation with dataset: %v", err)
	}
	if len(resp.Strategies) == 0 {
		t.Fatalf("expected strategies in response")
	}
	// Notes should reference the placeholder warning.
	found := false
	for _, n := range resp.Notes {
		if strings.Contains(strings.ToLower(n), "placeholder") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected placeholder warning in notes; got: %v", resp.Notes)
	}
}

// v0.7.5 — external dataset takes precedence over embedded placeholder.
func TestExternalDatasetTakesPrecedenceOverEmbedded(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dataDir := filepath.Join(filepath.Dir(exe), "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(dataDir, "arabidopsis1001.csv")
	content := []byte("# external test fixture (no placeholder marker)\naccession_id,m1,m2\nEXT_A,0,1\nEXT_B,1,2\nEXT_C,2,0\n")
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(target)
		// data/ may also contain other files written by earlier tests; only
		// remove if empty to avoid clobbering parallel state.
		_ = os.Remove(dataDir)
		resetDatasetCache()
	})
	resetDatasetCache()
	ds, err := loadDataset("arabidopsis1001")
	if err != nil {
		t.Fatalf("loadDataset: %v", err)
	}
	if !ds.external {
		t.Fatalf("expected external=true; got source=%q external=%v", ds.sourceFile, ds.external)
	}
	if ds.isPlaceholder {
		t.Fatalf("external real-data file must not be flagged as placeholder")
	}
	if ds.markerCount != 2 || len(ds.individuals) != 3 {
		t.Fatalf("expected 3 individuals × 2 markers; got %d × %d", len(ds.individuals), ds.markerCount)
	}
	if ds.accessionIDs[0] != "EXT_A" {
		t.Fatalf("expected first accession EXT_A; got %q", ds.accessionIDs[0])
	}
}

// TestSimulateNotesStayRunScoped guards the notes array of the biological
// simulate response against release-changelog content.
//
// Regression: v0.7.31 through v0.7.38 shipped a promptbio changelog as note #2.
// By v0.7.38 it was 28,982 characters — 96% of the whole notes payload — and it
// rendered verbatim into the demo Decision Report (static/app.js renders every
// note as a .note-item div). See issues-breedos/33.
//
// Notes describe the RUN (configuration, thresholds, scope limits, data
// provenance). Release information belongs in CHANGELOG.md and /api/version.
func TestSimulateNotesStayRunScoped(t *testing.T) {
	req := SimRequest{
		Seed: 42, PopulationSize: 60, Markers: 120, QTLCount: 20, Generations: 10,
		SelectionPercent: 20, Heritability: 0.4, MutationRate: 0.001,
		CrisprEnabled: true, CrisprEdits: 3, CrisprIntroPercent: 10,
		StrategySet: "core", Replicates: 2,
		InbreedingLimit: 0.25, DiversityLossLimit: 0.30,
	}
	resp, err := runSimulation(req)
	if err != nil {
		t.Fatalf("runSimulation failed: %v", err)
	}
	if len(resp.Notes) == 0 {
		t.Fatalf("expected run notes, got none")
	}

	// A changelog pasted into a note is long. Longest legitimate note today is
	// the 352-char edit-classifier explanation.
	const maxNoteLen = 600
	// Vocabulary that only appears in promptbio material. The biological
	// simulator has no reason to emit any of it.
	forbidden := []string{
		"promptbio", "prompt organism", "prompt genome", "genome_score",
		"pattern card", "anti-pattern", "pml-", "locus",
	}

	for i, n := range resp.Notes {
		if len(n) > maxNoteLen {
			t.Errorf("note[%d] is %d chars, exceeds %d — release changelogs belong in CHANGELOG.md / /api/version, not in simulation notes; got: %.120q...",
				i, len(n), maxNoteLen, n)
		}
		low := strings.ToLower(n)
		for _, bad := range forbidden {
			if strings.Contains(low, bad) {
				t.Errorf("note[%d] contains forbidden promptbio vocabulary %q — the biological simulate response must not describe the prompt substrate; got: %.120q...",
					i, bad, n)
			}
		}
	}

	// Sanity bound: notes are a short run summary, not a document.
	if len(resp.Notes) > 15 {
		t.Errorf("notes count %d is implausibly high for a run summary", len(resp.Notes))
	}
}
