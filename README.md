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
- **Line breeding** (`mating_system: ssd | dh`, `ssd_generations`) for self-pollinating crops.
  - Selected lines are crossed in pairs; each F1 becomes an inbred line by single-seed descent or one doubled-haploid step.
  - Synthetic founders are converted to homozygous lines; real inbred accessions are kept.
  - Inbreeding is coancestry among lines.
- **Climate as G×E.** With `climate: {mode, severity, frequency, rg}`, normal- and stress-season performance are two genetically correlated traits:
  - a fraction rg of the QTL is shared;
  - the rest act only in normal seasons (yield potential) or only under stress;
  - gain is reported on the target mix (1−f)·normal + f·(1−loss)·stress, so climate can change which strategy wins.

### Strategies

- **Neutral drift** and **random parents**: baselines. They are shown for comparison and never recommended.
- **Aggressive / phenotype truncation**: maximum short-term gain.
- **Optimum contribution selection (ΔF target)**: unequal parent contributions that maximise expected gain while holding the rate of inbreeding at `target_delta_f` per generation (default 1% ≈ Ne 50). It follows Meuwissen (1997), with coancestry from identity by descent. Contributions become exact gamete counts.
- **Diversity-preserving**, **balanced**, **OCS-like** (gain under a similarity penalty), **cross planner** (distant mating pairs).
- **Genomic selection mock**: prediction with a configurable accuracy (`genomic_accuracy`, default √h²) that decays as VG erodes. A placeholder for GBLUP / Bayesian / ML predictors.
- **Imported GEBV**: generation-0 parents ranked by breeding values from your own rrBLUP / BGLR / sommer / AlphaSimR pipeline.
- **CRISPR-enabled**: balanced + CRISPR seed and edit-aware introgression. Candidate edits are classified edit / cross / wait, with an EU NGT-1 / NGT-2 planning classification under Annex I (not legal advice).
- **Ancestral introgression** for climate-stress scenarios. The landrace lines are enriched for stress-adapted alleles and depleted at yield-potential loci.
- **Target-environment selection** (with a climate scenario): selection on the target mix of normal and stress seasons, instead of normal-season nurseries.

### Decision layer

- Replicates are aggregated with a worker pool and scored on a risk-adjusted rank.
- The Pareto frontier is shown on gain × combined risk, with N-D dominance for multi-trait runs.
- Hard constraints (max F, max diversity loss, min gain, min parents, max risk) select the best feasible strategy.
- A sensitivity sweep asks: does the recommendation hold across h², selection %, weights, or climate scenarios?
- Every report lists its assumptions, model limitations, and what could make the recommendation wrong. A floored parent pool is flagged as a degenerate run.

Core mode runs a four-strategy subset (plus the CRISPR strategy when enabled); advanced mode runs the full set.

### What it does not model yet

- **Cost.** Cycle length in years is an input (`cycle_years`, `genomic_cycle_years`), but the cost per cycle is not modelled.
- **Breeding-program structure.** Outcrossing and line breeding (SSD / DH) are modelled. Hybrids and heterosis, multi-stage trials, sexes and overlapping generations are not.
- **Trial structure.** Climate stress is modelled as G×E between two genetically correlated environments (normal and stress season), not as location × year trials. Multi-trait runs still use a uniform climate penalty.
- **Prediction.** The genomic-selection strategy uses a mock predictor with a configurable accuracy. Uploaded phenotypes do feed the engine: SNP-BLUP estimates the marker effects, see below. Uploaded pedigrees and edit tables are parsed but not used.

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
- OCS: `target_delta_f` (default 0.01);
- ploidy: `ploidy` (2 or 4; 0 = from the data, diploid for synthetic runs);
- gene pyramiding: `pyramid_genes` (1–8 synthetic target genes) or `target_loci` (column names of an uploaded table), `marker_distance_cm` (0 = the marker is the gene), `backcross_generations` (0 = no MABC strategy) and `recurrent_parent` (an id of the table; default: the best founder without target alleles).

Request and response shapes live in the handler types at the top of `mvp/main.go`.

Founder-genotype CSVs are `id,m1,...,mN` with 0/1/2 dosages (comma, semicolon or tab separated).

**Multi-allelic markers (SSR / microsatellites).** Genotype tables of allele sizes are read directly:
- one column per locus with `152/158` (or `152` for a homozygous call);
- two columns per locus (`RM1_1,RM1_2`);
- a GenAlEx export.

Two-column headers may also be `LOCUS_allele 1` / `LOCUS_allele 2`, and diploid calls packed as 3 + 3 digits (`304304`) are unpacked. Description columns among the loci (species, site, variety…) are skipped.

`0`, `000000`, `999`, `-9`, `NA`, `-` and `?` mean missing; missing calls are drawn from the locus allele frequencies and counted in the summary. Fragment sizes are rounded to whole base pairs.

Each allele becomes an indicator marker, and the indicators of a locus sit at one map position, so a locus is always inherited as a unit. Reports add Nei's expected heterozygosity per locus and `alleles_retained`, the number of SSR alleles each strategy keeps. The upload summary lists every locus with its alleles, He, observed heterozygosity, PIC and missing calls.

**Founder set.** `POST /api/founder-set {"upload": "<id>", "size": 20}` returns the accessions of an uploaded table that keep the most alleles (greedy maximum coverage), the coverage curve, the smallest greedy set holding every allele, He of the set, and what random sets of that size keep on average.

SSR alleles are not CRISPR edit targets.

**Autotetraploids (potato-like).** Set `ploidy: 4`, or let the upload detect it: SSR cells with three or four alleles, a `# ploidy: 4` comment line, or dosages up to 4.
- Each individual carries four homologues. In meiosis they pair at random into two bivalents, cross over within each bivalent, and every gamete takes one chromatid from each, so a duplex parent gives nulliplex : simplex : duplex gametes in 1 : 4 : 1. Double reduction is not modelled.
- Dosage tables use 0–4. A tetraploid SSR profile (`152/158/164`) does not show allele dosage; the missing copies are drawn among the alleles observed in that cell and counted as imputed.
- Inbreeding, coancestry and Ne account for 4N gene copies: Ne = 1/(4ΔF).
- Line breeding (SSD/DH) and multi-trait indices are diploid-only for now; pyramiding works in both.

**Gene pyramiding (marker-assisted selection).** Name the target genes — `target_loci: ["Pi1","Pi2","Pi33"]` for columns of an uploaded table (in an SSR table, biallelic `gene:Pi1` columns with 0/1/2 or `+`/`-`) — or ask for `pyramid_genes: K` synthetic genes, each carried homozygously by its own 2 % of founders and without effect on the selected trait.
- Two strategies are added:
  - **MAS gene pyramiding** ranks candidates by favourable target alleles first and phenotype second. Each target is weighted by 1/√p of its current frequency (Jannink 2010), so the only carriers of a rare gene are not out-ranked by plants with two common genes.
  - **MAS + OCS** uses the same ranking with optimum contributions at `target_delta_f`.
- Example upload: [`genotype_ssr_pi.csv`](mvp/fixtures/upload-toy/genotype_ssr_pi.csv), a synthetic SSR table with `gene:Pi-1`, `gene:Pi-2` and `gene:Pi-33` columns.
- Per generation the report gives the share of plants homozygous for every target (complete pyramid), carriers of every target, and each target's frequency. Per strategy it gives the generations to 50 % and 90 % complete pyramids, the number of targets lost, and `probability_target_loss`, the share of replicates that lost at least one target.
- Naming targets adds the constraint "complete pyramid ≥ 90 % of plants", so the best feasible strategy is one that assembles it. The complete-pyramid share is also a Pareto objective.
- A planning line gives the textbook population sizes for unlinked genes: (1/4)^K of an F2 and (1/2)^K of inbred lines carry all K genes homozygously; ln 0.05 / ln(1−p) plants find one with 95 % probability.
- **Marker-assisted backcrossing (MABC)** is a third strategy when `backcross_generations` > 0: the F1 with the recurrent parent, then that many backcrosses with foreground selection on the markers and background selection on the share of the recurrent parent's genome (identity by descent at the tracker loci), then intercrossing / line derivation to fix the genes. `recurrent_genome` is reported per generation for every strategy; the textbook expectation without background selection is 1 − (1/2)^(n+1). MABC's F measures convergence on the recurrent parent, which is its goal; the recurrent genome share is a Pareto objective.
- `marker_distance_cm` makes the marker imperfect: each gene becomes a locus that many cM from its marker (Haldane), in coupling in the founders. MAS selects on the markers; the report gives `pyramid_complete` by gene, `pyramid_by_marker`, and `pyramid_marker_only` — plants that pass marker screening without the full gene set.
- The genes have no pathogen behind them: race shifts and durability of resistance are not modelled.

When an upload also carries a phenotype CSV (`id,trait`) for at least 20 of the genotyped lines, the trait architecture comes from your data:
- marker effects are estimated by SNP-BLUP / rrBLUP, with λ = (1−h²)/h²·Σ2pq and at most 1000 phenotyped lines;
- they replace the synthetic QTL, and gain is reported in the phenotype's units;
- the response carries `effects_source` and `effect_estimate`, including 5-fold cross-validated predictive ability and accuracy.

Founders can be phenotyped or unphenotyped lines of the same genotype file. Marker positions are read from a `# Sites (chrom:pos), ...` comment line followed by `# chrom:pos, chrom:pos, ...`, or from `chrom:pos` header labels. Markers must be ordered by chromosome and position.

## Private data room

Tables that may not be published can be shown in person without publishing them. Put CSV files (genotype tables, as for uploads) in `private/data/` next to the binary and users in `private/auth`, one `user:sha256(user:password)` per line (`./breedos -private-hash user:password` prints one; `./set_private_password.sh [password] [user]` writes it to the deployed server, generating a password when none is given). `/private` asks for the login; logged in, it lists the tables, and the demo offers them under Founder population as "Private: <file>" (`dataset: "private:<file>"`), for that browser session only. Without `private/auth` the route answers 404.

## Project structure

```
breedos/
├── README.md
├── CHANGELOG.md
├── LICENSE
├── install.sh            systemd service installer (Linux)
├── deploy_breedos.sh     build + ship to a host running the self-update watcher
├── set_private_password.sh   set a /private login on the deployed server
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
    ├── ssr.go            multi-allelic (SSR) and tetraploid genotype parsing
    ├── pyramid.go        gene pyramiding targets, MAS scoring, linked markers, pyramid report
    ├── mabc.go           marker-assisted backcrossing (recurrent parent, background selection)
    ├── founders.go       founder set (maximum allele coverage) from an uploaded table
    ├── private.go        private data room: login, sessions, private tables
    ├── effects.go, ocs.go, gxe.go   rrBLUP effects, optimum contributions, climate G×E
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
