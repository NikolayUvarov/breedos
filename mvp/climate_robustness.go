package main

// v0.7.28 — Issue 31. Climate-robustness Decision Report section.
//
// Attached to SensitivityResult when the sweep was run with
// axis = climate_scenario and at least 2 scenarios were sampled. The
// section turns the table-of-rows the operator just received into a
// plain-language verdict their planning meeting can act on.
//
// Honest scope:
//   - Reads only quantities already in scope on a finished sweep
//     (per-scenario best-feasible code, baseline match, sweep verdict).
//   - Does NOT re-run any simulation or aggregate beyond the sweep
//     result.
//   - The ancestral-introgression paragraph is conditional on the
//     base request having included ancestral_introgression (i.e.
//     req.Base.AncestralIntroPercent > 0 AND strategy_set advanced).
//   - The honesty caveat is always present so the operator does not
//     mistake the population-uniform penalty for per-QTL tolerance
//     modelling.

import (
	"fmt"
	"sort"
	"strings"
)

// ClimateRobustness is the structured section. Each field renders as
// one paragraph in the report; empty fields are skipped in the UI.
type ClimateRobustness struct {
	Headline         string `json:"headline"`
	FailureModes     string `json:"failure_modes,omitempty"`
	AlternativeAdvice string `json:"alternative_advice,omitempty"`
	AncestralAdvice  string `json:"ancestral_advice,omitempty"`
	HonestyCaveat    string `json:"honesty_caveat"`
}

// buildClimateRobustnessSection produces the section for the sweep
// result. Returns nil when the section would not render (non-climate
// axis or fewer than 2 scenarios). Pure function — pulls only from
// the inputs; safe to call after assemble finishes.
func buildClimateRobustnessSection(req SensitivityRequest, scenarios []SensitivityScenario, summary SensitivitySummary) *ClimateRobustness {
	if req.Axis != axisClimateScenario {
		return nil
	}
	if len(scenarios) < 2 {
		return nil
	}

	out := &ClimateRobustness{
		HonestyCaveat: "Single-trait runs model each stress as genotype × environment interaction: normal- and stress-season performance are genetically correlated traits with shared, yield-potential-only and stress-only QTL (default rg per mode, 0.4–0.6, overridable), so rankings can change between scenarios. There is no location × year trial structure, and multi-trait runs still use the legacy uniform penalty.",
	}

	// Count how many scenarios match the baseline strategy.
	matched := 0
	failureModes := make([]string, 0)
	failureBests := make(map[string][]string) // best alternative code → list of mode labels.
	for i, s := range scenarios {
		if s.BaselineMatch && s.BestFeasibleCode != "" {
			matched++
			continue
		}
		// Failure or infeasible: collect a label for the report.
		label := s.AxisLabel
		if label == "" && i < len(req.ClimateValues) {
			label = req.ClimateValues[i].Mode
		}
		if label == "" {
			label = fmt.Sprintf("scenario %d", i+1)
		}
		failureModes = append(failureModes, label)
		if s.BestFeasibleCode != "" && s.BestFeasibleCode != summary.BaselineBestCode {
			failureBests[s.BestFeasibleCode] = append(failureBests[s.BestFeasibleCode], label)
		}
	}

	switch {
	case summary.BaselineBestCode == "":
		out.Headline = fmt.Sprintf("Across %d sampled climate scenarios, no baseline strategy was selected — the constraint engine produced no feasible recommendation under the operator's baseline weather year. Re-evaluate constraints before reading the per-scenario rows.", len(scenarios))
	case matched == len(scenarios):
		out.Headline = fmt.Sprintf("The recommendation \"%s\" stays best in all %d sampled climate scenarios — it is climate-robust within this sample. Operate against the baseline plan.", summary.BaselineBestCode, len(scenarios))
	default:
		out.Headline = fmt.Sprintf("The recommendation \"%s\" stays best in %d of %d sampled climate scenarios. The remaining %d are weather-year dependent.", summary.BaselineBestCode, matched, len(scenarios), len(failureModes))
	}

	if len(failureModes) > 0 {
		out.FailureModes = fmt.Sprintf("Baseline strategy loses out under: %s.", strings.Join(failureModes, ", "))
	}

	// Alternative-strategy advice: sort by code so the rendered text is
	// stable across runs with the same input.
	if len(failureBests) > 0 {
		codes := make([]string, 0, len(failureBests))
		for code := range failureBests {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		bits := make([]string, 0, len(codes))
		for _, code := range codes {
			bits = append(bits, fmt.Sprintf("If %s is the expected stress mode in the planning horizon, the better hedge is \"%s\".", strings.Join(failureBests[code], " or "), code))
		}
		out.AlternativeAdvice = strings.Join(bits, " ")
	}

	// Ancestral-introgression paragraph — only when the base run
	// included that strategy. We don't have the strategy_count here,
	// but we have the canonical signal: AncestralIntroPercent > 0
	// (which is what surfaces the strategy in buildStrategyConfigs).
	if req.Base.AncestralIntroPercent > 0 {
		// Look for ancestral_introgression as a best-feasible
		// substitution in any failure scenario.
		ancestralWinsIn := failureBests["ancestral_introgression"]
		switch {
		case len(ancestralWinsIn) > 0:
			out.AncestralAdvice = fmt.Sprintf("Ancestral introgression (%.0f%% landrace seeding) wins the best-feasible verdict under %s. Its landrace lines carry stress-adapted alleles but fewer yield-potential alleles, so it trades zero-stress performance for stress performance. The trade pays off only when selection sees the stress (target-environment or managed-stress trials) and the planning horizon expects the named modes often.", req.Base.AncestralIntroPercent, strings.Join(ancestralWinsIn, " or "))
		case summary.BaselineBestCode == "ancestral_introgression":
			out.AncestralAdvice = fmt.Sprintf("Ancestral introgression (%.0f%% landrace seeding) is the baseline best-feasible — the climate-stressed scenarios in this sweep already favour the landrace-augmented founder pool. Quantify the zero-stress trait drag separately before committing.", req.Base.AncestralIntroPercent)
		default:
			out.AncestralAdvice = fmt.Sprintf("Ancestral introgression was offered (%.0f%% landrace seeding configured) but does not win any sampled scenario — the modern baseline outperforms across the modes sampled. Re-evaluate if the planning horizon shifts to higher-severity stress modes.", req.Base.AncestralIntroPercent)
		}
	}

	return out
}
