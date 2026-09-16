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

// The now-playing bar and the tab bar cover the lower part of the window, so
// a menu keeps clear of them and turns upwards when it does not fit. The
// heights come from the bars themselves, which carry the safe area of the
// device and are not there at all before sign-in.
function menuGap() {
  let bars = 0;
  for (const id of ['now-bar', 'tab-bar']) {
    const el = document.getElementById(id);
    if (el && !el.classList.contains('hidden')) bars += el.getBoundingClientRect().height;
  }
  return { top: 70, bottom: Math.round(bars) + 12, left: 8, right: 8 };
}
// The rows of a list are in a card that hides what goes past its edge. A
// menu that places itself against the window, and not against the card, is
// not cut off by it. Bootstrap makes the menu objects itself, so the rule
// belongs with its defaults.
bootstrap.Dropdown.Default.popperConfig = (config) => ({
  ...config,
  strategy: 'fixed',
  modifiers: [
    ...(config.modifiers || []),
    // altAxis moves the menu along the axis the padding above is about.
    { name: 'flip', options: { padding: menuGap() } },
    { name: 'preventOverflow', options: { padding: menuGap(), altAxis: true } },
  ],
});

// openMenu is the row menu that is open now, if there is one. A list can be
// rebuilt while a menu in it is open, and Bootstrap keeps the listeners of a
// menu that is removed in that state, so clear closes it first. A menu that
// closes gives its Bootstrap object back at once, so a long list leaves
// nothing behind.
let openMenu = null;
document.addEventListener('shown.bs.dropdown', (e) => { openMenu = e.target; });
document.addEventListener('hidden.bs.dropdown', (e) => {
  openMenu = null;
  const menu = e.target;
  setTimeout(() => { if (menu.getAttribute('aria-expanded') !== 'true') bootstrap.Dropdown.getInstance(menu)?.dispose(); }, 0);
});

export function clear(el) {
  if (openMenu && el.contains(openMenu)) {
    bootstrap.Dropdown.getInstance(openMenu)?.dispose();
    openMenu = null;
  }
  while (el.firstChild) el.removeChild(el.firstChild);
  return el;
}

export function icon(name, extra = '') {
  return h('i', { class: `bi bi-${name} ${extra}`.trim(), 'aria-hidden': 'true' });
}

// The logo is "Music Library 2" from the Solar icon set by 480 Design,
// CC BY 4.0. It is drawn inline, so it takes the accent colour.
const LOGO_PATHS = [
  [null, 'M11.25 16.9999C11.25 16.5857 10.9142 16.2499 10.5 16.2499C10.0858 16.2499 9.75 16.5857 9.75 16.9999C9.75 17.4142 10.0858 17.7499 10.5 17.7499C10.9142 17.7499 11.25 17.4142 11.25 16.9999Z'],
  [null, 'M8.67239 7.54199H15.3276C18.7024 7.54199 20.3898 7.54199 21.3377 8.52882C22.2855 9.51564 22.0625 11.0403 21.6165 14.0895L21.1935 16.9811C20.8437 19.3723 20.6689 20.5679 19.7717 21.2839C18.8745 21.9999 17.5512 21.9999 14.9046 21.9999H9.09534C6.4488 21.9999 5.12553 21.9999 4.22834 21.2839C3.33115 20.5679 3.15626 19.3723 2.80648 16.9811L2.38351 14.0895C1.93748 11.0403 1.71447 9.51565 2.66232 8.52882C3.61017 7.54199 5.29758 7.54199 8.67239 7.54199ZM12.75 10.4999C12.75 10.0857 12.4142 9.74995 12 9.74995C11.5858 9.74995 11.25 10.0857 11.25 10.4999V14.878C11.0154 14.7951 10.763 14.7499 10.5 14.7499C9.25736 14.7499 8.25 15.7573 8.25 16.9999C8.25 18.2426 9.25736 19.2499 10.5 19.2499C11.7426 19.2499 12.75 18.2426 12.75 16.9999V13.3197C13.4202 13.8633 14.2617 14.2499 15 14.2499C15.4142 14.2499 15.75 13.9142 15.75 13.4999C15.75 13.0857 15.4142 12.7499 15 12.7499C14.6946 12.7499 14.1145 12.5313 13.5835 12.0602C13.0654 11.6006 12.75 11.0386 12.75 10.4999Z', true],
  ['0.4', 'M8.50956 2.00001H15.4897C15.7221 1.99995 15.9004 1.99991 16.0562 2.01515C17.164 2.12352 18.0708 2.78958 18.4553 3.68678H5.54395C5.92846 2.78958 6.83521 2.12352 7.94303 2.01515C8.09884 1.99991 8.27708 1.99995 8.50956 2.00001Z'],
  ['0.7', 'M6.3102 4.72266C4.91958 4.72266 3.77931 5.56241 3.39878 6.67645C3.39085 6.69967 3.38325 6.72302 3.37598 6.74647C3.77413 6.6259 4.18849 6.54713 4.60796 6.49336C5.68833 6.35485 7.05367 6.35492 8.6397 6.35501H15.5318C17.1178 6.35492 18.4832 6.35485 19.5635 6.49336C19.983 6.54713 20.3974 6.6259 20.7955 6.74647C20.7883 6.72302 20.7806 6.69967 20.7727 6.67645C20.3922 5.56241 19.2519 4.72266 17.8613 4.72266H6.3102Z'],
];

// logo draws the logo mark into el, or into a new span.logo.
export function logo(el = h('span.logo')) {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('aria-hidden', 'true');
  for (const [opacity, d, evenodd] of LOGO_PATHS) {
    const p = document.createElementNS(ns, 'path');
    p.setAttribute('d', d);
    p.setAttribute('fill', 'currentColor');
    if (opacity) p.setAttribute('opacity', opacity);
    if (evenodd) { p.setAttribute('fill-rule', 'evenodd'); p.setAttribute('clip-rule', 'evenodd'); }
    svg.append(p);
  }
  clear(el).append(svg);
  return el;
}

const toastIcons = { success: 'check-circle-fill', primary: 'info-circle-fill', warning: 'exclamation-triangle-fill', danger: 'x-circle-fill', secondary: 'info-circle' };

// toast shows a short message; kind is success, primary, warning, danger
// or secondary.
export function toast(message, kind = 'primary', delay = 4000) {
  const root = document.getElementById('toasts');
  const el = h('div', { role: 'status', class: `toast j-toast kind-${kind}` },
    h('div.toast-inner',
      icon(toastIcons[kind] || toastIcons.primary, 'toast-icon'),
      h('div.toast-body', message),
      h('button.btn-close', { type: 'button', 'data-bs-dismiss': 'toast', 'aria-label': 'Close' })));
  root.append(el);
  const t = new bootstrap.Toast(el, { delay });
  el.addEventListener('hidden.bs.toast', () => { t.dispose(); el.remove(); });
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
          h('div.modal-footer', h('button.btn.btn-outline-secondary', { type: 'button', 'data-bs-dismiss': 'modal' }, 'Cancel'), okBtn))));
    root.append(el);
    const m = new bootstrap.Modal(el);
    let result = false;
    okBtn.addEventListener('click', () => { result = true; m.hide(); });
    el.addEventListener('hidden.bs.modal', () => { m.dispose(); el.remove(); resolve(result); });
    m.show();
  });
}

// modal opens a dialog with custom content; returns {el, hide}.
export function modal({ title, body, footer, size = '' }) {
  const root = document.getElementById('modal-root');
  const el = h('div.modal.fade', { tabindex: '-1' },
    h('div', { class: `modal-dialog ${size}`.trim() },
      h('div.modal-content',
        h('div.modal-header', h('h5.modal-title', title), h('button.btn-close', { type: 'button', 'data-bs-dismiss': 'modal', 'aria-label': 'Close' })),
        h('div.modal-body', body),
        footer ? h('div.modal-footer', footer) : null)));
  root.append(el);
  const m = new bootstrap.Modal(el);
  el.addEventListener('hidden.bs.modal', () => { m.dispose(); el.remove(); });
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

// problemMessage joins a problem document's detail with its first error
// message and the fix command, when the server gave them.
export function problemMessage(problem, fallback = '') {
  let text = problem?.detail || fallback;
  const e = problem?.errors?.[0];
  if (e?.message) text += `${/[.!?]$/.test(text) ? ' ' : '. '}${e.message}`;
  if (e?.value) text += ` Command: ${e.value}`;
  return text;
}

// tzDatalist lists every IANA zone the browser knows, for a text input
// with list=id.
export function tzDatalist(id) {
  const list = h('datalist', { id });
  for (const z of (Intl.supportedValuesOf ? Intl.supportedValuesOf('timeZone') : [])) list.append(h('option', { value: z }));
  return list;
}

// spinner is a small loading placeholder.
export function spinner() {
  return h('div.text-center.py-4', h('div.spinner-border', { role: 'status' }, h('span.visually-hidden', 'Loading')));
}

// errorBox shows an API error inline.
export function errorBox(err) {
  return h('div.alert.alert-danger', icon('x-circle-fill'), h('div', err?.message || String(err)));
}

// dotsMenu is a "more" button with a Bootstrap dropdown. items is an array
// of menu nodes, or a function that builds them when the menu opens. cls
// sets the button look and size.
export function dotsMenu(label, items, cls = 'btn-ghost s30') {
  const ul = h('ul.dropdown-menu.dropdown-menu-end');
  const btn = h('button', { type: 'button', class: `btn btn-icon ${cls}`, 'data-bs-toggle': 'dropdown', 'aria-expanded': 'false', 'aria-label': label }, icon('three-dots'));
  const box = h('div.dropdown.flex-none', btn, ul);
  if (typeof items === 'function') box.addEventListener('show.bs.dropdown', () => { clear(ul).append(...items().flat(Infinity).filter(Boolean)); });
  else ul.append(...items.flat(Infinity).filter(Boolean));
  return box;
}

// menuItem is one dropdown entry with an icon.
export function menuItem(ic, label, onclick, cls = '') {
  return h('li', h('button', { type: 'button', class: `dropdown-item ${cls}`.trim(), onclick }, ic ? icon(ic) : null, label));
}

export function menuDivider() { return h('li', h('hr.dropdown-divider')); }
