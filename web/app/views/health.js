import * as A from '../api.js';
import { h, clear, icon, spinner, errorBox, fmtTime, toast } from '../dom.js';

const statusIcon = { ok: ['check-circle-fill', 'text-success'], warning: ['exclamation-triangle-fill', 'text-warning'], error: ['x-circle-fill', 'text-danger'] };

// alertCard renders one active alert with its dismiss button. Shared with
// the app shell's banner.
export function alertCard(a, onDismiss) {
  return h('div.alert.alert-warning.d-flex.gap-2.align-items-start.mb-2',
    icon('exclamation-triangle-fill', 'mt-1'),
    h('div.flex-grow-1',
      h('div.fw-semibold', a.message),
      a.fix ? h('div.small', a.fix) : null,
      h('div.small.text-body-secondary', `Since ${fmtTime(a.raised_at)}${a.count > 1 ? ` · seen ${a.count} times` : ''}`)),
    h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: async () => {
      try { await A.dismissAlert(a.id); if (onDismiss) onDismiss(); } catch (e) { toast(e.message, 'danger'); }
    } }, 'Dismiss'));
}

// healthView is the plain-language system status.
export async function healthView(main) {
  clear(main);
  const box = h('div.mx-auto', { style: 'max-width: 720px' }, spinner());
  main.append(box);
  async function load() {
    try {
      const [rep, info] = await Promise.all([A.health(), A.systemInfo().catch(() => null)]);
      render(rep, info);
    } catch (e) {
      clear(box).append(errorBox(e));
    }
  }
  function render(rep, info) {
    clear(box);
    const [ic, cls] = statusIcon[rep.status] || statusIcon.error;
    box.append(h('div.d-flex.align-items-center.gap-2.mb-3', icon(ic, `${cls} fs-3`),
      h('h1.h3.mb-0', rep.status === 'ok' ? 'System healthy' : rep.status === 'warning' ? 'System needs attention' : 'System has a problem')));
    if (rep.reason) box.append(h('div.alert.alert-danger', h('div.fw-semibold', rep.reason), rep.fix ? h('pre.pre-wrap.mb-0.mt-2', rep.fix) : null));
    for (const a of rep.alerts || []) box.append(alertCard(a, load));
    const list = h('div.list-group.mb-3');
    for (const c of rep.checks || []) {
      const [ci, ccls] = statusIcon[c.status] || statusIcon.error;
      list.append(h('div.list-group-item',
        h('div.d-flex.align-items-center.gap-2', icon(ci, ccls), h('span.fw-semibold', { style: 'min-width: 8em' }, c.name), h('span', c.summary)),
        c.fix ? h('pre.pre-wrap.small.text-body-secondary.mb-0.mt-1.ms-4', c.fix) : null));
    }
    box.append(list);
    if (info) {
      box.append(h('p.small.text-body-secondary', `jukem ${info.version} · schema ${info.schema_version} · ${info.runtime} · up ${Math.floor(info.uptime_seconds / 3600)}h ${Math.floor((info.uptime_seconds % 3600) / 60)}m`));
    }
    box.append(h('p.small.text-body-secondary', `Checked ${new Date().toLocaleTimeString()} · `, h('a', { href: '#/history' }, 'Play history')));
  }
  await load();
  return { onEvent(type) { if (type === 'health' || type === 'alerts' || type === 'devices') load(); } };
}

// historyView lists what played and when.
export async function historyView(main) {
  clear(main);
  const box = h('div.mx-auto', { style: 'max-width: 900px' });
  main.append(box);
  const list = h('div.list-group.row-list');
  const more = h('button.btn.btn-outline-secondary.mt-2', { type: 'button', onclick: () => load() }, 'Older');
  let before = '';
  box.append(h('h1.h3', 'Play history'), list, more);
  async function load() {
    more.disabled = true;
    let r;
    try { r = await A.history(before); } catch (e) { box.append(errorBox(e)); return; }
    if (!r.rows.length && !before) list.append(h('div.list-group-item.text-body-secondary', 'Nothing played yet.'));
    for (const row of r.rows) {
      list.append(h('div.list-group-item',
        h('span.mono.small.text-body-secondary', { style: 'min-width: 9em' }, fmtTime(row.started_at)),
        h('div.row-main', h('div.row-title', row.title || row.file), h('div.small.text-body-secondary', [row.artist, row.album].filter(Boolean).join(' · ') || row.file)),
        row.source ? h('span.badge.text-bg-secondary', row.source) : null));
    }
    if (r.rows.length) before = r.rows[r.rows.length - 1].started_at;
    more.classList.toggle('d-none', r.rows.length < 100);
    more.disabled = false;
  }
  await load();
  return {};
}
