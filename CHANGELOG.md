# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.7.39] - 2026-08-24

### Fixed — biological surface stops advertising the prompt substrate

Closes `issues-breedos/33-release-changelog-leak-in-simulate-notes.md`.

**`POST /api/simulate` notes no longer carry a release changelog.**
`buildNotes()` emitted a hand-edited "latest release" note as entry #2 of
the biological simulate response. From v0.7.31 through v0.7.38 that slot
carried promptbio changelog text; at v0.7.38 it was **28,982 characters**,
96% of the whole 30,144-byte notes payload, describing prompt-organism
design patterns. `static/app.js` renders every note verbatim into the demo
Decision Report, so a breeder running a simulation saw a 29 KB wall of
LLM-prompt jargon in position 2 of their breeding report.

- notes payload: **30,144 → 1,162 bytes (−96.1%)**; 8 → 7 entries;
  longest note 28,982 → 352 chars.
- Simulation numbers are **bit-identical** — `decision`, `strategies` and
  `candidate_edits` compare byte-for-byte on the same seed. Presentation only.
- New regression test `TestSimulateNotesStayRunScoped` guards the class of
  defect: per-note length cap (600 chars), a forbidden promptbio vocabulary
  list, and a note-count bound. Verified to fail when the v0.7.38 note is
  reintroduced.

**Demo page kicker no longer lists promptbio modules.** `demo.html` headed
the Selection Strategy Simulator with "Promptbio v0.1 Mapper + v0.2 Diff +
Issue 07 Substrate + v0.3 Evolution Loop". Replaced with the biological
workflow it actually describes: constraints, strategy comparison, Pareto,
decision report, edit-vs-cross-vs-wait.

Release information belongs in this file and in `/api/version`, never in a
simulation result. Both fixes restore the handoff §3.2 contract: no
promptbio change may alter biological BreedOS output, routes or UI.

### Changed

- Version footers across `index*.html`, `demo.html`, `datasets.html`,
  `theory.html`, `promptbio.html` were stale at v0.7.34/v0.7.38; all now
  read v0.7.39.

### Known issues

- `selection_percent` is a 0–100 percent with no range validation; passing a
  fraction (`0.2`) is accepted silently and produces a degenerate 2-parent
  run with F≈0.96 and a full, plausible-looking Decision Report. Tracked in
  `issues-breedos/34-selection-percent-unit-ambiguity.md` — the fix is an API
  contract change and is deliberately not bundled here.


## [0.7.38] - 2026-06-29

### Added — Issue 34 Promptbio v3.1 Prompt Organism Design Patterns

Zoology over the v3.0 anatomy. Closes
`issues-promptbio/34-prompt-organism-design-patterns.md`.

**12 canonical pattern cards.** Each with all 20 PATTERN CARD fields
populated (name / intent / use_when / avoid_when / organism_size /
agency_level / core_organs / optional_organs / genome / epistemology /
decision_planning / runtime / memory / tools / output_phenotype /
failure_modes / required_tests / minimal_prompt_skeleton /
production_architecture / anti_patterns / evolution_path):

- Explainer Cell (micro, A0)
- Research Synthesizer (meso, A1)
- Strategy Advisor (meso, A1)
- Strategy Critic (meso, A1)
- Code Repair (meso, A2)
- Document Review (meso, A1)
- Data Analysis (meso, A2)
- Eval Judge (meso, A1)
- Memory-Aware Companion (meso, A1)
- Agentic Tool-Using (macro, A3)
- High-Assurance Advisor (macro, A1 capped)
- Autopoietic Ecosystem (macro, A4 capped)

**Selection matrix.** 34 niche-keyword rows. Override priority:

1. tools=external_actions OR deployment=agent → Agentic Tool-Using
2. quality=high-assurance OR risk=high → High-Assurance Advisor
3. memory ∈ {project, long-term} → Memory-Aware Companion
4. Niche keyword match → matrix row
5. Default → Strategy Advisor

**8 composition rules (§1688-1737).** `no_agency_without_control /
no_memory_without_truth_or_privacy /
no_research_without_source_evaluation / no_strategy_without_constraints
/ no_critique_without_repair / no_high_assurance_on_low_risk /
no_one_giant_prompt / stable_core_adaptive_pattern`. Compositions
violating a rule emit RuleViolation with rule_id + rule_name + detail + fix.

**10 anti-pattern detectors (§1741-1929).** `God Prompt / Confident
Oracle / Tool Gremlin / Memory Hoarder / Eval Gamer / Paper Shield /
Format Tyrant / Agent Without Stop Condition / Strategy Fantasist /
Document Puppet`. Each runs against any v3.0 PromptOrganismSpec via
the linter endpoint; emits Finding(name, symptom, treatment, evidence).

**API.** Four new endpoints under `/api/promptbio/`:

- `POST /pattern-select` — PatternRecommendation (11-section bundle)
- `POST /pattern-compose` — CompositionPlan with rule violations
- `POST /anti-patterns` — AntiPatternResponse (linter findings)
- `GET  /patterns` — 12 cards + selection matrix + 8 composition rules + 10 anti-patterns

**UI.** v3.1 Design Patterns card on `/promptbio` with task-niche +
target-phenotype + 5 envelope knobs (deployment / risk / tools /
memory / quality), recommended-pattern badge with size + agency,
why-this-pattern rationale, required organs, anti-pattern risks,
next action, copy-paste minimal prompt skeleton, required tests
list, full 20-field card detail, plus 12-tile catalogue grid (click
any tile to re-select).

**Tests.** 22 new in `breedos/mvp/promptbio/patterns/patterns_test.go`:

- 12-card catalogue completeness (all 20 fields populated, no duplicates)
- core_organs subset-of-v3.0 validation
- selector matrix routing across 11 canonical niches
- override priority (tools / quality / memory each fires)
- GTM worked example: Strategy Advisor → composition with Critic +
  Eval Judge passes all 8 rules
- Rule 5 (no critique without repair) fires on Strategy Critic alone
- Rule 6 (no high-assurance on low-risk) fires on HA + Explainer Cell
- Rule 7 (no one giant prompt) fires on 4+ patterns
- All 10 anti-pattern detectors each fire on canonical bad-spec fixtures
- 10 anti-patterns / 8 composition rules / 34-row matrix counts
- JSON round-trip
- Deterministic across re-runs

Biological simulation path bit-identical to v0.7.37.

## [0.7.37] - 2026-06-29

### Added — Issue 33 Promptbio v3.0 Unified Prompt Organism Architecture

The architecture spine that wires every prior promptbio module into
one canonical anatomy. Closes `issues-promptbio/33-unified-prompt-organism-architecture.md`.

**Four catalogues land verbatim:**

- **16 organs (§5):** `genome / constitutional / context / epistemic /
  metabolic / immune / decision / planning / runtime / memory / tool /
  observability / evaluation / governance / reproductive / autopoietic`.
  Each with state / function / failure_modes / wired_to_module.
- **7 information flows (§6):** `input → epistemic → decision → planning
  → output → feedback → reproductive`. Each with ordered organ-stage
  hops and per-flow failure mode.
- **6 control loops (§13):** `homeostatic / epistemic / agentic /
  evaluation / governance / autopoietic`. Each with target invariant +
  step list + organ set + failure mode.
- **10 architectural principles (§20):** `spec_before_prompt /
  belief_before_recommendation / decision_before_action /
  plan_before_agent_loop / constitution_above_optimization /
  observability_before_autopoiesis / governance_before_production_evolution /
  deprecation_is_part_of_life / resource_proportionality /
  stable_core_adaptive_periphery`. Each enforced as a build-time check.

**Size router (§11).** 3 organism sizes (`micro / meso / macro`) each
with a required-organ gate. The router picks size from the input
envelope (deployment / risk / tools / memory / quality).

**PromptOrganismSpec (§15).** Full 17-block YAML schema: identity /
values / constitution / genome / expression_profiles / runtime /
epistemology / decision / planning / metabolism / immunity /
homeostasis / evaluation / observability / governance / autopoiesis /
lifecycle. Round-trips through JSON cleanly.

**Builder (§21).** `Build(req BuildRequest) BuildResponse` is the
entry point. Returns:

- the full `PromptOrganismSpec`
- the §12 anatomy diagram (organs rendered as `[●]` if materialised, `[ ]` if declared-but-empty)
- the organ_map wiring each of the 16 organs to a BreedOS module reference
- the 7 information flows + 6 control loops
- the §14 verbatim 8-step minimum-viable template parameterised to the input
- the §23 verbatim 16-step full-production path
- top_risks: 5 failure_mode + repair pairs scoped to the spec
- validation_report from the 10 §20 principle checks + §17/18/19 classification

**Validation (§17/18/19).** Five classification verdicts:

- `organism` — proper organism with values + constitution + belief state + control loops
- `agent` — organism + write-capable tools + action loop + permissions
- `agent_incomplete` — write tools registered but missing action loop or permission policy (§18 formula incomplete)
- `workflow` — has organs but no values / constitution / belief state / homeostatic loops (§17)
- `model_reference_only` — empty spec / runtime / genome (§19)

The §18 agent formula `Agent = PromptOrganism + ActionLoop + Tools +
Permissions` fires only on write-capable tools — read-only retrieval
tools don't trigger agent classification (single retrieval call is
not an agent loop).

**API.** Three new endpoints under `/api/promptbio/organism/`:

- `POST /build` — full BuildResponse bundle
- `POST /validate` — ValidationReport for an existing spec
- `GET  /catalogues` — 16 organs + 7 flows + 6 loops + 10 principles + 3 sizes

**UI.** v3.0 Organism Builder card on `/promptbio` with use-case +
target-phenotype + deployment + risk + tools + memory + quality
knobs, classification verdict badge, principle-violation list,
missing-organs list, anatomy diagram pre-block, 4-column anatomy view
(identity / genome+runtime / belief+decision / observability+evolution),
top-risks table, collapsible MVO + production-path lists.

**Tests.** 16 new tests in `breedos/mvp/promptbio/organism/builder_test.go`:

- Catalogues: 16 organs, 7 flows, 6 loops, 10 principles, 3 sizes
- GTM worked example: species=GTM Strategy, size=meso, verbatim values
- Principle 6: autopoiesis without observability → violation
- §18 agent formula incomplete: write tools without action loop/permission → agent_incomplete
- §17 workflow: empty spec → workflow classification
- §19 model boundary: empty runtime+genome → model_reference_only
- §14 MVO: 8 steps for any well-formed input
- §23 production path: 16 steps
- organ_map: all 16 organs wired to BreedOS module refs
- diagram contains all canonical anchor labels (VALUES / CONSTITUTION / GENOME / RUNTIME / BELIEF STATE / DECISION / VALIDATION GATES / OBSERVABILITY / EVALUATION / AUTOPOIESIS)
- JSON round-trip preserves organism type + 7 flows
- Deterministic across re-runs (same content-addressed ID)

Biological simulation path bit-identical to v0.7.36.

## [0.7.36] - 2026-06-28

### Added — Issue 16 Promptbio v1.3 Prompt Evaluation Lab

Closes `issues-promptbio/16-prompt-evaluation-lab.md`. Answers the
gating question "is this prompt actually better than its ancestor?"
with measurement instead of vibes.

**Core formulas land verbatim:**

- `F_system(P, E, T) = Score(Y = Run(P, E, T))`
- `F(P) = E[Score(Y)]`
- `Robustness(P) = 1 − Var(Score(Y))`
- `F_net = F_quality − λ·Cost`

**Test bank.** 10-test seed bank covering every §4 type
(`clean / sparse / noisy_context / conflict / prompt_injection /
constraint_leakage / format_stability / drift / overconfidence /
reproduction`) with one verbatim §15 GTM Product Launch case per type.

**Fitness functions.** §15 9-metric GTM Product Launch rubric
verbatim — weights `realism 0.18, specificity 0.15, actionability
0.15, constraint_fit 0.12, experiment_quality 0.10, measurability
0.10, risk_awareness 0.08, value_clarity 0.07, uncertainty_handling
0.05` (sum = 1.0) with per-level descriptors. Coding (`correctness
0.35`, `executability 0.20`, `edge_cases 0.15`, `security 0.10`,
`maintainability 0.10`, `clarity 0.05`, `efficiency 0.05`), research,
and teaching rubrics also catalogued.

**Eight-judge ensemble.** `format_judge / factual_judge /
constraint_judge / utility_judge / risk_judge / style_judge /
efficiency_judge / regression_judge` (§6 deterministic heuristic
versions over the v0.1 Mapper genome). Aggregator is geometric mean
so a single dominating judge cannot drown out a weakness on another
axis. Real LLM-as-judge integration queued for v1.4 Runtime.

**Harness.** Every (prompt, env, test) combination runs across 9
environments (`clean / sparse / noisy / conflicting / long_context /
high_temperature / tool_rich / tool_poor / different_model`). Score
matrix surfaces per-env mean + robustness + critical-failure column.

**Reaction norm.** Per-trait variance + worst-env + niche-fit verdict
(`broad / moderate / specialist / narrow`). Stable trait: variance < 0.04.

**Regression detector.** §11 verbatim template (`Improved / Degraded
/ Unchanged / New risks / Decision`). Safety boundary, constraint,
validation locus regressions promoted to first-class new risks.

**Ablation runner.** §13 knock-out for `immune_protocol /
metabolic_protocol / homeostatic_controller / reproduction_module /
long_style_rules` with `Keep?` = loss > 0.05.

**Cost telemetry.** Prompt length, output estimate, tool calls,
latency, slot-filling burden, user burden, maintenance complexity,
test count, dependency count → normalised single scalar for
`F_net = F_quality − λ·Cost`.

**Decision engine (§17).** Five verdicts: `accept /
accept_as_specialist / reject / split_into_profiles / mutate_again`.
Hard rules:

- clean-pass + injection/drift/constraint_leakage-fail → never accept
  (only specialist with explicit niche scoping, or mutate_again)
- F_quality < 0.45 → reject
- F_net < 0.40 with cost λ > 0 → mutate_again
- F_quality < 0.60 → accept_as_specialist

**API.** Two new POST endpoints under `/api/promptbio/eval/`:

- `/run` — full EvaluationLabReport with all 13 §18 sections.
- `/regression` — pairwise ancestor↔descendant regression report.

**UI.** Evaluation Lab card on `/promptbio` with target/ancestor
textareas, task-family + cost-λ knobs, deployment-decision badge
with rationale + critical failures + niche scoping, score-matrix
table, reaction-norm table with per-trait variance + worst env +
niche fit verdict, regression Improved/Degraded/New risks columns,
ablation table with Keep? decisions, ranked next-mutations list.

**Tests.** 12 new tests in `breedos/mvp/promptbio/eval/lab_test.go`:

- §16 ladder reproduction — P₀ raw rejected; P₁.₁ engineered
  ladder-monotone; P₁.₂ compiled accepted
- GTM rubric weights verbatim (9 metrics, sum = 1.0)
- Bank covers all 10 §4 test types
- Clean-pass + injection/safety-fail prompt → not accept (acceptance
  criterion 4)
- F_net surfaced
- Regression report non-empty when ancestor differs
- Nine environments in the reaction norm
- All 5 ablation modules with Keep? decisions
- JSON round-trip
- Deterministic across re-runs

**Mapper hardening.** The v0.1 Mapper task-locus detector gains
`output the / deliver / выведи / выдай` cues so compiled prompts
ending with `Output the …` are no longer flagged as missing the
task locus (was driving false drift failures in the Eval Lab).

Biological simulation path bit-identical to v0.7.35.

## [0.7.35] - 2026-06-28

### Added — Issue 30 Promptbio v2.7 Epistemology & Truth Maintenance

Ships the v2.7 truth-maintenance substrate every higher decision
layer (Decision Theory v2.8, Constitution, Observability) will
reuse. Closes `issues-promptbio/30-prompt-epistemology-truth-maintenance.md`.

**The Y = Generate(P, B_t, C_t) discipline.** Outputs must be
generated from a managed belief state, not raw context. Raw
incoming text is first classified into the 12-element claim
ontology (`fact / user_claim / document_claim / tool_result /
assumption / inference / hypothesis / recommendation / preference /
constraint / unknown / deprecated`), ranked on the 10-tier source
hierarchy (`current_user_correction > current_user_fact >
verified_tool_result > authoritative_document > confirmed_memory >
older_memory > unverified_user_claim > retrieved_snippet >
model_inference > assumption`), tagged on the 5-axis confidence
model (`source / interpretation / inference / action / freshness`)
with the qualitative label set (`high, medium, low, unknown,
needs_verification` — numeric probabilities are explicitly disallowed),
and binned into the 8-slot belief-state schema (`known_facts /
constraints / assumptions / hypotheses / unknowns / contradictions /
deprecated` keyed by objective).

**Truth Maintenance System.** Recommendations declare `depends_on`
claim IDs; when any dependency deprecates or its confidence drops,
TMS marks the recommendation `needs_revision` in the same response
cycle. The §13 7-step contradiction protocol surfaces conflicts via
9 classified contradiction types; critical equal-authority conflicts
emit a `clarification_request` rather than blending. The §14 8-step
belief-update protocol routes new evidence through the source
hierarchy and refuses to inflate confidence without a new evidence
record (anti-pattern §18.2).

**Runtime gate.** The §19 10-question pre-output checklist runs the
§18 9 anti-pattern detectors:

- `assumption_laundering` (§18.1): assumption surfaces in output without `working_assumption` tag
- `confidence_inflation` (§18.2): high-confidence language for assumption-typed claim
- `source_flattening` (§18.3): multiple source tiers; output omits provenance
- `memory_fossilization` (§18.4): older_memory claim lacks freshness label
- `contradiction_blending` (§18.5): contradictions exist; output doesn't surface
- `fake_precision` (§18.6): numeric probability without non-model source at conf ≥ medium
- `citation_laundering` (§18.7): quoted snippet without source attribution
- `tool_overtrust` (§18.8): tool_result rendered as authoritative truth
- `model_self_reference_as_evidence` (§18.9): model_inference cited as evidence

For medium/high risk_level the gate refuses to emit a response
lacking the verbatim Epistemic status block (§20) and renders it
automatically from the belief state.

**EpistemicScore.** The §22 9-component formula `0.15C + 0.15S +
0.15U + 0.10K + 0.10M + 0.10D + 0.10R + 0.10P + 0.05F` (claim
classification / source handling / uncertainty calibration /
contradiction handling / memory policy / dependency tracking /
recommendation grounding / provenance / freshness) → score in [0,5]
with the 5-band scale (`unsafe / weak / acceptable / robust /
high-assurance`).

**API.** Three new POST endpoints, all under `/api/promptbio/epistemology/`:

- `/plan` — full 16-section PromptEpistemologyPlan (extension over
  spec: also returns the derived belief_state so the UI doesn't
  need a second round-trip).
- `/gate` — runtime-gate verdict over a (belief_state, candidate_output) pair.
- `/update` — applies a new claim through the belief-update protocol;
  returns new_belief_state, deprecated_claims, recommendations_to_revise,
  contradictions.

**UI.** New Promptbio v2.7 card on `/promptbio` with use-case +
risk-level + free-form raw-context (one statement per line, prefix
with source kind), build-plan + run-gate buttons, and a four-pane
belief-state inspector (Known facts · Working assumptions · Unknowns
· Contradictions) plus classified-claims table with per-claim
type/source/confidence axes/state badges, runtime-gate verdict
panel with per-check pass/fail and per-anti-pattern detail, plus
drop-in EPISTEMIC PROTOCOL and PSL `epistemology:` reference blocks.

**Tests.** 14 new tests in `epistemology_test.go`: the §24 10-test
battery (assumption_laundering, user_correction, conflict_handling,
fake_precision, tool_overtrust, memory_fossilization, citation_laundering,
recommendation_dependency, stale_source, unsupported_market_claim)
passes 10/10. Plus 4 supporting tests: confidence-no-inflation,
high-risk requires status block, plan returns all 16 sections + 12
claim types + 10 source tiers + 10 gate questions + 7-step
contradiction protocol + 8-step update protocol, JSON round-trip.

**Non-goals (per Issue 30 spec).** No absolute truth or external
fact-checking — v2.7 maintains intra-task coherence only. No
decision-making over the belief state (that is v2.8). No persistent
cross-session memory implementation (policy only). No multilingual
claim parsing. No new-evidence generation (no web crawling). No
probabilistic / Bayesian numeric confidence (qualitative labels by
design). Does not replace the Constitution layer.

Biological simulation path bit-identical to v0.7.34.

## [0.7.34] - 2026-06-28

### Added — Issue 03 Prompt Evolution Loop

Ships the v0.3 module per Issue 03 acceptance criteria. From a
single ancestor prompt, grows a population of variants, scores
across 3 canonical niches with anti-correlated weight profiles,
selects per-niche specialists + global top-K, iterates N
generations, and emits a lineage tree whose edges are
content-addressed v0.2 mutation ledger entries. Closes
`issues-promptbio/03-prompt-evolution-loop.md`. This release
completes the **smallest end-to-end usable system** promised by the
handoff (Mapper → Diff → Evolution Loop), with all three modules
live on `/promptbio`.

**Scope (v0.7.34 minimum viable):**

- Synchronous endpoint — deterministic placeholder judge is fast
  enough that the async `/start` + `/status` pattern (Issue 03
  spec) is deferred to v0.4 when LLM-judge integration lands.
- 3 canonical niches with **sparse, anti-correlated** weight
  profiles: `core_breadth` (Task / Audience / Output / Constraint),
  `epistemic_depth` (Context / Method / Epistemic / Validation),
  `safety_first` (Safety / Constraint / Memory / Tool). A variant
  tuned for one niche cannot dominate the others — that's the
  property that produces niche specialists per acceptance criterion #2.
- 4 mutation kinds reused from v0.2 vocabulary (addition / deletion
  / substitution / amplification). The full 15+ operator catalogue
  from v5.7 (with risk tiers + governance gates) is queued for v0.4+.

**New in `mvp/promptbio/`:**

- `evolution.go` — `Evolve(req EvolveRequest) EvolveResponse`.
  Content-addressed `variantID` on (parent_id, generation, genome),
  3 canonical niches, `nicheFitness` over per-locus weight maps,
  `mutateRandomLocus` with bias toward addition at low statuses and
  deletion at strong statuses, deterministic counter-based `RNGv`
  (LCG, distinct from `math/rand` so determinism tests are
  independent of Go internal state), `selectParentsForEvolve` (per-
  niche top-1 + global top-K), `summarise` (mean / best / per-niche
  winner / Pareto front per generation), `paretoFront` over (mean
  fitness, robustness).
- `evolution_test.go` — 6 new tests: determinism (same seed →
  identical run_id, lineage, generation summaries); lineage tree
  well-formedness (every non-root variant has ≥ 1 parent edge,
  every edge has non-empty ledger_id); monotone mean-fitness
  improvement across 5 generations; niche specialisation (≥ 1
  niche specialist differs from global winner); canonical niches
  present (3 niches with ≥ 3 weighted loci each); lineage ledger
  ids content-addressed and ≥ 50% unique across edges.

**HTTP surface:**

- `POST /api/promptbio/evolve` (`mvp/promptbio_handler.go` —
  `promptbioEvolveHandler`). Request: `{ancestor_prompt,
  generations?, population_size?, selection_percent?,
  mutation_strategy?, seed?}`. Response: full `EvolveResponse` with
  `run_id`, `ancestor_genome` (from v0.1 mapper), `niches`,
  `generations` (per-gen population with `id`, `parent_id`,
  `genome`, `mutations_applied`, `fitness_per_niche`,
  `mean_fitness`, `robustness`), `lineage` (parent → child edges
  with content-addressed `ledger_id`), `changelog`,
  `global_winner_id`, `final_niche_winners`, `summary_text`,
  `honesty_banner`, `limitations`.
- Bounds: generations ≤ 12, population 5–20, enforced server-side.
- 400 on missing `ancestor_prompt`.

**UI:**

- New "Promptbio v0.3 — Prompt Evolution Loop" card on
  `/promptbio` (below the Issue 07 Simulate card). 5-field form
  (ancestor textarea + generations / population / selection % /
  seed). Result cards: run summary, 3-niche specialist grid (each
  niche card shows its winner id, niche fitness, "★ Also global
  winner" or "⚑ Niche specialist (differs from global)" badge),
  generation changelog table (Gen / Mean / Best / Best variant /
  Pareto IDs), lineage edges sample table (Parent / Child / Locus
  / Kind / Ledger id), honesty layer.

**Acceptance criteria (Issue 03):**

- ✅ #1 Monotone mean-fitness improvement — test
  `TestEvolve_MonotoneMeanFitnessImprovement` asserts
  `endMean > startMean` over 5 generations with seed 42.
- ✅ #2 Niche specialists differ from global winner — test
  `TestEvolve_NicheSpecialisation` asserts ≥ 1 niche specialist
  differs from global; live smoke shows **all 3** niches with
  different winners.
- ✅ #3 Lineage tree well-formed — test
  `TestEvolve_LineageTreeWellFormed` asserts every non-root
  variant has ≥ 1 parent edge with non-empty ledger_id.
- ✅ #4 Budget — synchronous endpoint completes 5 × 8 × 3 niches
  in ~5ms locally; well within budget.
- ✅ #5 Deterministic — test `TestEvolve_Deterministic` asserts
  same seed → identical run_id, lineage, generation summaries.

**Smoke (raw GTM → evolution, gen=4, pop=8, seed=42):**

```
run_id: r_0cd8286c0f657b2e
3 niches with 3 distinct winners:
  core_breadth    → v_72ac6a31897f (also global)
  epistemic_depth → v_4d5e6e832baf (specialist)
  safety_first    → v_f72b52ac6e4b (specialist)
39 lineage edges, mean fitness 0.103 → 0.190 (monotone)
```

**Design decisions:**

1. **Sparse, anti-correlated niches.** First draft had dense
   per-niche weight maps (14 loci each, high-weight overlap). All 3
   niches converged on the same variant. Redesigned to 3–4 locus
   identities per niche with zero overlap on peak loci — this is
   what makes niche specialisation a structural property of the
   design rather than a happy accident.
2. **Synchronous endpoint instead of `/start` + `/status`.** The
   deterministic placeholder judge runs in microseconds per variant
   per generation. Async pattern is queued behind LLM-judge.
3. **4-kind mutation taxonomy instead of v5.7's 15+.** v0.3
   minimum scope. v5.7's expanded catalogue with risk tiers and
   governance gates is queued for v0.4 once Ecology / Immunology
   land.
4. **Counter-based LCG RNG instead of `math/rand`.** Determinism
   tests pass independently of Go internal `math/rand` state
   changes across versions.

**Footer + version + CHANGELOG bumped to `v0.7.34`. Theory page
roadmap row for v0.3 marked shipped.**

## [0.7.33] - 2026-06-28

### Added — Issue 07 substrate abstraction

Ships the substrate abstraction contract per Issue 07, with the
promptbio side as the first non-biology substrate to implement it.
The biological simulation path is bit-identical to v0.7.32 (the
existing `organism` concrete type is unchanged and the kernel does
not yet operate through the interface). Closes
`issues-promptbio/07-engine-extension-prompt-organism-mode.md`.

**New `mvp/engine/` package** — substrate-agnostic contracts:

- `Individual interface { Genome() []byte; Clone() Individual }`
- `FitnessFunc func(Individual, Environment) float64`
- `MutationOp func(Individual, RNG) Individual`
- `RecombinationOp func(Individual, Individual, RNG) Individual`
- `RNG interface { Intn; Float64 }`
- `Substrate` enum (`biology`, `promptbio`)
- `StrategyTag` substrate-aware strategy registry key

The engine package imports nothing from any substrate; substrates
import from engine.

**New in `mvp/promptbio/`:**

- `organism.go` — `PromptOrganism{ Statuses [14]byte }` implementing
  `engine.Individual` over the 14-locus status vector (byte-encoded
  per locus, 0 = missing → 5 = not_applicable). `FromGenomeMap(g)`
  seeds an organism from a v0.1 mapper output. `MutationStep` flips
  one locus by one step on the status ladder.
  `RecombineUniform` splices loci from two parents at uniform
  random. `PlaceholderJudge` is the deterministic v0.7.33 fitness:
  locus-weighted sum of encoded statuses normalised to [0, 1].
- `simulate.go` — `Simulate(req SimulateRequest) SimulateResponse`
  runs the engine kernel over the prompt substrate. Five core moves
  registered as promptbio strategies — **truncation** (top-K),
  **balanced** (70% top-K + 30% drift), **drift** (uniform random),
  **OCS-like** (top-K with diversity weighting), **introgression
  equivalent** (top-K + ancestor-template injection). Per-strategy
  trajectory of mean fitness over generations, Pareto front on
  (gain, risk), best-risk-adjusted / best-gain / lowest-risk picks.
- `simulate_test.go` — 8 new tests: engine.Individual contract
  satisfaction; mutation determinism; recombination determinism;
  judge determinism and bounding; e2e Simulate with 5 strategies;
  reproducibility (same seed → identical response); all 5 core
  engine moves present; FromGenomeMap round-trip preserves locus
  statuses.

**HTTP surface:**

- `POST /api/promptbio/simulate` (`mvp/promptbio_handler.go` —
  `promptbioSimulateHandler`). Request: `{ancestor_prompt,
  population_size?, generations?, selection_percent?,
  mutation_rate?, replicates?, seed?}`. Response: substrate-uniform
  `SimulateResponse` with `substrate: "promptbio"`, 5
  `strategy_results` (Code, Name, Summary, FinalGain, FinalRisk,
  Trajectory, ParetoOptimal), `best_risk_adjusted_code`,
  `best_gain_code`, `lowest_risk_code`, `pareto_codes`,
  `summary_text`, `honesty_banner`, `limitations`,
  `what_could_be_wrong`, and the ancestor's `ancestor_genome` from
  the v0.1 mapper.
- Bounds: population ≤ 200, generations ≤ 30, replicates ≤ 10
  enforced server-side to keep response time predictable.
- 400 on missing `ancestor_prompt`.

**UI:**

- New "Issue 07 — Substrate Abstraction" Simulate card on
  `/promptbio` (below v0.2 Diff). 7-field form (ancestor textarea +
  population / generations / selection-% / mutation-rate /
  replicates / seed). Result cards show best-risk-adjusted summary +
  per-strategy outcome table with Pareto flag + per-strategy
  trajectory mini-SVG charts + honesty layer (limitations and
  what-could-be-wrong).

**Acceptance criteria (Issue 07):**

- ✅ Engine kernel does not import anything from `promptbio/`
  (engine package is a leaf; substrates import from engine).
- ✅ All 5 core engine moves run over the prompt substrate
  (truncation, balanced, drift, OCS-like, introgression).
- ✅ Biological simulation path bit-identical — `mvp` and `mvp/...`
  test suites all green, no biological code path touched.
- ✅ New promptbio tests cover e2e run, mutation determinism,
  recombination determinism, judge reproducibility.
- ✅ `POST /api/promptbio/simulate` accepts minimal request and
  returns valid SimulateResponse within local-runner budget.

**Design decisions:**

1. **Engine package addition is additive, not invasive.** The
   biological kernel keeps using its own concrete `organism` type
   and is not refactored to operate through `engine.Individual` —
   doing so safely would require a multi-day rewrite and risks
   breaking bit-identical biology. v0.7.33 satisfies "substrate is
   plug-in" (engine never imports a substrate); migrating biology
   to operate through the interface is queued (post-v0.8).
2. **DecisionSummary shape diverged.** The biological
   `DecisionSummary` carries NGT classification, EditDecisions, and
   per-trait gain — biology-specific fields that don't apply to
   promptbio. Rather than emit a polymorphic shape, promptbio
   returns its own `SimulateResponse` with the engine-uniform
   `strategy_results` array. Future UI substrate switch on /demo
   can render either shape; v0.7.33 ships the promptbio results
   directly on /promptbio.
3. **Demo-page substrate switch deferred.** Issue 07's spec
   includes adding a substrate switch to /demo. v0.7.33 ships the
   promptbio simulator on /promptbio instead — smaller surface,
   faster validation, no risk to the biology demo. Substrate switch
   on /demo is a v0.8 follow-up.
4. **Placeholder judge instead of LLM.** Per Issue 07 non-goal
   ("Real LLM-judge integration — that is a follow-up issue"),
   v0.7.33 uses a deterministic locus-weighted sum. Real LLM-judge
   is queued behind v0.3 Evolution Loop's fitness battery design.

**Footer + version + CHANGELOG bumped to `v0.7.33`. Demo and
landing footers updated.**

## [0.7.32] - 2026-06-28

### Added — Promptbio v0.2 Prompt Genome Diff

Ships the second Promptogenesis module, mirroring the v0.1 mapper
discipline (static, deterministic, no LLM, bilingual). Closes
`issues-promptbio/02-prompt-genome-diff.md`.

**Scope:** static-heuristic diff over the 14-locus vector emitted by
v0.1 — cosmetic rewording yields ΔG ≈ 0 because diffs live at the
genotype layer, not the text layer. Operates on the existing
`promptbio` subpackage; the biological BreedOS path stays
bit-identical.

**New in `mvp/promptbio/`:**

- `diff.go` — `DiffPrompts(req DiffRequest) PromptDiff`. Walks the
  14-locus map for ancestor + descendant, classifies each
  status transition into one of the six handoff-Section-6.4 mutation
  kinds (`addition` / `deletion` / `substitution` / `amplification` /
  `suppression` / `modularization`), computes a locus-weighted ΔG
  (Task / Constraint / Output_schema carry 1.5×; Tool / Memory 0.7×;
  UX / Evolution 0.5×; others 1.0×), projects a 7-axis ΔZ
  (structure / depth / accuracy / concreteness / style / usefulness
  / risks ∈ [-2, +2]) from locus deltas, and surfaces regressions
  (lost present/strong loci) + new risks (safety/validation weakened,
  conflicts introduced).
- Content-addressed mutation ledger: `ledger_id = "m_" + sha256(locus
  | kind | after_fragment)[:8]`, stable across runs. v0.3 Evolution
  Loop will reference these ids on lineage edges so identical
  mutations across branches deduplicate.
- ΔF is `null` with the honest `delta_f_explanation` `"no
  target_phenotype supplied; ΔF deferred to v0.3 fitness battery"`
  — v0.2 deliberately doesn't fabricate a fitness number without a
  target.
- Next-mutation suggestion picks the highest-leverage missing locus
  in the descendant (priority: Task → Constraint → Output → Epistemic
  → Validation → Context → Method → Audience → Role → Safety).
- `diff_test.go` — 11 tests: identical-input zero-delta; cosmetic-
  reword near-zero; raw→structured ≥ 0.3 ΔG with ≥ 3 additions and
  descendant score > ancestor; structured→raw ≥ 1 regression with
  ≥ 3 deletions; ledger-id stability across runs; ledger-id
  distinguishes locus + kind + fragment (4 unique on permutations);
  ΔF nil with explanation when no target; safety-locus deletion
  emits `new_risks` entry; next mutation prioritises core loci;
  phenotype-shift axes bounded in [-2, 2] and raw→structured shifts
  are non-negative on depth/accuracy/structure; JSON round-trip
  stability.

**HTTP surface:**

- `POST /api/promptbio/diff` (`mvp/promptbio_diff_handler.go` —
  small wrapper next to the v0.1 map handler). Request: `{ancestor,
  descendant, language?, species_hint?}`. Response: full
  `PromptDiff` with `diff_id`, ancestor + descendant genomes,
  `genomic_diff`, `mutation_ledger`, `delta_g`, `delta_z`,
  `delta_f` (nullable), `regressions`, `new_risks`,
  `next_mutation_suggestion`.
- Empty-input validation: `400 ancestor and descendant are required`
  if either is blank.

**UI:**

- New "Promptbio v0.2 — Prompt Genome Diff" card on `/promptbio`
  below the existing v0.1 Mapper card. Two textareas (Ancestor /
  Descendant) + Compare button + load-raw→structured example
  button. Results: ΔG pill (low/mid/high band), ancestor → descendant
  genome-score shift line, 7-axis ΔZ chips (colour-coded
  positive/negative/neutral), 14-row mutation-ledger table with
  colour-coded `kind` badges and short `ledger_id` display,
  regressions list, new-risks list, single highest-leverage next
  mutation card.
- Nav stays on the same page — no separate `/promptbio-diff`
  route — so the operator can run map → diff → map without
  navigation.

**Footer + version + CHANGELOG bumped to `v0.7.32`.**

## [0.7.31] - 2026-06-27

### Added — Promptbio v0.1 Prompt Genome Mapper

Ships the first Promptogenesis surface in BreedOS, per the founder-
authored 2026-06-27 handoff `ingest-done/handoff-dna-prompt.md.done`.
Closes `issues-promptbio/01-prompt-genome-mapper.md`.

**Scope (handoff Section 12 Definition of Done):** static heuristic
analyzer only. No live LLM, no API dependency, no RNG. Deterministic
given input. Bilingual (RU + EN cue tokens). The biological BreedOS
path is bit-identical.

**New `mvp/promptbio/` subpackage** (separate Go package from main):

- `types.go` — `LocusName` (14 constants from handoff Section 4.2:
  Task, Role, Audience, Context, Constraint, Method, Epistemic,
  Output schema, Validation, Tool, Memory, Safety/boundary, UX,
  Evolution), `LocusStatus` (6 states: missing / weak / present /
  strong / conflicting / not_applicable), `LocusAssessment`,
  `ExpectedPhenotype`, `MutationSuggestion`, `GenomeMap`,
  `MapRequest`.
- `mapper.go` — `MapPrompt(req MapRequest) GenomeMap` static
  analyzer. Each locus detected by small cue-token set; "strong"
  status requires multiple cues (e.g. epistemic = facts AND
  assumptions; output_schema = explicit named sections). The genome
  score is the normalised average of applicable locus scores in
  [0, 1] (`not_applicable` rows skipped).
- `mapper_test.go` — 11 tests covering the handoff's two canonical
  examples (raw GTM → low score; structured GTM → high with
  role/epistemic/output_schema = strong), 14-loci cardinality,
  mutation-plan coverage, deterministic prompt ids, language
  detection, JSON round-trip stability.

**HTTP surface:**

- `POST /api/promptbio/map` (handoff Section 6.2). Request:
  `{prompt, language?, species_hint?}`. Response: full `GenomeMap`
  with prompt_id, genome_score, loci, missing_loci, conflicting_loci,
  expected_phenotype, mutation_plan, tests_to_run.
- `mvp/promptbio_handler.go` is the only import edge between the
  main package and the subpackage.

**UI:**

- New public page `/promptbio` (`mvp/static/promptbio.html`):
  textarea + Analyze button + Load-raw / Load-structured example
  buttons + genome-score pill (low / mid / high colour band) +
  14-loci table with status badges + mutation plan + tests to run.
- Nav links from the demo and theory pages.

**Theory page wording adjustments (handoff Sections 1.2, 1.3):**

- `static/theory.html`: "behave identically" → "are structurally
  analogous, or — more precisely for the BreedOS simulator —
  operationally isomorphic" (the term the founder is comfortable
  defending publicly).
- New scope-boundary paragraph: "The full Promptogenesis framework
  later extends into runtime organisms, PML maturity levels,
  PromptOps governance, simulation environments, synthetic worlds,
  AutoPromptOps, and safety for auto-evolving systems. The current
  roadmap intentionally starts with the smallest measurable surface
  — mapping, diffing, and evolving prompt-genotypes." Links to
  `/promptbio`.
- Module-roadmap row for v0.1 updated to "shipped v0.7.31".

**Ingest lifecycle:**

- 77 source files digested in one cycle:
  `ingest/00..42-prompt-dna.md`, `ingest/44..76-prompt-dna.md`,
  and `handoff-dna-prompt.md` → `ingest-done/<name>.md.done`.
  Numeric slot 43 was absent in the source batch. The handoff is
  the canonical distillation; the 76 numbered files behind it are
  the conversational substrate.
- `issues-promptbio/00-README.md` updated to call out the handoff
  as primary reading.
- `issues-promptbio/01-prompt-genome-mapper.md` →
  `issues-promptbio-done/01-prompt-genome-mapper.md.done` with
  full completion section.
- Issue 07 (substrate refactor) downgraded from P1 to P2; it is no
  longer blocking the v0.1 module (the handoff demonstrates that
  a static analyzer can ship without the substrate kernel).

**Replaces the v0.7.25 scaffold:** `mvp/promptbio.go` (single-file
type stub from the original promptbio scoping work) is deleted in
the same commit. The handoff's 14-locus taxonomy supersedes the
scaffold's taxonomy.

### Tests — 11 new

In `mvp/promptbio/mapper_test.go`:

- `TestMapPrompt_RawGTM_LowScore` — handoff Section 5.2 prompt
  scores under 0.30, ≥ 8 missing loci, non-empty mutation plan
  + failure modes.
- `TestMapPrompt_StructuredGTM_HigherScore` — handoff Section 5.3
  prompt scores higher than raw; role/epistemic/output_schema =
  strong; constraint/validation ≥ present.
- `TestMapPrompt_EmptyPrompt_AllMissing` — score 0, all 14 missing,
  confidence high.
- `TestMapPrompt_DeterministicPromptID` — same input → same id +
  score.
- `TestMapPrompt_DetectsEnglishLanguage` — English sample → "en".
- `TestMapPrompt_JSONSerialisationStable` — round-trip safe.
- `TestMapPrompt_RoleDetection`, `TestMapPrompt_OutputSchemaDetectsStrong`,
  `TestMapPrompt_MutationPlanCoversMissingLoci`,
  `TestMapPrompt_TestsToRunAlwaysNonEmpty` — micro-coverage.

Full Go suite (`go test ./...`) green: both `breedos-mvp` and
`breedos-mvp/promptbio`.

### Non-goals (per handoff Section 13, deferred)

- Live LLM evaluation, full evolutionary search, AutoPromptOps,
  hidden tests, knowledge graph, PML certification, complex
  ontology, metabolism, agent runtime, real tool execution.
- v0.2 Diff, v0.3 Evolution Loop, v0.4 Ecology, v0.5 Immunology —
  queued as the next issues on the promptbio board.

## [0.7.30] - 2026-06-27

### Added — End-to-end breeder workflow (Issue 10)

Closes `issues-breedos/10-end-to-end-breeder-workflow.md` (P2). UX
polish: the demo now reads as a coherent 8-step workflow rather than
"parameter panel + charts".

**Workflow stepper (top of demo):** new 8-step horizontal list with
anchor links to each region: Data → Confirm → Constraints → Run →
Feasibility → Decision Report → Export → Next step. Each step has a
short subtitle so the operator knows what happens there before
clicking.

**Anchor map added across existing cards:**

- `#step-data` — left sticky panel (Simulation inputs).
- `#step-confirm` — `summary` summary-grid (the run header cards).
- `#step-constraints` — the constraints `<details>` block.
- `#step-run` — the button-grid in the left panel.
- `#step-feasibility` — the Strategy recommendations card.
- `#step-report` — the Decision engine output card.
- `#step-export` and `#step-next` — new bottom Export & next-step
  card.

**`↓ Jump to Decision Report` button** in the left button-grid.
Disabled until `currentData` exists; enabled after the first
successful run; clicking smooth-scrolls to `#step-report`.

**Empty-state placeholder:** new dashed-border card at the top of
the results panel before the first run. Explains the workflow
shape (summary cards → Decision panel → sensitivity → strategy
table → Pareto → AFS histogram → candidate-edit table) so the
operator does not stare at empty charts wondering what to expect.
Hidden as soon as `currentData` is set.

**Bottom Export & next-step card:** duplicate "Export full run
(JSON)" and "Copy plain-text summary" buttons (so the operator
who scrolled to the report does not have to scroll back up), plus
a five-point checklist for the actual next step:

- climate-stable → plan around the best-feasible strategy;
- climate-fragile → choose by expected weather year;
- EDIT-flagged loci → route to Benchling / Synthego / CRISPResso
  for guide design (BreedOS does not design guides);
- imported_gebv winning → record prediction-pipeline version in
  the exported JSON for audit;
- new run → change inputs, press Run, previous run shown as
  dotted overlay.

**Export coverage audit:** the existing `exportJsonBtn` exports
`currentData` which already contains `request`, `decision`,
`strategies`, `candidate_edits`, `notes`. Issue 10 acceptance
criterion #3 ("Export includes request, constraints, strategy
results, and report") satisfied by the existing implementation —
constraints live on `request.*`, the report lives on `decision`.

### CSS

- New `.workflow-stepper` class (horizontal list, wraps on narrow
  screens, blue accent for step titles, muted subtitles).
- `html { scroll-behavior: smooth; }` was already set; anchor
  jumps animate.

### Tests

No new tests — this release is HTML / CSS / JS-only and adds no
backend surface. Full Go test suite stays green.

### Issue closed

- `issues-breedos/10-end-to-end-breeder-workflow.md` →
  `issues-breedos-done/10-end-to-end-breeder-workflow.md.done`.

### Board state after this release

`issues-breedos/` active board is now **empty** — all 12 originally-
numbered issues + 5 NGT pack + 5 Holstein pack + 5 Methane pack + 5
Climate pack are closed. The next active work surfaces from
`issues-promptbio/` (engine extension scaffolded but not yet
implemented), `issues-science/` (12 learning modules + 12 textbooks
to write), `issues-human/` (outreach + advisor + domain-review-loop
cycle), `issues-tools/` (12 automation tools, mostly iterations),
or new issues filed via the domain-review feedback loop.

## [0.7.29] - 2026-06-27

### Added — Prediction-output integration (Issue 08)

Closes `issues-breedos/08-prediction-output-integration.md` (P2). The
upload workflow gains a fifth optional CSV, and the engine gains a
new strategy that demonstrates BreedOS's product positioning:
**decision layer above** external prediction pipelines, not a
replacement for them.

**New `predictions.csv` upload table** (`id, gebv[, uncertainty]`):
- Parsed by `parseUploadPredictions` in `mvp/upload.go`.
- Cross-validated against the genotype CSV: every predictions id
  MUST exist in the genotype id column. Mismatch returns 400 with
  the count and first rogue id named in the error body.
- Surfaced in the import summary as `used_by_engine: predictions
  (as gen-0 GEBV-aware selection signal in the imported_gebv
  strategy)`.

**New `imported_gebv` strategy** (advanced set only, auto-surfaced
when the upload carries predictions):
- Scores gen-0 selection by the imported GEBV, standardised against
  the simulator's true-trait moments so it lives on the same
  z-score scale as the other strategies.
- From gen ≥ 1 falls back to internal genomic scoring because
  offspring identities are not tracked through reproduction. NaN
  entries (missing predictions for an individual) also fall back
  per-individual.
- Carries `UseImportedGEBV` flag on `strategyConfig` and a
  per-strategy `Gen0GEBV []float64` slice (populated once in
  `runSimulation` after the upload is loaded).

**`selectParents` signature change:** now takes a `gen int` so the
gen-0 path can opt into imported GEBVs while later cycles fall back.
Single call site (in `simulateStrategy`); the multi-trait path is
unchanged (the `imported_gebv` strategy is single-trait only — adding
multi-trait support would require per-trait GEBV columns and is
deferred).

### Tests — 8 new

`upload_test.go` adds:

- Fixture parse happy-path (30 rows, has_uncertainty=true, sample
  ids).
- Wrong-header rejection (first col must be `id`).
- Missing-gebv-column rejection (second col must be `gebv`).
- Non-numeric GEBV rejection.
- Duplicate-id rejection.
- Multipart handler rejects mismatched ids (predictions id not
  present in genotype id column).
- `imported_gebv` strategy appears iff upload carries predictions.
- `gen0GEBVOverride` slice aligns with accession ids (spot-check
  `plant_001 = -0.317`).
- End-to-end: `imported_gebv` strategy completes a full run and
  produces non-zero final metrics.

Full suite green.

### Issue closed

- `issues-breedos/08-prediction-output-integration.md` →
  `issues-breedos-done/08-prediction-output-integration.md.done`.

### Non-goals (per issue, deferred)

- rrBLUP / BGLR / sommer / AlphaSimR implementations inside BreedOS
  (explicitly out of scope by Issue 12 / ROADMAP.md).
- Claims about prediction accuracy — BreedOS consumes the GEBVs the
  operator supplies; it does not score them.
- Marker-effect overrides (the issue's "optional marker effects"
  input is deferred to a future issue; predictions-per-individual
  is the v0.7.29 path).
- Multi-trait GEBV columns — single-trait only for this release.

## [0.7.28] - 2026-06-27

### Added — Climate-sweeps pack complete (Issues 29, 30, 31)

Closes the last three issues in the Climate-sweeps pack
(`issues-breedos/29..31`). The v0.9 climate workflow is now end-to-end:
sweep across climate scenarios, optionally enable ancestral
introgression as a hedge strategy, and read the plain-language verdict
in the Decision Report.

**Issue 29 — Climate-scenario sensitivity sweep.**
The v0.7.16/17 sweep engine gains a new axis: `climate_scenario`. The
operator picks up to 5 `{mode, severity}` `ClimateScenario` rows (a
structured form replaces the comma-separated numeric input). The
sweep runs the simulator once per row, assigning `scen.Climate` per
scenario. Verdict text adapts:

- stable → "climate-robust within the sampled stress modes"
- fragile → "weather-year dependent — choose strategy explicitly for
  the expected stress regime"

Backend: new `ClimateValues []ClimateScenario` field on
`SensitivityRequest`; new `axis_label` field on `SensitivityScenario`
so the UI renders `"heat_burst_anthesis (sev 0.50)"` instead of a
numeric index; `baselineValueForAxis` favours the `"normal"` mode if
present, else scenario 0. `SensitivityResult.ClimateValues` round-
trips so clients can replay.

UI: dataset-style structured 5-row picker in the sensitivity panel.

**Issue 30 — Ancestral-allele introgression strategy.**
New `ancestral_introgression` strategy in the advanced strategy set,
opt-in when `req.AncestralIntroPercent > 0` (hard cap 25%, validated).
The strategy models the planning question "what if we seed landrace
or wild-relative lines into the founder population?":

- At gen 0, the last K = round(N × pct/100) individuals are
  re-rolled with a Bernoulli-biased low-favourable-allele draw
  (P(favourable) = 0.30 vs the modern 0.50), dragging base trait
  mean down.
- The recorded climate penalty for this strategy is multiplied by
  `1 − pct × (1 − stress_tolerance)` (default `stress_tolerance =
  0.5`, exposed as `req.AncestralStressTolerance`). With defaults
  (15% × 0.5) → 7.5% penalty reduction; at cap (25% × 0.5) → 12.5%.

The dynamic the issue calls for is captured at the strategy level:
slower under zero stress (lower baseline), more resilient under
climate stress (lower effective penalty). Per-individual lineage
tracking through reproduction is deliberately out of scope (v0.9
non-goal).

Backend: new `Ancestral*` request fields with validation; new
`UseAncestralSeed` flag on `strategyConfig`; new `seedAncestralLines`
helper called from the worker loop right after `applyCrisprSeed`;
new `climateDiscountForStrategy` helper consulted in
`aggregateReplicates` and wired into both single-trait and multi-
trait climate-penalty applications via a new
`applyClimatePenaltyToMetricsWithDiscount` variant (the legacy
no-discount call still exists and is bit-identical to v0.7.24).

**Issue 31 — Climate-aware Decision Report section.**
New `ClimateRobustness` structured block attached to the sweep
result (only when `axis = climate_scenario` AND ≥ 2 scenarios were
sampled). Fields: headline, failure modes, alternative-strategy
advice, conditional ancestral-introgression paragraph, always-
present honesty caveat. UI: rendered under the verdict in the
sensitivity panel with the same styling as the verdict pill.

### Tests — 17 new

- `sensitivity_test.go` (6 new): climate-axis acceptance + rejections,
  bad-mode error message names the mode, max-values cap, baseline
  favours `normal`, climate-axis-specific stable / fragile note
  wording, `ClimateValues` round-trip.
- `ancestral_test.go` (10 new): seed rewrites the last K only and
  lowers mean allele count, zero-percent no-op, climate-discount
  off-strategy stays at 1.0, default discount ≈ 0.925, validation
  rejects intro_percent > 25, strategy appears iff intro_percent > 0,
  end-to-end climate-discount mechanism verified (ancestral retains
  more of its no-stress gain than balanced does).
- `climate_robustness_test.go` (6 new): non-climate axis returns nil,
  single-scenario returns nil, stable headline uses "stays best",
  fragile surfaces an alternative, ancestral paragraph is
  conditional on `req.Base.AncestralIntroPercent > 0`, empty-baseline
  edge case warns.

Full suite green.

### Issues closed

- `issues-breedos/29-climate-scenario-sweep.md` →
  `issues-breedos-done/29-climate-scenario-sweep.md.done`.
- `issues-breedos/30-climate-ancestral-allele-strategy.md` →
  `issues-breedos-done/30-climate-ancestral-allele-strategy.md.done`.
- `issues-breedos/31-climate-decision-report-section.md` →
  `issues-breedos-done/31-climate-decision-report-section.md.done`.

### Non-goals (deferred)

- 2-D sweeps (axis × severity grid) and climate × heritability cross-
  axis sweeps.
- Real landrace genotype dataset (synthetic ancestral lines only).
- Per-genotype heat-tolerance QTL modelling. Honesty caveat surfaced
  in the Decision Report section in lieu of this.

## [0.7.27] - 2026-06-27

### Added — Edit-vs-Cross-vs-Wait classifier (Issue 07)

Closes `issues-breedos/07-edit-vs-cross-vs-wait.md` (P1). For each
ranked candidate edit, the CRISPR layer now answers the operator
question: should we **edit**, **cross/select**, or **wait/validate**?

**Classifier rules** (priority-ordered, in `mvp/edit_classifier.go`):

1. **WAIT — NEAR_FIXATION.** `p ≥ 0.92` — selection or drift completes
   the lift; edit adds no realistic gain.
2. **WAIT — MARGINAL_EFFECT.** `|effect| < 0.10` — below the practical-
   gain threshold; validation cost likely exceeds expected benefit.
3. **WAIT — BOTTLENECK_RISK.** `baseDiversity < 0.15` AND `p < 0.10` —
   editing into a narrow founder set compounds the bottleneck.
4. **EDIT — LARGE_EFFECT_RARE_ALLELE.** `|effect| ≥ 0.30` AND
   `p < 0.20` — selection is too slow; editing produces immediate
   progress. When `effect > 1.4` AND `p < 0.10`, the posture tightens
   to ≤2% introgression with a pleiotropy / background-validation
   warning.
5. **CROSS — ALREADY_SEGREGATING.** `0.20 ≤ p < 0.92` AND
   `|effect| ≥ 0.10` — selection/crossing propagates it without an
   edit.
6. **EDIT — MID_BAND_RARE_FAVOUR_EDIT.** `p < 0.20` AND
   `|effect| ≥ 0.10` (not large) — gray-band edit case; ~5% posture.
7. **Default — CROSS.**

**Backend wiring:**

- New `EditDecision` struct (`class`, `reason_code`, `reason`,
  `introgression_posture`, `risk_warning`).
- New `Classification *EditDecision` field on `EditCandidate`. The
  legacy `decision` text field is kept and aligned with the new class
  for backward compatibility.
- New `EditDecisionSummary` aggregate
  (`{total_candidates, edit_count, cross_count, wait_count, headline}`)
  on `DecisionSummary` as `edit_decisions`.
- `rankEditCandidates` now takes `baseDiversity` and attaches a
  classification to every ranked candidate. Both single-trait and
  multi-trait paths wired identically.
- New honesty-layer note added to `notes` whenever the run plans
  edits, pointing operators at `decision.edit_decisions`.

**Demo UI:**

- New "Edit / Cross / Wait" column in the candidate-edit table,
  rendering a colour-coded pill badge (EDIT = accent green, CROSS =
  blue, WAIT = amber). Hovering any badge shows the per-candidate
  reason, introgression posture, and risk warning.
- New `editDecisionsCard` above the table renders the run-level
  headline + per-class counts + the classifier-rule legend.

### Tests — 10 new

`edit_classifier_test.go` covers the canonical cases the issue calls
for and the rule-summary integrity:

- `TestClassify_LargeEffectRareAllele_Edit` — canonical EDIT case.
- `TestClassify_VeryHighEffectVeryRare_EditCautious` — cautious ≤2%
  posture + pleiotropy warning for effect > 1.4 with very rare allele.
- `TestClassify_HighFrequencyMarginal_Wait_NearFixation`.
- `TestClassify_LowEffect_Wait_MarginalEffect`.
- `TestClassify_HighRisk_Wait_BottleneckRisk`.
- `TestClassify_Segregating_Cross`.
- `TestClassify_MidBandRareModerate_Edit`.
- `TestSummarize_AllEdit`, `TestSummarize_MixedHeadline`,
  `TestSummarize_Empty`.
- `TestRankEditCandidates_AttachesClassification` — every ranked
  candidate has a Classification; legacy `decision` text stays
  aligned with the new class.

Full suite green.

### Issue closed

- `issues-breedos/07-edit-vs-cross-vs-wait.md` →
  `issues-breedos-done/07-edit-vs-cross-vs-wait.md.done`.

### Non-goals (per the issue, deferred)

- Guide-RNA design.
- Off-target scoring.
- Lab-protocol generation.
- Edit-cost / validation-confidence / pleiotropy-from-external-tools
  rules — the rule body is structured so these slot in without
  refactor when the data path exists.

## [0.7.26] - 2026-06-27

### Added — Minimal upload workflow (Issue 05)

Closes the `issues-breedos/05-minimal-upload-workflow.md` P1 issue. The
narrow, honest CSV-upload path the issue called for.

**New `POST /api/upload`.** Multipart/form-data endpoint accepting up
to four CSVs in one bundle:

- `genotype` (required) — same format as the built-in dataset loader:
  header `id,marker_1,...,marker_N`, values `0`/`1`/`2`. Becomes the
  founder population.
- `phenotype` (optional) — `id,<trait_name>`. Parsed; surfaced in the
  summary with min/max/mean. Not yet consumed by the engine because the
  simulator generates phenotypes from its QTL model.
- `pedigree` (optional) — `id,sire,dam`. Parsed; surfaced with row
  count + unique-sire / unique-dam counts. Not consumed (simulator
  builds its own pedigree from random mating).
- `edits` (optional) — `marker_id,target_allele,expected_effect[,note]`.
  Parsed; full content round-tripped to the summary. Not consumed
  (simulator uses the existing `crispr_edits` counter, not marker-
  level edits).

Response: JSON `{upload_id, summary, note}` where `summary` is an
`UploadedDataset` with per-table fields plus explicit `used_by_engine`
and `ignored_by_engine` lists so the operator sees exactly what is
and isn't being used.

**Engine wiring.** New `Upload` field on `SimRequest`. When set, the
simulator looks up the upload (in-memory cache, 1-hour TTL, never
persisted), uses its genotype as the founder population, and adds an
explicit "EARLY IMPORT — not production integration" note to the
Decision Report. `Upload` takes precedence over `Dataset`. Missing or
expired upload returns a clear 400.

**Demo UI.** New "Uploaded CSVs (your data)" option in the founder-
population dropdown reveals a dashed-border upload card with:
- four file pickers (genotype required, others optional);
- "Upload & validate" button calling `/api/upload`;
- inline import summary showing per-table row/marker/trait counts;
- prominent `EARLY IMPORT — not production integration` label;
- ephemeral disclosure (`~1 hour, never persisted`);
- example links to the embedded toy fixtures.

**Toy fixtures (embedded).** Four CSVs under `mvp/fixtures/upload-toy/`:
- `genotype.csv` — 30 individuals × 80 markers.
- `phenotype.csv` — 30 trait values.
- `pedigree.csv` — 30 rows with 3 sires × 4 dams.
- `edits.csv` — 3 candidate edits.

Served at `/upload-fixture/<name>.csv` so the demo's example links
work on a fresh install with no external setup.

### Tests — 18 new

`upload_test.go` covers:
- Genotype parser: fixture happy path, out-of-range values, non-integer
  cells, column-count mismatch.
- Phenotype parser: fixture happy path, wrong header, non-numeric trait.
- Pedigree parser: fixture happy path, missing dam column.
- Edits parser: fixture happy path, out-of-range allele.
- Upload cache: put/get roundtrip, TTL eviction, capacity eviction.
- `runSimulation` with an uploaded genotype: verifies `PopulationSize`
  and `Markers` come from the upload; verifies the "Founder population
  came from upload" honesty note appears in `resp.Notes`; verifies
  unknown upload id is rejected with a clear error.
- `uploadHandler` HTTP: multipart happy path (200 + `upload_id`),
  missing genotype (400).

Full suite green (existing climate and biology paths unchanged).

### Issue closed

- `issues-breedos/05-minimal-upload-workflow.md` →
  `issues-breedos-done/05-minimal-upload-workflow.md.done`.

### Non-goals (per the issue, deferred)

- BrAPI integration.
- Large-file handling beyond the 16 MB per-file cap.
- Secure multi-user storage (uploads are in-memory, single-process).
- Full genotype imputation / QC.
- Wiring uploaded phenotypes/pedigree/edits into the engine.

## [0.7.25] - 2026-06-26

### Added — Promptbio direction scaffolded (Shape 3 + Shape 1 + Shape 2 foundation)

Opens a second simulation direction in BreedOS alongside biological
breeding: **prompt-organism simulation**. Prompts are treated as
DNA-like genotypes, LLM + context as the developmental environment,
responses as phenotypes. The BreedOS engine kernel (selection, drift,
mutation, recombination, multi-trait Pareto) is the same; the substrate
is what varies.

This release ships the **scaffolding** — board, theory page, type
surface, ingest lifecycle — with no runtime behaviour changes. The
biological simulation path is bit-identical to v0.7.24.

**Shape 3 — `issues-promptbio/` execution board.** New
[`issues-promptbio/`](issues-promptbio/) board with 10 files:

- `00-README.md` — board purpose, lifecycle, priority order.
- `01-prompt-genome-mapper.md` — v0.1 module (14-locus decomposition).
- `02-prompt-genome-diff.md` — v0.2 module (ancestor↔descendant diff).
- `03-prompt-evolution-loop.md` — v0.3 module (population selection).
- `04-prompt-ecology-analyzer.md` — v0.4 module (8 habitats, context-rot).
- `05-prompt-immunology-analyzer.md` — v0.5 module (14 pathogen types).
- `06-prompt-metabolism.md` — v0.6 module (sketched, deferred).
- `07-engine-extension-prompt-organism-mode.md` — substrate foundation (P1).
- `08-experiment-templates.md` — 9 measurement harnesses.
- `09-glossary-as-product.md` — glossary maintained on /theory.

**Shape 1 — `/theory` public page.** New static page
`breedos/mvp/static/theory.html` served at
[`/theory`](https://www.breedos.org/theory) with: mapping table
(biology ↔ LLM, 11 rows from the source thread), module roadmap (7
rows), and a stable-anchor glossary (~50 terms grouped into core /
evolutionary / ecological / immunological). Issue specs deep-link to
glossary anchors. Nav link added to landing page (`index.html`) and
demo page.

**Shape 2 — promptbio type surface.** New
`breedos/mvp/promptbio.go` ships the substrate type surface:
`PromptGenotype`, `PromptLocus` constants (14 loci), `LocusStatus`,
`LocusEntry`, `PhenotypePrediction`, `PromptMutation` + the six
mutation kinds, `TestSpec`, `ConflictPair`, `PromptbioSimRequest`. No
runtime behaviour; no engine wiring; no HTTP route yet. The next
session picks up implementation at Issue 07.

**Ingest lifecycle convention.** Raw theory threads land in
`ingest/<NN>-<theme>.md`; after digestion into a board they move to
`ingest-done/<NN>-<theme>.md.done`. The first batch (10 files of theme
`prompt-dna`) is now in `ingest-done/`. CLAUDE.md documents the
convention. The lifecycle parallels the existing
`issues-*` → `issues-*-done/` convention.

### Notes

- Biological simulation path unchanged. No new tests required for the
  biological substrate; existing 13 climate tests still pass.
- The promptbio.go file is types-only and compiles cleanly with no
  imports beyond what was already in the package.
- Three new files (`10`/`11`/`12-prompt-dna.md`) arrived in `ingest/`
  during this release cycle and remain there pending the next
  digestion cycle.

## [0.7.24] - 2026-06-26

### Added — Climate Issue 28: per-stage phenotype penalty wired into simulator

Builds on the v0.7.23 Climate Issue 27 catalog foundation. The simulator
now accepts a `climate` field on `/api/simulate` carrying a
`ClimateScenario` (mode + severity) and applies a phenotype penalty to
recorded gain.

**Penalty model.** `mvp/climate.go` adds `climateModePenalty`:
- `normal` = 0.00
- `heat_burst_booting` = 0.20
- `heat_burst_anthesis` = 0.32 (Khan et al. 2024 anthesis triplet)
- `heat_grain_filling` = 0.46 (Khan et al. 2024 grain-fill triplet)
- `combined_postflowering` = 0.59 (Khan et al. 2024 combined triplet)
- `prolonged_heat` = 0.30
- `drought_terminal` = 0.40
- `salinity_chronic` = 0.20

Penalty applied as `phenotype × (1 - coefficient × severity)`, clamped to
`[0, 0.95]` (cannot zero out the population).

**Rank-preserving by design.** The same multiplier is applied to every
individual's recorded phenotype, so candidate ordering and selection
decisions are unchanged — only the recorded gain trajectory drops. This
matches the operator-facing question Issue 28 answers ("if a heat burst
hits, how much of the projected gain do we lose?") without conflating
G×E with selection-strategy comparison.

**Hooks.** `applyClimatePenaltyToMetrics` runs in `aggregateReplicates`
after `populateNeTrajectory(metrics)`, and in `runMultiTraitSimulation`
across `agg.PerTraitMetrics` so multi-trait runs (Holstein-pack,
Methane-pack) also see the penalty.

**Backward compat.** `req.Climate = nil` is bit-identical to v0.7.23
behaviour.

### Tests

13 tests in `climate_test.go` (5 inherited from Issue 27 +
8 new for Issue 28):

- `TestApplyClimatePenalty_CalibrationTriplet` verifies the
  Khan et al. 2024 triplet (68/54/41 from baseline 100).
- `TestApplyClimatePenalty_SeverityScaling` verifies linear scaling.
- `TestApplyClimatePenalty_ClampedAt95Percent` verifies the floor.
- `TestApplyClimatePenalty_NilScenarioPassthrough` verifies bit-identity.
- `TestRunSimulation_ClimatePenaltyDropsRecordedGain` end-to-end verifies
  recorded gain ratio ≈ 0.54 under `combined_postflowering`.
- `TestRunSimulation_NoClimateIsBitIdenticalToBaseline` end-to-end
  verifies the `Climate = nil` path.

### Issue closed

- `issues-breedos/28-climate-phenotype-penalty.md` →
  `issues-breedos-done/28-climate-phenotype-penalty.md.done`.

## [0.7.23] - 2026-05-28

### Added — Five small improvements (A/B/C/D/E)

**A — Climate Issue 27 foundation.** `mvp/climate.go` introduces
`ClimateStressMode` / `ClimateScenario` types and an 8-mode catalog
(normal, heat_burst_anthesis, heat_burst_booting, heat_grain_filling,
combined_postflowering, prolonged_heat, drought_terminal, salinity_chronic)
with audited 2026-05-28 windows + effect families. `LookupClimateMode`,
`ValidateClimateScenario`, and `ClimateModesCatalog` helpers exposed.
**No simulation impact yet** — Issue 28 (phenotype-penalty model) wires
these into the simulator.

**B — Sensitivity sweep `trait_weight:<name>` axis.** The existing
v0.7.16/v0.7.17 sweep engine gains a dynamic axis family for multi-trait
runs. Operator can sweep e.g. `trait_weight:methane_intensity` across
`[-1, -0.5, 0, 0.5, 1]` and see how the recommended strategy shifts.
Closes the Methane pack workflow gap ("what if I move the methane weight
to -1.0?"). The form's axis dropdown auto-populates the trait-weight
options when multi-trait state is active.

**C — `/api/version` endpoint.** Returns `{version, commit, build_time}`
JSON. `breedosCommit` and `breedosBuildTime` overridable at build time
via `-ldflags="-X main.breedosCommit=<sha> -X main.breedosBuildTime=<rfc3339>"`;
fall back to "dev"/"unknown" otherwise. Useful for deploy verification
without grepping HTML.

**D — Form Reset button.** New "↺ Reset" button after the preset row.
Loads the Balanced default and clears `multiTraitState` (so a methane
preset gets fully wiped, not just the form fields).

**E — `/datasets` page Generated datasets section.** New "Generated
datasets — in-memory simulators, no download" section listing
`holstein_synthetic` with the honesty-layer disclosure ("NOT real bovine
genotypes...") and external-reference pointers to 1000 Bull Genomes Run 8
(NCBI public) and Run 9 (CNGB controlled access). Backend
`datasetAPIResponse.GeneratedDatasets` populated by
`generatedDatasetsCatalog()`; frontend renders the new section
conditionally.

6 new tests in `mvp/climate_test.go` cover: known/unknown mode lookup,
negative-severity rejection, unknown-mode rejection, zero-severity
acceptance, alphabetical catalog order, anthesis-window correctness for
the three canonical heat windows.

### Changed
- Version strings bumped `v0.7.22` → `v0.7.23` across `main.go`, four landing footers, demo kicker, datasets-page kicker.

## [0.7.22] - 2026-05-28

### Added — Methane-pack complete (Issues 22–26) + Holstein Issue 19

The full Methane-pack ships in one bundle, riding on the v0.7.21 multi-trait engine.

**Issue 22 — Methane trait module** (`mvp/methane_module.go`). Constants
for methane heritability (MeY 0.244, MeI 0.180, MeP 0.211 per Brito et al.
2022 meta-analysis) and the audited correlations with milk yield: MeI ×
corrected milk yield = −0.26 (favourable), MeY × corrected milk yield =
−0.43 (favourable), MeP × milk yield = +0.35 (unfavourable). Helpers
`methaneIntensityDefaults()` and `methaneProductionDefaults()` build the
2-trait config + correlation matrix used by the presets.

**Issue 23 — N-D Pareto + axis picker.** Backend
`multiTraitDominates` + `annotateMultiTraitPareto` generalise the existing
single-trait domination by adding one dimension per trait gain. Frontend
adds X / Y axis dropdowns above the Pareto canvas; the chart projects N-D
dominance onto the chosen 2D plane while keeping the non-dominance
white-outline accurate.

**Issue 24 — Selection-index weight composer.** New collapsible form
section "Selection index (weights per trait)" surfaces sliders + number
boxes for each trait's weight (range −2 to +2, step 0.05). Live preview
text labels the net selection direction ("prioritises X · suppresses Y").
Hidden when no multi-trait state is active.

**Issue 25 — Methane preset buttons.** Two new preset buttons next to
Holstein dairy: "Methane MeI" (favourable −0.26 correlation; the operator-
facing happy path) and "Methane MeP" (unfavourable +0.35; educational
contrast). Both bundle Holstein-typical scale + the methane trait + matching
genetic-correlation matrix.

**Issue 26 — Multi-trait decision report section.** When the run has 2+
traits, the Decision Report's interpretation block gets a paragraph
listing per-trait gains, the selection weights that drove them, and the
"additive-only model" caveat (no dominance / epistasis in the simulator).

**Holstein Issue 19 — Synthetic Holstein founder dataset**
(`mvp/holstein_dataset.go`). New `dataset = holstein_synthetic` value
generates a Beta(0.5, 0.5) MAF founder population sampled at Hardy-Weinberg
equilibrium. Honesty-layer disclosure in `sourceNotes` makes the synthetic
status explicit; the registry page entry points operators to 1000 Bull
Genomes Run 8 (public NCBI) and Run 9 (controlled CNGB) for real data.

**Holstein Issue 21 Pareto overlay — DEFERRED.** Conflating standardised
gain units with kg-of-milk inbreeding cost on the same axis would be
misleading. The text-only inbreeding-cost note (shipped v0.7.20) is the
authoritative reporting path. Documented in the issue's completion note.

Schema (additive only):

- `SimRequest.Traits` and `SimRequest.GeneticCorrelations` already
  introduced in v0.7.21 — now used by presets and by the new composer.
- `StrategyResult.PerTraitMetrics` and `DecisionSummary.PerTraitGain`
  consumed by the Pareto axis-picker and the report section.

10 new unit tests in `mvp/methane_test.go` cover: methane intensity / production preset shape and correlation signs (3); presets pass Cholesky decomposition (1); synthetic Holstein dataset shape, dosage range, and AFS U-shape verification (2); N-D Pareto domination — strict / tradeoff / better-on-other-axis (3); multi-trait report section appears (1); multi-trait + Holstein-synthetic end-to-end (1).

### Changed
- Version strings bumped `v0.7.21` → `v0.7.22` across `main.go`, four landing footers, demo kicker, datasets-page kicker.

## [0.7.21] - 2026-05-28

### Added — Multi-trait selection engine (Issue 18, shared infra)

When `req.Traits` is non-empty, the simulator branches to a new
`runMultiTraitSimulation` path in `mvp/multitrait.go`. Per-trait marker
effects are drawn with the requested genetic-correlation structure via
Cholesky decomposition of the T×T correlation matrix; per-trait
phenotypes are computed with their own h² environmental noise; selection
runs on a weighted standardised index `I_i = Σ_t w_t · (p_{t,i} − mean_t) / sd_t`.
Per-trait gain for the recommended (best risk-adjusted) strategy lands
in `DecisionSummary.PerTraitGain` (keyed by trait name); per-generation
per-trait metrics land in `StrategyResult.PerTraitMetrics`.

Schema (additive only):

- `SimRequest.Traits []TraitConfig` — per-trait architecture.
- `SimRequest.GeneticCorrelations [][]float64` — T×T correlation matrix.
- `StrategyResult.PerTraitMetrics [][]MetricPoint` (per-trait trajectories).
- `StrategyResult.SelectionIndex []float64` (per-generation mean index of selected parents).
- `DecisionSummary.PerTraitGain map[string]float64`.

**Backward compatibility:** `req.Traits` empty/nil → existing single-trait
path runs unchanged. The branch lives at the top of
`runSimulationWithCallbacks`. Verified by
`TestRunSimulation_SingleTraitPayloadStillWorks`.

**Strategy rules in the multi-trait path:** `no_selection` and `random`
behave as today; all other rules map to index-based truncation selection
in this MVP. The OCS-like similarity penalty is not yet ported to the
multi-trait path — documented in a run-level note.

15 new unit tests in `mvp/multitrait_test.go`:
empty traits / duplicate names / non-square matrix / bad diagonal /
out-of-range correlation / asymmetric correlation / modest-valid case;
Cholesky identity / 2×2 closed form / general `LL^T = C` verification on
a 3×3; selection-index closed-form (5-individual hand-checked);
QTL masking (top-N by |effect|); end-to-end smoke (per-trait gain map);
strong-correlation tug-of-war (finite outputs); single-trait backward-
compat regression.

### Changed
- Version strings bumped `v0.7.20` → `v0.7.21` across `main.go`, four landing footers, demo kicker, datasets-page kicker.

## [0.7.20] - 2026-05-28

### Added — Holstein-pack first slice (Issues 17, 20, 21)

First three issues of the Holstein-inbreeding pack ship together. Issue 18
(multi-trait engine) and Issue 19 (real-data dataset adapter) are deferred
to v0.7.21+ because they require substantially more work and don't gate
the visible value of the other three.

**Issue 17 — Holstein dairy preset.** New preset button "Holstein dairy" in
the demo's preset row. Defaults reflect the 2026-05-28 audited dairy
literature: N=800 cows, 1500 markers, 8-generation (~40-year) horizon,
h²=0.36 for milk yield, selection_percent=12 (bull-dam tier), 5 replicates,
inbreeding_limit=0.20. Single-trait until Issue 18 ships.

**Issue 20 — Effective-population-size (Ne) trajectory chart.** New chart
card "Effective population size" after the Diversity / Inbreeding-risk
pair. Backend computes `Ne = 1 / (2 ΔF)` per generation (Falconer & Mackay
ch. 5), capped at 10000 when ΔF ≤ 0 to avoid divide-by-zero on the
log-scale chart. Frontend renders Ne on log10 axis [10, 10000] with FAO
reference lines at Ne=100 (vulnerable, yellow dashed) and Ne=50
(long-term-viability, red dashed). One line per strategy with the existing
colour palette.

**Issue 21 — Inbreeding-cost interpretation in the Decision Report.** When
the recommended strategy ends with F > 0.01, the Decision Report appends a
sentence translating that F into expected milk-yield drag under published
Holstein inbreeding-depression coefficients (range 20–65 kg per 1% F,
default ≈ 45 kg/F — audited 2026-05-28 against Bjelland 2013, Italian
Holstein, Canadian Holstein literature). The note explicitly states the
single-trait assumption (treating the modelled trait AS milk yield) so
that multi-trait runs (when Issue 18 ships) can override the default.

Schema changes (additive only):

- `MetricPoint` gains `ne float64` (per-generation effective population
  size from the post-pass over the inbreeding trajectory).
- `DecisionSummary.Interpretation` includes the inbreeding-cost note when
  applicable (existing field, new entry).

9 new unit tests in `mvp/holstein_test.go` cover: ΔF≤0 cap; standard ΔF
values (0.01 → Ne=50; 0.005 → Ne=100; etc.); numerical-tiny ΔF cap;
generation-0 cap; monotonic-decrease verification; empty/singleton edge
cases; cost-formula zero/negative guards; audited-range cost at F=5%;
linear-in-F growth.

### Deferred to v0.7.21+

- Issue 18 (multi-trait engine — shared infra for Methane pack).
- Issue 19 (Holstein dataset adapter — needs operator-side dataset
  acquisition; Run 8 public via NCBI, Run 9 controlled access).

### Changed
- Version strings bumped `v0.7.19` → `v0.7.20` across `main.go`, four landing footers, demo kicker, datasets-page kicker.

## [0.7.19] - 2026-05-28

### Fixed — NGT Path (ii) gene-pool insertion check (Issue 32 errata to v0.7.18)

The v0.7.18 NGT pack was audited against the final EU regulation text
(Council adoption 2026-04-21, applies from mid-2028) and one correctness gap
was found: Annex I distinguishes two NGT-1 paths inside the 20-modification
envelope —

- **Path (i):** deletions / inversions of any size, OR insertions /
  substitutions of ≤ 20 arbitrary nucleotides; anywhere in the genome.
- **Path (ii):** any-sized contiguous DNA from the breeder's gene pool, but
  only if no endogenous gene is disrupted.

The v0.7.18 classifier did not encode the "no endogenous gene is disrupted"
clause and treated `donor_source = same_species | same_gene_pool` as NGT-1
eligible without that confirmation. This was a **silent false-positive in
the safe direction** — operator could be told "NGT-1" (deregulated) for a
case that should be NGT-2 (full GMO authorisation). For a planning aid this
is the worst failure mode.

**Fix:**

- `NGTContext` gains two new optional fields:
  - `variant_type`: `snv_or_small` (default) / `inversion` / `deletion` /
    `gene_pool_insertion`. Empty defaults to `snv_or_small` for v0.7.18
    payload compatibility.
  - `endogenous_gene_interrupted` (`*bool`): `nil` = not declared,
    `false` = operator confirmed no endogenous gene disrupted, `true` =
    disqualifies NGT-1. Mandatory for `variant_type = gene_pool_insertion`.
- `ClassifyEditSet` refactored to branch Path (i) vs Path (ii) explicitly.
  Path (ii) without `endogenous_gene_interrupted` set returns
  `unclassifiable` (never silently grants NGT-1).
- Path (ii) with `donor_source` other than `same_species` / `same_gene_pool`
  is rejected with an explicit disqualifier.
- Confidence-note disclaimer updated to cite Annex I and the 2026-04-21
  Council adoption date verbatim.
- Donor-source tooltip updated with the Annex I gene-pool definition.

UI: form gains a `variant_type` select inside the NGT collapsible. When
`gene_pool_insertion` is chosen, an additional `endogenous_gene_interrupted`
select becomes visible (hidden for Path (i) cases to keep the form lean).

9 new unit tests cover: disclaimer cites 2026-04-21; backward-compat default
to `snv_or_small`; invalid variant_type → unclassifiable; inversion / deletion
pass; Path (ii) unconfirmed → unclassifiable; Path (ii) interrupted → NGT-2;
Path (ii) clear → NGT-1; Path (ii) with `donor_source=none` fails; Path (ii)
with `donor_source=cross_species` fails twice. Full suite: 22 NGT tests pass
(13 pre-existing + 9 new); `go test ./...` clean.

### Changed
- Version strings bumped `v0.7.18` → `v0.7.19` across `main.go`, four landing footers, demo kicker, datasets-page kicker.

## [0.7.18] - 2026-05-24

### Added — EU NGT regulatory classification layer (Issues 13–16)

The EU NGT Regulation (Council adopted 2026-04-21, applies from mid-2028)
splits gene-edited plants into two categories with very different downstream
costs. BreedOS now classifies every planned edit set in real time so the
operator sees the category implication *before* committing to a CRISPR
strategy.

**Issue 13 — classification engine** (`mvp/ngt_classify.go`, ~170 lines + 13 tests):
classifies under the **20/20 rule** (max 20 modifications, each insertion ≤ 20 bp)
plus auto-exclusions (`herbicide_tolerance` and `insecticidal` trait classes
disqualify NGT-1) and donor-source rules (`cross_species` introduces foreign
DNA → disqualifies). Output: `NGT-1` | `NGT-2` | `unclassifiable` (when
inputs are missing). Every result carries a verbatim "Not legal advice"
disclaimer.

**Issue 14 — candidate-edit badge**: every row in the CRISPR edit candidates
table now ends with a colour-coded badge (green NGT-1, orange NGT-2, grey
unclassifiable). Hover/focus tooltip shows reasons, disqualifiers, and the
disclaimer.

**Issue 15 — Regulatory card in the Decision Report area**: a new card
above the candidate-edits table renders the category headline, a list of
reasons, disqualifiers (if any), one paragraph of downstream implications
(registration path, labelling, traceability), and the disclaimer.

**Issue 16 — patent / licensing declaration fields**: optional run-level
fields (`patent_id`, `licensing_status`, `notes`) collapsible inside the
CRISPR form section. When the run is classified NGT-1 *and* `patent_id` is
empty, the Regulatory card surfaces a warning that NGT-1 registration will
require an explicit patent declaration. All fields round-trip into the JSON
export so they propagate to a registration filing.

Schema changes (additive only):

- `SimRequest.NGT` (optional `NGTContext` with `target_trait_class`,
  `donor_source`, `patent_id`, `licensing_status`, `notes`).
- `DecisionSummary.NGT` (optional `*NGTClassification` with `category`,
  `reasons`, `disqualifiers`, `confidence_note`).

Backward compatibility: runs without `req.NGT` produce the existing API
shape unchanged (omitempty); UI hides the Regulatory card and shows a
"—" placeholder badge.

### Changed
- Version strings bumped `v0.7.17` → `v0.7.18` across `main.go`, four landing footers, demo kicker, datasets-page kicker.

## [0.7.17] - 2026-05-24

### Fixed — Sensitivity sweep "looked frozen" on slow prod

v0.7.16 passed a no-op progress callback into the inner simulation, so the
percent bar in the sensitivity sweep panel sat at one value (e.g. 0%, then
20%, then 40%, ...) for the entire duration of one scenario. On prod
(single-core VPS, ~15–30 s per scenario) this read as "frozen" to the
operator who reported the issue with a screenshot during a live sweep.

**Fix:**

- Propagate the inner-simulation progress callback into the sweep so each
  scenario fills its own 20% slice continuously. Outer percent =
  `(scenario_index * 100 + inner_percent) / num_scenarios`.
- Move `Percent` from `int` to `float64` in `SensitivityJobStatus` and the
  in-memory job store so the displayed value can be e.g. `27.4%` instead of
  rounding away the sub-scenario progress.
- Frontend formats one decimal place mid-run (`27.4%`), rounds to integer
  on completion (`100%`).
- Inner runner's own message string (`parallel run: 334/600 strategy-
  generations complete; ...`) is appended in parentheses so the operator
  sees that work is happening inside the scenario, not just at boundaries.

### Changed
- Version strings bumped `v0.7.16` → `v0.7.17` across `main.go`, four landing footers, demo kicker, datasets-page kicker.

## [0.7.16] - 2026-05-23

### Added — Sensitivity sweep (Issue 09)

Runs the same configuration across up to 5 values of one axis (heritability, selection percent, or generations horizon) and compares the best-feasible strategy across scenarios. The recommendation gets a verdict:

- **stable** — same strategy wins in every scenario; recommendation is robust to changes in that axis within the sampled range.
- **fragile** — strategy switches in at least one scenario; the single-run recommendation may not hold if the axis differs from the baseline.
- **inconclusive** — all scenarios infeasible; loosen constraints.

The UI lives under the Decision engine output: axis dropdown, comma-separated values (with sensible defaults per axis), Run sweep button, sweep-budget meter, results table (gain / diversity / inbreeding / combined risk / match-to-baseline per scenario), and the verdict banner.

API:
- `POST /api/sensitivity/start` → `{job_id}`
- `GET /api/sensitivity/status?id=X` → `{percent, message, done, result?}`

Budget cap: the **sum** of per-scenario budgets must be ≤ 1.5B cells (same cap as a single run, applied to the sweep total). Client-side pre-flight blocks oversized sweeps before submit. Server-side validation runs every scenario through `validateRequest` upfront so axis-specific range violations (e.g. h² outside [0,1]) get a per-scenario error message instead of a mid-sweep failure.

Backend in `mvp/sensitivity.go` (~250 lines). Tests in `mvp/sensitivity_test.go` cover validation rejects, baseline-nearest indexing, stable/fragile/inconclusive verdict logic, and the `BestFeasibleCode → BestRiskAdjustedCode` fallback when no constraints are applied.

### Changed
- Version strings bumped `v0.7.15` → `v0.7.16` across `main.go`, four landing footers, demo kicker, datasets-page kicker.

## [0.7.15] - 2026-05-22

### Added — Live budget meter under Run

`mvp/static/demo.html` shows a budget meter directly below the Run button. It prints the current run budget in cells (`N × markers × (generations+1) × strategies × replicates`), the formula breakdown, and the cap. The meter is reactive — updates on every keystroke via an `input` listener, not just on `change` — so the user sees the impact of typing as it happens.

States:
- **OK** (< 70% of cap): muted text, green budget number, Run enabled.
- **Warn** (70–100% of cap): warn-yellow budget number, Run still enabled.
- **Over** (> cap): red budget number, Run button disabled, and the four numeric inputs that multiply into the budget (`population_size`, `markers`, `generations`, `replicates`) get a red `over-budget` outline so the user can immediately see which knobs to turn down.

Pre-flight check in `runSimulation()` short-circuits over-budget submits so the request never leaves the browser — previously the only signal was a generic 400 after submit, which was easy to mistake for stuck client state when reducing one parameter still left the run over cap.

### Changed — Budget cap raised 800M → 1.5B

`validateRequest()` in `main.go` now caps `budget` at 1,500,000,000 (was 800,000,000). Rationale: 800M was set in the initial v0.6 commit without recorded justification; benchmarks show wall-clock cost is ~2.2–2.7 ns per cell on dev (multi-core), and the prod box is a single-core VPS. Raising to 1.5B keeps the upper-bound run at ~30–60 s on prod — long but tolerable for a public demo. The JS `BUDGET_CAP` constant in `app.js` is kept in sync.

### Changed
- Version strings bumped `v0.7.14` → `v0.7.15` across `main.go`, all four landing footers, demo kicker, and datasets-page kicker.

## [0.7.14] - 2026-05-22

### Fixed — Live histogram stuck on gen-0 (real root cause)

The v0.7.13 snapshot queue + playback shipped the right architecture but the user reported the chart still stuck on the first generation. DevTools network log showed the cursor frozen at `?since=1` for 50+ polls.

**Root cause (this time for real):** the worker pool enqueued tasks in `[strategy_index][replicate]` order. The tracked task (default "balanced") sat at queue position `trackIdx × replicates` (= 10 in the default core+CRISPR set), so on a slow production host the tracked task didn't START for 10-15 seconds while other strategies ran. The frontend got the gen-0 snapshot (emitted before the worker pool starts) and nothing else until then.

**Fix:** enqueue the tracked task FIRST. A worker grabs it immediately, snapshots start arriving on poll #1. The rest of the queue is enqueued in the usual order, skipping the already-queued tracked entry. Combined with the v0.7.13 client-side queue + playback, the user now sees the chart animate from poll #1 onwards even when total run time is 15+ seconds.

### Added — Favicon

`mvp/static/favicon.ico` — 32×32 32bpp ICO with a stylised mint-green "B" on a dark teal background, matching the landing-page accent palette. Generated by `tools/data/make_favicon.py` (stdlib-only Python, no PIL/cairo). All six HTML pages now reference it via `<link rel="icon" type="image/x-icon" href="/favicon.ico">`; the Go static handler emits `Content-Type: image/x-icon` for `.ico` paths.

### Changed
- Version strings bumped `v0.7.13` → `v0.7.14` across `main.go`, all four landing footers, demo kicker, and datasets-page kicker.

## [0.7.13] - 2026-05-22

### Fixed — Live histogram drops frames (snapshot queue + client playback)

Diagnosis: the simJob stored only one `LatestSnapshot`. The browser polled every 80 ms but the simulation often emitted all 15-30 per-generation snapshots in well under 80 ms, so most snapshots were overwritten before they were ever sent. The chart visibly jumped from generation 0 to whatever the latest single snapshot happened to be — not an animation.

Fix:

- **Backend.** `simJob` now keeps `Snapshots []AFSSnapshot` (the full history) plus a derived `SnapshotSeq = len(Snapshots)`. `updateSimulationJobSnapshot` appends to the slice instead of overwriting a single field. The `/api/simulate/status` handler accepts `?since=<N>` and returns `Snapshots[since:]` plus the new `snapshot_seq`. The legacy `latest_snapshot` field is still emitted for backwards compatibility.

- **Frontend.** The poll loop is the producer: it asks for `?since=snapshotSeq` and appends the returned `snapshots[]` to a client-side queue `pendingFrames[]`. An independent `setTimeout` chain (`playNextHistogramFrame`) is the consumer: it dequeues one frame and draws it every `PLAYBACK_FRAME_MS` (90 ms). The playback continues running after the poll loop exits, draining the queue. A backend that finishes 20 generations in 300 ms now plays back as a ~1.8 s animation in the UI — visibly live.

- **Smoke verification.** A 15-generation run produces `snapshot_seq=16` (generations 0-15 inclusive) and `?since=10` returns only the trailing 6 frames. Confirmed locally.

### Not changed
- Concurrency model: still one tracked strategy + replicate 0 only; `simJobStore.Mutex` guards the slice write/read.
- `latest_snapshot` is still emitted (omitempty) so older clients keep working.
- SSE / true streaming endpoint deferred — the polling+queue approach is a small diff with no new transport layer.

### Changed
- Version strings bumped `v0.7.12` → `v0.7.13` across `main.go`, all four landing footers, the demo kicker, and the datasets-page kicker.

## [0.7.12] - 2026-05-22

### Added — Public-wheat datasets registry

A curated list of public wheat genotype datasets is now bundled with BreedOS and exposed at **`/datasets`**.

`breedos/datasets/` (new folder):
- `README.md` — usage notes + fetch commands. Tracked.
- `.gitignore` — ignores everything except the README and itself. Tracked.
- Raw archive files (`*.vcf`, `*.zip`, `*.xlsb`, ...) — **gitignored**. Fetched locally per the README; not redistributed by BreedOS.

`breedos/mvp/datasets-manifest.json` (new tracked file, embedded into the binary via `//go:embed`):
- Six entries spanning small + large + manual-only sources:
  - `wheat_durum_figshare_259` — Figshare durum wheat 259 × 7817, VCFv4.2, 8.45 MB, full upload.
  - `wheat_dryad_159_55k` — Dryad 55K SNPs × 159 wheat, .xlsb, 20.4 MB, manual download (Dryad blocks programmatic).
  - `wheat_dryad_pakistani_37k` — Dryad Pakistani 37K, .xlsx set, 30.2 MB, manual download.
  - `wheat_inrae_1000_exomes` — INRAE 1000 wheat exome ZIP, 2.25 GB, truncated to 100 MB on server.
  - `wheat_zenodo_28m_v21` — Zenodo same SNPs lifted to RefSeq v2.1, 9.2 GB VCF, truncated to 100 MB on server.
  - `wheat_watkins_g2b` — Watkins G2B portal landraces+cultivars, per-chromosome VCFs, manual.

### Added — `/datasets` page and `/api/datasets` endpoint

`mvp/datasets_api.go`:
- `GET /api/datasets` reads the embedded manifest, merges it with the current on-server file sizes from `<bindir>/data/datasets/`, returns combined JSON. Each entry carries: id, name, full `size_bytes`, current `deployed_bytes`, `status` (`full` / `truncated` / `manual` / `missing` / `stale`), category, deploy strategy, source URL, landing URL, license, content description.

`mvp/static/datasets.html`:
- Plain-JS page that fetches `/api/datasets` on load and renders a table with columns: Name, Original size, On server, Status, Accessions × markers, Format, License, Source, Content.

### Changed — `deploy_breedos.sh`

The deploy script now iterates `mvp/datasets-manifest.json` entries and uploads files from `breedos/datasets/` to `<server>/data/datasets/<filename>` per the declared `deploy_strategy`:
- `full` → `scp` whole file (size-skip applies).
- `truncate_head_100mb` → upload only `head -c 100MB` (truncation cap is the manifest's `deploy_truncate_mb`).
- `manual` / `large_external` → skipped.

Set `BREEDOS_DEPLOY_FULL_LARGE=1` to override truncation and upload the full local file (use after a server-disk upgrade).

### Changed
- Version strings bumped `v0.7.11` → `v0.7.12` in `main.go` run notes, all four landing footers, and the demo kicker.

### Not in this release
- Big-dataset local downloads via the sandbox proxy timed out at partial bytes (848 MB of the INRAE 2.25 GB, 461 MB of the Zenodo 9.2 GB). The partials are valid file prefixes — the 100 MB truncation for server deploy works from them. Operator should re-fetch full archives on their workstation for a complete local copy.
- Dryad files (55K + Pakistani 37K) cannot be fetched programmatically from this sandbox (HTTP 403). Marked `deploy_strategy: "manual"` in the manifest; operator visits the landing URL and saves files into `breedos/datasets/` before the next deploy.

## [0.7.11] - 2026-05-22

### Fixed — Demo shell width alignment

The demo page now uses one explicit demo-width container (`--demo-max: 1340px`) for the navigation, honesty banner, title card, and bottom workbench. This fixes the remaining visual mismatch where the top of `/demo` was constrained by the generic landing-page `.wrap` width (`1180px`) while the lower simulation workbench appeared wider.

Added nested `min-width: 0` guards for demo cards, result panels, grid children, chart cards, histogram cards, and table wrappers so charts/tables shrink or scroll inside the shared shell instead of widening the page.

### Changed
- Demo kicker and version strings bumped `v0.7.10` → `v0.7.11`.

## [0.7.10] - 2026-05-22

### Fixed — demo-grid width (definitive)

`.demo-grid` switched from CSS Grid (`grid-template-columns: 360px 1fr` / later `minmax(0, 1fr)`) to Flexbox:

```css
.demo-grid {
  display: flex;
  flex-direction: row;
  gap: 18px;
  align-items: flex-start;
}
.demo-grid > .sticky-panel { flex: 0 0 360px; }
.demo-grid > .results { flex: 1 1 0%; min-width: 0; }
```

`flex: 1 1 0%` + `min-width: 0` is the classic shrinkable-fill pattern and constrains the flex container to its parent width without the track-sizing surprises Grid was producing. Verified on production v0.7.9 that the earlier `minmax(0, 1fr)` + `min-width: 0` on grid items was still rendering the demo-grid visibly wider than the top hero / title cards.

The `@media (max-width: 980px)` rule was updated to use `flex-direction: column` for narrow viewports (sticky panel collapses to full-width stacked above results).

### Reverted — Cache-control hacks from v0.7.9

v0.7.9 introduced `Cache-Control: no-cache, must-revalidate` on the Go static handler and `?v=v0.7.9` query strings on every `<link rel="stylesheet">`. These were a misdiagnosis (the user works in dev mode with cache disabled; the width issue was the real CSS bug, not a stale cache). Removed both:

- Static handler returns to default no-cache-header behaviour. If real cache headers are wanted later, that's a separate, deliberate decision with its own scope.
- All five HTML files reference `/style.css` (no query string).

### Changed
- Version strings bumped `v0.7.9` → `v0.7.10` across `main.go`, all four landing footers, and the demo kicker.

## [0.7.9] - 2026-05-22

### Fixed — Demo-grid width (proper fix)

The v0.7.8 attempt (`min-width: 0` on grid items) wasn't sufficient. Stronger fix:

- `grid-template-columns: 360px 1fr` → `360px minmax(0, 1fr)`. Bare `1fr` resolves its minimum size to the column's min-content (which was being inflated by 900-px-wide canvases and a 14-column strategy table). `minmax(0, 1fr)` explicitly caps the minimum at 0, so the column never expands the grid past the parent `.wrap`.
- `max-width: 100%` belt-and-suspenders on `.demo-grid`.

### Fixed — Browser cache serving stale CSS / JS across deploys

The Go static handler did not set any cache headers, so browsers heuristic-cached `/style.css`, `/app.js`, and `/index.html` for several minutes. After a deploy, users would still see the previous version's styles even though the new binary was serving fresh content.

Two-pronged fix:

- The static handler now emits `Cache-Control: no-cache, must-revalidate` on every response. Browsers will revalidate on each load. (Bandwidth impact is small; the assets are tens of KB and we always send 200, but at least we are never stale.)
- All five HTML files reference `<link rel="stylesheet" href="/style.css?v=v0.7.9">`. The query string busts the URL-keyed cache for already-cached browsers that load the new HTML first.

### Changed
- Version strings bumped `v0.7.8` → `v0.7.9` in `main.go` run notes, all four landing footers, and the demo kicker.

### Background
- The user reported that after the v0.7.8 deploy the demo-grid was still visibly wider than the top hero cards. Diagnosed two contributing causes: (a) `min-width: 0` on grid items isn't enough when the track itself is `1fr` (track resolves min size to min-content), and (b) browser cache likely served the v0.7.7 CSS even though the v0.7.8 binary was serving fresh content. This release addresses both.

## [0.7.8] - 2026-05-22

### Fixed — Demo-grid width / top hero narrower than bottom

Top hero cards (honesty banner, "Selection Strategy Simulator" title) rendered visibly narrower than the bottom two-column `.demo-grid`. Root cause: the `1fr` right column of `.demo-grid` was being forced to its min-content size by the embedded 900px-wide canvases (chart_gain, chart_pareto, the new chart_histogram). Without `min-width: 0` on grid items, CSS Grid expands `1fr` past its parent's width to accommodate min-content. The result: `.demo-grid` was ~30 px wider than `.wrap`.

Fix: `.demo-grid > * { min-width: 0; }` and an explicit `min-width: 0` on `.results`. Both columns now honor the parent `.wrap` width; the demo-grid and the top hero cards line up.

### Fixed — Live histogram delay at start + jitter at end

Three small fixes addressing the user-reported delay + twitching:

- **Initial generation-0 snapshot.** The backend emits a snapshot of the founder population *before* the worker pool starts running generations, so the live histogram has content the instant the client begins polling — no perceived startup delay waiting for generation 1 to finish.
- **Stable Y-axis.** `drawLiveHistogram` now anchors the Y-axis to the total marker count (sum of bins, constant within a run) instead of the dynamic per-snapshot max. Bars represent the fraction of markers in each frequency bin and the scale never rescales — no visible "jumping" as alleles drift toward fixation in late generations.
- **Dedup'd redraws.** The polling loop now keeps a `lastDrawnGeneration` counter and only redraws when the snapshot's generation actually advances. Repeated polls reading the same final snapshot no longer trigger `clearRect` + redraw, which was causing subtle jitter near run end.
- **Snappier polling (120 ms → 80 ms).** Tighter loop interval for crisper per-generation updates. The status endpoint is cheap; no meaningful extra server load.

### Added — Tracked-strategy picker for the live histogram

New `tracked_strategy` field on `SimRequest` (string; default `""` / `"auto"` = prefer "balanced" else first configured strategy). When set to a strategy code (e.g. `"aggressive"`, `"ocs_like"`), the live AFS histogram tracks that strategy instead of the default. If the selected code isn't part of the current strategy set (e.g. an advanced-only code while running `strategy_set: "core"`), the server falls back to auto behaviour — no error.

Demo form gains a "Live histogram tracks" `<select>` below the strategy-set dropdown with all 11 strategy codes plus "auto". `requestSignature` and `changedParams` include `tracked_strategy` so cache invalidation works when the operator changes only the picked strategy.

### Changed
- `runSimulation` and `runSimulationWithProgress` still wrap `runSimulationWithCallbacks` (unchanged signatures).
- Version strings bumped `v0.7.7` → `v0.7.8` in `main.go` run notes, all four landing footers, and the demo kicker.

### Not changed
- Wheat fetch script unchanged in this release. (The CerealsDB endpoint returned a 503 through the proxy during one fetch attempt; retry after CerealsDB is healthy.)
- Histogram concurrency model (single tracked strategy, single replicate, mutex-protected snapshot writes) unchanged.

## [0.7.7] - 2026-05-22

### Fixed — Misleading language switcher on demo

The language switcher previously appeared in the demo nav too, but clicking RU/ES/UZ on demo threw the user to the localized landing — not to a translated demo (the demo is intentionally English-only, one source of truth for the technical surface). The user reasonably read this as a broken promise.

Fixes:
- Language switcher removed from `demo.html` nav. The demo now shows only the "Landing" link back to /. Users who want to switch language do so from the landing page.
- Each localized landing (`index-ru.html`, `index-es.html`, `index-uz.html`) gains a small italic `.lang-note` directly under the demo CTA stating that the demo and Decision Report are English. Removes the surprise of clicking "Запустить демо" on the Russian landing and arriving on an English page.

### Changed
- New `.lang-note` style in `style.css` — subtle muted italic note, max-width 640 px.
- Version strings bumped v0.7.6 → v0.7.7 in `main.go` run notes, all four landing footers, and the demo kicker.

### Not changed
- The landing-page language switcher remains exactly as in v0.7.6 — that one IS honest (clicking RU on the English landing takes you to the actual Russian landing).
- Histogram, wheat fetcher, dataset loader, constraint engine, honesty layer, self-update — all unchanged.

## [0.7.6] - 2026-05-22

### Added — Live allele-frequency-spectrum (AFS) histogram

While a simulation runs, the demo now shows a small live histogram below the Pareto chart that updates per generation. It bucketed the current allele frequencies of ONE tracked strategy (preferring `balanced`; otherwise the first strategy in the run) into 10 bins of width 0.1 and displays them as a bar chart. The final snapshot stays visible after the run completes.

Backend additions (`mvp/main.go`):
- `AFSSnapshot` struct with `generation`, `total_generations`, `strategy_code`, `strategy_name`, and `[10]int` bins.
- `snapshotFunc` callback type threaded through `runSimulationWithCallbacks` (new entry point; `runSimulation` and `runSimulationWithProgress` are preserved as thin wrappers so all existing tests pass).
- `simJob.LatestSnapshot` + `SimJobStatus.latest_snapshot` (JSON, `omitempty` for nullable client-side).
- `afsBinsFromPop` helper next to `alleleFreq`.

The decision engine picks ONE tracked strategy and only its replicate 0 emits snapshots — this avoids concurrent writes from parallel workers entirely. Snapshot writes still go through the existing `simJobStore.Mutex` for defense-in-depth. Verified with `go test -race ./...` (clean).

Frontend (`app.js`):
- `lastSnapshot` state, `drawLiveHistogram(snap)`, `resetLiveHistogram()`.
- The existing 120 ms `/api/simulate/status` polling loop now consumes `job.latest_snapshot` and redraws the canvas on each poll. No new polling cadence.
- Bar colour reuses the strategy's color from the existing `colors` map.

UI:
- New `.histogram-card` between the Pareto chart and CRISPR card (`demo.html`).
- Heading "Live allele-frequency spectrum — &lt;strategy&gt; generation N/M", ~800 × 160 canvas, explanatory note.
- `.histogram-card` and `.histogram-label` styles in `style.css`.

### Added — Wheat data fetcher (`tools/data/fetch_wheat_t3.py`)

Standalone Python 3 script (no external dependencies) that downloads a public wheat genotype subset and writes a BreedOS founder-CSV. Output dataset name: `wheat_t3`.

Default source: **CerealsDB 35K Wheat Breeders' Array** (University of Bristol — fully public, no auth required). Auto-detects ZIP / CSV / TSV / VCF (gzip-aware). Defaults to `--n 500 --m 5000 --maf 0.05`; output ~5 MB.

Supports `--source <url>` override so the operator can point it at a manually-downloaded T3/Wheat VCF (T3 requires a free account for the genotype-download endpoint) or the Watkins 12.7× WGS VCF (Cheng et al. 2024).

Hexaploidy is handled by treating per-locus diploid calls (AA / AB / BB from arrays, or 0/0 / 0/1 / 1/1 from VCF callers) as 0/1/2 dosage — documented in the script header and in the output CSV `# Ploidy note:` block.

Demo dropdown extended with `Wheat (T3 / CerealsDB)` option. As with Arabidopsis, the operator runs the fetcher locally, then `deploy_breedos.sh` uploads the resulting CSV to the server alongside the binary (size-skip).

### Added — Marketing localization (Russian, Spanish B1, Uzbek)

Three new landing pages alongside English:

- `/ru` → `index-ru.html` — Russian (native register, professional but not academic).
- `/es` → `index-es.html` — Spanish at **CEFR B1** level: simple tenses (presente, pretérito, pretérito perfecto, futuro simple), short sentences (≤ 20 words), common vocabulary, no subjunctive where avoidable.
- `/uz` → `index-uz.html` — Uzbek (Latin script, standard since 2018). Recommend founder review for terminology choices (`naslchilik` vs `seleksiya`, `belgi` vs `xususiyat`, etc.).

The demo and the Decision Report stay English (technical content; one source of truth). The nav on every page (landing + demo) has a four-way language switcher (`EN · RU · ES · UZ`) with `.active` styling on the current language.

Go routes added in `main.go`: `/ru`, `/es`, `/uz` serve the respective HTML; everything else routes unchanged.

CSS: new `.lang-switcher` block in `style.css` (subtle separator, hover, active highlight in accent colour).

### Changed
- Version strings bumped `v0.7.5` → `v0.7.6` in `main.go` run notes, all four landing footers (`index.html`, `index-ru.html`, `index-es.html`, `index-uz.html`), and the demo kicker.
- `runSimulation` and `runSimulationWithProgress` are now thin wrappers around the new `runSimulationWithCallbacks` (which also accepts a `snapshotFunc`). API and existing test signatures unchanged.

### Followup ideas (deferred)
- A select control above the live histogram letting the user pick which strategy to track. Today the choice is fixed at run-start (prefer `balanced`).
- Detect language preference via `Accept-Language` or cookie and redirect `/` accordingly; today users land on English and switch manually.
- Founder review of the Uzbek translation for terminology choices.

## [0.7.5] - 2026-05-22

### Changed — External real-data CSVs (not embedded in binary)

Large founder-data CSVs (Arabidopsis 1001 and any future maize / wheat panels) are no longer embedded into the binary via `//go:embed`. They live as separate files on the server alongside the binary in `<bindir>/data/<name>.csv`. The binary stays small (~6.5 MB); the data deploys independently.

`mvp/dataset.go` lookup order for a `dataset=<name>` request:

1. `<bindir>/data/<name>.csv` (external — operator-deployed).
2. Embedded `data/<name>.csv` (only if explicitly added to the `//go:embed` directive — currently only the placeholder is included).
3. Embedded `data/example_founders.csv` (placeholder fallback so the dropdown still works on fresh installs).

Parsed datasets are cached in a package-level map (`datasetCache`); repeat requests within the same binary instance reuse the parsed matrix instead of re-reading and re-parsing the file. A 100 MB CSV would otherwise cost ~1 s of parse time per simulation; with the cache, only the first request pays.

The `//go:embed` directive is narrowed from `data/*.csv` to `data/example_founders.csv` so that adding new CSVs to `mvp/data/` locally does NOT bloat the compiled binary.

### Changed — `.gitignore`

`mvp/data/*.csv` is now gitignored, with `mvp/data/example_founders.csv` whitelisted. Large CSVs produced by `tools/data/fetch_*.py` stay local; only the placeholder ships in git.

### Changed — `deploy_breedos.sh`

Before uploading the binary, the deploy script now uploads external data files conditionally:

- Iterates `mvp/data/*.csv` and selects files that are gitignored (i.e., external).
- Creates `<bindir>/data/` on the remote via SSH.
- For each external file, compares local size with remote size (`ssh stat -c %s`).
- Uploads via `scp` only when remote is missing or sizes differ.
- Skips upload when remote and local sizes match (byte-exact).

This keeps repeat deploys fast: the 100 MB Arabidopsis CSV is uploaded once and skipped on every subsequent deploy unless it changes.

### Added — Test for external-precedence

`TestExternalDatasetTakesPrecedenceOverEmbedded` writes a CSV into `<testbin-dir>/data/arabidopsis1001.csv` and verifies that `loadDataset` reads the external file (not the embedded placeholder), sets `ds.external = true`, and clears the placeholder flag.

### Changed — Notes language

`buildNotes` now distinguishes three cases when reporting the founder-population source:

- Placeholder fixture (warning).
- External real-data file (`external = true`).
- Embedded real-data file (only possible if a CSV is explicitly bundled — rare).

### Changed
- Version strings bumped `v0.7.4` → `v0.7.5` in `main.go` run notes, `index.html` footer, and `demo.html` kicker.
- Run-notes top description rewritten to mention the external-data deploy semantics.

### Migration notes
- Existing deployments with v0.7.4 had the (small) `arabidopsis1001.csv` embedded if the operator ran the fetcher before building. After upgrading to v0.7.5: on first run, the loader will NOT find the embedded data (new embed pattern), will look for `<bindir>/data/arabidopsis1001.csv`, and if missing, fall back to the placeholder. The deploy script handles this transition: it uploads the local CSV (gitignored) to `<bindir>/data/` before swapping the binary.
- Repos that imported `mvp/data/<some-csv>` into git inadvertently: the file will become gitignored. Remove from index with `git rm --cached`.

## [0.7.4] - 2026-05-22

### Added — Real-data founder population loader (closes `issues-breedos/04`)

The simulator can now load real founder genotypes from an embedded CSV instead of generating a synthetic population. The on-ramp for the Arabidopsis 1001 Genomes Project is the first wired-up dataset; the same loader works for any matrix in the BreedOS founder-CSV format.

`SimRequest` adds:

- `dataset` (string) — `"synthetic"` (default, current behaviour) or `"arabidopsis1001"` (loads from embedded CSV).

New module `mvp/dataset.go`:

- `loadDataset(name)` reads the matching `data/<name>.csv` via `//go:embed`, falling back to `data/example_founders.csv` so the dropdown still works even before the user runs the fetch script. Comment lines starting with `#` are ignored except for `# placeholder: true`, which marks the file as the non-real placeholder.
- `parseDatasetCSV` performs strict 0/1/2 validation; out-of-range values fail loudly.
- `subsampleDataset(ds, n, m, rng)` samples up to `n` accessions and the first `m` markers; called from `runSimulationWithProgress` so the existing `population_size` / `markers` knobs continue to scope the run.

When a dataset is selected, the simulator:

- Replaces the generated founder population with the loaded one.
- Overrides `req.PopulationSize` and `req.Markers` to match the loaded matrix.
- Adds a run note that either announces the real data source ("Founder population loaded from real-data file ...") or warns clearly that the placeholder is in use ("⚠ Dataset 'arabidopsis1001' is the embedded PLACEHOLDER fixture — run tools/data/fetch_arabidopsis_1001.py and rebuild ...").
- Updates the honesty banner, `key_assumptions`, and `limitations` to acknowledge real founders while making clear that selection / recombination / mutation in subsequent generations remain synthetic.

### Added — Python fetch script (`tools/data/fetch_arabidopsis_1001.py`)

A standalone Python 3 script (no external dependencies) that:

- Streams the 1001 Genomes v3.1 VCF (gzip-aware, HTTP or local file).
- Filters to biallelic SNPs with `--maf` ≥ 0.05 and `--max-missing` ≤ 0.10.
- Samples `--n` accessions and `--m` markers deterministically (`--seed`).
- Imputes any remaining missing genotypes at sampled sites with mean-dose rounding.
- Writes a BreedOS founder-CSV to `breedos/mvp/data/arabidopsis1001.csv`.

The Go loader picks up the new CSV on the next binary rebuild (`./mvp/build.sh ...`). See `tools/data/README.md` for the workflow.

### Added — Placeholder fixture

`breedos/mvp/data/example_founders.csv` ships a 60 × 200 synthetic-but-real-format matrix so that the dataset dropdown is exercised by tests and the live demo without requiring the user to run the fetch script first. The file is marked `# placeholder: true` and the simulator emits a prominent warning note when it's in use.

### Added — Dataset dropdown in demo

`demo.html` exposes a "Founder population" dropdown at the top of the simulation-inputs form with two options: `Synthetic (generated)` (default) and `Arabidopsis 1001 (subset)`. The selection is passed in the `dataset` request field; `requestSignature` and `changedParams` include it so cache invalidation behaves correctly when the operator switches data sources.

### Added — Tests

`mvp/main_test.go` adds four tests:

- `TestParseDatasetCSVRoundTrip` — parses a tiny 3 × 3 CSV with comments and the `placeholder: true` marker; verifies parsed values, accession IDs, and the placeholder flag.
- `TestParseDatasetCSVRejectsOutOfRange` — guarantees that a value outside `0..2` causes a parse error.
- `TestLoadDatasetFallsBackToPlaceholder` — verifies the loader fallback path when the requested dataset file doesn't exist.
- `TestDatasetRoutedThroughSimulation` — runs an end-to-end simulation with `dataset: "arabidopsis1001"` (which falls back to the placeholder) and confirms the placeholder warning appears in the response notes.

### Changed
- Version strings bumped `v0.7.3` → `v0.7.4` in `main.go` run notes, `index.html` footer, and `demo.html` kicker.
- `buildNotes` signature now accepts an optional `*loadedDataset` so the dataset-related warning/announcement can be emitted alongside the existing notes.

### Not in this release
- The simulator does NOT ship real Arabidopsis 1001 Genomes data — that data is large and the user fetches it locally. The binary ships with the placeholder fixture so all code paths and the UI dropdown are exercisable on first install. To use real data: run `tools/data/fetch_arabidopsis_1001.py`, then rebuild.
- No streaming visualisation — that lands separately in v0.7.5.
- No new selection strategies; constraint engine (v0.7.3), honesty layer (v0.7.2), and self-update mechanism (v0.7.1) unchanged.

## [0.7.3] - 2026-05-21

### Added — Constraint engine and feasible-strategy selection (closes `issues-breedos/03`)

The simulator now evaluates each strategy against user-supplied program constraints and surfaces a separate "best feasible" recommendation in the decision report.

`SimRequest` adds six optional constraint fields (zero = no constraint):

- `max_inbreeding` — hard cap on mean final inbreeding coefficient.
- `max_diversity_loss` — hard cap on diversity loss as a fraction of baseline (e.g., 0.30 = lose at most 30%).
- `max_rare_useful_loss` — hard cap on count of rare-useful loci lost.
- `min_genetic_gain` — floor on final mean genetic gain.
- `min_effective_parents` — floor on effective parent count.
- `max_combined_risk` — hard cap on the combined risk score (weighted inbreeding-breach / diversity-collapse / rare-allele-loss probabilities).

`FinalStats` per strategy adds:

- `feasible` (bool) — true if all active constraints pass for that strategy's mean outcome.
- `failed_constraints` (string slice) — human-readable list of which constraints were violated (e.g., `"inbreeding 0.3812 > max 0.2500"`).

`DecisionSummary` adds:

- `best_feasible_code` / `best_feasible_name` — top risk-adjusted strategy among feasible ones (empty when none feasible or no constraints applied).
- `feasibility_note` — explanation that either names the best feasible strategy or identifies the most-binding constraint when nothing passes.
- `constraints_applied` — human-readable list of the constraints that were evaluated for this run (e.g., `"max inbreeding ≤ 0.2500"`).

When no constraints are supplied, behaviour is identical to v0.7.2 — every strategy is treated as feasible and the decision report explicitly notes "No hard constraints supplied".

### Added — Constraint inputs in demo

`demo.html` exposes a new collapsible "Program constraints" form section with the six fields. Each input defaults to 0 (no constraint). Numeric inputs validate as non-negative.

### Added — Feasibility in decision report and strategy table

`renderDecisionPanel` (`app.js`) now renders:

- A "Best feasible" card alongside "Recommended" / "Max gain" / "Lowest risk".
- A `feasibility-note` block summarising how many strategies passed and the most-binding constraint when none did.
- A `constraints-applied` chip row showing the list of evaluated constraints.

`renderStrategyTable` (`app.js`) adds a feasibility column showing ✓ / ✗ plus a tooltip listing the failed constraints. Infeasible rows are visually de-emphasised so the user reads feasible options first.

### Added — Tests for the constraint engine

`mvp/main_test.go` adds three test cases:

- `TestConstraintEngineFeasibleStrategyExists` — sets a permissive `min_genetic_gain` floor and verifies at least one strategy is feasible and `BestFeasibleCode` is populated.
- `TestConstraintEngineNoFeasibleStrategy` — sets an impossibly tight `max_inbreeding` and verifies `BestFeasibleCode` is empty and `FeasibilityNote` references the binding constraint.
- `TestConstraintEngineAggressiveRejectedByRiskCap` — sets a tight `max_combined_risk` that aggressive selection cannot meet; verifies aggressive ends in `failed_constraints` while a more balanced strategy passes.

### Changed
- `buildSummaryText` now mentions feasibility status when constraints are active.
- `Interpretation` list in DecisionSummary explicitly states whether constraints were supplied and which strategy is best feasible.
- Run-notes mention v0.7.3 in the budget-guard line and main feature description.
- Version strings bumped `v0.7.2` → `v0.7.3` in `main.go` run notes, `index.html` footer, and `demo.html` kicker.

### Not in this release
- No new selection strategies (out of scope per the issue).
- No full mathematical optimisation (per the issue's non-goals).
- Honesty layer (v0.7.2) and self-update mechanism (v0.7.1) unchanged.

## [0.7.2] - 2026-05-21

### Added — Scientific Honesty / Trust Layer (closes `issues-breedos/06`)

The Decision Report now includes three honesty-oriented fields and the demo carries a visible banner so domain users and reviewers can see the scope and limits of the simulator at a glance.

`DecisionSummary` response object adds:

- `honesty_banner` — one-line banner: "Decision-layer simulator on synthetic data — minimal CRISPR demo (when enabled) — not a deployable recommendation without your own genotype/phenotype data and domain review."
- `limitations` — explicit modelling limits: diploid biallelic markers, simplified inheritance, additive trait architecture (no dominance / epistasis / pleiotropy), no GxE, mock genomic-prediction signal, no real germplasm or pedigree ingested, user-set risk thresholds. CRISPR-mode adds explicit "not guide-RNA design / not off-target scoring / not regulatory feasibility" lines.
- `what_could_be_wrong` — context-aware list of scenarios that would invalidate the recommendation: risk-adjusted-vs-max-gain disagreement under a different inbreeding tolerance, low replicate count, small population, mis-estimated heritability, non-additive trait architecture, population substructure, infeasible selection intensity, CRISPR off-target / regulatory hurdles.

### Added — Demo honesty banner

`demo.html` carries a visible `.honesty-banner` above the title card that states the simulator's scope ("Decision-layer simulator on synthetic data. Not a wet-lab protocol, not a guide-RNA designer, not a deployable recommendation without your own genotype/phenotype data and domain review.") and points readers to the new Decision Report sections.

### Added — Decision Report sections

`renderDecisionPanel` in `app.js` now renders two new `<details>` blocks in the Decision Report:

- **What could make this recommendation wrong?** — open by default; orange accent (`decision-what-could-be-wrong` class).
- **Model limitations** — collapsed by default; blue accent (`decision-limitations` class).

A small inline honesty banner is also rendered at the top of the Decision Report panel itself (`decision-honesty-banner` class), so users who jump straight to the report still see the scope statement.

### Changed
- `buildNotes` in `main.go` now reflects v0.7.2 in the run-notes string and budget-guard note.
- Version strings bumped `v0.7.1` → `v0.7.2` in `main.go` run notes, landing footer (`index.html`), and demo kicker (`demo.html`).

### Operational changes (from previous Unreleased)
- `deploy_breedos.sh` now reads its defaults from a local `.env` file next to the script (gitignored). A new tracked `.env.example` documents the format. Override precedence: positional `$1` > `BREEDOS_DEPLOY_TARGET` env var (current shell or `.env`). When no target is configured and no `.env` exists, the script prints actionable instructions for creating one and exits non-zero. Help text is shown on `-h` / `--help` / `help`.

### Not in this release
- No algorithm changes (selection, simulation, Pareto, risk, self-update contract all unchanged from v0.7.1).
- No API breaking changes — the three new `DecisionSummary` fields are additive.
- Domain-expert review (issues-breedos/11) and constraint engine (issues-breedos/03) remain open.

## [0.7.1] - 2026-05-21

### Added — Self-update module (`mvp/selfupdate.go`)

The running binary now watches for a sibling file named `<binary>.UPDATE`. When the file appears, the running process:

1. Ensures the candidate is executable (chmods if needed).
2. Runs the candidate with `--self-check` and verifies it prints the literal token `OK` on stdout. If not, the candidate is left in place for inspection and the watcher continues polling — the running service does NOT go down.
3. Renames the running binary to `<binary>.bak.<YYYYMMDDHHmmss>`.
4. Renames the candidate into the running binary's name.
5. Exits with non-zero so that systemd (`Restart=on-failure` in the unit file generated by `install.sh`) restarts the now-new binary.

All steps fail loudly to journal/stderr and either skip the swap or attempt a rollback. The candidate `.UPDATE` file is left in place when self-check or swap fails so that a human can inspect.

### Added — `--self-check` flag

`main.go` accepts `--self-check` (or `-self-check`); the binary prints `OK` and exits 0. This is the contract the self-update watcher uses to validate any candidate binary.

### Added — `deploy_breedos.sh`

Build a portable static binary via `mvp/build.sh`, verify it locally with `--self-check`, then `scp` to the remote host as `<binary>.UPDATE`. Target is parameter or `BREEDOS_DEPLOY_TARGET` env var. The running service on the remote host detects the `.UPDATE` file within ~60 s and performs the swap.

### Added — tests

- `TestPerformSwapRenamesBinaryAndCreatesBackup` — exercises the rename/swap logic in a temp directory; verifies the running path holds the update content, the `.UPDATE` file is gone, and a backup file matching `breedos.bak.*` contains the previous content.
- `TestEnsureExecutableSetsExecBit` — verifies the helper sets the owner execute bit on a non-executable file.

### Changed
- `main.go` adds the `os` package import, parses the `--self-check` flag (before any work), and starts the self-update watcher before `ListenAndServe`.
- Watcher polling interval defaults to 60 seconds; self-check command timeout is 10 seconds. Both are constants in `mvp/selfupdate.go`.
- Version strings bumped `v0.7.0` → `v0.7.1` in main.go run notes, landing footer, and demo kicker.

### Operational note
The very first deployment to a host running v0.7.0 or earlier must be done manually (the older binary does not know about the `.UPDATE` contract). Subsequent deploys to a v0.7.1+ host can use `deploy_breedos.sh`.

### Out of scope
- Signed/verified updates — the contract here is `--self-check` returns `OK`; that is *not* cryptographic verification. For a hostile environment, future work should add signed binaries and signature verification.
- Rolling deploys across multiple hosts — `deploy_breedos.sh` ships to one target per invocation.

## [0.7.0] - 2026-05-21

### Added — Decision Report (closes `issues-breedos/02`)

`DecisionSummary` response object now includes six new structured fields:

- `tradeoffs` — up to three pairwise observations comparing top strategies on gain vs. risk, risk-adjusted vs. min-risk, and Pareto/aggressive-vs-diversity. Each entry has `a`, `b`, `theme`, and `note`.
- `avoid_strategies` — strategies with combined risk ≥ 0.5 that are *not* Pareto-optimal. Each entry lists which risk-probability thresholds were breached.
- `key_assumptions` — explicit list of modelling assumptions this run depends on (synthetic population, mock genomic-selection signal, additive trait architecture, heritability, selection percent, risk thresholds, CRISPR seed parameters when enabled).
- `missing_data_warnings` — dynamic flags for replicates < 5, population < 50 or < 10, extreme heritability, mutation_rate = 0, short horizon, low baseline diversity.
- `next_analysis` — heuristic suggestion for the next experiment (vary seed, tighten constraints, raise replicates, sweep selection intensity, etc.) depending on the shape of this run's outcome.
- `summary_text` — single paragraph copy-pasteable export of the recommendation, max-gain / lowest-risk strategies, top trade-off, avoid list, first caveat, and next analysis.

Frontend:

- `renderDecisionPanel` now renders the new fields as collapsible `<details>` sections (Top trade-offs and Strategies to avoid open by default; Missing-data warnings shown with warning styling; Key assumptions collapsed by default).
- "Recommended next analysis" callout block prominently displayed.
- "Copy summary" button now prefers `decision.summary_text` from the server, falling back to the client-side `buildSummaryText` for older responses.
- `style.css` adds `.decision-section`, `.decision-next`, `.decision-warnings` styles.

Tests:

- `TestDecisionReportPopulatesNewFields` — verifies all six new fields are populated for a basic run.
- `TestDecisionReportFlagsHighRiskAggressiveOrIncludesItInTradeoff` — verifies aggressive strategy appears in AvoidStrategies or Tradeoffs in a small-N high-pressure scenario.
- `TestDecisionReportSummaryReferencesRecommendedStrategy` — verifies `summary_text` references the recommended strategy by name and `next_analysis` matches one of the expected heuristic keywords.

### Changed
- `buildDecisionSummary` signature changed to take `(req SimRequest, results []StrategyResult, baseDiversity float64)` (was `(results []StrategyResult)`). Single internal call site updated.
- Version strings bumped `v0.6.7` → `v0.7.0` in main.go run notes, landing footer, and demo kicker.

### Non-goals (deferred)
- Best-feasible-strategy field — deferred to v0.7.1 alongside the constraint engine in `issues-breedos/03`.
- Tighter assumption/missing-data integration with a "scientific honesty" trust layer — deferred to v0.7.2 with `issues-breedos/06`.
- PDF export and LLM-generated explanation — out of scope per the issue.

## [0.6.7] - 2026-05-21

### Fixed
- Removed two residual references to the deleted `customer-discovery` page that v0.6.6 missed:
  - `mvp/static/index.html` line 138: the secondary CTA button "Open customer discovery page" linking to `/customer-discovery` (would have 404'd on the live deployment). Replaced with a "View on GitHub" link.
  - `README.md` project-structure tree: removed the `customer_discovery.html` entry from the static/ listing.

### Changed
- Updated MVP version strings (landing footer, demo kicker, run notes) from `v0.6.6` to `v0.6.7`.

## [0.6.6] - 2026-05-18

### Removed
- Customer-discovery page from public deployment (`mvp/static/customer_discovery.html`, the `/customer-discovery` route handler in `mvp/main.go`, and the corresponding `<a>` navigation links in `index.html` / `demo.html`). The page contained an internal customer-discovery framework (target segments and interview questions) intended for the founder's own workflow, not for public/investor visibility. Removing it focuses the landing on product narrative and the interactive demo only.

### Changed
- Updated MVP version strings (landing footer, demo kicker, run notes) from `v0.6.5` to `v0.6.6`.
- README "Demo pages" list no longer lists `/customer-discovery`.

## [0.6.5] - 2026-05-18

### Added
- `mvp/build.sh` — portable static-binary build script. Uses `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w'` to produce a fully static binary with no glibc dependency, runnable on any reasonably modern Linux regardless of the build host's glibc version. Default output is `../breedos` (next to `install.sh`).
- `install.sh` pre-flight check: runs the binary briefly and parses common dynamic-loader errors (`GLIBC`, `symbol not found`, `cannot load shared object`, `exec format error`). Fails fast with a clear remediation pointing to `./mvp/build.sh` or `CGO_ENABLED=0 go build`.
- `install.sh` post-start verification: after `systemctl start`, polls `systemctl is-active` for up to 5 seconds; if the service does not reach `active`, dumps recent journal output and aborts with a clear error instead of silently reporting success.

### Changed
- `install.sh` refactored from positional arguments to a flag-based CLI: `--binary`, `--service`, `--args`, `--user`, `--workdir`, `--description`, `--non-interactive`, `--force` (with short aliases `-b -s -a -u -w -d -y -f`). Each install/uninstall/info subcommand parses its own flags.
- `install.sh` no longer hard-codes binary-specific runtime defaults. The previous version assumed `-listen 0.0.0.0:8080` for breedos; now an empty `--args` means empty args and the binary uses its own defaults. The systemd unit `Description=` is generic (`<service> service`) by default, overridable via `--description`. The `Documentation=` field is no longer hard-coded to the breedos repository URL.
- Updated README "Run as a systemd service" section: now references `mvp/build.sh`, shows both interactive and non-interactive install invocations, and documents the empty-args / binary-defaults convention.
- Updated MVP version strings (landing footer, demo kicker, run notes) from `v0.6.4` to `v0.6.5`.

## [0.6.4] - 2026-05-17

### Added
- `install.sh` — systemd-service installer for the BreedOS MVP binary. Generic enough to manage any Go binary placed next to it, but tuned for breedos defaults (binds `0.0.0.0:8080`, working directory = the script's directory). Supports subcommands `install` / `uninstall` / `info` / `help`. Interactive prompts for arguments, run user, and working directory. Prints status/log/control commands after install and starts the service.
- README section "Run as a systemd service" with end-to-end deployment recipe.

### Changed
- Updated MVP version strings (landing footer, demo kicker, run notes) from `v0.6.3` to `v0.6.4`.

## [0.6.3] - 2026-05-17

### Changed
- Landing page (`mvp/static/index.html`) refined for public release at the canonical domain:
  - Title and meta description rewritten to match the canonical one-liner ("decision engine for selection strategies in CRISPR-enabled crop breeding").
  - Dropped the "AI" prefix from the hero kicker for consistency with README, CHANGELOG, and the public pack.
  - Replaced the secondary hero CTA "Read the pitch" with "View on GitHub" pointing directly to the source repository.
  - Genomic Selection Planner module flagged as roadmap; description softened to acknowledge that production integration is scheduled for the program build (MVP currently demonstrates the kernel only).
  - CRISPR section heading softened: "CRISPR gives breeders a limited but powerful write function" (was "biology a write function") and the body adds an explicit non-replacement framing against Benchling, Synthego, and CRISPResso.
  - Footer adds a direct "View source on GitHub" link.
- Updated MVP version strings (landing footer, demo kicker, run notes) from `v0.6.2` to `v0.6.3`.

## [0.6.2] - 2026-05-17

### Fixed
- Corrected the diversity-collapse probability calculation in `aggregateReplicates`. The previous version compared the inbreeding coefficient against the diversity-loss limit, which effectively duplicated the inbreeding-breach metric. The fix now computes the relative diversity loss `(baseDiversity − finalDiversity) / baseDiversity` and compares it against the limit, so the probability reflects actual diversity collapse rather than re-measuring inbreeding.

### Changed
- Updated MVP version strings (landing footer, demo kicker, run notes) from `v0.6.1` to `v0.6.2`.

## [0.6.1] - 2026-05-16

### Changed
- Core thesis statement refined to acknowledge that prediction is part of breeding's complexity rather than separate from it. New wording: "Breeding is not only a prediction problem. It is, more fundamentally, a multi-generation control problem over an evolving population." The previous absolutist framing risked being read as dismissive of genomic prediction work (rrBLUP, BGLR, deep-learning predictors) by domain reviewers.
- Updated MVP version strings in landing-page footer, demo-page kicker, and run-notes from `v0.6` to `v0.6.1`.

## [0.6.0] - 2026-05-15

### Added
- Monte Carlo replicates per strategy.
- Worker-pool execution for `strategies × replicates` jobs.
- Core and advanced strategy sets.
- Neutral drift and random parent baselines.
- Phenotype truncation selection.
- Genomic selection mock (placeholder for GBLUP/Bayesian/ML predictors).
- OCS-like constrained selection (gain under a similarity/diversity penalty).
- Cross planner strategy with more distant mate allocation.
- Edit-aware introgression planner for minimal CRISPR decision-layer integration.
- Risk probabilities: inbreeding breach, diversity collapse, rare useful allele loss.
- Risk-adjusted score, decision rank, and decision summary panel.
- Pareto gain/risk chart.
- Tests for advanced strategy set, risk probabilities, decision ranks, and Pareto summary.

### Changed
- Demo reframed as an early decision engine rather than a single-strategy simulator.

## [0.5.0]

### Added
- Parallel strategy execution: independent strategy simulations run concurrently and results are returned in canonical order.
- Aggregate parallel strategy-generation progress reporting.
- Tests for parallel execution notes and strategy order.

### Changed
- After a run, the browser form is updated with the normalized request returned by the server, making no-change detection reliable.

### Removed
- Automatic page-load calculation: the demo now waits for an explicit Run simulation click.
- No-change run guard: if the current form matches the last completed run, pressing Run simulation does not start a backend job.

## [0.4.0]

### Added
- Asynchronous API endpoints: `POST /api/simulate/start` and `GET /api/simulate/status?id=<job_id>`.
- Real backend job progress: the Run simulation button shows percentage while the server computes.
- Request dirty-state behavior: changing any input shows "Press Run simulation to update graphs."
- Regression coverage confirming a `mutation_rate` change triggers a fresh run on manual submit.
- Progress callback tests.

### Removed
- Auto-run checkbox and automatic recalculation after field changes.

### Notes
- `POST /api/simulate` is retained as a synchronous endpoint for tests and direct API use.

## [0.3.0]

### Fixed
- `mutation_rate=0` is no longer treated as missing and is no longer rewritten to the default.
- `crispr_edits=0` now disables CRISPR candidate ranking instead of silently returning three candidates.

### Added
- Expected mutation-flip notes and summary cards so small mutation-rate changes are easier to interpret.
- Changed-parameter notes: when only mutation rate changes, the run notes explicitly show the previous and current values.
- Frontend support for comma decimal input normalization.
- Regression tests for zero mutation rate and disabled CRISPR edits.

## [0.2.0]

### Added
- Previous-run dotted chart overlays.
- Field-level tooltips on the demo form.
- Fixed-loci metrics and chart.
- Presets, random-seed button, copy-summary, JSON export, and run notes.
- `.gitignore` rules to exclude local build artifacts.

### Changed
- Population-size minimum lowered to 2 to expose small-population drift and fixation.
- Population-size maximum raised to 5000, protected by a global simulation budget guard.

## [0.1.0]

### Added
- First runnable BreedOS MVP: landing page, simulation demo, and CRISPR-aware seed strategy.
