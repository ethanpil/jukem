// DOM helpers. Track titles and file names come from uploaded files, so
// every view builds elements with textContent and attributes, never HTML.

// h('tag.class#id', {attr: value, onclick: fn}, ...children)
export function h(spec, attrs, ...children) {
  const m = /^([a-z0-9-]+)?((?:[.#][\w-]+)*)$/i.exec(spec);
  const el = document.createElement((m && m[1]) || 'div');
  if (m && m[2]) {
    for (const part of m[2].match(/[.#][\w-]+/g) || []) {
      if (part[0] === '.') el.classList.add(part.slice(1));
      else el.id = part.slice(1);
    }
  }
  if (attrs && typeof attrs === 'object' && !(attrs instanceof Node) && !Array.isArray(attrs)) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === null || v === undefined || v === false) continue;
      if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
      else if (k === 'class') el.className = v;
      // The CSP forbids style attributes. The CSSOM path is permitted.
      else if (k === 'style') el.style.cssText = v;
      else if (k === 'dataset') Object.assign(el.dataset, v);
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, v);
    }
  } else if (attrs !== undefined) {
    children.unshift(attrs);
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children.flat(Infinity)) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

export function clear(el) {
  while (el.firstChild) el.removeChild(el.firstChild);
  return el;
}

export function icon(name, extra = '') {
  return h('i', { class: `bi bi-${name} ${extra}`.trim(), 'aria-hidden': 'true' });
}

// toast shows a short message; kind is a Bootstrap colour name.
export function toast(message, kind = 'primary', delay = 4000) {
  const root = document.getElementById('toasts');
  const el = h('div.toast.align-items-center.border-0', { role: 'status', class: `toast align-items-center border-0 text-bg-${kind}` },
    h('div.d-flex',
      h('div.toast-body', message),
      h('button.btn-close.btn-close-white.me-2.m-auto', { type: 'button', 'data-bs-dismiss': 'toast', 'aria-label': 'Close' })));
  root.append(el);
  const t = new bootstrap.Toast(el, { delay });
  el.addEventListener('hidden.bs.toast', () => el.remove());
  t.show();
}

// confirmDialog resolves true when the person confirms.
export function confirmDialog({ title, body, confirmText = 'OK', danger = false }) {
  return new Promise((resolve) => {
    const root = document.getElementById('modal-root');
    const okBtn = h('button.btn', { type: 'button', class: `btn ${danger ? 'btn-danger' : 'btn-primary'}` }, confirmText);
    const el = h('div.modal.fade', { tabindex: '-1' },
      h('div.modal-dialog',
        h('div.modal-content',
          h('div.modal-header', h('h5.modal-title', title), h('button.btn-close', { type: 'button', 'data-bs-dismiss': 'modal', 'aria-label': 'Close' })),
          h('div.modal-body', body),
          h('div.modal-footer', h('button.btn.btn-secondary', { type: 'button', 'data-bs-dismiss': 'modal' }, 'Cancel'), okBtn))));
    root.append(el);
    const m = new bootstrap.Modal(el);
    let result = false;
    okBtn.addEventListener('click', () => { result = true; m.hide(); });
    el.addEventListener('hidden.bs.modal', () => { el.remove(); resolve(result); });
    m.show();
  });
}

// modal opens a dialog with custom content; returns {el, hide}.
export function modal({ title, body, footer, size = '' }) {
  const root = document.getElementById('modal-root');
  const el = h('div.modal.fade', { tabindex: '-1' },
    h('div.modal-dialog', { class: `modal-dialog ${size}`.trim() },
      h('div.modal-content',
        h('div.modal-header', h('h5.modal-title', title), h('button.btn-close', { type: 'button', 'data-bs-dismiss': 'modal', 'aria-label': 'Close' })),
        h('div.modal-body', body),
        footer ? h('div.modal-footer', footer) : null)));
  root.append(el);
  const m = new bootstrap.Modal(el);
  el.addEventListener('hidden.bs.modal', () => el.remove());
  m.show();
  return { el, hide: () => m.hide() };
}

export function fmtDuration(seconds) {
  if (!seconds && seconds !== 0) return '';
  const s = Math.max(0, Math.round(seconds));
  const m = Math.floor(s / 60);
  const h = Math.floor(m / 60);
  const mm = h ? String(m % 60).padStart(2, '0') : String(m);
  return (h ? `${h}:` : '') + `${mm}:${String(s % 60).padStart(2, '0')}`;
}

export function fmtBytes(n) {
  if (n === null || n === undefined) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function fmtTime(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  return d.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
}

// copyText copies to the clipboard, with the execCommand fallback that
// works over plain HTTP.
export async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const ta = h('textarea', { style: 'position:fixed;opacity:0' }, text);
  document.body.append(ta);
  ta.select();
  document.execCommand('copy');
  ta.remove();
}

// spinner is a small loading placeholder.
// tzDatalist lists every IANA zone the browser knows, for a text input
// with list=id.
export function tzDatalist(id) {
  const list = h('datalist', { id });
  for (const z of (Intl.supportedValuesOf ? Intl.supportedValuesOf('timeZone') : [])) list.append(h('option', { value: z }));
  return list;
}

export function spinner() {
  return h('div.text-center.py-4', h('div.spinner-border.text-secondary', { role: 'status' }, h('span.visually-hidden', 'Loading')));
}

// errorBox shows an API error inline.
export function errorBox(err) {
  return h('div.alert.alert-danger', err?.message || String(err));
}
