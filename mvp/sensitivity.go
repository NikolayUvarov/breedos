package main

// v0.7.16 — sensitivity analysis (Issue 09).
//
// Run the same base simulation across N values of one axis (heritability,
// selection_percent, generations) and ask: does the best-feasible strategy
// stay the same? If yes the recommendation is "stable"; if it changes the
// recommendation is "fragile" — the user should not trust a single run.
//
// Reuses the existing simulation engine. Each scenario is one full
// runSimulationWithCallbacks call. Scenarios run sequentially so the
// budget guard composes cleanly with the per-run cap.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// Same cap as a single simulation, applied to the SUM of all scenario
	// budgets. Matches the per-run cap so a sweep can't outgrow what one
	// large run is allowed. Kept in sync with validateRequest in main.go.
	sensitivityBudgetCap = 1500000000

	// Practical cap to keep UI useful and prod queue short. 5 is the
	// default; users can ask for less. We don't allow more for v0.7.16
	// because the table view stays readable at 5.
	sensitivityMaxValues = 5
)

type SensitivityRequest struct {
	Base   SimRequest `json:"base"`
	Axis   string     `json:"axis"`
	Values []float64  `json:"values,omitempty"`
	// v0.7.28 — Issue 29. Used when Axis == axisClimateScenario.
	// Numeric `Values` is ignored in that case; each entry here is a
	// {mode, severity} pair applied to scen.Climate per simulation.
	ClimateValues []ClimateScenario `json:"climate_values,omitempty"`
}

// v0.7.28 — Issue 29. Sentinel for the structured axis. Distinct from
// the float-valued static axes (heritability / selection_percent /
// generations) and from the trait_weight:<name> dynamic family.
const axisClimateScenario = "climate_scenario"

type SensitivityScenario struct {
	AxisValue        float64 `json:"axis_value"`
	// v0.7.28 — Issue 29. Human-readable axis-value rendering. Empty
	// for numeric axes (UI falls back to formatting AxisValue);
	// populated for the climate_scenario axis to render
	// "heat_burst_anthesis (sev 0.50)" instead of a meaningless 0/1/2.
	AxisLabel        string  `json:"axis_label,omitempty"`
	BestFeasibleCode string  `json:"best_feasible_code"`
	BestFeasibleName string  `json:"best_feasible_name"`
	GeneticGain      float64 `json:"genetic_gain"`
	Diversity        float64 `json:"diversity"`
	Inbreeding       float64 `json:"inbreeding"`
	RareUsefulLost   int     `json:"rare_useful_lost"`
	CombinedRisk     float64 `json:"combined_risk"`
	BaselineMatch    bool    `json:"baseline_match"`
	InfeasibleNote   string  `json:"infeasible_note,omitempty"`
}

type SensitivitySummary struct {
	BaselineAxisValue float64  `json:"baseline_axis_value"`
	BaselineBestCode  string   `json:"baseline_best_code"`
	BaselineBestName  string   `json:"baseline_best_name"`
	Stable            bool     `json:"stable"`
	Verdict           string   `json:"verdict"` // "stable" | "fragile" | "inconclusive"
	StrategySwitches  int      `json:"strategy_switches"`
	Notes             []string `json:"notes"`
}

type SensitivityResult struct {
	Base      SimRequest            `json:"base"`
	Axis      string                `json:"axis"`
	Values    []float64             `json:"values,omitempty"`
	// v0.7.28 — Issue 29. Populated only when Axis == axisClimateScenario.
	ClimateValues []ClimateScenario     `json:"climate_values,omitempty"`
	Scenarios []SensitivityScenario `json:"scenarios"`
	Summary   SensitivitySummary    `json:"summary"`
	// v0.7.28 — Issue 31. Plain-language Decision Report section.
	// Populated only when Axis == axisClimateScenario AND len(scenarios) >= 2.
	ClimateRobustness *ClimateRobustness `json:"climate_robustness,omitempty"`
}

// Percent is float64 so the client can show fractional progress while a
// single scenario is mid-flight. On prod (single-core VPS) one scenario
// can take 15–30 seconds; without sub-percent granularity the meter
// looks frozen until the scenario boundary.
type SensitivityJobStatus struct {
	JobID   string             `json:"job_id"`
	Percent float64            `json:"percent"`
	Message string             `json:"message"`
	Done    bool               `json:"done"`
	Error   string             `json:"error,omitempty"`
	Result  *SensitivityResult `json:"result,omitempty"`
}

type sensJob struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
	Percent   float64
	Message   string
	Done      bool
	Error     string
	Result    *SensitivityResult
}

var sensJobStore = struct {
	sync.Mutex
	NextID uint64
	Jobs   map[string]*sensJob
}{Jobs: make(map[string]*sensJob)}

// validSensitivityAxes maps axis name → predicate that applies the value to
// a SimRequest. Adding a new axis only requires adding one entry here.
//
// v0.7.23 — Issue B. In addition to the static axes below, the sweep
// supports dynamic `trait_weight:<trait_name>` axes when the run is
// multi-trait. These are resolved by applyForAxis below, not by direct
// map lookup. The dropdown in the form lists them at submit time.
var validSensitivityAxes = map[string]func(*SimRequest, float64){
	"heritability":      func(r *SimRequest, v float64) { r.Heritability = v },
	"selection_percent": func(r *SimRequest, v float64) { r.SelectionPercent = v },
	"generations":       func(r *SimRequest, v float64) { r.Generations = int(v + 0.5) },
	// v0.8.4 — Issue 35. "Does the GS recommendation survive a slower genomic cycle?"
	"genomic_cycle_years": func(r *SimRequest, v float64) { r.GenomicCycleYears = v },
	// v0.8.5 — Issue 36. "How much gain does each extra 0.5% of ΔF buy?"
	"target_delta_f": func(r *SimRequest, v float64) { r.TargetDeltaF = v },
}

const traitWeightAxisPrefix = "trait_weight:"

// applyForAxis applies value to scen for the given axis. Handles both the
// static catalog and the dynamic trait_weight:<name> family. Returns an
// error if the axis is unknown or the trait name doesn't match the run.
func applyForAxis(scen *SimRequest, axis string, value float64) error {
	if fn, ok := validSensitivityAxes[axis]; ok {
		fn(scen, value)
		return nil
	}
	if strings.HasPrefix(axis, traitWeightAxisPrefix) {
		name := strings.TrimPrefix(axis, traitWeightAxisPrefix)
		if len(scen.Traits) == 0 {
			return fmt.Errorf("axis %q requires a multi-trait run (req.Traits is empty)", axis)
		}
		for i, tr := range scen.Traits {
			if tr.Name == name {
				scen.Traits[i].SelectionWeight = value
				return nil
			}
		}
		return fmt.Errorf("axis %q: trait name %q not found in req.Traits", axis, name)
	}
	return fmt.Errorf("axis %q is not recognised", axis)
}

func validateSensitivityRequest(req SensitivityRequest) error {
	// v0.7.23 — Issue B. Accept dynamic trait_weight:<name> axes
	// when req.Base.Traits carries that name; static axes go through the
	// existing fast-path catalog. v0.7.28 — Issue 29. Accept the
	// structured climate_scenario axis.
	axisOK := false
	if _, ok := validSensitivityAxes[req.Axis]; ok {
		axisOK = true
	} else if req.Axis == axisClimateScenario {
		axisOK = true
	} else if strings.HasPrefix(req.Axis, traitWeightAxisPrefix) {
		name := strings.TrimPrefix(req.Axis, traitWeightAxisPrefix)
		for _, t := range req.Base.Traits {
			if t.Name == name {
				axisOK = true
				break
			}
		}
	}
	if !axisOK {
		keys := make([]string, 0, len(validSensitivityAxes)+2)
		for k := range validSensitivityAxes {
			keys = append(keys, k)
		}
		keys = append(keys, axisClimateScenario)
		if len(req.Base.Traits) > 0 {
			for _, t := range req.Base.Traits {
				keys = append(keys, traitWeightAxisPrefix+t.Name)
			}
		}
		return fmt.Errorf("axis must be one of %s; got %q", strings.Join(keys, ", "), req.Axis)
	}
	// v0.7.28 — Issue 29. Climate-scenario axis is structured: validate
	// climate_values, not numeric values.
	if req.Axis == axisClimateScenario {
		if len(req.ClimateValues) == 0 {
			return errors.New("climate_values must be a non-empty list when axis = climate_scenario")
		}
		if len(req.ClimateValues) > sensitivityMaxValues {
			return fmt.Errorf("at most %d climate values per sweep (v0.7.16 limit); got %d", sensitivityMaxValues, len(req.ClimateValues))
		}
		total := int64(0)
		for i, cs := range req.ClimateValues {
			if err := ValidateClimateScenario(&cs); err != nil {
				return fmt.Errorf("climate_values[%d]: %v", i, err)
			}
			scen := req.Base
			if len(req.Base.Traits) > 0 {
				scen.Traits = append([]TraitConfig(nil), req.Base.Traits...)
			}
			normalizeRequest(&scen)
			csCopy := cs
			scen.Climate = &csCopy
			strategies := buildStrategyConfigs(scen)
			if err := validateRequest(scen, len(strategies)); err != nil {
				return fmt.Errorf("climate_values[%d] (%s sev %.2f) invalid: %v", i, cs.Mode, cs.Severity, err)
			}
			total += simulationBudget(scen, len(strategies))
		}
		if total > sensitivityBudgetCap {
			return fmt.Errorf("sensitivity sweep budget too high: sum of per-scenario budgets must be <= %d; got %d (reduce population, markers, generations, replicates, or number of climate scenarios)", sensitivityBudgetCap, total)
		}
		return nil
	}
	if len(req.Values) == 0 {
		return errors.New("values must be a non-empty list")
	}
	if len(req.Values) > sensitivityMaxValues {
		return fmt.Errorf("at most %d values per sweep (v0.7.16 limit); got %d", sensitivityMaxValues, len(req.Values))
	}
	// Validate each scenario individually first — clearer errors than a
	// budget overflow. Also catches axis-specific range violations (e.g.
	// heritability outside [0,1]).
	total := int64(0)
	for i, v := range req.Values {
		scen := req.Base
		// v0.7.23 — deep-copy traits so per-scenario apply doesn't mutate
		// the shared base when axis = trait_weight:<name>.
		if len(req.Base.Traits) > 0 {
			scen.Traits = append([]TraitConfig(nil), req.Base.Traits...)
		}
		normalizeRequest(&scen)
		if err := applyForAxis(&scen, req.Axis, v); err != nil {
			return fmt.Errorf("scenario %d (%s=%g): %v", i+1, req.Axis, v, err)
		}
		strategies := buildStrategyConfigs(scen)
		if err := validateRequest(scen, len(strategies)); err != nil {
			return fmt.Errorf("scenario %d (%s=%g) invalid: %v", i+1, req.Axis, v, err)
		}
		total += simulationBudget(scen, len(strategies))
	}
	if total > sensitivityBudgetCap {
		return fmt.Errorf("sensitivity sweep budget too high: sum of per-scenario budgets must be <= %d; got %d (reduce population, markers, generations, replicates, or number of values)", sensitivityBudgetCap, total)
	}
	return nil
}

func sensitivityStartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var req SensitivityRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := authorizePrivateDataset(r, req.Base.Dataset); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	normalizeRequest(&req.Base)
	if err := validateSensitivityRequest(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := createSensJob()
	go runSensitivityJob(id, req)
	writeJSON(w, SimJobStartResponse{JobID: id})
}

func sensitivityStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	sensJobStore.Lock()
	job, ok := sensJobStore.Jobs[id]
	sensJobStore.Unlock()
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	writeJSON(w, SensitivityJobStatus{
		JobID:   job.ID,
		Percent: job.Percent,
		Message: job.Message,
		Done:    job.Done,
		Error:   job.Error,
		Result:  job.Result,
	})
}

func createSensJob() string {
	sensJobStore.Lock()
	defer sensJobStore.Unlock()
	sensJobStore.NextID++
	id := fmt.Sprintf("sens-%d-%d", time.Now().UnixNano(), sensJobStore.NextID)
	sensJobStore.Jobs[id] = &sensJob{ID: id, CreatedAt: time.Now(), UpdatedAt: time.Now(), Message: "queued"}
	return id
}

func updateSensJob(id string, percent float64, message string, result *SensitivityResult, errMsg string, done bool) {
	sensJobStore.Lock()
	defer sensJobStore.Unlock()
	job, ok := sensJobStore.Jobs[id]
	if !ok {
		return
	}
	if percent >= 0 {
		job.Percent = percent
	}
	if message != "" {
		job.Message = message
	}
	if result != nil {
		job.Result = result
	}
	if errMsg != "" {
		job.Error = errMsg
	}
	if done {
		job.Done = true
	}
	job.UpdatedAt = time.Now()
}

func runSensitivityJob(jobID string, req SensitivityRequest) {
	// v0.7.28 — Issue 29. Resolve cardinality + per-scenario apply
	// uniformly across both value families.
	n := scenarioCount(req)
	scenarios := make([]SensitivityScenario, 0, n)
	for i := 0; i < n; i++ {
		scen := req.Base
		// v0.7.23 — deep-copy traits for trait_weight axis.
		if len(req.Base.Traits) > 0 {
			scen.Traits = append([]TraitConfig(nil), req.Base.Traits...)
		}
		normalizeRequest(&scen)
		var axisValue float64
		var axisLabel string
		if req.Axis == axisClimateScenario {
			cs := req.ClimateValues[i]
			scen.Climate = &cs
			axisValue = float64(i)
			axisLabel = fmt.Sprintf("%s (sev %.2f)", cs.Mode, cs.Severity)
		} else {
			axisValue = req.Values[i]
			if err := applyForAxis(&scen, req.Axis, axisValue); err != nil {
				updateSensJob(jobID, 100, "", nil, fmt.Sprintf("scenario %d (%s=%g): %v", i+1, req.Axis, axisValue, err), true)
				return
			}
		}
		// Outer progress at scenario boundary: i scenarios are done, current
		// is about to start. Inner ticks below smoothly fill the gap.
		baseLabel := fmt.Sprintf("scenario %d/%d: %s", i+1, n, req.Axis)
		if axisLabel != "" {
			baseLabel += " = " + axisLabel
		} else {
			baseLabel += fmt.Sprintf("=%g", axisValue)
		}
		updateSensJob(jobID, 100.0*float64(i)/float64(n), baseLabel+" — 0%", nil, "", false)

		// v0.7.17: propagate inner progress so the bar moves continuously.
		// Inner pct (0–100 of one scenario) maps to outer pct
		// (i*100 + inner) / n across the whole sweep. We keep the simulate
		// engine's own message — it has more detail than we'd synthesise here.
		scenIdx := i
		scenLabel := baseLabel
		innerProgress := func(innerPct int, innerMsg string) {
			if innerPct < 0 {
				innerPct = 0
			} else if innerPct > 100 {
				innerPct = 100
			}
			outer := (float64(scenIdx)*100.0 + float64(innerPct)) / float64(n)
			msg := scenLabel + fmt.Sprintf(" — %d%%", innerPct)
			if innerMsg != "" {
				msg += " (" + innerMsg + ")"
			}
			updateSensJob(jobID, outer, msg, nil, "", false)
		}
		resp, err := runSimulationWithCallbacks(scen,
			innerProgress,
			func(AFSSnapshot) {}) // snapshots ignored on sweep — chart noise.
		if err != nil {
			updateSensJob(jobID, 100, "", nil, fmt.Sprintf("scenario %d (%s) failed: %v", i+1, scenLabel, err), true)
			return
		}
		sc := summarizeScenario(axisValue, resp)
		sc.AxisLabel = axisLabel
		scenarios = append(scenarios, sc)
	}
	result := assembleSensitivityResult(req, scenarios)
	updateSensJob(jobID, 100, "complete", result, "", true)
}

// v0.7.28 — Issue 29. Scenario count across both value families.
func scenarioCount(req SensitivityRequest) int {
	if req.Axis == axisClimateScenario {
		return len(req.ClimateValues)
	}
	return len(req.Values)
}

// summarizeScenario picks the headline numbers we want in the sweep table
// for one finished simulation. BestFeasibleCode is only populated when
// constraints are applied; when no constraints are active we fall back to
// BestRiskAdjustedCode (the de facto recommendation in that mode).
func summarizeScenario(axisValue float64, resp SimResponse) SensitivityScenario {
	code := resp.Decision.BestFeasibleCode
	name := resp.Decision.BestFeasibleName
	if code == "" {
		code = resp.Decision.BestRiskAdjustedCode
		// BestRiskAdjustedName doesn't exist; derive from strategies list.
		for _, s := range resp.Strategies {
			if s.Code == code {
				name = s.Name
				break
			}
		}
	}
	out := SensitivityScenario{
		AxisValue:        axisValue,
		BestFeasibleCode: code,
		BestFeasibleName: name,
		InfeasibleNote:   resp.Decision.FeasibilityNote,
	}
	if code == "" {
		return out
	}
	for _, s := range resp.Strategies {
		if s.Code == code {
			out.GeneticGain = s.Final.GeneticGain
			out.Diversity = s.Final.Diversity
			out.Inbreeding = s.Final.Inbreeding
			out.RareUsefulLost = s.Final.RareUsefulLost
			out.CombinedRisk = combinedRisk(s.Final)
			break
		}
	}
	return out
}

// assembleSensitivityResult derives the stable/fragile verdict by comparing
// the BestFeasibleCode of each scenario against the baseline. Baseline = the
// scenario whose axis value is closest to the value already in req.Base.
func assembleSensitivityResult(req SensitivityRequest, scenarios []SensitivityScenario) *SensitivityResult {
	baselineValue := baselineValueForAxis(req)
	// v0.7.28 — Issue 29. For the climate_scenario axis, baselineValue
	// IS the index (returned by baselineValueForAxis); for numeric
	// axes we still need nearestIndex to find the closest scenario.
	var baselineIdx int
	if req.Axis == axisClimateScenario {
		baselineIdx = int(baselineValue)
	} else {
		baselineIdx = nearestIndex(req.Values, baselineValue)
	}
	var baselineCode, baselineName string
	if baselineIdx >= 0 && baselineIdx < len(scenarios) {
		baselineCode = scenarios[baselineIdx].BestFeasibleCode
		baselineName = scenarios[baselineIdx].BestFeasibleName
	}
	switches := 0
	infeasibleCount := 0
	for i := range scenarios {
		scenarios[i].BaselineMatch = scenarios[i].BestFeasibleCode == baselineCode && baselineCode != ""
		if !scenarios[i].BaselineMatch && scenarios[i].BestFeasibleCode != "" && baselineCode != "" {
			switches++
		}
		if scenarios[i].BestFeasibleCode == "" {
			infeasibleCount++
		}
	}
	summary := SensitivitySummary{
		BaselineAxisValue: baselineValue,
		BaselineBestCode:  baselineCode,
		BaselineBestName:  baselineName,
		StrategySwitches:  switches,
	}
	switch {
	case infeasibleCount > 0 && infeasibleCount < len(scenarios):
		summary.Stable = false
		summary.Verdict = "fragile"
		summary.Notes = append(summary.Notes, fmt.Sprintf("%d of %d scenarios produced no feasible strategy — constraints are at the edge of what this configuration can deliver.", infeasibleCount, len(scenarios)))
	case infeasibleCount == len(scenarios):
		summary.Stable = false
		summary.Verdict = "inconclusive"
		summary.Notes = append(summary.Notes, "All scenarios infeasible. Loosen constraints (max_inbreeding, max_diversity_loss, etc.) or reduce min thresholds.")
	case switches == 0:
		summary.Stable = true
		summary.Verdict = "stable"
		// v0.7.28 — Issue 29. Axis-specific wording so climate sweeps
		// read as "climate-robust" rather than "robust to changes in
		// climate_scenario".
		if req.Axis == axisClimateScenario {
			summary.Notes = append(summary.Notes, fmt.Sprintf("Best-feasible strategy %q stays the same across all %d climate scenarios. The recommendation is climate-robust within the sampled stress modes.", baselineCode, len(scenarios)))
		} else {
			summary.Notes = append(summary.Notes, fmt.Sprintf("Best-feasible strategy %q stays the same across all %d scenarios. The recommendation is robust to changes in %s within the sampled range.", baselineCode, len(scenarios), req.Axis))
		}
	default:
		summary.Stable = false
		summary.Verdict = "fragile"
		if req.Axis == axisClimateScenario {
			summary.Notes = append(summary.Notes, fmt.Sprintf("Best-feasible strategy switches in %d of %d climate scenarios. The single-run recommendation %q is weather-year dependent — choose the strategy explicitly for the expected stress regime.", switches, len(scenarios), baselineCode))
		} else {
			summary.Notes = append(summary.Notes, fmt.Sprintf("Best-feasible strategy changes in %d of %d scenarios. The single-run recommendation %q may not hold if %s differs from the baseline.", switches, len(scenarios), baselineCode, req.Axis))
		}
	}
	result := &SensitivityResult{
		Base:          req.Base,
		Axis:          req.Axis,
		Values:        req.Values,
		ClimateValues: req.ClimateValues,
		Scenarios:     scenarios,
		Summary:       summary,
	}
	// v0.7.28 — Issue 31. Attach the climate-robustness section when
	// the axis is climate_scenario and we sampled ≥ 2 scenarios.
	result.ClimateRobustness = buildClimateRobustnessSection(req, scenarios, summary)
	return result
}

func baselineValueForAxis(req SensitivityRequest) float64 {
	switch req.Axis {
	case "heritability":
		return req.Base.Heritability
	case "selection_percent":
		return req.Base.SelectionPercent
	case "generations":
		return float64(req.Base.Generations)
	case axisClimateScenario:
		// v0.7.28 — Issue 29. Baseline = the "normal" mode if the
		// operator included it; otherwise scenario 0. Returns the
		// chosen index as float64 so the nearest-index lookup
		// downstream picks the right row.
		for i, cs := range req.ClimateValues {
			if cs.Mode == "normal" {
				return float64(i)
			}
		}
		return 0
	}
	return 0
}

func nearestIndex(values []float64, target float64) int {
	if len(values) == 0 {
		return -1
	}
	best := 0
	bestDist := abs64(values[0] - target)
	for i := 1; i < len(values); i++ {
		d := abs64(values[i] - target)
		if d < bestDist {
			bestDist = d
			best = i
		}
	}
	return best
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
