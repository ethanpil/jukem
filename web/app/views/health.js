import * as A from '../api.js';
import { h, clear, icon, spinner, errorBox, fmtTime, toast } from '../dom.js';

const statusIcon = { ok: 'check-circle-fill', warning: 'exclamation-triangle-fill', error: 'x-circle-fill' };

// alertCard renders one active alert with its dismiss button.
function alertCard(a, onDismiss) {
  return h('div.alert.alert-warning.mb-0',
    icon('exclamation-triangle-fill'),
    h('div.flex-1',
      h('div.fw-semibold', a.message),
      a.fix ? h('div', a.fix) : null,
      h('div.small.opacity-75.mt-1', `Since ${fmtTime(a.raised_at)}${a.count > 1 ? ` · seen ${a.count} times` : ''}`)),
    h('button.btn.btn-sm.btn-outline-secondary.flex-none', { type: 'button', onclick: async () => {
      try { await A.dismissAlert(a.id); if (onDismiss) onDismiss(); } catch (e) { toast(e.message, 'danger'); }
    } }, 'Dismiss'));
}

// healthView is the plain-language system status.
export async function healthView(main) {
  clear(main);
  const box = h('section.page.page-narrow', spinner());
  main.append(box);
  async function load() {
    try {
      const [rep, info, al] = await Promise.all([A.health(), A.systemInfo().catch(() => null), A.alerts().catch(() => ({ alerts: [] }))]);
      render(rep, info, al.alerts);
    } catch (e) {
      clear(box).append(errorBox(e));
    }
  }
  function render(rep, info, alerts) {
    clear(box);
    const status = statusIcon[rep.status] ? rep.status : 'error';
    box.append(h('div.page-head',
      h('div.health-hero',
        h('div', { class: `big ${status}` }, icon(statusIcon[status])),
        h('div',
          h('h1.page-title', rep.status === 'ok' ? 'System healthy' : rep.status === 'warning' ? 'System needs attention' : 'System has a problem'),
          h('div.small-note', `Checked ${new Date().toLocaleTimeString()}`)))));
    const stack = h('div.stack');
    box.append(stack);
    if (rep.reason) stack.append(h('div.alert.alert-danger.mb-0', icon('x-circle-fill'), h('div.flex-1', h('div.fw-semibold', rep.reason), rep.fix ? h('pre.pre-wrap.mt-2', rep.fix) : null)));
    for (const a of alerts) stack.append(alertCard(a, load));
    const checks = h('div.panel.clip', h('div.panel-head', h('h2.panel-title', 'Checks')));
    for (const c of rep.checks || []) {
      const cs = statusIcon[c.status] ? c.status : 'error';
      checks.append(h('div.check-row',
        h('div.line', icon(statusIcon[cs], `status-${cs}`), h('span.check-name', c.name), h('span.flex-1', c.summary)),
        c.fix ? h('pre.pre-wrap', c.fix) : null));
    }
    if (!rep.checks?.length) checks.append(h('div.panel-empty', 'No checks reported.'));
    stack.append(checks);
    const foot = h('div.small-note.d-flex.flex-wrap.gap-3');
    if (info) foot.append(h('span.mono', `jukem ${info.version} · schema ${info.schema_version} · ${info.runtime} · up ${Math.floor(info.uptime_seconds / 3600)}h ${Math.floor((info.uptime_seconds % 3600) / 60)}m`));
    foot.append(h('a', { href: '#/history' }, 'Play history'));
    stack.append(foot);
  }
  await load();
  return { onEvent(type) { if (type === 'health' || type === 'alerts' || type === 'devices') load(); } };
}

// historyView lists what played and when.
export async function historyView(main) {
  clear(main);
  const box = h('section.page.page-md');
  main.append(box);
  const list = h('div.rows');
  const more = h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => load() }, 'Older');
  const foot = h('div.pager.tinted', more);
  let before = 0;
  box.append(h('div.page-head', h('h1.page-title', 'Play history')), h('div.panel.clip', list, foot));
  async function load() {
    more.disabled = true;
    let r;
    try { r = await A.history(before); } catch (e) { box.append(h('div.mt-3', errorBox(e))); return; }
    if (!r.rows.length && !before) list.append(h('div.panel-empty', 'Nothing played yet.'));
    for (const row of r.rows) {
      list.append(h('div.row-item',
        h('span.row-time.stamp.nowrap', fmtTime(row.started_at)),
        h('div.row-main', h('div.row-title', row.title || row.file), h('div.row-sub', [row.artist, row.album].filter(Boolean).join(' · ') || row.file)),
        row.source ? h('span.chip.chip-neutral', row.source) : null));
    }
    if (r.rows.length) before = r.rows[r.rows.length - 1].id;
    foot.classList.toggle('hidden', r.rows.length < 100);
    more.disabled = false;
  }
  await load();
  return {};
}
