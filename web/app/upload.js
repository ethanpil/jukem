// Upload manager. It is part of the app shell, so a change of screen does
// not interrupt a batch. Three files go at a time, one request per file, over
// XMLHttpRequest for upload progress.

import * as A from './api.js';
import { h, clear, icon, toast, fmtBytes, problemMessage } from './dom.js';

const PARALLEL = 3;
const ATTEMPTS = 4; // one try and three retries
const CHECK_BATCH = 2000;

const state = {
  items: [],          // {file, rel, size, status, sent, error, attempts, xhr, exists}
  target: '',
  conflict: 'skip',
  running: false,
  paused: false,
  checking: 0,
  startedAt: 0,
  sentBytes: 0,
  totalBytes: 0,
  scanning: false,
  scanTarget: '',
  scanCount: 0,
  free: null,
  wakeLock: null,
};

let ui = null; // {panel, offcanvas, summary, bar, rows, actions}
let frame = 0;

// open shows the panel targeting folder.
export function open(folder) {
  buildPanel();
  if (state.running) {
    if (folder !== state.target) toast(`A batch into ${state.target || 'the music root'} is running. Wait for it to finish.`, 'warning');
  } else {
    if (folder !== state.target) state.items = [];
    state.target = folder || '';
  }
  ui.offcanvas.show();
  render();
}

function buildPanel() {
  if (ui) return;
  const panel = h('div.offcanvas', { tabindex: '-1', id: 'upload-panel' },
    h('div.offcanvas-header', h('h5.offcanvas-title', 'Upload'), h('button.btn-close', { type: 'button', 'data-bs-dismiss': 'offcanvas', 'aria-label': 'Close' })),
    h('div.offcanvas-body'));
  document.body.append(panel);
  // A phone gets the panel from the bottom.
  const mq = window.matchMedia('(max-width: 767.98px)');
  const place = () => { panel.classList.toggle('offcanvas-bottom', mq.matches); panel.classList.toggle('offcanvas-end', !mq.matches); };
  mq.addEventListener('change', place);
  place();
  panel.addEventListener('dragover', (e) => { e.preventDefault(); });
  panel.addEventListener('drop', onDrop);
  // A drop that misses the panel must not open the file in the tab.
  document.addEventListener('dragover', (e) => { e.preventDefault(); });
  document.addEventListener('drop', (e) => { if (!panel.contains(e.target)) e.preventDefault(); });
  ui = { panel, offcanvas: new bootstrap.Offcanvas(panel), summary: h('div.mb-2'), bar: h('div.progress.mb-2', { role: 'progressbar' }, h('div.progress-bar')), rows: h('div.list-group.mb-3'), actions: h('div.d-flex.gap-2') };
}

function render() {
  if (!ui) return;
  const b = clear(ui.panel.querySelector('.offcanvas-body'));
  const target = h('div.mb-3', h('div.small.text-body-secondary', 'Into'), h('div.mono', state.target || '(music root)'));
  const filePick = h('input', { type: 'file', multiple: true, class: 'd-none' });
  const dirPick = h('input', { type: 'file', multiple: true, class: 'd-none' });
  dirPick.setAttribute('webkitdirectory', '');
  filePick.addEventListener('change', () => addFiles([...filePick.files].map((f) => ({ file: f, rel: f.name }))));
  dirPick.addEventListener('change', () => addFiles([...dirPick.files].map((f) => ({ file: f, rel: f.webkitRelativePath || f.name }))));
  const dropZone = h('div.border.rounded.p-3.text-center.text-body-secondary.mb-3', { style: 'border-style: dashed !important' },
    icon('cloud-upload', 'fs-3 d-block'), 'Drop files or folders here',
    h('div.mt-2.d-flex.gap-2.justify-content-center',
      h('button.btn.btn-sm.btn-outline-primary', { type: 'button', onclick: () => filePick.click() }, 'Choose files'),
      h('button.btn.btn-sm.btn-outline-primary.d-none.d-md-inline-block', { type: 'button', onclick: () => dirPick.click() }, 'Choose folder')),
    filePick, dirPick);
  const conflict = h('select.form-select.form-select-sm', { disabled: state.running, onchange: (e) => { state.conflict = e.target.value; renderProgress(); } },
    h('option', { value: 'skip', selected: state.conflict === 'skip' }, 'Skip files that already exist'),
    h('option', { value: 'replace', selected: state.conflict === 'replace' }, 'Replace files that already exist'));
  b.append(target, state.running ? null : dropZone, h('div.mb-3', conflict));
  if (!window.isSecureContext) b.append(h('p.small.text-body-secondary', 'Keep the screen on during a large batch. A sleeping phone pauses uploads.'));
  b.append(ui.summary, ui.bar, ui.rows, ui.actions);
  renderProgress();
}

// counts derives every figure from the items, so nothing can drift.
function counts() {
  const c = { pending: [], active: [], failed: [], done: [], skipped: [], unsupported: [] };
  for (const i of state.items) {
    if (i.status === 'uploading' || i.status === 'retrying') c.active.push(i);
    else if (c[i.status]) c[i.status].push(i);
  }
  c.finished = c.done.length + c.skipped.length;
  return c;
}

// pendingBytes counts the files that still have to be sent. Files done
// in an earlier batch are not part of the next total.
function pendingBytes() {
  return state.items.reduce((n, i) => n + (i.status === 'pending' || i.status === 'uploading' || i.status === 'retrying' ? i.size : 0), 0);
}

// scheduleRender coalesces the many progress events into one frame.
function scheduleRender() {
  if (frame) return;
  frame = requestAnimationFrame(() => { frame = 0; renderProgress(); });
}

function pct(a, b) { return b ? Math.round((a / b) * 100) : 100; }

function renderProgress() {
  if (!ui) return;
  const c = counts();
  const s = clear(ui.summary);
  if (!state.items.length) {
    s.append(h('p.text-body-secondary.small', 'No files yet.'));
    clear(ui.rows); clear(ui.actions);
    ui.bar.classList.add('d-none');
    renderBadge(c);
    return;
  }
  ui.bar.classList.remove('d-none');
  const sent = state.sentBytes + c.active.reduce((n, i) => n + i.sent, 0);
  const barEl = ui.bar.firstChild;
  barEl.style.width = `${pct(sent, state.totalBytes)}%`;
  barEl.textContent = `${pct(sent, state.totalBytes)}%`;
  const elapsed = (Date.now() - state.startedAt) / 1000;
  const speed = state.running && elapsed > 1 ? sent / elapsed : 0;
  const remaining = speed ? (state.totalBytes - sent) / speed : 0;
  const willSkip = state.conflict === 'skip' ? c.pending.filter((i) => i.exists).length : 0;
  if (state.checking) {
    s.append(h('div', icon('hourglass-split', 'me-1'), 'Checking files…'));
  } else if (state.running) {
    s.append(h('div', `${fmtBytes(sent)} of ${fmtBytes(state.totalBytes)} · ${c.finished}/${state.items.length} files`),
      h('div.small.text-body-secondary', speed ? `${fmtBytes(speed)}/s · about ${Math.max(1, Math.ceil(remaining / 60))} min left` : 'Starting…'));
  } else if (state.scanning) {
    s.append(h('div', icon('arrow-repeat', 'me-1'), 'Scanning library…'));
  } else if (c.done.length || c.failed.length || c.skipped.length) {
    s.append(h('div', `${c.done.length} uploaded${c.skipped.length ? `, ${c.skipped.length} skipped` : ''}${c.failed.length ? `, ${c.failed.length} failed` : ''}`));
  } else {
    s.append(h('div', `${c.pending.length} files · ${fmtBytes(pendingBytes())}`),
      c.unsupported.length ? h('div.small.text-warning', `${c.unsupported.length} unsupported, will not be sent`) : null,
      willSkip ? h('div.small.text-body-secondary', `${willSkip} already exist and will be skipped`) : null,
      state.free > 0 && pendingBytes() > state.free ? h('div.small.text-danger', `Not enough space: ${fmtBytes(state.free)} available`) : null);
  }
  const rows = clear(ui.rows);
  for (const i of [...c.active, ...c.failed]) {
    rows.append(h('div.list-group-item.small',
      h('div.d-flex.justify-content-between', h('span.text-truncate', i.rel), h('span.text-body-secondary', i.status === 'failed' ? 'failed' : i.status === 'retrying' ? 'retrying' : `${pct(i.sent, i.size)}%`)),
      i.status === 'failed' ? h('div.text-danger', i.error) : h('div.progress', { style: 'height: 4px' }, h('div.progress-bar', { style: `width: ${pct(i.sent, i.size)}%` }))));
  }
  if (c.finished) rows.append(h('div.list-group-item.small.text-body-secondary', `${c.finished} finished`));
  const acts = clear(ui.actions);
  if (state.running) {
    acts.append(h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => { state.paused = !state.paused; if (!state.paused) pump(); renderProgress(); } }, state.paused ? 'Continue' : 'Pause'),
      h('button.btn.btn-outline-danger', { type: 'button', onclick: cancel }, 'Cancel'));
  } else {
    const toSend = c.pending.length - willSkip;
    if (toSend > 0 && !state.checking) acts.append(h('button.btn.btn-primary', { type: 'button', onclick: start }, `Upload ${toSend} file${toSend === 1 ? '' : 's'}`));
    if (c.failed.length) acts.append(h('button.btn.btn-outline-primary', { type: 'button', onclick: retryFailed }, 'Retry failed'));
    if (state.items.length) acts.append(h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => { state.items = []; state.scanning = false; render(); } }, 'Clear'));
  }
  renderBadge(c);
}

let lastBadge = '';
// renderBadge updates the Library tab with the batch progress, only when
// the figures changed.
function renderBadge(c) {
  const key = `${state.running}:${c.finished}:${state.items.length}`;
  if (key === lastBadge) return;
  lastBadge = key;
  document.dispatchEvent(new CustomEvent('upload-progress', { detail: { running: state.running, done: c.finished, total: state.items.length } }));
}

async function onDrop(e) {
  e.preventDefault();
  const entries = [];
  for (const it of [...(e.dataTransfer?.items || [])]) {
    const entry = it.webkitGetAsEntry?.();
    if (entry) entries.push(entry);
  }
  if (entries.length) {
    const files = [];
    for (const entry of entries) await walkEntry(entry, '', files);
    addFiles(files);
  } else {
    addFiles([...(e.dataTransfer?.files || [])].map((f) => ({ file: f, rel: f.name })));
  }
}

// walkEntry reads a dropped folder tree, keeping its structure.
function walkEntry(entry, prefix, out) {
  return new Promise((resolve) => {
    if (entry.isFile) {
      entry.file((file) => { out.push({ file, rel: prefix + file.name }); resolve(); }, resolve);
    } else if (entry.isDirectory) {
      const reader = entry.createReader();
      const all = [];
      const readMore = () => reader.readEntries(async (batch) => {
        if (!batch.length) {
          for (const child of all) await walkEntry(child, prefix + entry.name + '/', out);
          resolve();
          return;
        }
        all.push(...batch);
        readMore();
      }, resolve);
      readMore();
    } else resolve();
  });
}

// addFiles adds items and checks them with the server in batches. Only the
// new items are touched when the answer comes, so a batch that started
// meanwhile is not disturbed.
async function addFiles(list) {
  if (state.running) { toast('Wait for the current batch to finish.', 'warning'); return; }
  const base = state.target ? state.target + '/' : '';
  const added = [];
  for (const { file, rel } of list) {
    const clean = rel.replace(/\\/g, '/').replace(/^\/+/, '');
    if (!clean || clean.split('/').some((s) => s.startsWith('.'))) continue;
    const item = { file, rel: base + clean, size: file.size, status: 'checking', sent: 0, attempts: 0, exists: false };
    state.items.push(item);
    added.push(item);
  }
  state.checking++;
  renderProgress();
  try {
    for (let i = 0; i < added.length; i += CHECK_BATCH) {
      const slice = added.slice(i, i + CHECK_BATCH);
      const check = await A.api.post('/library/files/check', { paths: slice.map((it) => it.rel) });
      state.free = check.free_bytes;
      const unsupported = new Set([...check.unsupported, ...check.invalid]);
      const existing = new Set(check.existing);
      for (const it of slice) {
        if (unsupported.has(it.rel)) it.status = 'unsupported';
        else if (it.size > check.max_bytes) { it.status = 'failed'; it.error = `Larger than the ${fmtBytes(check.max_bytes)} limit`; }
        else it.status = 'pending';
        it.exists = existing.has(it.rel);
      }
    }
  } catch (err) {
    toast(err.message, 'danger');
    for (const it of added) if (it.status === 'checking') it.status = 'pending';
  }
  state.checking--;
  renderProgress();
}

async function start() {
  if (state.running || state.checking) return;
  state.running = true;
  state.paused = false;
  state.startedAt = Date.now();
  state.sentBytes = 0;
  // Files the check said exist are skipped before any byte is sent.
  for (const i of state.items) {
    if (i.status === 'pending' && i.exists && state.conflict === 'skip') i.status = 'skipped';
  }
  state.totalBytes = pendingBytes();
  window.addEventListener('beforeunload', warnUnload);
  document.addEventListener('visibilitychange', keepAwake);
  keepAwake();
  render();
  pump();
}

function warnUnload(e) { e.preventDefault(); e.returnValue = ''; }

// keepAwake holds a screen wake lock while a batch runs. Browsers drop it
// when the tab is hidden, so it is requested again on return.
async function keepAwake() {
  if (!state.running || document.visibilityState !== 'visible' || !navigator.wakeLock || !window.isSecureContext) return;
  try { state.wakeLock = await navigator.wakeLock.request('screen'); } catch { /* not granted */ }
}

function pump() {
  if (!state.running || state.paused) return;
  const c = counts();
  const next = c.pending.slice(0, Math.max(0, PARALLEL - c.active.length));
  for (const item of next) send(item);
  if (!c.active.length && !next.length) finish();
}

// permanent says whether an HTTP status will not change on a retry.
function permanent(status) { return status < 500 || status === 507; }

function send(item) {
  item.status = 'uploading';
  item.sent = 0;
  item.attempts++;
  const xhr = new XMLHttpRequest();
  item.xhr = xhr;
  xhr.open('PUT', `/api/v1/library/files?path=${encodeURIComponent(item.rel)}&conflict=${state.conflict}`);
  xhr.setRequestHeader('Content-Type', 'application/octet-stream');
  xhr.setRequestHeader('X-CSRF-Token', A.csrf());
  xhr.upload.onprogress = (e) => { item.sent = e.loaded; scheduleRender(); };
  const retryLater = () => {
    item.status = 'retrying';
    item.sent = 0;
    setTimeout(() => { if (item.status === 'retrying') { item.status = 'pending'; pump(); } }, 1500 * item.attempts);
    scheduleRender();
  };
  xhr.onload = () => {
    item.xhr = null;
    if (xhr.status === 201 || xhr.status === 200) {
      state.sentBytes += item.size;
      item.sent = item.size;
      let res = {};
      try { res = JSON.parse(xhr.responseText); } catch { /* empty */ }
      item.status = res.skipped ? 'skipped' : 'done';
    } else if (!permanent(xhr.status) && item.attempts < ATTEMPTS) {
      retryLater();
      return;
    } else {
      item.status = 'failed';
      item.error = problemText(xhr);
    }
    scheduleRender();
    pump();
  };
  xhr.onerror = xhr.ontimeout = () => {
    item.xhr = null;
    if (item.attempts < ATTEMPTS) { retryLater(); return; }
    item.status = 'failed';
    item.error = 'Network error';
    scheduleRender();
    pump();
  };
  xhr.onabort = () => { item.xhr = null; item.status = 'pending'; item.sent = 0; scheduleRender(); };
  xhr.send(item.file);
}

// problemText reads the server's message, with the fix and command when
// it gave them.
function problemText(xhr) {
  let problem = null;
  try { problem = JSON.parse(xhr.responseText); } catch { /* not a problem document */ }
  return problemMessage(problem, `HTTP ${xhr.status}`);
}

function cancel() {
  state.paused = true;
  for (const i of state.items) {
    if (i.xhr) i.xhr.abort();
    else if (i.status === 'retrying') { i.status = 'pending'; i.sent = 0; }
  }
  finish(true);
}

function retryFailed() {
  for (const i of state.items) if (i.status === 'failed' && i.file) { i.status = 'pending'; i.attempts = 0; i.error = ''; }
  start();
}

function finish(cancelled = false) {
  state.running = false;
  window.removeEventListener('beforeunload', warnUnload);
  document.removeEventListener('visibilitychange', keepAwake);
  if (state.wakeLock) { state.wakeLock.release().catch(() => {}); state.wakeLock = null; }
  const uploaded = counts().done.length;
  if (!cancelled && uploaded) {
    // The figures for the toast are fixed now, because Clear or a new
    // target can change the items before the scan ends.
    state.scanning = true;
    state.scanTarget = state.target;
    state.scanCount = uploaded;
  }
  render();
  if (cancelled) toast('Upload cancelled', 'secondary');
}

// The server scans once after the batch and sends an upload event.
document.addEventListener('jukem-upload-done', () => {
  if (!state.scanning) return;
  state.scanning = false;
  toast(`${state.scanCount} track${state.scanCount === 1 ? '' : 's'} added to ${state.scanTarget || 'the library'}`, 'success', 6000);
  render();
});
