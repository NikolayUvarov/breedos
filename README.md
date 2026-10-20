# BreedOS

**A decision engine for artificial selection and CRISPR-enabled breeding.**

BreedOS helps breeding teams compare selection strategies across multiple generations — predicting genetic gain, tracking drift and inbreeding, and quantifying the long-term cost of short-term decisions.

## Core idea

Breeding is not only a prediction problem. It is, more fundamentally, a multi-generation **control problem** over an evolving population.

Most breeding software optimizes the next generation's prediction. BreedOS optimizes the trajectory. Aggressive selection can mathematically destroy the future search space — diversity collapses, rare beneficial alleles fixate or vanish, inbreeding rises. By generation 15, the population can stop responding to selection.

BreedOS makes these dynamics visible and comparable across strategies.

## What it does

BreedOS runs Monte Carlo simulations of a breeding population under competing selection strategies and compares where each one leaves the population after 10–40+ generations: genetic gain, inbreeding, effective population size, diversity, fixation, and rare favourable alleles lost.

### Genetic model

- **Linked genome.** Markers sit on chromosomes with a genetic map in Morgans. Each meiosis draws Poisson crossovers per chromosome (Haldane map, no interference), and individuals carry phased haplotypes, so linkage disequilibrium, hitchhiking and linkage drag emerge from the model. The synthetic map defaults to 10 chromosomes × 1 M. Datasets that carry marker positions use their real chromosomes.
- **Inbreeding by descent.** Every founder haplotype is labelled at up to 512 tracker loci. F is the probability that two allele copies in the population are identical by descent (IBD) relative to the founders, so selecting on observed marker state cannot lower it. Diversity (expected heterozygosity) is reported separately.
- **Fixed environment.** Environmental variance is set once from h² and the founder genetic variance. As selection erodes VG, realised heritability, and with it the accuracy of phenotypic selection, falls.
- **Interpretable units.** Gain is reported in trait units and in founder-σA units. ΔF per generation and Ne = 1/(2ΔF) are reported for every strategy.
- **Calendar time (optional).** With `cycle_years` set, every strategy runs as many of its own cycles as fit into the same horizon. Genomic strategies can cycle faster (`genomic_cycle_years`). Results add gain per year and ΔF per year, and the charts switch to a year axis.
- Additive QTL effects; optional multi-trait selection indices with genetic correlations.

### Strategies

- **Neutral drift** and **random parents**: baselines. They are shown for comparison and never recommended.
- **Aggressive / phenotype truncation**: maximum short-term gain.
- **Optimum contribution selection (ΔF target)**: unequal parent contributions that maximise expected gain while holding the rate of inbreeding at `target_delta_f` per generation (default 1% ≈ Ne 50). It follows Meuwissen (1997), with coancestry from identity by descent. Contributions become exact gamete counts.
- **Diversity-preserving**, **balanced**, **OCS-like** (gain under a similarity penalty), **cross planner** (distant mating pairs).
- **Genomic selection mock**: prediction with a configurable accuracy (`genomic_accuracy`, default √h²) that decays as VG erodes. A placeholder for GBLUP / Bayesian / ML predictors.
- **Imported GEBV**: generation-0 parents ranked by breeding values from your own rrBLUP / BGLR / sommer / AlphaSimR pipeline.
- **CRISPR-enabled**: balanced + CRISPR seed and edit-aware introgression. Candidate edits are classified edit / cross / wait, with an EU NGT-1 / NGT-2 planning classification under Annex I (not legal advice).
- **Ancestral introgression** for climate-stress scenarios.

### Decision layer

- Replicates are aggregated with a worker pool and scored on a risk-adjusted rank.
- The Pareto frontier is shown on gain × combined risk, with N-D dominance for multi-trait runs.
- Hard constraints (max F, max diversity loss, min gain, min parents, max risk) select the best feasible strategy.
- A sensitivity sweep asks: does the recommendation hold across h², selection %, weights, or climate scenarios?
- Every report lists its assumptions, model limitations, and what could make the recommendation wrong. A floored parent pool is flagged as a degenerate run.

Core mode runs a four-strategy subset (plus the CRISPR strategy when enabled); advanced mode runs the full set.

### What it does not model yet

- **Cost.** Cycle length in years is an input (`cycle_years`, `genomic_cycle_years`), but the cost per cycle is not modelled.
- **Breeding-program structure.** There is no selfing, doubled haploids, hybrids or trial stages, no sexes and no overlapping generations.
- **Genotype × environment interaction.** Climate stress is a uniform penalty on the trait mean.
- **Prediction and phenotypes.** There is no production GBLUP model, and uploaded phenotypes and pedigrees are parsed but not yet used by the engine.

## CRISPR positioning

BreedOS is **not** a guide-RNA design tool. That space is covered by Benchling, Synthego, and others.

BreedOS is a **decision layer** for CRISPR-enabled breeding: which candidate edits are worth introducing, in which genetic background, and how to propagate them through a breeding population without bottlenecks. Edit-aware introgression is one of the implemented selection strategies.

## Status

Early prototype. Working simulation engine with a linked genome and IBD inbreeding, interactive demo UI, programmatic JSON API. Active development. Feedback from breeders, computational biologists, and CRISPR researchers is genuinely welcome — open an issue.

## Quickstart

Requires Go 1.23 or later.

```
git clone https://github.com/NikolayUvarov/breedos.git
cd breedos/mvp
go run .
```

Then open `http://127.0.0.1:8080/`.

### Demo pages

- `/` — landing page (`/ru`, `/es`, `/uz` — translations)
- `/demo` — interactive simulation UI
- `/datasets` — public genotype datasets registry

## Run as a systemd service

For server deployment on a systemd-based Linux host, the repository ships:

- **`mvp/build.sh`** — builds a portable static binary (`CGO_ENABLED=0`, no glibc version pinning, works on any reasonably modern Linux).
- **`install.sh`** — generic flag-based systemd-service installer with `install` / `uninstall` / `info` / `help` subcommands.

```
git clone https://github.com/NikolayUvarov/breedos.git
cd breedos
./mvp/build.sh                    # writes ./breedos next to install.sh
sudo ./install.sh install         # interactive (prompts for args, user, workdir)
```

Or non-interactive with flags:

```
sudo ./install.sh install \
    --user backup \
    --workdir /var/lib/breedos \
    --args "-listen 0.0.0.0:8080" \
    --non-interactive
```

The installer makes no binary-specific assumptions. If you leave args empty, breedos uses its own defaults (binds `0.0.0.0:8080`). After install it verifies the service reached `active` state and prints the management commands; if the service crashed on start it dumps the recent journal and aborts with a clear error.

Manage and inspect:

```
sudo systemctl status   breedos
sudo systemctl restart  breedos
sudo journalctl -u breedos -f
```

To inspect the unit file and recent logs without root: `./install.sh info`. To remove: `sudo ./install.sh uninstall`. Full options: `./install.sh help`.

## API

- `GET  /health` — health check
- `GET  /api/version` — build version
- `POST /api/simulate` — synchronous full run
- `POST /api/simulate/start` — async start (returns job ID)
- `GET  /api/simulate/status?id=<id>` — async poll
- `POST /api/sensitivity/start`, `GET /api/sensitivity/status?id=<id>` — sensitivity sweep
- `POST /api/upload` — ephemeral genotype / phenotype / pedigree / edits / predictions CSV upload
- `GET  /api/datasets` — dataset registry

Minimal request (omitted fields take defaults):

```json
{
  "seed": 7,
  "population_size": 100,
  "markers": 1000,
  "qtl_count": 50,
  "generations": 40,
  "selection_percent": 10,
  "heritability": 0.25,
  "strategy_set": "core",
  "replicates": 20
}
```

`selection_percent` is a percent on a 0–100 scale (`20` = 20%). Values below 1 are rejected. Optional fields:
- genome: `chromosomes` (default 10), `chromosome_length_morgans` (default 1.0), `genomic_accuracy` (default √h²);
- time axis: `cycle_years` (0 = off), `genomic_cycle_years` (default `cycle_years`).
- OCS: `target_delta_f` (default 0.01). Request and response shapes live in the handler types at the top of `mvp/main.go`.

Founder-genotype CSVs are `id,m1,...,mN` with 0/1/2 dosages. Marker positions are read from a `# Sites (chrom:pos), ...` comment line followed by `# chrom:pos, chrom:pos, ...`, or from `chrom:pos` header labels. Markers must be ordered by chromosome and position.

## Project structure

```
breedos/
├── README.md
├── CHANGELOG.md
├── LICENSE
├── install.sh            systemd service installer (Linux)
├── deploy_breedos.sh     build + ship to a host running the self-update watcher
└── mvp/
    ├── build.sh          portable static build (CGO_ENABLED=0)
    ├── go.mod            no external dependencies
    ├── main.go           HTTP server, strategies, selection, decision report
    ├── genome.go         genetic map, meiosis with crossovers, IBD tracking
    ├── multitrait.go     multi-trait selection indices
    ├── sensitivity.go    sensitivity sweeps
    ├── climate*.go, ancestral.go     climate-stress scenarios
    ├── edit_classifier.go, ngt_classify.go   edit / cross / wait, EU NGT classification
    ├── dataset.go, upload.go, datasets_api.go  founder data loading
    ├── promptbio/        separate experimental substrate; not used by the breeding engine
    └── static/           frontend (vanilla JS, no framework)
```

## Tests

```
cd mvp
go test ./... -v
```

## Background

Built by Nikolay Uvarov. The thesis behind BreedOS comes from earlier academic work on the mathematical modeling of gene drift under artificial selection.

## Contributing

Early prototype under active development. If you'd like to contribute:

1. Open an issue describing what you want to change or add.
2. For non-trivial work, wait for a response before opening a PR.
3. Run `go test ./...` and `go vet ./...` before submitting.

## License

MIT — see [LICENSE](LICENSE).
