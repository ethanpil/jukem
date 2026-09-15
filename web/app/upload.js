// Upload manager. It lives in the app shell, so changing screens does not
// interrupt a batch. Three files go at a time, one request per file, over
// XMLHttpRequest for upload progress.

import * as A from './api.js';
import { h, clear, icon, toast, fmtBytes } from './dom.js';

const PARALLEL = 3;
const RETRIES = 3;

const state = {
  items: [],          // {file, rel, size, status, sent, error, attempts, xhr}
  target: '',
  conflict: 'skip',
  running: false,
  paused: false,
  startedAt: 0,
  sentBytes: 0,
  totalBytes: 0,
  scanning: false,
  done: 0,
  failed: 0,
  skipped: 0,
  panel: null,
  wakeLock: null,
};

let offcanvas = null;
let rendered = null;

// open shows the panel targeting folder.
export function open(folder) {
  if (!state.running) { state.target = folder || ''; state.items = []; state.done = state.failed = state.skipped = 0; }
  buildPanel();
  offcanvas.show();
  render();
}

function buildPanel() {
  if (state.panel) return;
  const el = h('div.offcanvas.offcanvas-end', { tabindex: '-1', id: 'upload-panel' },
    h('div.offcanvas-header', h('h5.offcanvas-title', 'Upload'), h('button.btn-close', { type: 'button', 'data-bs-dismiss': 'offcanvas', 'aria-label': 'Close' })),
    h('div.offcanvas-body'));
  document.body.append(el);
  state.panel = el;
  // A phone gets the panel from the bottom.
  const mq = window.matchMedia('(max-width: 767.98px)');
  const place = () => { el.classList.toggle('offcanvas-bottom', mq.matches); el.classList.toggle('offcanvas-end', !mq.matches); };
  mq.addEventListener('change', place);
  place();
  offcanvas = new bootstrap.Offcanvas(el);
  el.addEventListener('dragover', (e) => { e.preventDefault(); });
  el.addEventListener('drop', onDrop);
}

function body() { return state.panel.querySelector('.offcanvas-body'); }

function render() {
  if (!state.panel) return;
  const b = clear(body());
  const target = h('div.mb-3', h('div.small.text-body-secondary', 'Into'), h('div.mono', state.target || '(music root)'));
  const filePick = h('input', { type: 'file', multiple: true, class: 'd-none', accept: 'audio/*' });
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
  const conflict = h('select.form-select.form-select-sm', { onchange: (e) => { state.conflict = e.target.value; } },
    h('option', { value: 'skip', selected: state.conflict === 'skip' }, 'Skip files that already exist'),
    h('option', { value: 'replace', selected: state.conflict === 'replace' }, 'Replace files that already exist'));
  b.append(target, state.running ? null : dropZone, h('div.mb-3', conflict));
  if (!window.isSecureContext) b.append(h('p.small.text-body-secondary', 'Keep the screen on during a large batch. A sleeping phone pauses uploads.'));

  rendered = { summary: h('div.mb-2'), bar: h('div.progress.mb-2', { role: 'progressbar' }, h('div.progress-bar')), rows: h('div.list-group.mb-3'), actions: h('div.d-flex.gap-2') };
  b.append(rendered.summary, rendered.bar, rendered.rows, rendered.actions);
  renderProgress();
}

function renderProgress() {
  if (!rendered || !state.panel) return;
  const items = state.items;
  const pending = items.filter((i) => i.status === 'pending');
  const active = items.filter((i) => i.status === 'uploading');
  const failed = items.filter((i) => i.status === 'failed');
  const done = items.filter((i) => i.status === 'done' || i.status === 'skipped');
  const s = clear(rendered.summary);
  if (!items.length) {
    s.append(h('p.text-body-secondary.small', 'No files yet.'));
    clear(rendered.rows); clear(rendered.actions);
    rendered.bar.classList.add('d-none');
    return;
  }
  rendered.bar.classList.remove('d-none');
  const sent = state.sentBytes + active.reduce((n, i) => n + i.sent, 0);
  const pct = state.totalBytes ? Math.round((sent / state.totalBytes) * 100) : 0;
  const barEl = rendered.bar.firstChild;
  barEl.style.width = `${pct}%`;
  barEl.textContent = `${pct}%`;
  const elapsed = (Date.now() - state.startedAt) / 1000;
  const speed = state.running && elapsed > 1 ? sent / elapsed : 0;
  const remaining = speed ? (state.totalBytes - sent) / speed : 0;
  if (state.running) {
    s.append(h('div', `${fmtBytes(sent)} of ${fmtBytes(state.totalBytes)} · ${done.length}/${items.length} files`),
      h('div.small.text-body-secondary', speed ? `${fmtBytes(speed)}/s · about ${Math.ceil(remaining / 60)} min left` : 'Starting…'));
  } else if (state.scanning) {
    s.append(h('div', icon('arrow-repeat', 'me-1'), 'Scanning library…'));
  } else if (done.length || failed.length) {
    s.append(h('div', `${done.length} uploaded${state.skipped ? ` (${state.skipped} skipped)` : ''}${failed.length ? `, ${failed.length} failed` : ''}`));
  } else {
    const unsupported = items.filter((i) => i.status === 'unsupported').length;
    const existing = items.filter((i) => i.exists).length;
    s.append(h('div', `${items.length} files · ${fmtBytes(state.totalBytes)}`),
      unsupported ? h('div.small.text-warning', `${unsupported} unsupported, will be skipped`) : null,
      existing ? h('div.small.text-body-secondary', `${existing} already exist (${state.conflict})`) : null,
      state.free !== undefined && state.free !== null && state.free > 0 && state.totalBytes > state.free ? h('div.small.text-danger', `Not enough space: ${fmtBytes(state.free)} available`) : null);
  }
  const rows = clear(rendered.rows);
  for (const i of [...active, ...failed]) {
    rows.append(h('div.list-group-item.small',
      h('div.d-flex.justify-content-between', h('span.text-truncate', i.rel), h('span.text-body-secondary', i.status === 'failed' ? 'failed' : `${Math.round((i.sent / i.size) * 100)}%`)),
      i.status === 'failed' ? h('div.text-danger', i.error) : h('div.progress', { style: 'height: 4px' }, h('div.progress-bar', { style: `width: ${Math.round((i.sent / i.size) * 100)}%` }))));
  }
  if (done.length) rows.append(h('div.list-group-item.small.text-body-secondary', `${done.length} finished`));
  const acts = clear(rendered.actions);
  if (state.running) {
    acts.append(h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => { state.paused = !state.paused; if (!state.paused) pump(); renderProgress(); } }, state.paused ? 'Continue' : 'Pause'),
      h('button.btn.btn-outline-danger', { type: 'button', onclick: cancel }, 'Cancel'));
  } else {
    if (pending.length) acts.append(h('button.btn.btn-primary', { type: 'button', onclick: start }, `Upload ${pending.length} file${pending.length === 1 ? '' : 's'}`));
    if (failed.length) acts.append(h('button.btn.btn-outline-primary', { type: 'button', onclick: retryFailed }, 'Retry failed'));
    if (items.length) acts.append(h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => { state.items = []; state.done = state.failed = state.skipped = 0; render(); } }, 'Clear'));
  }
  renderBadge();
}

// renderBadge updates the Library tab with the batch progress.
function renderBadge() {
  document.dispatchEvent(new CustomEvent('upload-progress', { detail: { running: state.running, done: state.done, total: state.items.length } }));
}

async function onDrop(e) {
  e.preventDefault();
  const entries = [];
  const items = [...(e.dataTransfer?.items || [])];
  for (const it of items) {
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

async function addFiles(list) {
  if (state.running) { toast('Wait for the current batch to finish.', 'warning'); return; }
  const base = state.target ? state.target + '/' : '';
  for (const { file, rel } of list) {
    const clean = rel.replace(/\\/g, '/').replace(/^\/+/, '');
    if (!clean || clean.split('/').some((s) => s.startsWith('.'))) continue;
    state.items.push({ file, rel: base + clean, size: file.size, status: 'pending', sent: 0, attempts: 0 });
  }
  state.totalBytes = state.items.reduce((n, i) => n + (i.status === 'pending' ? i.size : 0), 0);
  try {
    const check = await A.api.post('/library/files/check', { paths: state.items.filter((i) => i.status === 'pending').map((i) => i.rel) });
    state.free = check.free_bytes;
    const unsupported = new Set(check.unsupported);
    const invalid = new Set(check.invalid);
    const existing = new Set(check.existing);
    for (const i of state.items) {
      if (unsupported.has(i.rel) || invalid.has(i.rel)) i.status = 'unsupported';
      i.exists = existing.has(i.rel);
      if (i.size > check.max_bytes) { i.status = 'failed'; i.error = `Larger than the ${fmtBytes(check.max_bytes)} limit`; }
    }
    state.totalBytes = state.items.reduce((n, i) => n + (i.status === 'pending' ? i.size : 0), 0);
  } catch (err) { toast(err.message, 'danger'); }
  renderProgress();
}

async function start() {
  if (state.running) return;
  state.running = true;
  state.paused = false;
  state.startedAt = Date.now();
  state.sentBytes = 0;
  // Files the check said exist are skipped before any byte is sent.
  for (const i of state.items) {
    if (i.status === 'pending' && i.exists && state.conflict === 'skip') { i.status = 'skipped'; state.skipped++; }
  }
  state.totalBytes = state.items.reduce((n, i) => n + (i.status === 'pending' ? i.size : 0), 0);
  window.addEventListener('beforeunload', warnUnload);
  if (navigator.wakeLock && window.isSecureContext) {
    try { state.wakeLock = await navigator.wakeLock.request('screen'); } catch { /* not granted */ }
  }
  render();
  pump();
}

function warnUnload(e) { e.preventDefault(); e.returnValue = ''; }

function pump() {
  if (!state.running || state.paused) return;
  const active = state.items.filter((i) => i.status === 'uploading').length;
  const next = state.items.filter((i) => i.status === 'pending').slice(0, Math.max(0, PARALLEL - active));
  for (const item of next) send(item);
  if (!active && !next.length) finish();
}

function send(item) {
  item.status = 'uploading';
  item.sent = 0;
  item.attempts++;
  const xhr = new XMLHttpRequest();
  item.xhr = xhr;
  xhr.open('PUT', `/api/v1/library/files?path=${encodeURIComponent(item.rel)}&conflict=${state.conflict}`);
  xhr.setRequestHeader('Content-Type', 'application/octet-stream');
  xhr.setRequestHeader('X-CSRF-Token', A.csrf());
  xhr.upload.onprogress = (e) => { item.sent = e.loaded; renderProgress(); };
  xhr.onload = () => {
    item.xhr = null;
    if (xhr.status === 201 || xhr.status === 200) {
      state.sentBytes += item.size;
      item.sent = item.size;
      let res = {};
      try { res = JSON.parse(xhr.responseText); } catch { /* empty */ }
      if (res.skipped) { item.status = 'skipped'; state.skipped++; } else { item.status = 'done'; state.done++; }
    } else if (xhr.status >= 500 && item.attempts < RETRIES) {
      setTimeout(() => { item.status = 'pending'; pump(); }, 1000 * item.attempts);
      renderProgress();
      return;
    } else {
      let detail = `HTTP ${xhr.status}`;
      try { const p = JSON.parse(xhr.responseText); detail = p.detail || detail; if (p.fix) detail += ` ${p.fix}`; if (p.command) detail += ` Command: ${p.command}`; } catch { /* keep */ }
      item.status = 'failed';
      item.error = detail;
      state.failed++;
    }
    renderProgress();
    pump();
  };
  xhr.onerror = xhr.ontimeout = () => {
    item.xhr = null;
    if (item.attempts < RETRIES) {
      setTimeout(() => { item.status = 'pending'; pump(); }, 2000 * item.attempts);
    } else {
      item.status = 'failed';
      item.error = 'Network error';
      state.failed++;
      pump();
    }
    renderProgress();
  };
  xhr.onabort = () => { item.xhr = null; item.status = 'pending'; item.sent = 0; renderProgress(); };
  xhr.send(item.file);
}

function cancel() {
  state.paused = true;
  for (const i of state.items) if (i.xhr) i.xhr.abort();
  finish(true);
}

function retryFailed() {
  for (const i of state.items) if (i.status === 'failed' && i.file) { i.status = 'pending'; i.attempts = 0; i.error = ''; state.failed--; }
  state.totalBytes = state.items.reduce((n, i) => n + (i.status === 'pending' || i.status === 'done' ? i.size : 0), 0);
  start();
}

function finish(cancelled = false) {
  state.running = false;
  window.removeEventListener('beforeunload', warnUnload);
  if (state.wakeLock) { state.wakeLock.release().catch(() => {}); state.wakeLock = null; }
  const uploaded = state.items.filter((i) => i.status === 'done').length;
  if (!cancelled && uploaded) state.scanning = true;
  render();
  if (cancelled) toast('Upload cancelled', 'secondary');
}

// The server scans once after the batch and sends an upload event.
document.addEventListener('jukem-upload-done', () => {
  if (!state.scanning) return;
  state.scanning = false;
  const uploaded = state.items.filter((i) => i.status === 'done').length;
  toast(`${uploaded} track${uploaded === 1 ? '' : 's'} added to ${state.target || 'the library'}`, 'success', 6000);
  render();
});

export function isRunning() { return state.running; }
