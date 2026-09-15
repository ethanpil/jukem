import * as A from '../api.js';
import { h, clear, icon, spinner, errorBox } from '../dom.js';

const statusIcon = { ok: ['check-circle-fill', 'text-success'], warning: ['exclamation-triangle-fill', 'text-warning'], error: ['x-circle-fill', 'text-danger'] };

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
    box.append(h('p.small.text-body-secondary', `Checked ${new Date().toLocaleTimeString()}`));
  }
  await load();
  return { onEvent(type) { if (type === 'health' || type === 'alerts' || type === 'devices') load(); } };
}
