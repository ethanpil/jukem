// File operation dialogs: rename, move, delete, add to playlist, new
// folder, and the library upload panel. The Library and Playlists views
// call these directly.

import * as A from './api.js';
import { h, clear, icon, toast, confirmDialog, modal, spinner, errorBox } from './dom.js';
import * as upload from './upload.js';

function opMessage(err) {
  const e = err.problem?.errors?.[0];
  if (!e?.message) return err.message;
  return `${err.message}. ${e.message}${e.value ? ` Command: ${e.value}` : ''}`;
}

function fail(err) { toast(opMessage(err), 'danger', 10000); }

export function openUpload(folder) { upload.open(folder); }

export async function newFolder(parent, reload) {
  const name = prompt('New folder name:');
  if (!name || !name.trim()) return;
  try {
    await A.api.post('/library/folders', { path: (parent ? parent + '/' : '') + name.trim() });
    toast(`Folder "${name.trim()}" created`, 'success');
    reload();
  } catch (err) { fail(err); }
}

export async function rename(path, reload) {
  const dir = path.includes('/') ? path.slice(0, path.lastIndexOf('/') + 1) : '';
  const base = path.slice(dir.length);
  const name = prompt('New name:', base);
  if (!name || name.trim() === base) return;
  try {
    await A.api.post('/library/move', { from: path, to: dir + name.trim() });
    toast('Renamed', 'success');
    reload();
  } catch (err) { fail(err); }
}

// folderPicker opens a dialog to choose a folder in the library.
export function folderPicker(title, start, onPick) {
  let path = start || '';
  const crumb = h('div.mono.small.mb-2');
  const list = h('div.list-group');
  async function load() {
    clear(list).append(spinner());
    try {
      const pg = await A.api.get(`/library/browse?path=${encodeURIComponent(path)}&page=0`);
      crumb.textContent = '/' + path;
      clear(list);
      if (path) list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { path = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : ''; load(); } }, icon('arrow-90deg-up', 'me-2'), '..'));
      for (const e of pg.entries) {
        if (e.type !== 'directory') continue;
        list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { path = e.path; load(); } }, icon('folder', 'me-2'), e.name));
      }
    } catch (e) { clear(list).append(errorBox(e)); }
  }
  const dlg = modal({ title, body: [crumb, list], footer: h('button.btn.btn-primary', { type: 'button', onclick: () => { dlg.hide(); onPick(path); } }, 'Choose this folder') });
  load();
}

export function move(paths, reload) {
  if (!paths.length) return;
  const start = paths[0].includes('/') ? paths[0].slice(0, paths[0].lastIndexOf('/')) : '';
  folderPicker(`Move ${paths.length} item${paths.length === 1 ? '' : 's'} to`, start, async (dest) => {
    let moved = 0;
    for (const p of paths) {
      const base = p.slice(p.lastIndexOf('/') + 1);
      try {
        await A.api.post('/library/move', { from: p, to: (dest ? dest + '/' : '') + base });
        moved++;
      } catch (err) { fail(err); break; }
    }
    if (moved) toast(`Moved ${moved} item${moved === 1 ? '' : 's'}`, 'success');
    reload();
  });
}

export async function remove(paths, reload) {
  if (!paths.length) return;
  let ins;
  try { ins = await A.api.post('/library/inspect', { paths }); } catch (err) { fail(err); return; }
  const body = [
    h('p', `Delete ${ins.files} file${ins.files === 1 ? '' : 's'}${ins.folders ? ` and ${ins.folders} folder${ins.folders === 1 ? '' : 's'}` : ''} for good?`),
    ins.playlists.length ? h('div.alert.alert-warning.small', `Used by playlist${ins.playlists.length === 1 ? '' : 's'}: ${ins.playlists.join(', ')}. The entries are removed.`) : null,
    ins.schedules.length ? h('div.alert.alert-danger.small', `Used by schedule${ins.schedules.length === 1 ? '' : 's'}: ${ins.schedules.join(', ')}. Those rules will have nothing to play.`) : null,
  ];
  if (!await confirmDialog({ title: 'Delete', body, confirmText: 'Delete', danger: true })) return;
  try {
    await A.api.post('/library/delete', { paths });
    toast('Deleted', 'success');
    reload();
  } catch (err) { fail(err); }
}

// addToPlaylist offers the existing playlists or a new one.
export async function addToPlaylist(files) {
  if (!files.length) return;
  let lists;
  try { lists = (await A.api.get('/playlists')).playlists; } catch (err) { fail(err); return; }
  const list = h('div.list-group.mb-3');
  const pick = async (id) => {
    try {
      const pl = await A.api.put(`/playlists/${id}`, { append: files });
      toast(`Added ${files.length} track${files.length === 1 ? '' : 's'} to ${pl.name}`, 'success');
      dlg.hide();
    } catch (err) { fail(err); }
  };
  for (const pl of lists) list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => pick(pl.id) }, icon('list-ul', 'me-2'), pl.name, h('span.text-body-secondary.small.ms-2', `${pl.count} tracks`)));
  if (!lists.length) list.append(h('div.list-group-item.text-body-secondary', 'No playlists yet.'));
  const name = h('input.form-control', { type: 'text', placeholder: 'New playlist name', required: true });
  const form = h('form.d-flex.gap-2', { onsubmit: async (e) => {
    e.preventDefault();
    try {
      const pl = await A.api.post('/playlists', { name: name.value.trim(), files });
      toast(`Playlist "${pl.name}" created with ${files.length} track${files.length === 1 ? '' : 's'}`, 'success');
      dlg.hide();
    } catch (err) { fail(err); }
  } }, name, h('button.btn.btn-outline-primary', { type: 'submit' }, 'Create'));
  const dlg = modal({ title: 'Add to playlist', body: [list, form] });
}
