import * as A from './api.js';
import { h, clear, icon, modal, errorBox } from './dom.js';

// dirPicker opens a server-side directory browser and calls onPick with
// the chosen absolute path. Settings and the setup wizard share it.
export function dirPicker({ start, onPick }) {
  let path = start || '/';
  const list = h('div.list-group');
  const crumb = h('div.mono.small.mb-2');
  async function load() {
    try {
      const d = await A.api.get(`/system/directories?path=${encodeURIComponent(path)}`);
      path = d.path;
      crumb.textContent = path;
      clear(list);
      if (d.parent !== undefined && d.parent !== null) list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { path = d.parent; load(); } }, icon('arrow-90deg-up', 'me-2'), '..'));
      for (const e of d.entries) list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { path = e.path; load(); } }, icon('folder', 'me-2'), e.name));
      if (!d.entries.length) list.append(h('div.list-group-item.text-body-secondary', 'No subfolders.'));
    } catch (e) { clear(list).append(errorBox(e)); }
  }
  const dlg = modal({ title: 'Choose the music root', body: [crumb, list], footer: h('button.btn.btn-primary', { type: 'button', onclick: () => { onPick(path); dlg.hide(); } }, 'Use this folder') });
  load();
}
