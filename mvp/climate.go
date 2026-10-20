package main

// v0.7.23 — Issue 27 (Climate-pack foundation). Climate-stress mode types
// and catalog. NO simulation impact here — this file only declares the
// data structures and validation. Issue 28 (per-stage phenotype penalty
// model) wires these into the simulator.
//
// Catalogue updated per the 2026-05-28 audit (Khan et al. 2024 calibration):
//
//   * heat_burst_anthesis    32% yield loss at moderate severity (Khan 2024)
//   * heat_grain_filling     46% yield loss at moderate severity
//   * combined_postflowering 59% yield loss (combined heat + drought)
//   * heat_burst_booting     spike fertility down (booting stage stress)
//   * prolonged_heat         grain weight down (less severe than combined)
//   * drought_terminal       30-60% at severe drought
//   * salinity_chronic       ~20% at moderate EC dS/m
//   * normal                 baseline
//
// References:
//   Khan et al. 2024 "Effects of Heat Stress during Anthesis and Grain Filling
//     Stages on Some Physiological and Agronomic Traits" — Plants 2024
//     (calibration triplet 32% / 46% / 59%).
//   Porter & Gawith 1999 "Temperatures and the growth and development of
//     wheat: a review" — Eur J Agron (peak sensitivity windows around anthesis).
//   FAO salinity yield-loss tables (~20% at moderate EC).

import (
	"errors"
	"fmt"
)

// ClimateStressMode is the catalogue entry for a single stress mode. The
// catalog is static and looked up by Code. Each entry documents the window
// (in days relative to anthesis), the severity dimension, and the effect
// family (consumed by Issue 28 to compute per-stage penalties).
type ClimateStressMode struct {
	Code             string  `json:"code"`
	Name             string  `json:"name"`
	WindowStart      int     `json:"window_start"` // days relative to anthesis (negative = before)
	WindowEnd        int     `json:"window_end"`
	SeverityDim      string  `json:"severity_dim"`
	EffectFamily     string  `json:"effect_family"`
	BaselineSeverity float64 `json:"baseline_severity"`
}

// ClimateScenario is the run-level user input — Mode references a catalog
// entry by Code; Severity scales the effect (Issue 28 multiplies).
type ClimateScenario struct {
	Mode     string  `json:"mode"`
	Severity float64 `json:"severity"`
	// v0.8.7 — Issue 38 (gxe.go). Frequency is the share of seasons under
	// this stress (default 0.5); RG overrides the mode's genetic
	// correlation between normal- and stress-season performance.
	Frequency float64 `json:"frequency,omitempty"`
	RG        float64 `json:"rg,omitempty"`
}

// climateModes is the immutable catalogue. Adding a new mode requires
// adding one entry here AND its phenotype-penalty calibration in Issue 28.
var climateModes = map[string]ClimateStressMode{
	"normal": {
		Code:             "normal",
		Name:             "Baseline (no climate stress)",
		WindowStart:      0,
		WindowEnd:        0,
		SeverityDim:      "n/a",
		EffectFamily:     "baseline",
		BaselineSeverity: 0,
	},
	"heat_burst_anthesis": {
		Code:             "heat_burst_anthesis",
		Name:             "Heat burst at anthesis",
		WindowStart:      -3,
		WindowEnd:        +3,
		SeverityDim:      "peak temp °C above 30",
		EffectFamily:     "sink_reduction",
		BaselineSeverity: 1.0,
	},
	"heat_burst_booting": {
		Code:             "heat_burst_booting",
		Name:             "Heat burst at booting (−18 to −10 d before anthesis)",
		WindowStart:      -18,
		WindowEnd:        -10,
		SeverityDim:      "peak temp °C above 30",
		EffectFamily:     "spike_fertility",
		BaselineSeverity: 1.0,
	},
	"heat_grain_filling": {
		Code:             "heat_grain_filling",
		Name:             "Heat at mid-grain-filling (anthesis +5 to +25 d)",
		WindowStart:      +5,
		WindowEnd:        +25,
		SeverityDim:      "days × °C above 30 (sum)",
		EffectFamily:     "grain_weight",
		BaselineSeverity: 1.0,
	},
	"combined_postflowering": {
		Code:             "combined_postflowering",
		Name:             "Combined heat + drought, anthesis through maturity",
		WindowStart:      0,
		WindowEnd:        +30,
		SeverityDim:      "composite stress index",
		EffectFamily:     "combined_yield",
		BaselineSeverity: 1.0,
	},
	"prolonged_heat": {
		Code:             "prolonged_heat",
		Name:             "Prolonged heat, anthesis through physiological maturity",
		WindowStart:      0,
		WindowEnd:        +30,
		SeverityDim:      "days × °C above 30 (sum)",
		EffectFamily:     "grain_weight",
		BaselineSeverity: 1.0,
	},
	"drought_terminal": {
		Code:             "drought_terminal",
		Name:             "Terminal drought (last 25% of cycle)",
		WindowStart:      +1,
		WindowEnd:        +35,
		SeverityDim:      "water-deficit index",
		EffectFamily:     "yield_component",
		BaselineSeverity: 1.0,
	},
	"salinity_chronic": {
		Code:             "salinity_chronic",
		Name:             "Chronic salinity (whole cycle)",
		WindowStart:      -120,
		WindowEnd:        +30,
		SeverityDim:      "EC dS/m above tolerance",
		EffectFamily:     "yield_component",
		BaselineSeverity: 1.0,
	},
}

// LookupClimateMode returns the catalog entry for the given code or an
// error if the code is not recognised. Used by validation and (in Issue 28)
// by the phenotype-penalty computation.
func LookupClimateMode(code string) (ClimateStressMode, error) {
	m, ok := climateModes[code]
	if !ok {
		return ClimateStressMode{}, fmt.Errorf("unknown climate mode %q (known: %s)", code, knownClimateModes())
	}
	return m, nil
}

// knownClimateModes returns a comma-joined list of code names for error
// messages. Order is non-deterministic (map iteration); fine for diagnostic
// strings.
func knownClimateModes() string {
	out := ""
	first := true
	for k := range climateModes {
		if !first {
			out += ", "
		}
		out += k
		first = false
	}
	return out
}

// ValidateClimateScenario checks a single scenario for catalog membership
// and severity bounds. Severity = 0 is allowed (means "no stress" within
// the chosen mode). Negative severity rejected.
func ValidateClimateScenario(scen *ClimateScenario) error {
	if scen == nil {
		return errors.New("climate scenario is nil")
	}
	if _, err := LookupClimateMode(scen.Mode); err != nil {
		return err
	}
	if scen.Severity < 0 {
		return fmt.Errorf("climate severity must be ≥ 0, got %v", scen.Severity)
	}
	if scen.Frequency < 0 || scen.Frequency > 1 {
		return fmt.Errorf("climate frequency must be in [0, 1] (share of stressed seasons), got %v", scen.Frequency)
	}
	if scen.RG < 0 || scen.RG > 1 {
		return fmt.Errorf("climate rg must be in [0, 1], got %v", scen.RG)
	}
	return nil
}

// v0.7.24 — Issue 28. Penalty coefficient per mode, calibrated so that
// severity = 1.0 ("moderate") reproduces the published yield-loss figure.
// Reference: Khan et al. 2024 in MDPI Plants for the 32% / 46% / 59%
// triplet (anthesis / grain-filling / combined post-flowering). Other modes
// calibrated from per-mode literature in the file header.
//
// Runtime: penalty = climateModePenalty[mode] × scenario.Severity, clamped
// to [0, 0.95] so phenotypes never collapse to zero in pathological cases.
var climateModePenalty = map[string]float64{
	"normal":                 0.00,
	"heat_burst_anthesis":    0.32, // Khan 2024
	"heat_burst_booting":     0.20, // smaller than anthesis (spike fertility)
	"heat_grain_filling":     0.46, // Khan 2024
	"combined_postflowering": 0.59, // Khan 2024 (heat + drought)
	"prolonged_heat":         0.30, // less severe than combined
	"drought_terminal":       0.40, // moderate; severe at sev≈1.5 → 0.60
	"salinity_chronic":       0.20, // ~20% at moderate EC
}

// climateMaxPenalty caps the multiplicative reduction so phenotypes can't
// collapse to zero — keeps the simulator numerically sane under
// pathological inputs.
const climateMaxPenalty = 0.95

// climatePenaltyFor returns the multiplicative penalty (in [0, climateMaxPenalty])
// for the given scenario. Returns 0 for nil / normal / unknown mode.
func climatePenaltyFor(scenario *ClimateScenario) float64 {
	if scenario == nil || scenario.Mode == "" || scenario.Mode == "normal" {
		return 0
	}
	coef, ok := climateModePenalty[scenario.Mode]
	if !ok {
		return 0
	}
	p := coef * scenario.Severity
	if p < 0 {
		return 0
	}
	if p > climateMaxPenalty {
		return climateMaxPenalty
	}
	return p
}

// ApplyClimatePenalty returns phenotype × (1 − penalty) for the given
// scenario. Backward-compatible: nil scenario, empty mode, or "normal" all
// return the phenotype unchanged.
func ApplyClimatePenalty(phenotype float64, scenario *ClimateScenario) float64 {
	p := climatePenaltyFor(scenario)
	if p == 0 {
		return phenotype
	}
	return phenotype * (1 - p)
}

// applyClimatePenaltyToMetrics scales TraitMean and GeneticGain on every
// MetricPoint in the slice by (1 − penalty). Other fields (Diversity,
// Inbreeding, AlleleDrift, RareUsefulLost, FixedLoci, Ne) are population-
// genetics quantities unaffected by climate stress and stay untouched.
// Uniform-across-population assumption: penalty preserves rank, so selection
// is unaffected; only recorded mean / gain trajectories drop.
func applyClimatePenaltyToMetrics(metrics []MetricPoint, scenario *ClimateScenario) {
	applyClimatePenaltyToMetricsWithDiscount(metrics, scenario, 1.0)
}

// v0.7.28 — Issue 30. Variant that scales the climate penalty by a
// per-strategy discount before applying it. discount = 1.0 (default)
// reproduces the v0.7.24 behaviour bit-for-bit; discount < 1 reduces
// the penalty (used for ancestral_introgression, which the issue
// models as "ancestral lines suffer half the penalty"). discount
// clamped to [0, 1] defensively.
func applyClimatePenaltyToMetricsWithDiscount(metrics []MetricPoint, scenario *ClimateScenario, discount float64) {
	p := climatePenaltyFor(scenario)
	if p == 0 {
		return
	}
	if discount < 0 {
		discount = 0
	}
	if discount > 1 {
		discount = 1
	}
	effective := p * discount
	factor := 1 - effective
	for i := range metrics {
		metrics[i].TraitMean = round4(metrics[i].TraitMean * factor)
		metrics[i].GeneticGain = round4(metrics[i].GeneticGain * factor)
		metrics[i].GainSigma = round4(metrics[i].GainSigma * factor)
	}
}

// ClimateModesCatalog returns a JSON-friendly snapshot of all catalog
// entries — useful for a UI dropdown or /api endpoint. Order is by code
// alphabetical so the UI gets a stable list.
func ClimateModesCatalog() []ClimateStressMode {
	codes := make([]string, 0, len(climateModes))
	for k := range climateModes {
		codes = append(codes, k)
	}
	// simple insertion sort — small list
	for i := 1; i < len(codes); i++ {
		for j := i; j > 0 && codes[j-1] > codes[j]; j-- {
			codes[j-1], codes[j] = codes[j], codes[j-1]
		}
	}
	out := make([]ClimateStressMode, len(codes))
	for i, c := range codes {
		out[i] = climateModes[c]
	}
	return out
}
