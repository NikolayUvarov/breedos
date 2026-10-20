const colors = {
  neutral: '#d5dbe8',
  random: '#b9b0ff',
  phenotype: '#f4b2df',
  genomic: '#a6f0ff',
  aggressive: '#ff9a9a',
  diversity: '#8cb7ff',
  balanced: '#8fffd1',
  balanced_crispr: '#ffd38c',
  ocs: '#f2f2a0',
  ocs_like: '#c2ff8c',
  cross_planner: '#ffdf70',
  edit_introgression: '#ffb070'
};

const labels = {
  neutral: 'Neutral drift',
  random: 'Random baseline',
  phenotype: 'Phenotype truncation',
  genomic: 'Genomic mock',
  aggressive: 'Aggressive',
  diversity: 'Diversity-preserving',
  balanced: 'Balanced',
  balanced_crispr: 'Balanced + CRISPR',
  ocs: 'OCS (ΔF target)',
  ocs_like: 'OCS-like',
  cross_planner: 'Cross planner',
  edit_introgression: 'Edit introgression'
};

let currentData = null;
let previousData = null;
// v0.7.6 live histogram: latest AFS snapshot received from /api/simulate/status.
// v0.7.8 — also cache the last-drawn generation key to skip redundant redraws.
// v0.7.13 — keep a client-side queue of snapshots and play them back at a fixed
// UI tempo so the chart looks like an animation even when the backend finishes
// all generations in a few hundred milliseconds. The poll loop is the
// producer (cheap network ops); a separate setTimeout chain is the consumer.
let lastSnapshot = null;
let lastDrawnGeneration = -1;
let snapshotSeq = 0;            // next snapshot index to fetch
let pendingFrames = [];         // FIFO queue of snapshots to render
let playbackTimer = null;       // setTimeout handle for the next frame
const PLAYBACK_FRAME_MS = 90;   // ~11 fps; fast enough to feel live, slow
                                // enough that an 8-generation backend run
                                // takes ~720ms in the UI.

// v0.7.15 budget meter. Mirrors the server-side cap in validateRequest():
//   budget = N × markers × (generations+1) × strategies × replicates ≤ 1.5B
// The meter is the only place the user sees this number before clicking Run;
// without it the only signal is a 400 after submit, which is easy to mistake
// for a stuck client state when reducing one parameter still leaves budget
// over cap. Cap was raised from 800M to 1.5B in v0.7.15 — prod is single-core
// so the upper-bound run is ~30–60s wall-clock, still acceptable for the demo.
const BUDGET_CAP = 1500000000;
const BUDGET_WARN_FRAC = 0.7;
// Inputs that multiply into the budget formula. Highlighted red when over cap
// so the user immediately sees which knobs to turn down.
const BUDGET_INPUT_IDS = ['population_size', 'markers', 'generations', 'replicates'];

function byId(id) { return document.getElementById(id); }

function numberValue(id) {
  const el = byId(id);
  if (!el) return 0;
  const raw = String(el.value || "").trim().replace(",", ".");
  const v = Number(raw);
  return Number.isFinite(v) ? v : 0;
}

// v0.7.31 — Issue 05. Holds the in-memory upload id returned by /api/upload.
// Cleared when the user picks a non-upload dataset.
let uploadState = {id: null, summary: null};

function requestFromForm() {
  const dsVal = (byId('dataset') && byId('dataset').value) || 'synthetic';
  const useUpload = dsVal === '__upload__' && uploadState.id;
  return {
    dataset: useUpload ? '' : dsVal,
    upload: useUpload ? uploadState.id : '',
    seed: Math.trunc(numberValue('seed')),
    population_size: Math.trunc(numberValue('population_size')),
    markers: Math.trunc(numberValue('markers')),
    qtl_count: Math.trunc(numberValue('qtl_count')),
    generations: Math.trunc(numberValue('generations')),
    cycle_years: numberValue('cycle_years'),
    genomic_cycle_years: numberValue('genomic_cycle_years'),
    selection_percent: numberValue('selection_percent'),
    heritability: numberValue('heritability'),
    mutation_rate: numberValue('mutation_rate'),
    crispr_enabled: byId('crispr_enabled').checked,
    crispr_edits: Math.trunc(numberValue('crispr_edits')),
    crispr_intro_percent: numberValue('crispr_intro_percent'),
    strategy_set: (byId('strategy_set') && byId('strategy_set').value) || 'core',
    tracked_strategy: (byId('tracked_strategy') && byId('tracked_strategy').value) || '',
    replicates: Math.trunc(numberValue('replicates')),
    worker_count: Math.trunc(numberValue('worker_count')),
    inbreeding_limit: numberValue('inbreeding_limit'),
    diversity_loss_limit: numberValue('diversity_loss_limit'),
    target_delta_f: numberValue('target_delta_f') / 100,
    max_inbreeding: numberValue('max_inbreeding'),
    max_diversity_loss: numberValue('max_diversity_loss'),
    max_rare_useful_loss: Math.trunc(numberValue('max_rare_useful_loss')),
    min_genetic_gain: numberValue('min_genetic_gain'),
    min_effective_parents: Math.trunc(numberValue('min_effective_parents')),
    max_combined_risk: numberValue('max_combined_risk'),
    // v0.7.18 — Issue 13/14/16. NGT regulatory context. All fields optional;
    // unset values force "unclassifiable" on the backend.
    // v0.7.19 — Issue 32. Added variant_type and endogenous_gene_interrupted
    // to encode the Annex I Path (i) vs Path (ii) split.
    ngt: ngtContextFromForm(),
    // v0.7.22 — Issues 24/25. Multi-trait state. Included only when active;
    // empty multi-trait state ⇒ single-trait code path on the backend.
    ...(multiTraitState.traits && multiTraitState.traits.length
        ? {traits: multiTraitState.traits.map(t => ({...t})), genetic_correlations: multiTraitState.genetic_correlations.map(r => r.slice())}
        : {})
  };
}

// v0.7.19 — Issue 32. Collects NGT context fields and serialises
// endogenous_gene_interrupted as the JSON values true/false/null so the
// Go *bool unmarshal handles "unset" correctly.
function ngtContextFromForm() {
  const out = {
    target_trait_class: (byId('ngt_target_trait_class') && byId('ngt_target_trait_class').value) || '',
    donor_source:       (byId('ngt_donor_source')       && byId('ngt_donor_source').value)       || '',
    variant_type:       (byId('ngt_variant_type')       && byId('ngt_variant_type').value)       || '',
    patent_id:          (byId('ngt_patent_id')          && byId('ngt_patent_id').value)          || '',
    licensing_status:   (byId('ngt_licensing_status')   && byId('ngt_licensing_status').value)   || '',
    notes:              (byId('ngt_notes')              && byId('ngt_notes').value)              || ''
  };
  const egiEl = byId('ngt_endogenous_gene_interrupted');
  if (egiEl) {
    const v = egiEl.value;
    if (v === 'true')  out.endogenous_gene_interrupted = true;
    if (v === 'false') out.endogenous_gene_interrupted = false;
    // empty string ⇒ omit field so backend sees nil pointer.
  }
  return out;
}

// v0.7.19 — show the endogenous-gene-interrupted question only when the
// operator picks variant_type = gene_pool_insertion. The classifier requires
// it for Path (ii); hiding it for Path (i) keeps the form lean.
function updateNGTEndogenousFieldVisibility() {
  const vt = byId('ngt_variant_type');
  const wrap = byId('ngt_endogenous_gene_field');
  if (!vt || !wrap) return;
  wrap.hidden = vt.value !== 'gene_pool_insertion';
}

// v0.7.22 — Issue 24. Build / refresh the selection-index sliders from
// the current multiTraitState. Called by setFormValues (preset load) and
// by the composer's own slider 'input' handlers. Hides itself when
// multiTraitState is empty (single-trait runs).
function refreshSelectionIndexComposer() {
  const wrap = byId('selectionIndexComposer');
  const list = byId('selectionIndexSliders');
  const preview = byId('selectionIndexPreview');
  if (!wrap || !list) return;
  const traits = multiTraitState.traits;
  if (!traits || !traits.length) {
    wrap.hidden = true;
    list.innerHTML = '';
    return;
  }
  list.innerHTML = traits.map((t, i) => `
    <div class="field">
      <label>
        <span>${escapeHtml(t.name)} <span class="tip" tabindex="0" data-tip="Weight in the selection index. Higher = pull harder. Negative = select against this trait. Range −2.0 to +2.0.">?</span></span>
        <span>weight</span>
      </label>
      <input type="range" id="trait_weight_${i}" min="-2.0" max="2.0" step="0.05" value="${Number(t.selection_weight) || 0}" data-trait-idx="${i}">
      <input type="number" id="trait_weight_n_${i}" min="-2.0" max="2.0" step="0.05" value="${Number(t.selection_weight) || 0}" data-trait-idx="${i}" style="width:80px;">
    </div>
  `).join('');
  // Wire change events so the sliders + number boxes mirror each other,
  // and both write back into multiTraitState.
  list.querySelectorAll('input[data-trait-idx]').forEach(el => {
    el.addEventListener('input', () => {
      const idx = Number(el.getAttribute('data-trait-idx'));
      const v = Number(el.value);
      multiTraitState.traits[idx].selection_weight = v;
      const slider = byId('trait_weight_' + idx);
      const num = byId('trait_weight_n_' + idx);
      if (slider && slider !== el) slider.value = v;
      if (num && num !== el) num.value = v;
      updateSelectionIndexPreview();
    });
  });
  wrap.hidden = false;
  updateSelectionIndexPreview();
}

function updateSelectionIndexPreview() {
  const preview = byId('selectionIndexPreview');
  if (!preview) return;
  const traits = multiTraitState.traits || [];
  const pro = traits.filter(t => t.selection_weight > 0).map(t => `${t.name} (${(t.selection_weight > 0 ? '+' : '')}${Number(t.selection_weight).toFixed(2)})`);
  const con = traits.filter(t => t.selection_weight < 0).map(t => `${t.name} (${Number(t.selection_weight).toFixed(2)})`);
  const flat = traits.filter(t => t.selection_weight === 0).map(t => t.name);
  const parts = [];
  if (pro.length) parts.push(`<strong style="color:var(--ok)">prioritises</strong> ${pro.join(', ')}`);
  if (con.length) parts.push(`<strong style="color:var(--warn)">suppresses</strong> ${con.join(', ')}`);
  if (flat.length) parts.push(`ignores ${flat.join(', ')}`);
  preview.innerHTML = parts.length ? 'Net selection direction: ' + parts.join(' · ') : '';
}

// v0.7.22 — Issues 24/25. Out-of-DOM state for the multi-trait config.
// Form sliders (Issue 24) and presets (Issue 25) write here; requestFromForm
// reads here and injects into the API payload. Cleared by clearMultiTraitState
// when the operator picks a single-trait preset.
let multiTraitState = {traits: null, genetic_correlations: null};

function setFormValues(values) {
  // Handle multi-trait fields first so a preset can flip into multi-trait
  // mode in one click. setFormValues skips IDs that don't exist in the
  // DOM; traits and genetic_correlations live in state rather than DOM.
  if (Array.isArray(values.traits) && Array.isArray(values.genetic_correlations)) {
    multiTraitState.traits = values.traits.map(t => ({...t}));
    multiTraitState.genetic_correlations = values.genetic_correlations.map(r => r.slice());
  } else if ('traits' in values || 'genetic_correlations' in values) {
    // explicit empty/null on a preset → clear multi-trait state.
    multiTraitState.traits = null;
    multiTraitState.genetic_correlations = null;
  } else {
    // Preset has no multi-trait field → treat as single-trait (clear state).
    multiTraitState.traits = null;
    multiTraitState.genetic_correlations = null;
  }
  for (const [id, value] of Object.entries(values)) {
    if (id === 'traits' || id === 'genetic_correlations') continue;
    const el = byId(id);
    if (!el) continue;
    if (el.type === 'checkbox') el.checked = Boolean(value);
    // v0.8.5: the request carries ΔF as a fraction; the field is in percent.
    else if (id === 'target_delta_f') el.value = String(Math.round(Number(value) * 1e6) / 1e4);
    else el.value = String(value);
    // v0.8.0: presets now carry a dataset; let the selector's handler
    // hide the upload panel when a preset switches away from uploads.
    if (id === 'dataset') el.dispatchEvent(new Event('change'));
  }
  updateBudgetMeter();
  refreshSelectionIndexComposer();
  refreshSensAxisOptions();
}

const presets = {
  tiny: {
    dataset: 'synthetic',
    cycle_years: 0,
    genomic_cycle_years: 0,
    seed: 20260505,
    population_size: 8,
    markers: 180,
    qtl_count: 18,
    generations: 60,
    selection_percent: 25,
    heritability: 0.45,
    mutation_rate: 0.00002,
    crispr_enabled: true,
    crispr_edits: 3,
    crispr_intro_percent: 25,
    strategy_set: 'advanced',
    replicates: 20,
    worker_count: 0,
    inbreeding_limit: 0.25,
    diversity_loss_limit: 0.30
  },
  balanced: {
    dataset: 'synthetic',
    cycle_years: 0,
    genomic_cycle_years: 0,
    seed: 20260505,
    population_size: 400,
    markers: 900,
    qtl_count: 45,
    generations: 30,
    selection_percent: 10,
    heritability: 0.4,
    mutation_rate: 0.00005,
    crispr_enabled: true,
    crispr_edits: 3,
    crispr_intro_percent: 8,
    strategy_set: 'core',
    replicates: 3,
    worker_count: 0,
    inbreeding_limit: 0.25,
    diversity_loss_limit: 0.30
  },
  large: {
    dataset: 'synthetic',
    cycle_years: 0,
    genomic_cycle_years: 0,
    seed: 20260505,
    population_size: 2500,
    markers: 800,
    qtl_count: 50,
    generations: 35,
    selection_percent: 8,
    heritability: 0.35,
    mutation_rate: 0.00003,
    crispr_enabled: true,
    crispr_edits: 4,
    crispr_intro_percent: 4,
    strategy_set: 'core',
    replicates: 1,
    worker_count: 0,
    inbreeding_limit: 0.25,
    diversity_loss_limit: 0.30
  },
  // v0.8.4 — Issue 35. Same per-cycle accuracy for GS and phenotypic
  // selection; GS cycles twice as fast (2.5 vs 5 years) over a 30-year horizon.
  gs_years: {
    dataset: 'synthetic',
    cycle_years: 5,
    genomic_cycle_years: 2.5,
    seed: 20261020,
    population_size: 300,
    markers: 1500,
    qtl_count: 60,
    generations: 6,
    selection_percent: 10,
    heritability: 0.3,
    mutation_rate: 0,
    crispr_enabled: false,
    crispr_edits: 0,
    crispr_intro_percent: 0,
    strategy_set: 'advanced',
    replicates: 5,
    worker_count: 0,
    inbreeding_limit: 0.25,
    diversity_loss_limit: 0.30
  },
  crispr: {
    dataset: 'synthetic',
    cycle_years: 0,
    genomic_cycle_years: 0,
    seed: 20260505,
    population_size: 240,
    markers: 700,
    qtl_count: 55,
    generations: 36,
    selection_percent: 12,
    heritability: 0.5,
    mutation_rate: 0.00004,
    crispr_enabled: true,
    crispr_edits: 6,
    crispr_intro_percent: 12,
    strategy_set: 'advanced',
    replicates: 3,
    worker_count: 0,
    inbreeding_limit: 0.25,
    diversity_loss_limit: 0.30
  },
  // v0.7.20 — Issue 17. Holstein dairy preset. Defaults reflect the published
  // dairy literature audited 2026-05-28: N=800 cows, ~1500 markers, 8-generation
  // (≈ 40-year) horizon, h²=0.36 for milk yield, selection_percent=12 (typical
  // bull-dam tier), 5 replicates for noisy short-horizon. inbreeding_limit
  // 0.20 reflects the published Holstein concern range; single-trait until
  // the multi-trait engine ships (Issue 18).
  holstein: {
    dataset: 'holstein_synthetic',
    cycle_years: 0,
    genomic_cycle_years: 0,
    seed: 20260601,
    population_size: 800,
    markers: 1500,
    qtl_count: 50,
    generations: 8,
    selection_percent: 12,
    heritability: 0.36,
    mutation_rate: 0.00001,
    crispr_enabled: false,
    crispr_edits: 0,
    crispr_intro_percent: 0,
    strategy_set: 'core',
    replicates: 5,
    worker_count: 0,
    inbreeding_limit: 0.20,
    diversity_loss_limit: 0.35
  },
  // v0.7.22 — Issue 25. Methane (dairy, intensity) preset. Two-trait run
  // with the FAVOURABLE correlation between milk yield and methane
  // intensity (r_g = −0.26 per audited 2026-05-28 literature). Selection
  // against methane intensity also improves milk yield — the operator-
  // facing "happy path".
  methane_dairy_intensity: {
    dataset: 'holstein_synthetic',
    cycle_years: 0,
    genomic_cycle_years: 0,
    seed: 20260601,
    population_size: 200,
    markers: 600,
    qtl_count: 40,
    generations: 8,
    selection_percent: 12,
    heritability: 0.36,
    mutation_rate: 0.00001,
    crispr_enabled: false,
    crispr_edits: 0,
    crispr_intro_percent: 0,
    strategy_set: 'core',
    replicates: 3,
    worker_count: 0,
    inbreeding_limit: 0.25,
    diversity_loss_limit: 0.35,
    traits: [
      {name: 'milk_yield',        heritability: 0.36,  qtl_count: 30, effect_scale: 1.0, selection_weight: 1.0},
      {name: 'methane_intensity', heritability: 0.180, qtl_count: 20, effect_scale: 1.0, selection_weight: -0.5}
    ],
    genetic_correlations: [
      [1.0, -0.26],
      [-0.26, 1.0]
    ]
  },
  // v0.7.22 — Issue 25. Methane (dairy, production) preset — educational
  // contrast. UNFAVOURABLE positive correlation (r_g = +0.35 audited):
  // selecting against methane production trades off against milk yield.
  // Same shape as the intensity preset; different correlation sign.
  methane_dairy_production: {
    dataset: 'holstein_synthetic',
    cycle_years: 0,
    genomic_cycle_years: 0,
    seed: 20260601,
    population_size: 200,
    markers: 600,
    qtl_count: 40,
    generations: 8,
    selection_percent: 12,
    heritability: 0.36,
    mutation_rate: 0.00001,
    crispr_enabled: false,
    crispr_edits: 0,
    crispr_intro_percent: 0,
    strategy_set: 'core',
    replicates: 3,
    worker_count: 0,
    inbreeding_limit: 0.25,
    diversity_loss_limit: 0.35,
    traits: [
      {name: 'milk_yield',         heritability: 0.36,  qtl_count: 30, effect_scale: 1.0, selection_weight: 1.0},
      {name: 'methane_production', heritability: 0.211, qtl_count: 20, effect_scale: 1.0, selection_weight: -0.5}
    ],
    genetic_correlations: [
      [1.0, +0.35],
      [+0.35, 1.0]
    ]
  }
};

function setStatus(text, cls) {
  const el = byId('status');
  if (!el) return;
  el.textContent = text || '';
  el.className = 'status ' + (cls || '');
}

async function runSimulation() {
  const btn = byId('runBtn');
  const request = requestFromForm();
  if (currentData && requestSignature(request) === requestSignature(currentData.request || {})) {
    setStatus('No parameter changes since the current run. Recalculation skipped.', 'ok');
    return;
  }
  // v0.7.15: stop the request from leaving the browser if the run is over the
  // server-side budget cap. Without this the user sees a generic 400, and
  // reducing one parameter while the rest still keep budget over cap looks
  // like a stuck client.
  if (updateBudgetMeter().over) {
    const cells = formatCells(runBudgetCells(request));
    setStatus(`Run budget ${cells} cells exceeds the MVP cap of ${formatCells(BUDGET_CAP)}. Reduce population size, markers, generations, or replicates.`, 'error');
    return;
  }
  // v0.8.0 — Issue 34. Mirror the server's selection_percent check here:
  // the production proxy replaces 400 bodies, so the server's unit hint
  // would never reach the user.
  const sel = Number(request.selection_percent);
  if (!(sel >= 1 && sel <= 100)) {
    const n = Number(request.population_size) || 0;
    const hint = sel > 0 && sel < 1
      ? ` ${sel} selects ${(n * sel / 100).toFixed(2)} of ${n} individuals — if you meant ${sel * 100}%, enter ${sel * 100}.`
      : '';
    setStatus(`Selection intensity is a percent on a 1–100 scale (e.g. 20 for 20%).${hint}`, 'error');
    return;
  }
  const originalText = btn ? btn.textContent : '';
  if (btn) {
    btn.disabled = true;
    setRunButtonProgress(0, 'Starting');
  }
  setStatus('Starting simulation job...', '');
  // v0.7.6 live histogram: reset snapshot state for a fresh run.
  // v0.7.13: also reset the playback queue + seq cursor.
  lastSnapshot = null;
  lastDrawnGeneration = -1;
  snapshotSeq = 0;
  pendingFrames = [];
  if (playbackTimer) {
    clearTimeout(playbackTimer);
    playbackTimer = null;
  }
  resetLiveHistogram();
  try {
    const startRes = await fetch('/api/simulate/start', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(request)
    });
    if (!startRes.ok) {
      const text = await startRes.text();
      throw new Error(text || `HTTP ${startRes.status}`);
    }
    const start = await startRes.json();
    if (!start.job_id) throw new Error('Server did not return a job id');

    let job = null;
    for (;;) {
      // v0.7.13: pass ?since=snapshotSeq so the server returns only new frames
      // we haven't seen yet. The full history is on the server; we accumulate
      // missed frames into a queue that the playback timer consumes at a
      // fixed UI tempo (PLAYBACK_FRAME_MS), so a fast backend looks animated
      // rather than jumping to the final state.
      const statusURL = '/api/simulate/status?id='
        + encodeURIComponent(start.job_id)
        + '&since=' + snapshotSeq;
      const statusRes = await fetch(statusURL, {cache: 'no-store'});
      if (!statusRes.ok) {
        const text = await statusRes.text();
        throw new Error(text || `HTTP ${statusRes.status}`);
      }
      job = await statusRes.json();
      const percent = Number.isFinite(Number(job.percent)) ? Number(job.percent) : 0;
      setRunButtonProgress(percent, job.done ? 'Finishing' : 'Running');
      setStatus(`${job.message || 'Running'} — ${Math.round(percent)}%`, '');
      if (Array.isArray(job.snapshots) && job.snapshots.length > 0) {
        for (const s of job.snapshots) pendingFrames.push(s);
        startHistogramPlayback();
      }
      if (Number.isFinite(Number(job.snapshot_seq))) {
        snapshotSeq = Number(job.snapshot_seq);
      }
      if (job.done) break;
      await sleep(80);
    }

    if (job.error) throw new Error(job.error);
    if (!job.result) throw new Error('Simulation finished without a result');

    const data = job.result;
    setFormValues(data.request || request);
    if (currentData) previousData = currentData;
    currentData = data;
    renderAll(currentData, previousData);
    // v0.7.31 — Issue 10. Hide the empty-state placeholder and enable
    // the jump-to-report button once results exist.
    const emptyEl = byId('emptyStateCard');
    if (emptyEl) emptyEl.hidden = true;
    const jumpEl = byId('jumpToReportBtn');
    if (jumpEl) jumpEl.disabled = false;
    const changed = previousData ? changedParams(previousData.request || {}, currentData.request || {}) : [];
    const changedText = changed.length ? ' Changed: ' + changed.join('; ') + '.' : '';
    setStatus(previousData ? 'Simulation complete. Dotted lines show the previous run.' + changedText : 'Simulation complete.', 'ok');
  } catch (err) {
    console.error(err);
    setStatus(err.message || String(err), 'error');
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.textContent = originalText || 'Run simulation';
    }
  }
}

function setRunButtonProgress(percent, label) {
  const btn = byId('runBtn');
  if (!btn) return;
  const p = Math.max(0, Math.min(100, Math.round(Number(percent) || 0)));
  btn.textContent = `${label || 'Running'} ${p}%`;
}

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function clearComparison() {
  previousData = null;
  if (currentData) renderAll(currentData, null);
  setStatus('Previous-run overlay cleared.', 'ok');
}

function randomizeSeed() {
  byId('seed').value = String(Math.floor(1 + Math.random() * 2147483646));
  markDirty('seed changed');
}

// v0.7.23 — Issue D. Reset form to the same defaults as the initial
// page load (balanced preset). Also clears multi-trait state so any
// methane / multi-trait config is wiped along with the form.
function resetFormToDefaults() {
  setFormValues(presets.balanced);
  setStatus('Form reset to "Balanced default" preset.', 'ok');
}

function applyPreset(name) {
  if (!presets[name]) return;
  setFormValues(presets[name]);
  const names = {
    tiny: 'Tiny drift demo',
    balanced: 'Balanced default',
    large: 'Large fast demo',
    crispr: 'CRISPR seed demo',
    gs_years: 'GS vs phenotype on a calendar axis (5-year vs 2.5-year cycles)',
    holstein: 'Holstein dairy (educational — no sexes or overlapping generations)',
    methane_dairy_intensity: 'Methane (dairy, intensity) — favourable -0.26 correlation',
    methane_dairy_production: 'Methane (dairy, production) — unfavourable +0.35 correlation'
  };
  markDirty(`${names[name] || name} preset loaded`);
}

function renderAll(data, prev) {
  renderDecisionPanel(data);
  renderSummary(data, prev);
  renderNotes(data, prev);
  drawMetricChart('chart_gain', 'legend_gain', data, prev, 'gain_sigma', 'Genetic gain (founder σA)');
  drawMetricChart('chart_diversity', 'legend_diversity', data, prev, 'diversity', 'Diversity (expected heterozygosity)');
  drawMetricChart('chart_inbreeding', 'legend_inbreeding', data, prev, 'inbreeding', 'Inbreeding F (identity by descent)');
  drawNeChart('chart_ne', 'legend_ne', data, prev);
  drawMetricChart('chart_drift', 'legend_drift', data, prev, 'allele_drift', 'Allele drift');
  drawMetricChart('chart_lost', 'legend_lost', data, prev, 'rare_useful_lost', 'Rare useful loci lost');
  drawMetricChart('chart_fixed', 'legend_fixed', data, prev, 'fixed_loci', 'Fixed loci');
  // v0.7.22 — Issue 23. Multi-trait Pareto axis controls.
  refreshParetoAxisControls(data);
  drawParetoChart('chart_pareto', data);
  // v0.7.18 — pass set-level NGT classification to both renderers.
  const ngt = (data.decision && data.decision.ngt) || null;
  renderNGTRegulatoryCard(ngt, data.request);
  // v0.7.31 — Issue 07. Render the edit-vs-cross-vs-wait headline card
  // above the candidate-edit table from decision.edit_decisions.
  renderEditDecisionsCard((data.decision && data.decision.edit_decisions) || null);
  renderEditTable(data.candidate_edits || [], ngt);
  renderStrategyTable(data.strategies || [], prev);
}


function renderDecisionPanel(data) {
  const el = byId('decisionPanel');
  if (!el) return;
  const d = data.decision || {};
  const best = findStrategy(data, d.best_risk_adjusted_code);
  const bestGain = findStrategy(data, d.best_gain_code);
  const lowest = findStrategy(data, d.lowest_risk_code);
  const bestFeasible = d.best_feasible_code ? findStrategy(data, d.best_feasible_code) : null;
  const constraintsApplied = Array.isArray(d.constraints_applied) ? d.constraints_applied : [];
  const lines = (d.interpretation || []).map(x => `<li>${escapeHtml(x)}</li>`).join('');
  const pareto = (d.pareto_codes || []).map(code => `<span class="pill">${escapeHtml(labels[code] || code)}</span>`).join(' ');
  const tradeoffs = (d.tradeoffs || []).map(t => `<li><strong>${escapeHtml((labels[t.a] || t.a) + ' ↔ ' + (labels[t.b] || t.b))}</strong> <em>(${escapeHtml(t.theme)})</em><br><span>${escapeHtml(t.note)}</span></li>`).join('');
  const avoid = (d.avoid_strategies || []).map(a => `<li><strong>${escapeHtml(labels[a.code] || a.name)}</strong> — ${escapeHtml(a.reason)}</li>`).join('');
  const warnings = (d.missing_data_warnings || []).map(w => `<li>${escapeHtml(w)}</li>`).join('');
  const assumptions = (d.key_assumptions || []).map(a => `<li>${escapeHtml(a)}</li>`).join('');
  const wrong = (d.what_could_be_wrong || []).map(w => `<li>${escapeHtml(w)}</li>`).join('');
  const limitations = (d.limitations || []).map(l => `<li>${escapeHtml(l)}</li>`).join('');
  const next = d.next_analysis ? `<p class="decision-next"><strong>Recommended next analysis:</strong> ${escapeHtml(d.next_analysis)}</p>` : '';
  const honesty = (data.degenerate_run
      ? `<div class="decision-honesty-banner decision-degenerate-banner">⚠ Degenerate run: the selection intensity left fewer than two parents, so every selecting strategy ran on a 2-parent bottleneck. The report below does not describe your intended program — raise selection % or population size.</div>`
      : '')
    + (d.honesty_banner ? `<div class="decision-honesty-banner">${escapeHtml(d.honesty_banner)}</div>` : '');
  const feasibilityCard = constraintsApplied.length
    ? `<div class="decision-card feasible-card"><span>Best feasible</span><strong>${escapeHtml(bestFeasible ? (labels[bestFeasible.code] || bestFeasible.name) : 'none')}</strong><small>${bestFeasible ? 'risk-adjusted score ' + fmt(bestFeasible.final.risk_adjusted_score) : 'no strategy passes all constraints'}</small></div>`
    : '';
  const feasibilityNote = d.feasibility_note
    ? `<div class="feasibility-note${bestFeasible ? '' : ' feasibility-note-empty'}"><strong>Feasibility:</strong> ${escapeHtml(d.feasibility_note)}</div>`
    : '';
  const constraintsChips = constraintsApplied.length
    ? `<div class="constraints-applied"><strong>Constraints applied:</strong> ${constraintsApplied.map(c => `<span class="pill">${escapeHtml(c)}</span>`).join(' ')}</div>`
    : '';
  el.innerHTML = `
    ${honesty}
    <div class="decision-grid${constraintsApplied.length ? ' decision-grid-4' : ''}">
      <div class="decision-card"><span>Recommended</span><strong>${escapeHtml(best ? (labels[best.code] || best.name) : '-')}</strong><small>risk-adjusted score ${best ? fmt(best.final.risk_adjusted_score) : '-'}</small></div>
      <div class="decision-card"><span>Max gain</span><strong>${escapeHtml(bestGain ? (labels[bestGain.code] || bestGain.name) : '-')}</strong><small>${bestGain ? fmt(bestGain.final.genetic_gain_sigma) + ' σA' : '-'} final gain</small></div>
      <div class="decision-card"><span>Lowest risk</span><strong>${escapeHtml(lowest ? (labels[lowest.code] || lowest.name) : '-')}</strong><small>${lowest ? fmt(combinedRisk(lowest.final)) : '-'} combined risk</small></div>
      ${feasibilityCard}
    </div>
    ${feasibilityNote}
    ${constraintsChips}
    <ul class="decision-list">${lines}</ul>
    <div class="pareto-list"><strong>Pareto candidates:</strong> ${pareto || '-'}</div>
    ${next}
    ${tradeoffs ? `<details class="decision-section" open><summary><strong>Top trade-offs</strong> (${(d.tradeoffs || []).length})</summary><ul class="decision-list">${tradeoffs}</ul></details>` : ''}
    ${avoid ? `<details class="decision-section" open><summary><strong>Strategies to avoid</strong> (${(d.avoid_strategies || []).length})</summary><ul class="decision-list">${avoid}</ul></details>` : ''}
    ${wrong ? `<details class="decision-section decision-what-could-be-wrong" open><summary><strong>What could make this recommendation wrong?</strong> (${(d.what_could_be_wrong || []).length})</summary><ul class="decision-list">${wrong}</ul></details>` : ''}
    ${warnings ? `<details class="decision-section decision-warnings"><summary><strong>⚠ Missing-data warnings</strong> (${(d.missing_data_warnings || []).length})</summary><ul class="decision-list">${warnings}</ul></details>` : ''}
    ${assumptions ? `<details class="decision-section"><summary><strong>Key assumptions</strong> (${(d.key_assumptions || []).length})</summary><ul class="decision-list">${assumptions}</ul></details>` : ''}
    ${limitations ? `<details class="decision-section decision-limitations"><summary><strong>Model limitations</strong> (${(d.limitations || []).length})</summary><ul class="decision-list">${limitations}</ul></details>` : ''}
  `;
}

function renderNotes(data, prev) {
  const el = byId('notes');
  if (!el) return;
  const notes = [...(data.notes || [])];
  if (prev) {
    notes.unshift('Comparison mode: solid lines are the current run; dotted lines are the previous run.');
    const changed = changedParams(prev.request || {}, data.request || {});
    if (changed.length) notes.unshift('Changed parameters: ' + changed.join('; '));
  }
  const req = data.request || {};
  const budget = (Number(req.population_size) || 0) * (Number(req.markers) || 0) * ((Number(req.generations) || 0) + 1);
  const strategyCount = (data.strategies || []).length;
  const reps = Number(req.replicates) || 1;
  notes.push(`Run budget: ${formatInt(budget * strategyCount * reps)} population-marker-generation-strategy-replicate cells.`);
  el.innerHTML = notes.map(n => `<div class="note-item">${escapeHtml(n)}</div>`).join('');
}

function renderSummary(data, prev) {
  const s = data.strategies || [];
  const bestGain = maxBy(s, x => x.final.genetic_gain);
  const bestDiversity = maxBy(s, x => x.final.diversity);
  const lowestInbreeding = minBy(s, x => x.final.inbreeding);
  const balanced = s.find(x => x.code === 'balanced') || s[0];
  const req = data.request || {};
  const prevBalanced = findStrategy(prev, balanced ? balanced.code : 'balanced');
  const balancedDelta = balanced && prevBalanced ? signedDelta(balanced.final.genetic_gain - prevBalanced.final.genetic_gain) + ' vs previous' : 'no previous run';
  const expectedFlips = expectedMutationFlips(req);
  const cards = [
    ['Best final gain', bestGain ? labels[bestGain.code] : '-', bestGain ? fmt(bestGain.final.genetic_gain) : '-'],
    ['Best diversity', bestDiversity ? labels[bestDiversity.code] : '-', bestDiversity ? fmt(bestDiversity.final.diversity) : '-'],
    ['Lowest inbreeding', lowestInbreeding ? labels[lowestInbreeding.code] : '-', lowestInbreeding ? fmt(lowestInbreeding.final.inbreeding) : '-'],
    ['BreedOS default', balancedDelta, balanced ? fmt(balanced.final.genetic_gain) + ' gain' : '-'],
    ['Population', Number(req.population_size) < 50 ? 'small-pop drift regime' : 'standard demo regime', formatInt(req.population_size)],
    ['Markers / QTL', `${formatInt(req.qtl_count)} causal loci`, `${formatInt(req.markers)} markers`],
    ['Mutation rate', 'per allele per generation', formatParamValue(req.mutation_rate)],
    ['Expected flips', 'per strategy replicate', expectedFlips < 10 ? fmt(expectedFlips) : formatInt(expectedFlips)],
    ['Strategy set', `${(data.strategies || []).length} strategies`, String(req.strategy_set || 'core')],
    ['Replicates', 'Monte Carlo runs per strategy', formatInt(req.replicates || 1)],
    ['Best decision', data.decision ? (labels[data.decision.best_risk_adjusted_code] || data.decision.best_risk_adjusted_name || '-') : '-', data.decision ? (data.decision.best_risk_adjusted_code || '-') : '-']
  ];
  byId('summary').innerHTML = cards.map(([label, name, value]) => `
    <div class="metric-card">
      <div class="label">${escapeHtml(label)}</div>
      <div class="value">${escapeHtml(value)}</div>
      <div class="label">${escapeHtml(name)}</div>
    </div>
  `).join('');
}

function renderEditTable(edits, ngt) {
  if (!edits.length) {
    byId('editTable').innerHTML = '<tr><td colspan="9">No candidate edits returned.</td></tr>';
    return;
  }
  // v0.7.18 — Issue 14. The NGT classification is set-level (one verdict
  // per run), so every row shows the same badge. The badge stays meaningful
  // because it tells the user "this whole edit set falls into category X".
  const badge = ngtBadgeHtml(ngt);
  byId('editTable').innerHTML = edits.map(e => `
    <tr>
      <td>${e.rank}</td>
      <td>${e.locus}</td>
      <td>${fmt(e.effect)}</td>
      <td>${fmt(e.allele_frequency)}</td>
      <td>${fmt(e.expected_gain_score)}</td>
      <td>${escapeHtml(e.diversity_risk)}</td>
      <td>${editClassBadgeHtml(e.classification)}</td>
      <td>${escapeHtml(e.decision)}</td>
      <td>${badge}</td>
    </tr>
  `).join('');
}

// v0.7.31 — Issue 31. Renders the climate-robustness Decision Report
// section under the sweep verdict. Pulls from sweep result's
// climate_robustness field; hidden when nil (non-climate axis or
// single-scenario sweep).
function renderClimateRobustnessSection(cr) {
  const el = byId('sensClimateRobustness');
  if (!el) return;
  if (!cr) {
    el.hidden = true;
    el.innerHTML = '';
    return;
  }
  const parts = [];
  parts.push('<h3 style="margin:0 0 8px; color:var(--accent2);">Climate robustness</h3>');
  if (cr.headline)           parts.push('<p style="margin:0 0 8px;">' + escapeHtml(cr.headline) + '</p>');
  if (cr.failure_modes)      parts.push('<p style="margin:0 0 8px;">' + escapeHtml(cr.failure_modes) + '</p>');
  if (cr.alternative_advice) parts.push('<p style="margin:0 0 8px;">' + escapeHtml(cr.alternative_advice) + '</p>');
  if (cr.ancestral_advice)   parts.push('<p style="margin:0 0 8px; padding:8px 10px; border-left:3px solid var(--accent); background:rgba(143,255,209,0.06);">' + escapeHtml(cr.ancestral_advice) + '</p>');
  if (cr.honesty_caveat)     parts.push('<p style="margin:8px 0 0; padding:8px 10px; border-left:3px solid var(--muted); background:rgba(0,0,0,0.18); font-size:12px; color:var(--muted);">⚠ ' + escapeHtml(cr.honesty_caveat) + '</p>');
  el.innerHTML = parts.join('');
  el.hidden = false;
}

// v0.7.31 — Issue 07. Renders the headline card above the candidate-
// edit table. Hidden when no edits were ranked.
function renderEditDecisionsCard(summary) {
  const el = byId('editDecisionsCard');
  if (!el) return;
  if (!summary || !summary.total_candidates) {
    el.hidden = true;
    return;
  }
  const headline = summary.headline || '';
  el.innerHTML = `
    <h3 style="color:var(--accent2);">Edit / Cross / Wait — decision mix</h3>
    <p style="margin:0 0 8px;">${escapeHtml(headline)}</p>
    <div style="display:flex; gap:14px; flex-wrap:wrap; font-size:13px;">
      <span><span class="edit-class-badge edit">EDIT</span> ${summary.edit_count}</span>
      <span><span class="edit-class-badge cross">CROSS</span> ${summary.cross_count}</span>
      <span><span class="edit-class-badge wait">WAIT</span> ${summary.wait_count}</span>
      <span style="color:var(--muted);">of ${summary.total_candidates} ranked candidate(s)</span>
    </div>
    <p class="ngt-disclaimer" style="margin-top:10px;">Classification rules: <strong>EDIT</strong> — large effect (≥ 0.30) on a rare allele (p &lt; 0.20), or any meaningful effect (≥ 0.10) on a rare allele where selection is too slow. <strong>CROSS</strong> — favourable allele already segregating (p ≥ 0.20). <strong>WAIT</strong> — effect below the marginal-gain threshold (0.10), or allele near fixation (p ≥ 0.92), or bottleneck risk (founder diversity &lt; 0.15 with rare allele). Hover any row's badge for the per-candidate reason.</p>
  `;
  el.hidden = false;
}

// v0.7.31 — Issue 07. Colour-coded edit-vs-cross-vs-wait badge with
// the full reason / posture / risk tooltip. Pairs with the CSS classes
// .edit-class-badge.{edit,cross,wait,unknown}.
function editClassBadgeHtml(c) {
  if (!c || !c.class) return '<span class="edit-class-badge unknown" title="No classification.">—</span>';
  const cls = c.class === 'edit' ? 'edit'
            : c.class === 'cross' ? 'cross'
            : c.class === 'wait' ? 'wait'
            : 'unknown';
  const label = c.class === 'edit' ? 'EDIT'
              : c.class === 'cross' ? 'CROSS'
              : c.class === 'wait' ? 'WAIT'
              : '—';
  const tipParts = [];
  if (c.reason) tipParts.push(c.reason);
  if (c.introgression_posture) tipParts.push('Posture: ' + c.introgression_posture);
  if (c.risk_warning) tipParts.push('Risk: ' + c.risk_warning);
  if (c.reason_code) tipParts.push('Code: ' + c.reason_code);
  const tip = tipParts.join('\n\n');
  return `<span class="edit-class-badge ${cls}" tabindex="0" title="${escapeHtml(tip)}">${label}</span>`;
}

// v0.7.18 — Issue 14. Renders a colour-coded NGT badge from an
// NGTClassification or returns an em-dash placeholder when absent.
function ngtBadgeHtml(ngt) {
  if (!ngt || !ngt.category) return '<span class="ngt-badge ngt-unclassifiable" title="No edits planned or NGT context not set.">—</span>';
  const cls = ngt.category === 'NGT-1' ? 'ngt-1'
            : ngt.category === 'NGT-2' ? 'ngt-2'
            : 'ngt-unclassifiable';
  const tooltipBits = [];
  (ngt.reasons || []).forEach(r => tooltipBits.push('• ' + r));
  (ngt.disqualifiers || []).forEach(d => tooltipBits.push('✗ ' + d));
  if (ngt.confidence_note) tooltipBits.push('— ' + ngt.confidence_note);
  return `<span class="ngt-badge ${cls}" tabindex="0" title="${escapeHtml(tooltipBits.join('\n'))}">${escapeHtml(ngt.category)}</span>`;
}

// v0.7.18 — Issue 15. Regulatory card rendered above the candidate-edit
// table. Shows the category headline, reasons, disqualifiers, optional
// patent-declaration warning (Issue 16), and the verbatim disclaimer.
function renderNGTRegulatoryCard(ngt, request) {
  const el = byId('ngtRegulatoryCard');
  if (!el) return;
  if (!ngt) {
    el.hidden = true;
    return;
  }
  const cls = ngt.category === 'NGT-1' ? 'ngt-1'
            : ngt.category === 'NGT-2' ? 'ngt-2'
            : 'ngt-unclassifiable';
  el.className = 'ngt-regulatory-card ' + cls;
  const headline = ngt.category === 'NGT-1'
    ? 'NGT-1 — equivalent to conventionally bred; exempt from GMO legislation under the new EU framework.'
    : ngt.category === 'NGT-2'
      ? 'NGT-2 — full GMO authorisation, traceability, and labelling required.'
      : 'Unclassifiable — set target trait class and donor source above to classify.';
  const reasonsList = (ngt.reasons || []).map(r => '<li>' + escapeHtml(r) + '</li>').join('');
  const disqList = (ngt.disqualifiers || []).map(d => '<li>' + escapeHtml(d) + '</li>').join('');
  const downstream = ngt.category === 'NGT-1'
    ? 'Downstream: registration in the new EU variety framework, comparable to conventionally bred varieties. Labelling required on seed and other reproductive material; not on the variety itself. Offspring requires no further checks. Patent rights must be declared at registration.'
    : ngt.category === 'NGT-2'
      ? 'Downstream: full GMO authorisation procedure, traceability through the supply chain, labelling on the variety and downstream products, ongoing post-market monitoring.'
      : 'Downstream: unknown until classification is settled.';
  // Patent warning (Issue 16): only when classified NGT-1 and the user did
  // not fill the patent_id field.
  const patentMissing = ngt.category === 'NGT-1'
    && request && request.ngt
    && (!request.ngt.patent_id || !request.ngt.patent_id.trim());
  const warning = patentMissing
    ? '<div class="ngt-patent-warning">⚠ NGT-1 registration will require explicit patent declaration. None recorded for this run. See "Patent / licensing declaration" in the form.</div>'
    : '';
  el.innerHTML = `
    <h3>${escapeHtml(headline)}</h3>
    <div class="ngt-section-title">Reasons</div>
    <ul>${reasonsList || '<li>—</li>'}</ul>
    ${disqList ? `<div class="ngt-section-title">Disqualifiers</div><ul>${disqList}</ul>` : ''}
    <div class="ngt-section-title">Downstream implications</div>
    <p style="margin:0">${escapeHtml(downstream)}</p>
    ${warning}
    <div class="ngt-disclaimer">${escapeHtml(ngt.confidence_note || '')}</div>
  `;
  el.hidden = false;
}

function renderStrategyTable(strategies, prev) {
  if (!strategies.length) {
    byId('strategyTable').innerHTML = '<tr><td colspan="14">No strategies returned.</td></tr>';
    return;
  }
  byId('strategyTable').innerHTML = strategies.map(s => {
    const p = findStrategy(prev, s.code);
    const delta = p ? [
      `gain ${signedDelta(s.final.genetic_gain - p.final.genetic_gain)}`,
      `div ${signedDelta(s.final.diversity - p.final.diversity)}`,
      `F ${signedDelta(s.final.inbreeding - p.final.inbreeding)}`
    ].join('<br>') : '-';
    const failed = Array.isArray(s.final.failed_constraints) ? s.final.failed_constraints : [];
    const feasibleCell = s.final.feasible
      ? '<span class="feasible-yes" title="Passes all active constraints">✓</span>'
      : (failed.length
        ? `<span class="feasible-no" title="${escapeHtml(failed.join('; '))}">✗ <small>${failed.length}</small></span>`
        : '<span>—</span>');
    const rowClass = s.final.feasible === false && failed.length ? ' class="row-infeasible"' : '';
    return `
      <tr${rowClass}>
        <td><strong style="color:${colors[s.code] || '#fff'}">${escapeHtml(labels[s.code] || s.name)}</strong><br><span>${escapeHtml(s.summary)}</span></td>
        <td>${formatInt(s.final.decision_rank)}</td>
        <td>${fmt(s.final.risk_adjusted_score)}</td>
        <td>${fmt(s.final.genetic_gain)}<br><span>±${fmt(s.final.gain_std)} · ${fmt(s.final.genetic_gain_sigma)} σA${s.final.years ? ` · ${fmt(s.final.gain_sigma_per_year)} σA/yr` : ''}</span></td>
        <td>${fmt(s.final.diversity)}<br><span>±${fmt(s.final.diversity_std)}</span></td>
        <td>${fmt(s.final.inbreeding)}<br><span>±${fmt(s.final.inbreeding_std)} · ΔF ${(100 * (Number(s.final.mean_delta_f) || 0)).toFixed(2)}% · Ne ${formatInt(Math.round(Number(s.final.ne) || 0))}</span></td>
        <td>${fmt(combinedRisk(s.final))}</td>
        <td>${fmt(s.final.probability_inbreeding_breach)} / ${fmt(s.final.probability_diversity_collapse)} / ${fmt(s.final.probability_rare_useful_loss)}</td>
        <td>${formatInt(s.final.fixed_loci)}</td>
        <td>${formatInt(s.final.effective_parents)}</td>
        <td>${s.final.pareto_optimal ? 'yes' : 'no'}</td>
        <td>${feasibleCell}</td>
        <td>${delta}</td>
        <td>${escapeHtml(s.final.recommended_next)}</td>
      </tr>
    `;
  }).join('');
}


// v0.7.22 — Issue 23. Axis identifiers used by the picker:
//   "combined_risk"  → combinedRisk(strategy.final)
//   "trait:<name>"   → last per-trait gain from strategy.per_trait_metrics
//   "single_gain"    → strategy.final.genetic_gain (single-trait fallback)
function paretoAxisExtractor(axisKey, traits) {
  if (axisKey === 'combined_risk') return s => combinedRisk(s.final);
  if (axisKey === 'single_gain') return s => Number(s.final.genetic_gain);
  if (axisKey && axisKey.startsWith('trait:')) {
    const name = axisKey.slice(6);
    const idx = (traits || []).findIndex(t => t.name === name);
    if (idx < 0) return s => 0;
    return s => {
      const ptm = s.per_trait_metrics;
      if (!ptm || !ptm[idx] || !ptm[idx].length) return 0;
      return Number(ptm[idx][ptm[idx].length - 1].genetic_gain) || 0;
    };
  }
  return s => Number(s.final.genetic_gain);
}

// v0.7.22 — Issue 23. Populate axis dropdowns based on what the run carries.
// Multi-trait runs get the controls visible; single-trait keeps them hidden.
function refreshParetoAxisControls(data) {
  const wrap = byId('paretoAxisControls');
  const xSel = byId('paretoAxisX');
  const ySel = byId('paretoAxisY');
  if (!wrap || !xSel || !ySel) return;
  const traits = (data && data.request && data.request.traits) || [];
  if (!traits.length) {
    wrap.hidden = true;
    return;
  }
  const opts = [
    {key: 'combined_risk', label: 'combined risk'},
    ...traits.map(t => ({key: 'trait:' + t.name, label: t.name + ' (gain)'}))
  ];
  const renderOpts = sel => {
    const prev = sel.value;
    sel.innerHTML = opts.map(o => `<option value="${escapeHtml(o.key)}">${escapeHtml(o.label)}</option>`).join('');
    if (opts.find(o => o.key === prev)) sel.value = prev;
  };
  renderOpts(xSel);
  renderOpts(ySel);
  if (!xSel.value) xSel.value = 'combined_risk';
  if (!ySel.value) ySel.value = 'trait:' + traits[0].name;
  wrap.hidden = false;
}

function drawParetoChart(canvasId, data) {
  const canvas = byId(canvasId);
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const width = canvas.width;
  const height = canvas.height;
  ctx.clearRect(0, 0, width, height);
  const strategies = data.strategies || [];
  if (!strategies.length) return;
  const margin = {left: 62, right: 24, top: 28, bottom: 52};
  const innerW = width - margin.left - margin.right;
  const innerH = height - margin.top - margin.bottom;
  // v0.7.22 — Issue 23. If the multi-trait axis picker is visible, use its
  // selected values; otherwise the existing single-trait default
  // (combined risk × genetic gain) is used.
  const traits = (data.request && data.request.traits) || [];
  const xCtrl = byId('paretoAxisX');
  const yCtrl = byId('paretoAxisY');
  const xKey = (traits.length && xCtrl && xCtrl.value) ? xCtrl.value : 'combined_risk';
  const yKey = (traits.length && yCtrl && yCtrl.value) ? yCtrl.value : 'single_gain';
  const xLabelText = (traits.length && xCtrl) ? xCtrl.options[xCtrl.selectedIndex].text : 'combined risk probability';
  const yLabelText = (traits.length && yCtrl) ? yCtrl.options[yCtrl.selectedIndex].text : 'genetic gain';
  const xGet = paretoAxisExtractor(xKey, traits);
  const yGet = paretoAxisExtractor(yKey, traits);
  const xs = strategies.map(xGet);
  const ys = strategies.map(yGet);
  let minX = Math.min(...xs), maxX = Math.max(...xs);
  let minY = Math.min(...ys), maxY = Math.max(...ys);
  if (Math.abs(maxX - minX) < 1e-9) { minX -= 0.1; maxX += 0.1; }
  if (Math.abs(maxY - minY) < 1e-9) { minY -= 1; maxY += 1; }
  const padX = (maxX - minX) * 0.12;
  const padY = (maxY - minY) * 0.12;
  minX = Math.max(0, minX - padX); maxX += padX;
  minY -= padY; maxY += padY;
  const x = v => margin.left + (Number(v) - minX) / (maxX - minX) * innerW;
  const y = v => margin.top + (1 - (Number(v) - minY) / (maxY - minY)) * innerH;
  ctx.fillStyle = 'rgba(255,255,255,0.025)';
  ctx.fillRect(margin.left, margin.top, innerW, innerH);
  ctx.strokeStyle = 'rgba(255,255,255,0.12)';
  ctx.lineWidth = 1;
  ctx.fillStyle = 'rgba(237,248,245,0.72)';
  ctx.font = '12px system-ui, sans-serif';
  ctx.textAlign = 'right'; ctx.textBaseline = 'middle';
  for (let i = 0; i <= 4; i++) {
    const yy = margin.top + innerH * i / 4;
    ctx.beginPath(); ctx.moveTo(margin.left, yy); ctx.lineTo(margin.left + innerW, yy); ctx.stroke();
    ctx.fillText(fmt(maxY - (maxY - minY) * i / 4), margin.left - 8, yy);
  }
  ctx.textAlign = 'center'; ctx.textBaseline = 'top';
  for (let i = 0; i <= 4; i++) {
    const val = minX + (maxX - minX) * i / 4;
    const xx = x(val);
    ctx.fillText(fmt(val), xx, margin.top + innerH + 12);
  }
  ctx.fillStyle = 'rgba(237,248,245,0.90)';
  ctx.textAlign = 'left'; ctx.fillText('Pareto frontier: ' + yLabelText + ' vs ' + xLabelText, margin.left, 6);
  ctx.textAlign = 'center'; ctx.fillText(xLabelText, margin.left + innerW / 2, height - 22);
  ctx.save();
  ctx.translate(16, margin.top + innerH / 2);
  ctx.rotate(-Math.PI / 2);
  ctx.fillText(yLabelText, 0, 0);
  ctx.restore();
  for (const s of strategies) {
    const xx = x(xGet(s));
    const yy = y(yGet(s));
    ctx.fillStyle = colors[s.code] || '#fff';
    ctx.strokeStyle = s.final.pareto_optimal ? 'rgba(255,255,255,0.95)' : 'rgba(0,0,0,0.25)';
    ctx.lineWidth = s.final.pareto_optimal ? 3 : 1;
    ctx.beginPath(); ctx.arc(xx, yy, s.final.pareto_optimal ? 7 : 5, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
    ctx.fillStyle = 'rgba(237,248,245,0.86)';
    ctx.font = '11px system-ui, sans-serif';
    ctx.textAlign = 'left'; ctx.textBaseline = 'bottom';
    ctx.fillText(labels[s.code] || s.code, xx + 8, yy - 4);
  }
}

// v0.7.6 live histogram: render the per-generation AFS bins as a bar chart.
// `snap` is the latest_snapshot object from /api/simulate/status: it carries
// {generation, total_generations, strategy_code, strategy_name, bins[10]}.
function drawLiveHistogram(snap) {
  const canvas = byId('chart_histogram');
  if (!canvas) return;
  const label = byId('histogramLabel');
  if (!snap || !Array.isArray(snap.bins) || snap.bins.length === 0) return;
  const total = Number(snap.total_generations) || 0;
  const gen = Number(snap.generation) || 0;
  const niceName = labels[snap.strategy_code] || snap.strategy_name || snap.strategy_code || 'strategy';
  if (label) {
    label.textContent = `Live allele-frequency spectrum — ${niceName}, generation ${gen} / ${total}`;
  }
  const ctx = canvas.getContext('2d');
  const width = canvas.width;
  const height = canvas.height;
  ctx.clearRect(0, 0, width, height);
  const margin = {left: 44, right: 16, top: 14, bottom: 30};
  const innerW = width - margin.left - margin.right;
  const innerH = height - margin.top - margin.bottom;
  const bins = snap.bins.map(v => Number(v) || 0);
  // v0.7.8: fix Y-axis to total markers (sum of bins). Bins always sum to M,
  // so this gives a stable scale across all generations — no rescaling
  // jitter as alleles drift toward fixation. Bars represent fraction of
  // markers in each frequency bin.
  const totalMarkers = bins.reduce((a, b) => a + b, 0);
  const maxV = Math.max(1, totalMarkers);
  ctx.fillStyle = 'rgba(255,255,255,0.025)';
  ctx.fillRect(margin.left, margin.top, innerW, innerH);
  ctx.strokeStyle = 'rgba(255,255,255,0.10)';
  ctx.lineWidth = 1;
  ctx.fillStyle = 'rgba(237,248,245,0.72)';
  ctx.font = '11px system-ui, sans-serif';
  ctx.textAlign = 'right';
  ctx.textBaseline = 'middle';
  for (let i = 0; i <= 4; i++) {
    const yy = margin.top + innerH * i / 4;
    ctx.beginPath();
    ctx.moveTo(margin.left, yy);
    ctx.lineTo(margin.left + innerW, yy);
    ctx.stroke();
    const val = Math.round(maxV - (maxV * i / 4));
    ctx.fillText(String(val), margin.left - 6, yy);
  }
  const barColor = colors[snap.strategy_code] || '#8fffd1';
  const barW = innerW / bins.length;
  for (let k = 0; k < bins.length; k++) {
    const h = innerH * (bins[k] / maxV);
    const x = margin.left + k * barW;
    const y = margin.top + innerH - h;
    ctx.fillStyle = barColor;
    ctx.globalAlpha = 0.85;
    ctx.fillRect(x + 2, y, barW - 4, Math.max(1, h));
    ctx.globalAlpha = 1;
  }
  ctx.fillStyle = 'rgba(237,248,245,0.72)';
  ctx.textAlign = 'center';
  ctx.textBaseline = 'top';
  for (let k = 0; k <= bins.length; k++) {
    const tickX = margin.left + k * barW;
    const labelVal = (k / bins.length).toFixed(1);
    ctx.fillText(labelVal, tickX, margin.top + innerH + 6);
  }
  ctx.fillStyle = 'rgba(237,248,245,0.86)';
  ctx.textAlign = 'left';
  ctx.fillText('markers per allele-frequency bin', margin.left, 2);
}

// v0.7.13 — playback timer that pulls one snapshot at a time from
// pendingFrames and draws it. Independent of the polling loop, so it
// continues drawing the queued tail after the simulation completes.
function startHistogramPlayback() {
  if (playbackTimer) return; // already running
  playNextHistogramFrame();
}
function playNextHistogramFrame() {
  playbackTimer = null;
  if (pendingFrames.length === 0) return;
  const snap = pendingFrames.shift();
  if (snap) {
    const incomingGen = Number(snap.generation);
    if (incomingGen !== lastDrawnGeneration) {
      lastSnapshot = snap;
      lastDrawnGeneration = incomingGen;
      drawLiveHistogram(snap);
    }
  }
  if (pendingFrames.length > 0) {
    playbackTimer = setTimeout(playNextHistogramFrame, PLAYBACK_FRAME_MS);
  }
}

// resetLiveHistogram clears the canvas and resets the label so consecutive runs
// don't briefly show stale data. v0.7.6 live histogram.
function resetLiveHistogram() {
  const canvas = byId('chart_histogram');
  if (canvas) {
    const ctx = canvas.getContext('2d');
    ctx.clearRect(0, 0, canvas.width, canvas.height);
  }
  const label = byId('histogramLabel');
  if (label) label.textContent = 'Waiting for first generation snapshot…';
}

function drawMetricChart(canvasId, legendId, data, prev, metric, title) {
  const canvas = byId(canvasId);
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const width = canvas.width;
  const height = canvas.height;
  ctx.clearRect(0, 0, width, height);

  const strategies = data.strategies || [];
  const prevStrategies = prev ? (prev.strategies || []) : [];
  const margin = {left: 58, right: 18, top: 18, bottom: 42};
  const innerW = width - margin.left - margin.right;
  const innerH = height - margin.top - margin.bottom;

  let all = [];
  let maxGen = 0;
  const xOf = chartXAccessor(data);
  function collect(series) {
    for (const s of series) {
      for (const p of s.metrics || []) {
        const val = Number(p[metric]);
        if (Number.isFinite(val)) all.push(val);
        maxGen = Math.max(maxGen, xOf(p));
      }
    }
  }
  collect(strategies);
  collect(prevStrategies);
  if (!all.length) return;
  let minY = Math.min(...all);
  let maxY = Math.max(...all);
  if (Math.abs(maxY - minY) < 1e-9) {
    minY -= 1;
    maxY += 1;
  }
  const pad = (maxY - minY) * 0.08;
  minY -= pad;
  maxY += pad;

  function x(gen) {
    return margin.left + (Number(gen) / Math.max(1, maxGen)) * innerW;
  }
  function y(v) {
    return margin.top + (1 - (Number(v) - minY) / (maxY - minY)) * innerH;
  }

  ctx.fillStyle = 'rgba(255,255,255,0.025)';
  ctx.fillRect(margin.left, margin.top, innerW, innerH);

  ctx.strokeStyle = 'rgba(255,255,255,0.10)';
  ctx.lineWidth = 1;
  ctx.fillStyle = 'rgba(237,248,245,0.72)';
  ctx.font = '12px system-ui, sans-serif';
  ctx.textAlign = 'right';
  ctx.textBaseline = 'middle';
  ctx.setLineDash([]);
  for (let i = 0; i <= 4; i++) {
    const yy = margin.top + innerH * i / 4;
    ctx.beginPath();
    ctx.moveTo(margin.left, yy);
    ctx.lineTo(margin.left + innerW, yy);
    ctx.stroke();
    const val = maxY - (maxY - minY) * i / 4;
    ctx.fillText(fmt(val), margin.left - 8, yy);
  }
  ctx.textAlign = 'center';
  ctx.textBaseline = 'top';
  for (let i = 0; i <= 5; i++) {
    const gen = Math.round(maxGen * i / 5);
    const xx = x(gen);
    ctx.fillText(String(gen), xx, margin.top + innerH + 12);
  }
  ctx.fillStyle = 'rgba(237,248,245,0.86)';
  ctx.textAlign = 'left';
  ctx.fillText(title, margin.left, 2);
  ctx.textAlign = 'center';
  ctx.fillText(xOf.label, margin.left + innerW / 2, height - 18);

  drawSeries(ctx, prevStrategies, metric, x, y, true, xOf);
  drawSeries(ctx, strategies, metric, x, y, false, xOf);

  const currentLegend = strategies.map(s => `
    <span><i class="dot" style="background:${colors[s.code] || '#fff'}"></i>${escapeHtml(labels[s.code] || s.name)}</span>
  `).join('');
  const previousLegend = prevStrategies.length ? '<span><i class="dash-sample"></i>previous run</span>' : '';
  byId(legendId).innerHTML = currentLegend + previousLegend;
}

// v0.7.20 — Issue 20. Effective population size chart on a log scale, with
// FAO reference lines at Ne=100 (vulnerable, yellow) and Ne=50 (long-term-
// viability, red). The backend already computes Ne per generation via
// populateNeTrajectory; this is a render-only function.
function drawNeChart(canvasId, legendId, data, prev) {
  const canvas = byId(canvasId);
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const width = canvas.width;
  const height = canvas.height;
  ctx.clearRect(0, 0, width, height);

  const strategies = data.strategies || [];
  const prevStrategies = prev ? (prev.strategies || []) : [];
  const margin = {left: 64, right: 18, top: 18, bottom: 42};
  const innerW = width - margin.left - margin.right;
  const innerH = height - margin.top - margin.bottom;

  // Fixed log10 range: Ne in [10, 10000]. Keeps the chart comparable
  // across runs and makes the reference lines land at stable positions.
  const logMin = 1; // log10(10)
  const logMax = 4; // log10(10000)
  let maxGen = 0;
  const xOf = chartXAccessor(data);
  function scanGen(series) {
    for (const s of series) {
      for (const p of s.metrics || []) {
        maxGen = Math.max(maxGen, xOf(p));
      }
    }
  }
  scanGen(strategies);
  scanGen(prevStrategies);
  if (maxGen === 0) return;

  function x(gen) {
    return margin.left + (Number(gen) / maxGen) * innerW;
  }
  function y(ne) {
    let v = Number(ne);
    if (!Number.isFinite(v) || v <= 0) v = 10;
    if (v < 10) v = 10;
    if (v > 10000) v = 10000;
    const lg = Math.log10(v);
    return margin.top + (1 - (lg - logMin) / (logMax - logMin)) * innerH;
  }

  // Chart background.
  ctx.fillStyle = 'rgba(255,255,255,0.025)';
  ctx.fillRect(margin.left, margin.top, innerW, innerH);

  // Gridlines at each integer log10 decade (10, 100, 1000, 10000).
  ctx.strokeStyle = 'rgba(255,255,255,0.10)';
  ctx.lineWidth = 1;
  ctx.fillStyle = 'rgba(237,248,245,0.72)';
  ctx.font = '12px system-ui, sans-serif';
  ctx.textAlign = 'right';
  ctx.textBaseline = 'middle';
  ctx.setLineDash([]);
  for (let i = logMin; i <= logMax; i++) {
    const yy = y(Math.pow(10, i));
    ctx.beginPath();
    ctx.moveTo(margin.left, yy);
    ctx.lineTo(margin.left + innerW, yy);
    ctx.stroke();
    ctx.fillText(String(Math.pow(10, i)), margin.left - 6, yy);
  }

  // FAO reference lines (dashed):
  // - Ne = 100 (vulnerable threshold, yellow / warn)
  // - Ne =  50 (long-term-viability threshold, red / danger)
  function refLine(ne, colour, label) {
    ctx.save();
    ctx.strokeStyle = colour;
    ctx.fillStyle = colour;
    ctx.setLineDash([5, 5]);
    ctx.lineWidth = 1.5;
    const yy = y(ne);
    ctx.beginPath();
    ctx.moveTo(margin.left, yy);
    ctx.lineTo(margin.left + innerW, yy);
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.textAlign = 'left';
    ctx.font = '11px system-ui, sans-serif';
    ctx.fillText(label, margin.left + 8, yy - 6);
    ctx.restore();
  }
  refLine(100, '#ffd38c', 'Ne = 100 (FAO vulnerable)');
  refLine(50,  '#ff9a9a', 'Ne = 50 (long-term viability)');

  // X-axis labels (generation ticks every ~5 generations).
  ctx.fillStyle = 'rgba(237,248,245,0.72)';
  ctx.textAlign = 'center';
  ctx.textBaseline = 'top';
  const tickStep = Math.max(1, Math.ceil(maxGen / 8));
  for (let g = 0; g <= maxGen; g += tickStep) {
    ctx.fillText(String(g), x(g), margin.top + innerH + 4);
  }

  // Series: previous run (dashed) then current (solid).
  drawSeries(ctx, prevStrategies, 'ne', x, y, true, xOf);
  drawSeries(ctx, strategies,     'ne', x, y, false, xOf);

  // Legend.
  const currentLegend = strategies.map(s => `
    <span><i class="dot" style="background:${colors[s.code] || '#fff'}"></i>${escapeHtml(labels[s.code] || s.name)}</span>
  `).join('');
  const previousLegend = prevStrategies.length ? '<span><i class="dash-sample"></i>previous run</span>' : '';
  byId(legendId).innerHTML = currentLegend + previousLegend;
}

// v0.8.4 — Issue 35. Charts plot by calendar year when the run has a time
// axis (metrics carry `year`), by generation otherwise.
function chartXAccessor(data) {
  const yearsOn = Number((data && data.request && data.request.cycle_years) || 0) > 0;
  const fn = yearsOn ? p => Number(p.year || 0) : p => Number(p.generation);
  fn.label = yearsOn ? 'year' : 'generation';
  return fn;
}

function drawSeries(ctx, strategies, metric, x, y, dashed, xOf) {
  xOf = xOf || (p => Number(p.generation));
  ctx.save();
  ctx.globalAlpha = dashed ? 0.38 : 1;
  ctx.setLineDash(dashed ? [7, 7] : []);
  for (const s of strategies) {
    const pts = s.metrics || [];
    if (!pts.length) continue;
    ctx.strokeStyle = colors[s.code] || '#ffffff';
    ctx.lineWidth = dashed ? 2 : 3;
    ctx.beginPath();
    pts.forEach((p, idx) => {
      const xx = x(xOf(p));
      const yy = y(p[metric]);
      if (idx === 0) ctx.moveTo(xx, yy); else ctx.lineTo(xx, yy);
    });
    ctx.stroke();
    if (!dashed) {
      const last = pts[pts.length - 1];
      ctx.fillStyle = colors[s.code] || '#ffffff';
      ctx.beginPath();
      ctx.arc(x(xOf(last)), y(last[metric]), 4, 0, Math.PI * 2);
      ctx.fill();
    }
  }
  ctx.restore();
}

function exportJSON() {
  if (!currentData) return;
  downloadBlob(JSON.stringify(currentData, null, 2), 'breedos-run.json', 'application/json');
}

async function copySummary() {
  if (!currentData) return;
  const serverText = currentData && currentData.decision && currentData.decision.summary_text;
  const lines = serverText && serverText.length > 0 ? serverText : buildSummaryText(currentData, previousData);
  try {
    await navigator.clipboard.writeText(lines);
    setStatus('Run summary copied to clipboard.', 'ok');
  } catch (err) {
    setStatus('Clipboard copy failed: ' + (err.message || String(err)), 'error');
  }
}

function buildSummaryText(data, prev) {
  const req = data.request || {};
  const lines = [
    'BreedOS simulation summary',
    `Population=${req.population_size}, markers=${req.markers}, QTL=${req.qtl_count}, generations=${req.generations}, seed=${req.seed}, mutation_rate=${formatParamValue(req.mutation_rate)}, expected_flips_per_strategy=${fmt(expectedMutationFlips(req))}`,
    ''
  ];
  for (const s of data.strategies || []) {
    const p = findStrategy(prev, s.code);
    const delta = p ? `, delta_gain=${signedDelta(s.final.genetic_gain - p.final.genetic_gain)}` : '';
    lines.push(`${labels[s.code] || s.name}: gain=${fmt(s.final.genetic_gain)}, diversity=${fmt(s.final.diversity)}, inbreeding=${fmt(s.final.inbreeding)}, fixed_loci=${s.final.fixed_loci}${delta}`);
  }
  return lines.join('\n');
}

function downloadBlob(text, filename, type) {
  const blob = new Blob([text], {type});
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}


function requestSignature(req) {
  if (!req) return '';
  return JSON.stringify({
    dataset: String(req.dataset || 'synthetic'),
    seed: Number(req.seed) || 0,
    population_size: Number(req.population_size) || 0,
    markers: Number(req.markers) || 0,
    qtl_count: Number(req.qtl_count) || 0,
    generations: Number(req.generations) || 0,
    cycle_years: Number(req.cycle_years) || 0,
    genomic_cycle_years: Number(req.genomic_cycle_years) || 0,
    selection_percent: Number(req.selection_percent) || 0,
    heritability: Number(req.heritability) || 0,
    mutation_rate: Number(req.mutation_rate) || 0,
    crispr_enabled: Boolean(req.crispr_enabled),
    crispr_edits: Number(req.crispr_edits) || 0,
    crispr_intro_percent: Number(req.crispr_intro_percent) || 0,
    strategy_set: String(req.strategy_set || 'core'),
    tracked_strategy: String(req.tracked_strategy || ''),
    replicates: Number(req.replicates) || 0,
    worker_count: Number(req.worker_count) || 0,
    inbreeding_limit: Number(req.inbreeding_limit) || 0,
    diversity_loss_limit: Number(req.diversity_loss_limit) || 0,
    target_delta_f: Number(req.target_delta_f) || 0,
    max_inbreeding: Number(req.max_inbreeding) || 0,
    max_diversity_loss: Number(req.max_diversity_loss) || 0,
    max_rare_useful_loss: Number(req.max_rare_useful_loss) || 0,
    min_genetic_gain: Number(req.min_genetic_gain) || 0,
    min_effective_parents: Number(req.min_effective_parents) || 0,
    max_combined_risk: Number(req.max_combined_risk) || 0
  });
}

function changedParams(prevReq, curReq) {
  const fields = [
    ['dataset', 'dataset'],
    ['seed', 'seed'],
    ['population_size', 'N'],
    ['markers', 'markers'],
    ['qtl_count', 'QTL'],
    ['generations', 'generations'],
    ['cycle_years', 'cycle years'],
    ['genomic_cycle_years', 'genomic cycle years'],
    ['selection_percent', 'selection %'],
    ['heritability', 'heritability'],
    ['mutation_rate', 'mutation rate'],
    ['crispr_enabled', 'CRISPR enabled'],
    ['crispr_edits', 'CRISPR edits'],
    ['crispr_intro_percent', 'edit intro %'],
    ['strategy_set', 'strategy set'],
    ['tracked_strategy', 'tracked strategy'],
    ['replicates', 'replicates'],
    ['worker_count', 'worker count'],
    ['inbreeding_limit', 'inbreeding limit'],
    ['target_delta_f', 'OCS target ΔF'],
    ['diversity_loss_limit', 'diversity loss limit'],
    ['max_inbreeding', 'max inbreeding'],
    ['max_diversity_loss', 'max diversity loss'],
    ['max_rare_useful_loss', 'max rare-useful loss'],
    ['min_genetic_gain', 'min genetic gain'],
    ['min_effective_parents', 'min effective parents'],
    ['max_combined_risk', 'max combined risk']
  ];
  const out = [];
  for (const [key, label] of fields) {
    const a = prevReq ? prevReq[key] : undefined;
    const b = curReq ? curReq[key] : undefined;
    if (String(a) !== String(b)) out.push(`${label}: ${formatParamValue(a)} → ${formatParamValue(b)}`);
  }
  return out;
}

function combinedRisk(final) {
  if (!final) return 0;
  return 0.45 * (Number(final.probability_inbreeding_breach) || 0) +
         0.35 * (Number(final.probability_diversity_collapse) || 0) +
         0.20 * (Number(final.probability_rare_useful_loss) || 0);
}

function expectedMutationFlips(req) {
  return (Number(req.population_size) || 0) * (Number(req.markers) || 0) * 2 * (Number(req.generations) || 0) * (Number(req.mutation_rate) || 0);
}

// Mirrors buildStrategyConfigs() in main.go. Keep in sync if strategies change.
function strategyCountForRequest(req) {
  const advanced = String(req.strategy_set || 'core') === 'advanced';
  // v0.8.5: advanced adds OCS with a ΔF target (single-trait only).
  const multi = !!(multiTraitState.traits && multiTraitState.traits.length);
  const base = advanced ? (multi ? 9 : 10) : 4;
  const crisprActive = Boolean(req.crispr_enabled) && (Number(req.crispr_edits) || 0) > 0;
  if (!crisprActive) return base;
  return base + (advanced ? 2 : 1);
}

function runBudgetCells(req) {
  const n = Number(req.population_size) || 0;
  const m = Number(req.markers) || 0;
  // v0.8.4 — mirrors maxGenerations(): a shorter genomic cycle runs more
  // generations over the same calendar horizon.
  const gens = Number(req.generations) || 0;
  const cyc = Number(req.cycle_years) || 0;
  const gcyc = Number(req.genomic_cycle_years) || 0;
  const multi = !!(multiTraitState.traits && multiTraitState.traits.length);
  const g = (cyc > 0 && gcyc > 0 && gcyc < cyc && !multi ? Math.max(1, Math.round(gens * cyc / gcyc)) : gens) + 1;
  const s = strategyCountForRequest(req);
  const r = Math.max(1, Number(req.replicates) || 0);
  return n * m * g * s * r;
}

function formatCells(n) {
  if (!Number.isFinite(n) || n <= 0) return '0';
  if (n >= 1e9) return (n / 1e9).toFixed(n >= 1e10 ? 1 : 2) + 'B';
  if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e8 ? 0 : 1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(0) + 'k';
  return String(Math.round(n));
}

// Returns { over: bool } so callers (runSimulation) can pre-flight the request.
function updateBudgetMeter() {
  const el = byId('budgetMeter');
  if (!el) return { over: false };
  const req = requestFromForm();
  const budget = runBudgetCells(req);
  const strategies = strategyCountForRequest(req);
  const over = budget > BUDGET_CAP;
  const warn = !over && budget > BUDGET_CAP * BUDGET_WARN_FRAC;
  el.classList.remove('warn', 'over');
  if (over) el.classList.add('over');
  else if (warn) el.classList.add('warn');
  const formula = `${req.population_size || 0} N × ${req.markers || 0} markers × ${(Number(req.generations) || 0) + 1} gens × ${strategies} strategies × ${Math.max(1, Number(req.replicates) || 0)} reps`;
  let hint;
  if (over) {
    hint = `Over the ${formatCells(BUDGET_CAP)} cap — Run is disabled. Reduce population, markers, generations, or replicates.`;
  } else if (warn) {
    hint = `Near the ${formatCells(BUDGET_CAP)} cap — close to MVP limit.`;
  } else {
    hint = `MVP cap is ${formatCells(BUDGET_CAP)} cells per run.`;
  }
  el.innerHTML = `Run budget: <strong>${formatCells(budget)} cells</strong> <small>${formula}</small><small>${hint}</small>`;
  const btn = byId('runBtn');
  if (btn) {
    if (over) btn.setAttribute('disabled', 'disabled');
    else btn.removeAttribute('disabled');
  }
  // Highlight every input that multiplies into the budget, so the user can
  // see at a glance which knobs to turn down. We don't single one out — they
  // all contribute multiplicatively, any of them works.
  for (const id of BUDGET_INPUT_IDS) {
    const input = byId(id);
    if (!input) continue;
    if (over) input.classList.add('over-budget');
    else input.classList.remove('over-budget');
  }
  return { over };
}

function formatParamValue(v) {
  if (v === null || v === undefined) return '-';
  if (typeof v === 'boolean') return v ? 'yes' : 'no';
  const n = Number(v);
  if (!Number.isFinite(n)) return String(v);
  if (Math.abs(n) > 0 && Math.abs(n) < 0.0001) return n.toExponential(3);
  if (Math.abs(n) < 1) return n.toFixed(8).replace(/0+$/,'').replace(/\.$/,'');
  return fmt(n);
}

function markDirty(reason) {
  updateBudgetMeter();
  const req = requestFromForm();
  const currentSig = currentData ? requestSignature(currentData.request || {}) : '';
  const nextSig = requestSignature(req);
  if (currentData && nextSig === currentSig) {
    setStatus('Parameters match the current run.', 'ok');
    return;
  }
  const prefix = reason || 'Parameters changed';
  setStatus(`${prefix}. Press Run simulation to update graphs.`, 'dirty');
}

function findStrategy(data, code) {
  if (!data || !code) return null;
  return (data.strategies || []).find(x => x.code === code) || null;
}

function maxBy(arr, fn) {
  let best = null;
  let bestVal = -Infinity;
  for (const x of arr || []) {
    const v = fn(x);
    if (v > bestVal) { best = x; bestVal = v; }
  }
  return best;
}

function minBy(arr, fn) {
  let best = null;
  let bestVal = Infinity;
  for (const x of arr || []) {
    const v = fn(x);
    if (v < bestVal) { best = x; bestVal = v; }
  }
  return best;
}

function fmt(v) {
  if (v === null || v === undefined || Number.isNaN(Number(v))) return '-';
  const n = Number(v);
  if (Math.abs(n) >= 100) return n.toFixed(1);
  if (Math.abs(n) >= 10) return n.toFixed(2);
  return n.toFixed(4).replace(/0+$/,'').replace(/\.$/,'');
}

function signedDelta(v) {
  if (v === null || v === undefined || Number.isNaN(Number(v))) return '-';
  const n = Number(v);
  const sign = n > 0 ? '+' : '';
  return sign + fmt(n);
}

function formatInt(v) {
  const n = Number(v);
  if (!Number.isFinite(n)) return '-';
  return Math.round(n).toLocaleString('en-US');
}

function escapeHtml(s) {
  return String(s)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}

// v0.7.16 — sensitivity sweep client. Mirrors the API shape exposed by
// sensitivity.go. Defaults per axis are chosen to bracket common breeder
// questions ("what if h² is lower than assumed?", "what if we cut selection
// intensity?", "what if we only have 10 generations?"). 5 values max because
// the server caps at sensitivityMaxValues and the table stays readable.
const SENS_DEFAULT_VALUES = {
  heritability:      '0.2, 0.35, 0.5, 0.65, 0.8',
  selection_percent: '5, 10, 15, 20, 30',
  generations:       '10, 20, 30, 50, 80',
  genomic_cycle_years: '1, 1.5, 2, 3, 4',
  target_delta_f:    '0.005, 0.01, 0.015, 0.02, 0.03'
};
// v0.7.23 — Issue B. Default value sets for the dynamic trait_weight
// axes. The picker auto-fills these when the operator switches the axis.
const SENS_TRAIT_WEIGHT_DEFAULTS = '-1.0, -0.5, 0.0, 0.5, 1.0';
const SENS_AXIS_LABEL = {
  heritability:      'h²',
  selection_percent: 'sel %',
  generations:       'gens',
  genomic_cycle_years: 'GS cycle',
  target_delta_f:    'OCS ΔF'
};
const SENS_AXIS_FORMAT = {
  heritability:      v => v.toFixed(2),
  selection_percent: v => v.toFixed(0) + '%',
  generations:       v => String(Math.round(v)),
  genomic_cycle_years: v => v + ' yr',
  target_delta_f:    v => (100 * v).toFixed(1) + '%'
};
let sensRunInFlight = false;

// v0.7.23 — Issue B. Refresh axis dropdown options based on the current
// multiTraitState. Static axes always present; trait_weight:<name> options
// appear when multi-trait is active. Preserves the user's current
// selection if still valid.
function refreshSensAxisOptions() {
  const sel = byId('sensAxis');
  if (!sel) return;
  const prev = sel.value;
  const opts = [
    {key: 'heritability',      label: 'Heritability (h²)'},
    {key: 'selection_percent', label: 'Selection intensity (%)'},
    {key: 'generations',       label: 'Generations horizon'},
    // v0.8.4/0.8.5 — Issues 35/36. GS cycle needs the time axis; OCS ΔF
    // needs the advanced strategy set.
    {key: 'genomic_cycle_years', label: 'Genomic cycle, years (time axis)'},
    {key: 'target_delta_f',    label: 'OCS target ΔF per generation'},
    // v0.7.31 — Issue 29. Structured climate-scenario axis.
    {key: 'climate_scenario',  label: 'Climate scenario (mode × severity)'}
  ];
  const traits = multiTraitState.traits || [];
  traits.forEach(t => {
    opts.push({key: 'trait_weight:' + t.name, label: 'Selection weight: ' + t.name});
  });
  sel.innerHTML = opts.map(o => `<option value="${escapeHtml(o.key)}">${escapeHtml(o.label)}</option>`).join('');
  if (opts.find(o => o.key === prev)) sel.value = prev;
  // Update SENS_DEFAULT_VALUES / SENS_AXIS_LABEL / SENS_AXIS_FORMAT for the
  // dynamic axes so the rest of the sweep code works unchanged.
  traits.forEach(t => {
    const key = 'trait_weight:' + t.name;
    SENS_DEFAULT_VALUES[key] = SENS_TRAIT_WEIGHT_DEFAULTS;
    SENS_AXIS_LABEL[key] = 'w(' + t.name + ')';
    SENS_AXIS_FORMAT[key] = v => v.toFixed(2);
  });
}

function parseSensValues(text) {
  return String(text || '')
    .split(/[,\s]+/)
    .map(s => s.trim().replace(',', '.'))
    .filter(s => s.length > 0)
    .map(s => Number(s))
    .filter(v => Number.isFinite(v));
}

// v0.7.31 — Issue 29. Toggle between the comma-separated numeric values
// input and the structured climate-scenario picker, based on axis.
function syncSensValuesUI(axis) {
  const numericLabel = byId('sensValuesLabel');
  const climateBlock = byId('sensClimateValues');
  const isClimate = axis === 'climate_scenario';
  if (numericLabel) numericLabel.hidden = isClimate;
  if (climateBlock) climateBlock.hidden = !isClimate;
}

// v0.7.31 — Issue 29. Read the structured climate rows. Returns a list
// of {mode, severity} for rows where mode is non-empty.
function gatherSensClimateValues() {
  const modes = document.querySelectorAll('#sensClimateValues .sens-climate-mode');
  const sevs = document.querySelectorAll('#sensClimateValues .sens-climate-sev');
  const out = [];
  for (let i = 0; i < modes.length; i++) {
    const mode = String(modes[i].value || '').trim();
    if (!mode) continue;
    let sev = Number(sevs[i] ? sevs[i].value : 0);
    if (!Number.isFinite(sev)) sev = 0;
    if (sev < 0) sev = 0;
    if (sev > 1) sev = 1;
    out.push({mode, severity: sev});
  }
  return out;
}

function sensitivityCostCells(req, values) {
  const single = runBudgetCells(req);
  return single * Math.max(1, values.length);
}

function updateSensBudgetMeter() {
  const el = byId('sensBudget');
  if (!el) return { over: false };
  const axis = byId('sensAxis') ? byId('sensAxis').value : 'heritability';
  syncSensValuesUI(axis);
  // v0.7.31 — Issue 29. Cardinality comes from either the numeric values
  // input or the structured climate-scenario rows, depending on axis.
  let values;
  let climateValues = null;
  if (axis === 'climate_scenario') {
    climateValues = gatherSensClimateValues();
    values = new Array(climateValues.length); // budget meter only cares about length.
  } else {
    values = parseSensValues(byId('sensValues') ? byId('sensValues').value : '');
  }
  const req = requestFromForm();
  const sweep = sensitivityCostCells(req, values);
  const over = sweep > BUDGET_CAP;
  const warn = !over && sweep > BUDGET_CAP * BUDGET_WARN_FRAC;
  el.classList.remove('warn', 'over');
  if (over) el.classList.add('over');
  else if (warn) el.classList.add('warn');
  const single = runBudgetCells(req);
  const formula = `${values.length || 0} scenarios × ${formatCells(single)} cells per run`;
  let hint;
  if (values.length === 0) {
    hint = 'Enter 1–5 axis values.';
  } else if (values.length > 5) {
    hint = `Only 5 values per sweep are allowed (got ${values.length}).`;
  } else if (over) {
    hint = `Over the ${formatCells(BUDGET_CAP)} cap — reduce number of values, or shrink base run.`;
  } else if (warn) {
    hint = `Near the ${formatCells(BUDGET_CAP)} cap.`;
  } else {
    hint = `Sum stays within the ${formatCells(BUDGET_CAP)} cap.`;
  }
  el.innerHTML = `Sweep budget: <strong>${formatCells(sweep)} cells</strong> <small>${formula}</small><small>${hint}</small>`;
  const btn = byId('sensRunBtn');
  if (btn) {
    if (over || values.length === 0 || values.length > 5 || sensRunInFlight) btn.setAttribute('disabled', 'disabled');
    else btn.removeAttribute('disabled');
  }
  return { over, values, climateValues };
}

function setSensStatus(text, cls) {
  const el = byId('sensStatus');
  if (!el) return;
  el.textContent = text || '';
  el.className = 'status ' + (cls || '');
}

async function runSensitivitySweep() {
  if (sensRunInFlight) return;
  const axis = byId('sensAxis').value;
  const meter = updateSensBudgetMeter();
  if (meter.over) {
    setSensStatus(`Sweep budget exceeds the ${formatCells(BUDGET_CAP)} cap. Reduce number of values or shrink base run.`, 'error');
    return;
  }
  if (!meter.values || meter.values.length === 0) {
    setSensStatus('Enter at least one axis value (comma-separated).', 'error');
    return;
  }
  sensRunInFlight = true;
  const btn = byId('sensRunBtn');
  const originalLabel = btn ? btn.textContent : '';
  if (btn) { btn.disabled = true; btn.textContent = 'Starting...'; }
  // Hide previous result so the user doesn't conflate runs.
  byId('sensVerdict').hidden = true;
  byId('sensTableWrap').hidden = true;
  setSensStatus('Starting sweep...', '');
  try {
    // v0.7.31 — Issue 29. Send climate_values + axis=climate_scenario
    // when picked; otherwise the legacy numeric path.
    const body = {base: requestFromForm(), axis: axis};
    if (axis === 'climate_scenario') {
      body.climate_values = meter.climateValues || [];
    } else {
      body.values = meter.values;
    }
    const startRes = await fetch('/api/sensitivity/start', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body)
    });
    if (!startRes.ok) {
      const text = await startRes.text();
      throw new Error(text || `HTTP ${startRes.status}`);
    }
    const start = await startRes.json();
    let job = null;
    for (;;) {
      const res = await fetch('/api/sensitivity/status?id=' + encodeURIComponent(start.job_id), {cache: 'no-store'});
      if (!res.ok) { throw new Error(await res.text() || `HTTP ${res.status}`); }
      job = await res.json();
      // v0.7.17 — backend returns float percent. Show one decimal while
      // mid-run so the user sees the bar moving on slow prod (single core),
      // round to integer on completion to avoid 99.9 stutter.
      const pct = Number(job.percent) || 0;
      const pctStr = job.done || pct >= 99.95 ? Math.round(pct).toString() : pct.toFixed(1);
      setSensStatus(`${job.message || 'Running'} — ${pctStr}%`, '');
      if (btn) btn.textContent = `Sweep ${pctStr}%`;
      if (job.done) break;
      // 150ms ≈ 6 polls/sec. Backend cost is a single map lookup; this is
      // dominated by inner-progress tick rate (~30-100 Hz during a scenario)
      // not by polling cadence.
      await sleep(150);
    }
    if (job.error) throw new Error(job.error);
    if (!job.result) throw new Error('Sweep finished without a result');
    renderSensResult(axis, job.result);
    setSensStatus('Sweep complete.', 'ok');
  } catch (err) {
    console.error(err);
    setSensStatus(err.message || String(err), 'error');
  } finally {
    sensRunInFlight = false;
    if (btn) { btn.disabled = false; btn.textContent = originalLabel || 'Run sweep'; }
    updateSensBudgetMeter();
  }
}

function renderSensResult(axis, result) {
  const verdictEl = byId('sensVerdict');
  const wrap = byId('sensTableWrap');
  const body = byId('sensTable');
  const summary = result.summary || {};
  const verdict = summary.verdict || 'inconclusive';
  verdictEl.classList.remove('stable', 'fragile', 'inconclusive');
  verdictEl.classList.add(verdict);
  const verdictLabel = verdict.toUpperCase();
  const notes = Array.isArray(summary.notes) ? summary.notes.join(' ') : '';
  verdictEl.innerHTML = `<strong>${verdictLabel}</strong> — ${escapeHtml(notes)}`;
  verdictEl.hidden = false;
  // v0.7.31 — Issue 31. Climate-robustness Decision Report section.
  renderClimateRobustnessSection(result.climate_robustness);
  const fmt = SENS_AXIS_FORMAT[axis] || (v => String(v));
  const rows = (result.scenarios || []).map(s => {
    const matchCell = s.best_feasible_code === ''
      ? '<span class="sens-match-no">infeasible</span>'
      : (s.baseline_match
          ? '<span class="sens-match-yes">yes</span>'
          : `<span class="sens-match-no">no (→ ${escapeHtml(s.best_feasible_code)})</span>`);
    // v0.7.31 — Issue 29. Prefer the server-supplied human label
    // when present (climate_scenario axis); fall back to numeric.
    const axisCell = s.axis_label ? escapeHtml(s.axis_label) : fmt(Number(s.axis_value));
    return '<tr>'
      + `<td>${axisCell}</td>`
      + `<td>${escapeHtml(s.best_feasible_name || s.best_feasible_code || '—')}</td>`
      + `<td>${signedDelta(s.genetic_gain)}</td>`
      + `<td>${Number(s.diversity).toFixed(3)}</td>`
      + `<td>${Number(s.inbreeding).toFixed(3)}</td>`
      + `<td>${Number(s.combined_risk).toFixed(3)}</td>`
      + `<td>${matchCell}</td>`
      + '</tr>';
  }).join('');
  body.innerHTML = rows;
  wrap.hidden = false;
}

window.addEventListener('DOMContentLoaded', () => {
  const runBtn = byId('runBtn');
  if (!runBtn) return;
  runBtn.addEventListener('click', runSimulation);
  byId('clearCompareBtn').addEventListener('click', clearComparison);
  const resetBtn = byId('resetFormBtn');
  if (resetBtn) resetBtn.addEventListener('click', resetFormToDefaults);
  byId('randomSeedBtn').addEventListener('click', randomizeSeed);
  byId('exportJsonBtn').addEventListener('click', exportJSON);
  byId('copySummaryBtn').addEventListener('click', copySummary);
  // v0.7.31 — Issue 10. Bottom-of-page duplicate buttons + jump-to-
  // report shortcut. Same handlers as the left-panel originals;
  // duplicated so the operator who scrolled to the report doesn't
  // need to scroll back up to export.
  const exportBtn2 = byId('exportJsonBtn2');
  if (exportBtn2) exportBtn2.addEventListener('click', exportJSON);
  const copyBtn2 = byId('copySummaryBtn2');
  if (copyBtn2) copyBtn2.addEventListener('click', copySummary);
  const jumpBtn = byId('jumpToReportBtn');
  if (jumpBtn) jumpBtn.addEventListener('click', () => {
    const target = byId('step-report');
    if (target) target.scrollIntoView({behavior: 'smooth', block: 'start'});
  });
  document.querySelectorAll('[data-preset]').forEach(btn => {
    btn.addEventListener('click', () => applyPreset(btn.getAttribute('data-preset')));
  });
  document.querySelectorAll('input, select').forEach(input => {
    input.addEventListener('change', () => markDirty(`${input.id} changed`));
    // 'input' fires while typing — keeps the budget meter reactive without
    // waiting for blur. markDirty itself stays on 'change' so the status text
    // doesn't churn on every keystroke.
    input.addEventListener('input', () => {
      updateBudgetMeter();
      updateSensBudgetMeter();
    });
  });
  updateBudgetMeter();
  // v0.7.19 — Issue 32. Show / hide the endogenous-gene-interrupted field
  // based on variant_type so the Path (ii) question only appears when relevant.
  const vt = byId('ngt_variant_type');
  if (vt) {
    vt.addEventListener('change', updateNGTEndogenousFieldVisibility);
    updateNGTEndogenousFieldVisibility();
  }
  // v0.7.22 — Issue 23. Pareto axis pickers re-render on change. Re-uses
  // the last cached run state so the chart updates without a full re-run.
  const px = byId('paretoAxisX');
  const py = byId('paretoAxisY');
  const redrawPareto = () => {
    if (currentData) drawParetoChart('chart_pareto', currentData);
  };
  if (px) px.addEventListener('change', redrawPareto);
  if (py) py.addEventListener('change', redrawPareto);
  // v0.7.16 sensitivity panel.
  const sensAxis = byId('sensAxis');
  const sensValues = byId('sensValues');
  const sensRunBtn = byId('sensRunBtn');
  if (sensAxis && sensValues && sensRunBtn) {
    sensAxis.addEventListener('change', () => {
      // v0.7.31 — Issue 29. Don't overwrite the numeric values input
      // when switching to the climate axis; just toggle UI visibility.
      if (sensAxis.value !== 'climate_scenario') {
        sensValues.value = SENS_DEFAULT_VALUES[sensAxis.value] || '';
      }
      updateSensBudgetMeter();
    });
    // v0.7.31 — Issue 29. The climate rows live outside `input, select`
    // listeners attached at init because they're inside #sensClimateValues
    // and don't carry stable ids; wire them explicitly.
    document.querySelectorAll('#sensClimateValues .sens-climate-mode, #sensClimateValues .sens-climate-sev').forEach(el => {
      el.addEventListener('change', updateSensBudgetMeter);
      el.addEventListener('input', updateSensBudgetMeter);
    });
    sensValues.addEventListener('input', updateSensBudgetMeter);
    sensRunBtn.addEventListener('click', runSensitivitySweep);
    updateSensBudgetMeter();
  }
  // v0.7.31 — Issue 05. Upload UI: show/hide the upload card, handle the
  // multipart POST, render the import summary.
  setupUploadUI();
  setStatus('Ready. Press Run simulation to calculate.', '');
});

function setupUploadUI() {
  const datasetSel = byId('dataset');
  const uploadField = byId('uploadField');
  const uploadBtn = byId('uploadBtn');
  if (!datasetSel || !uploadField || !uploadBtn) return;

  const syncVisibility = () => {
    const useUpload = datasetSel.value === '__upload__';
    uploadField.hidden = !useUpload;
    if (!useUpload) {
      uploadState = {id: null, summary: null};
      const status = byId('uploadStatus');
      if (status) {
        status.textContent = 'no upload yet';
        status.style.color = '';
      }
      const sum = byId('uploadSummary');
      if (sum) { sum.hidden = true; sum.innerHTML = ''; }
    }
  };
  datasetSel.addEventListener('change', syncVisibility);
  syncVisibility();

  uploadBtn.addEventListener('click', async () => {
    const genoEl = byId('uploadGenotype');
    if (!genoEl || !genoEl.files || !genoEl.files[0]) {
      alert('Genotype CSV is required.');
      return;
    }
    const fd = new FormData();
    fd.append('genotype', genoEl.files[0]);
    for (const [field, id] of [['phenotype','uploadPhenotype'],['pedigree','uploadPedigree'],['edits','uploadEdits'],['predictions','uploadPredictions']]) {
      const el = byId(id);
      if (el && el.files && el.files[0]) fd.append(field, el.files[0]);
    }
    const status = byId('uploadStatus');
    // v0.8.6 — Issue 37. Mirror the server's phenotype-id check: the
    // production proxy replaces 400 bodies, so the reason would be lost.
    const phenoEl = byId('uploadPhenotype');
    if (phenoEl && phenoEl.files && phenoEl.files[0]) {
      const firstColumn = text => text.split(/\r?\n/).filter(l => l.trim() && !l.startsWith('#')).slice(1).map(l => l.split(',')[0].trim());
      const genoIds = firstColumn(await genoEl.files[0].text());
      const phenoIds = firstColumn(await phenoEl.files[0].text());
      const known = new Set(genoIds);
      if (phenoIds.length && !phenoIds.some(id => known.has(id))) {
        if (status) {
          status.textContent = `upload failed: none of the ${phenoIds.length} phenotype ids match a genotype id (phenotype starts "${phenoIds[0]}", genotype starts "${genoIds[0] || ''}"). Both id columns must use the same identifiers.`;
          status.style.color = 'var(--danger)';
        }
        return;
      }
    }
    if (status) { status.textContent = 'uploading...'; status.style.color = ''; }
    uploadBtn.disabled = true;
    try {
      const res = await fetch('/api/upload', {method: 'POST', body: fd});
      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || ('HTTP ' + res.status));
      }
      const data = await res.json();
      uploadState = {id: data.upload_id, summary: data.summary};
      renderUploadSummary(data.summary, data.upload_id);
      if (status) { status.textContent = 'upload ready ✓ (' + data.upload_id + ')'; status.style.color = 'var(--ok)'; }
      markDirty('upload changed');
    } catch (e) {
      if (status) { status.textContent = 'upload failed: ' + e.message; status.style.color = 'var(--danger)'; }
      uploadState = {id: null, summary: null};
    } finally {
      uploadBtn.disabled = false;
    }
  });
}

function renderUploadSummary(summary, id) {
  const el = byId('uploadSummary');
  if (!el) return;
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const parts = [];
  if (summary.genotype) {
    parts.push('<div><strong>Genotype</strong>: ' + summary.genotype.individuals + ' individuals × ' + summary.genotype.markers + ' markers (sample ids: ' + summary.genotype.sample_ids.map(esc).join(', ') + ')</div>');
  }
  if (summary.phenotype) {
    parts.push('<div><strong>Phenotype</strong>: ' + summary.phenotype.rows + ' rows, trait <code>' + esc(summary.phenotype.trait_name) + '</code> ∈ [' + summary.phenotype.min.toFixed(3) + ', ' + summary.phenotype.max.toFixed(3) + '], mean ' + summary.phenotype.mean.toFixed(3) + (summary.phenotype.matched_genotype != null ? ', ' + summary.phenotype.matched_genotype + ' matched to genotyped lines' : '') + '</div>');
  }
  if (summary.pedigree) {
    parts.push('<div><strong>Pedigree</strong>: ' + summary.pedigree.rows + ' rows, ' + summary.pedigree.unique_sires + ' unique sires × ' + summary.pedigree.unique_dams + ' unique dams</div>');
  }
  if (summary.edits) {
    parts.push('<div><strong>Edits</strong>: ' + summary.edits.rows + ' candidate edits</div>');
  }
  if (summary.predictions) {
    parts.push('<div><strong>Predictions</strong>: ' + summary.predictions.rows + ' GEBV rows' + (summary.predictions.has_uncertainty ? ' (with uncertainty)' : '') + ', range [' + summary.predictions.min.toFixed(3) + ', ' + summary.predictions.max.toFixed(3) + '], mean ' + summary.predictions.mean.toFixed(3) + '</div>');
  }
  if (summary.used_by_engine && summary.used_by_engine.length) {
    parts.push('<div style="margin-top:6px;"><strong style="color:var(--ok)">Used by engine:</strong> ' + summary.used_by_engine.map(esc).join('; ') + '</div>');
  }
  if (summary.ignored_by_engine && summary.ignored_by_engine.length) {
    parts.push('<div><strong style="color:var(--warn)">Loaded but NOT consumed by engine:</strong><ul style="margin:4px 0 0 18px; padding:0;">' + summary.ignored_by_engine.map(s => '<li>' + esc(s) + '</li>').join('') + '</ul></div>');
  }
  el.innerHTML = parts.join('');
  el.hidden = false;
}
