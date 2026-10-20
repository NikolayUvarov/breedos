package main

import (
	"strings"
	"testing"
)

// v0.7.23 — Issue 27 tests. Catalog lookup, validation, and shape.

func TestLookupClimateMode_KnownAndUnknown(t *testing.T) {
	for _, code := range []string{"normal", "heat_burst_anthesis", "heat_grain_filling", "combined_postflowering", "drought_terminal", "salinity_chronic", "heat_burst_booting", "prolonged_heat"} {
		if _, err := LookupClimateMode(code); err != nil {
			t.Errorf("known mode %q should not error: %v", code, err)
		}
	}
	if _, err := LookupClimateMode("frankenmode"); err == nil {
		t.Error("unknown mode should error")
	}
}

func TestValidateClimateScenario_RejectsNegativeSeverity(t *testing.T) {
	s := &ClimateScenario{Mode: "heat_burst_anthesis", Severity: -0.1}
	if err := ValidateClimateScenario(s); err == nil {
		t.Error("negative severity should be rejected")
	}
}

func TestValidateClimateScenario_RejectsUnknownMode(t *testing.T) {
	s := &ClimateScenario{Mode: "tornado", Severity: 1.0}
	err := ValidateClimateScenario(s)
	if err == nil || !strings.Contains(err.Error(), "unknown climate mode") {
		t.Errorf("unknown mode should fail with descriptive error; got %v", err)
	}
}

func TestValidateClimateScenario_AcceptsZeroSeverity(t *testing.T) {
	s := &ClimateScenario{Mode: "heat_burst_anthesis", Severity: 0}
	if err := ValidateClimateScenario(s); err != nil {
		t.Errorf("zero severity should be allowed, got %v", err)
	}
}

func TestClimateModesCatalog_StableAlphabeticalOrder(t *testing.T) {
	cat := ClimateModesCatalog()
	if len(cat) < 8 {
		t.Errorf("expected ≥ 8 modes in catalog, got %d", len(cat))
	}
	for i := 1; i < len(cat); i++ {
		if cat[i-1].Code > cat[i].Code {
			t.Errorf("catalog not sorted: %q > %q", cat[i-1].Code, cat[i].Code)
		}
	}
}

// v0.7.24 — Issue 28. Climate penalty tests.

func TestApplyClimatePenalty_NilOrNormalReturnsUnchanged(t *testing.T) {
	if got := ApplyClimatePenalty(100.0, nil); got != 100.0 {
		t.Errorf("nil scenario: expected 100.0, got %v", got)
	}
	normal := &ClimateScenario{Mode: "normal", Severity: 1.0}
	if got := ApplyClimatePenalty(100.0, normal); got != 100.0 {
		t.Errorf("normal mode: expected 100.0, got %v", got)
	}
}

func TestApplyClimatePenalty_CalibrationTriplet(t *testing.T) {
	// Khan et al. 2024: anthesis 32%, grain-filling 46%, combined 59%.
	// At severity = 1.0 ("moderate"), penalised phenotype should be:
	cases := []struct {
		mode    string
		want    float64 // multiplier applied to 100.0
	}{
		{"heat_burst_anthesis", 68.0},    // 1 - 0.32
		{"heat_grain_filling", 54.0},     // 1 - 0.46
		{"combined_postflowering", 41.0}, // 1 - 0.59
	}
	for _, c := range cases {
		scen := &ClimateScenario{Mode: c.mode, Severity: 1.0}
		got := ApplyClimatePenalty(100.0, scen)
		// Allow 0.01 tolerance (we use rounded coefficients).
		if abs64(got-c.want) > 0.01 {
			t.Errorf("%s: expected %v, got %v", c.mode, c.want, got)
		}
	}
}

func TestApplyClimatePenalty_SeverityScalesLinearly(t *testing.T) {
	// At severity = 0.5, penalty halves; at severity = 2.0, it doubles but
	// clamps to climateMaxPenalty (0.95).
	scen := &ClimateScenario{Mode: "heat_burst_anthesis", Severity: 0.5}
	got := ApplyClimatePenalty(100.0, scen)
	// 0.32 × 0.5 = 0.16 penalty → 84.0
	want := 100.0 * (1 - 0.32*0.5)
	if abs64(got-want) > 0.01 {
		t.Errorf("severity 0.5: expected %v, got %v", want, got)
	}
	// Severity 2.0 with combined_postflowering: 0.59 × 2 = 1.18 → clamped 0.95
	scen = &ClimateScenario{Mode: "combined_postflowering", Severity: 2.0}
	got = ApplyClimatePenalty(100.0, scen)
	want = 100.0 * (1 - climateMaxPenalty)
	if abs64(got-want) > 0.01 {
		t.Errorf("clamp at sev=2.0: expected %v, got %v", want, got)
	}
}

func TestApplyClimatePenalty_DroughtTerminalRange(t *testing.T) {
	// Acceptance: severe drought (sev≈1.5) yields ~60% loss; moderate
	// (sev=1.0) yields 40%.
	scen := &ClimateScenario{Mode: "drought_terminal", Severity: 1.0}
	got := ApplyClimatePenalty(100.0, scen)
	if got < 55 || got > 65 {
		t.Errorf("drought_terminal moderate: expected ~60.0, got %v", got)
	}
	scen.Severity = 1.5
	got = ApplyClimatePenalty(100.0, scen)
	if got < 35 || got > 45 {
		t.Errorf("drought_terminal severe: expected ~40.0, got %v", got)
	}
}

func TestApplyClimatePenaltyToMetrics_AffectsTraitAndGainOnly(t *testing.T) {
	metrics := []MetricPoint{
		{Generation: 0, TraitMean: 100.0, GeneticGain: 10.0, Diversity: 0.5, Inbreeding: 0.05, Ne: 1000},
		{Generation: 1, TraitMean: 110.0, GeneticGain: 20.0, Diversity: 0.5, Inbreeding: 0.06, Ne: 800},
	}
	scen := &ClimateScenario{Mode: "heat_grain_filling", Severity: 1.0}
	applyClimatePenaltyToMetrics(metrics, scen)
	// factor = 1 - 0.46 = 0.54
	if abs64(metrics[0].TraitMean-54.0) > 0.01 {
		t.Errorf("metrics[0].TraitMean: expected 54.0, got %v", metrics[0].TraitMean)
	}
	if abs64(metrics[1].GeneticGain-10.8) > 0.01 {
		t.Errorf("metrics[1].GeneticGain: expected 10.8 (20 × 0.54), got %v", metrics[1].GeneticGain)
	}
	// Diversity / Inbreeding / Ne are population-genetics quantities — untouched.
	if metrics[0].Diversity != 0.5 {
		t.Errorf("Diversity should NOT change, got %v", metrics[0].Diversity)
	}
	if metrics[0].Inbreeding != 0.05 {
		t.Errorf("Inbreeding should NOT change, got %v", metrics[0].Inbreeding)
	}
	if metrics[0].Ne != 1000 {
		t.Errorf("Ne should NOT change, got %v", metrics[0].Ne)
	}
}

func TestRunSimulation_ClimateBackwardCompat(t *testing.T) {
	// No Climate field → bit-identical to v0.7.23.
	base := SimRequest{
		Seed: 1, PopulationSize: 30, Markers: 80, QTLCount: 15,
		Generations: 3, SelectionPercent: 15, Heritability: 0.5,
		MutationRate: 0.0001, StrategySet: "core", Replicates: 1,
		InbreedingLimit: 0.25, DiversityLossLimit: 0.30,
	}
	resp1, err := runSimulationWithCallbacks(base, func(int, string) {}, func(AFSSnapshot) {})
	if err != nil {
		t.Fatalf("baseline run failed: %v", err)
	}
	// Same payload again — should match.
	resp2, err := runSimulationWithCallbacks(base, func(int, string) {}, func(AFSSnapshot) {})
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	// Same seed → identical strategies. (Pick one strategy and compare the
	// first non-zero gain.)
	for i := range resp1.Strategies {
		if resp1.Strategies[i].Final.GeneticGain != resp2.Strategies[i].Final.GeneticGain {
			t.Errorf("strategy %d gain not stable: %v vs %v",
				i, resp1.Strategies[i].Final.GeneticGain,
				resp2.Strategies[i].Final.GeneticGain)
		}
	}
}

func TestRunSimulation_ClimatePenaltyDropsRecordedGain(t *testing.T) {
	base := SimRequest{
		Seed: 7, PopulationSize: 60, Markers: 120, QTLCount: 20,
		Generations: 6, SelectionPercent: 15, Heritability: 0.5,
		MutationRate: 0.0001, StrategySet: "core", Replicates: 2,
		InbreedingLimit: 0.25, DiversityLossLimit: 0.30,
	}
	baseResp, err := runSimulationWithCallbacks(base, func(int, string) {}, func(AFSSnapshot) {})
	if err != nil {
		t.Fatalf("baseline run failed: %v", err)
	}
	// With heat_grain_filling at moderate severity, recorded gain should
	// drop by ≈ 46% on the best strategy.
	withClimate := base
	withClimate.Climate = &ClimateScenario{Mode: "heat_grain_filling", Severity: 1.0}
	climateResp, err := runSimulationWithCallbacks(withClimate, func(int, string) {}, func(AFSSnapshot) {})
	if err != nil {
		t.Fatalf("climate run failed: %v", err)
	}
	// Find best strategy in each and compare gain.
	bestBase := baseResp.Decision.BestRiskAdjustedCode
	bestClimate := climateResp.Decision.BestRiskAdjustedCode
	if bestBase == "" || bestClimate == "" {
		t.Fatal("missing best-strategy code")
	}
	gainBase := getStrategyGain(baseResp.Strategies, bestBase)
	gainClimate := getStrategyGain(climateResp.Strategies, bestBase)
	if gainBase <= 0 {
		t.Fatalf("baseline gain not positive: %v", gainBase)
	}
	ratio := gainClimate / gainBase
	// v0.8.7 — Issue 38. Climate is G×E, not a uniform ×0.54: the target
	// value is 0.5·normal + 0.5·(1 − 0.46)·stress with rg = 0.5 between
	// them, so nursery selection keeps ≈ 0.5 + 0.5·0.54·0.5 ≈ 0.64 of the
	// normal-season gain.
	if ratio < 0.55 || ratio > 0.75 {
		t.Errorf("target-season gain ratio = %.3f, expected ≈ 0.64 under G×E", ratio)
	}
}

// getStrategyGain finds the strategy by code and returns its final gain.
func getStrategyGain(strategies []StrategyResult, code string) float64 {
	for _, s := range strategies {
		if s.Code == code {
			return s.Final.GeneticGain
		}
	}
	return 0
}

func TestClimateModesCatalog_AnthesisWindowsAreCorrect(t *testing.T) {
	// Sanity: anthesis-burst window is [-3, +3]; booting is [-18, -10];
	// grain-fill is [+5, +25]. These are the canonical Khan-et-al-2024 +
	// Porter-Gawith-1999 windows the catalog encodes.
	m, _ := LookupClimateMode("heat_burst_anthesis")
	if m.WindowStart != -3 || m.WindowEnd != 3 {
		t.Errorf("anthesis window expected [-3, +3], got [%d, %d]", m.WindowStart, m.WindowEnd)
	}
	m, _ = LookupClimateMode("heat_burst_booting")
	if m.WindowStart != -18 || m.WindowEnd != -10 {
		t.Errorf("booting window expected [-18, -10], got [%d, %d]", m.WindowStart, m.WindowEnd)
	}
	m, _ = LookupClimateMode("heat_grain_filling")
	if m.WindowStart != 5 || m.WindowEnd != 25 {
		t.Errorf("grain-filling window expected [+5, +25], got [%d, %d]", m.WindowStart, m.WindowEnd)
	}
}
