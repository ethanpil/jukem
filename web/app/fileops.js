// File operation dialogs: rename, move, delete, add to playlist, new
// folder, the library picker and the upload panel. The Library and
// Playlists views call these directly.

import * as A from './api.js';
import { h, clear, icon, toast, confirmDialog, modal, spinner, errorBox, fmtDuration, problemMessage } from './dom.js';
import * as upload from './upload.js';

export function fail(err) { toast(problemMessage(err.problem, err.message), 'danger', 10000); }

function parentOf(path) { return path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : ''; }
export function baseOf(path) { return path.slice(path.lastIndexOf('/') + 1); }

// queueToast reports a queue action result the same way everywhere.
const actionText = { play_now: 'Playing', play_next: 'Playing next', add: 'Added to the queue' };
export function queueToast(action, r) {
  let msg = `${actionText[action]}: ${r.added} track${r.added === 1 ? '' : 's'}`;
  if (r.truncated) msg += ' (first 20,000 only)';
  toast(msg, 'success');
}

export function openUpload(folder) { upload.open(folder); }

export async function newFolder(parent, reload) {
  const name = prompt('New folder name:');
  if (!name || !name.trim()) return;
  if (name.includes('/')) { toast('A folder name cannot contain a slash.', 'warning'); return; }
  try {
    await A.api.post('/library/folders', { path: (parent ? parent + '/' : '') + name.trim() });
    toast(`Folder "${name.trim()}" created`, 'success');
    reload();
  } catch (err) { fail(err); }
}

export async function rename(path, reload) {
  const base = baseOf(path);
  const name = prompt('New name:', base);
  if (!name || name.trim() === base) return;
  if (name.includes('/')) { toast('A name cannot contain a slash. Use Move to change the folder.', 'warning'); return; }
  const dir = parentOf(path);
  try {
    await A.api.post('/library/move', { from: path, to: (dir ? dir + '/' : '') + name.trim() });
    toast('Renamed', 'success');
    reload();
  } catch (err) { fail(err); }
}

// libraryPicker browses the library in a dialog. With files off it picks
// a folder; with files on it picks tracks or a whole folder. With single on
// it takes one file, for a setting that holds one.
export function libraryPicker({ title, start = '', files = false, single = false, onPick }) {
  let path = start;
  const selected = new Set();
  const crumb = h('div.mono.small.mb-2');
  const list = h('div.list-group');
  const count = h('span.text-body-secondary.small.me-auto');
  const pickBtn = single ? null
    : h('button.btn.btn-primary', { type: 'button', disabled: files, onclick: () => { dlg.hide(); onPick(files ? [...selected] : path); } }, files ? 'Add selected' : 'Choose this folder');
  const folderBtn = files && !single ? h('button.btn.btn-outline-primary', { type: 'button', onclick: async () => {
    try {
      const all = [];
      for (let page = 0; ; page++) {
        const pg = await A.api.get(`/library/browse?path=${encodeURIComponent(path)}&page=${page}`);
        for (const e of pg.entries) if (e.type === 'file') all.push(e.path);
        if (page + 1 >= pg.pages) break;
      }
      dlg.hide();
      onPick(all);
    } catch (e) { toast(e.message, 'danger'); }
  } }, 'Add all tracks here') : null;
  function refreshCount() {
    if (!files || single) return;
    count.textContent = `${selected.size} selected`;
    pickBtn.disabled = !selected.size;
  }
  let loadGen = 0;
  async function load() {
    const gen = ++loadGen;
    clear(list).append(spinner());
    try {
      // Folders come first, so every subfolder fits on the pages read here.
      const entries = [];
      let pages = 1;
      for (let page = 0; page < pages && page < 5; page++) {
        const pg = await A.api.get(`/library/browse?path=${encodeURIComponent(path)}&page=${page}`);
        pages = pg.pages;
        entries.push(...pg.entries);
        if (!files && pg.entries.some((e) => e.type === 'file')) break;
      }
      // A quicker second tap has moved on; its own load renders.
      if (gen !== loadGen) return;
      crumb.textContent = '/' + path;
      clear(list);
      if (path) list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { path = parentOf(path); load(); } }, icon('arrow-90deg-up', 'me-2'), '..'));
      for (const e of entries) {
        if (e.type === 'directory') {
          list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { path = e.path; load(); } }, icon('folder-fill', 'folder-icon'), e.name));
        } else if (single) {
          // One file: the tap picks it and closes the dialog.
          list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { dlg.hide(); onPick([e.path]); } },
            icon('music-note-beamed'), e.title || e.name, h('span.small.ms-2.mono', fmtDuration(e.duration))));
        } else if (files) {
          const ic = icon(selected.has(e.path) ? 'check-square-fill' : 'square', 'me-2');
          list.append(h('button', { type: 'button', class: `list-group-item list-group-item-action ${selected.has(e.path) ? 'active' : ''}`, onclick: (ev) => {
            const on = !selected.has(e.path);
            if (on) selected.add(e.path); else selected.delete(e.path);
            ev.currentTarget.classList.toggle('active', on);
            ic.className = `bi bi-${on ? 'check-square-fill' : 'square'} me-2`;
            refreshCount();
          } }, ic, e.title || e.name, h('span.small.ms-2.mono', fmtDuration(e.duration))));
        }
      }
      if (files && pages > 5) list.append(h('div.list-group-item.small.text-body-secondary', 'This folder has more tracks than the picker shows. Use "Add all tracks here" for the whole folder.'));
    } catch (e) { clear(list).append(errorBox(e)); }
  }
  const dlg = modal({ title, body: [crumb, list], footer: [count, folderBtn, pickBtn], size: files ? 'modal-lg' : '' });
  refreshCount();
  load();
}

export function move(paths, reload) {
  if (!paths.length) return;
  const start = parentOf(paths[0]);
  libraryPicker({ title: `Move ${paths.length} item${paths.length === 1 ? '' : 's'} to`, start, onPick: async (dest) => {
    if (dest === start && paths.every((p) => parentOf(p) === start)) { toast('Already in that folder', 'secondary'); return; }
    let moved = 0;
    for (const p of paths) {
      try {
        await A.api.post('/library/move', { from: p, to: (dest ? dest + '/' : '') + baseOf(p) });
        moved++;
      } catch (err) { fail(err); break; }
    }
    if (moved) toast(`Moved ${moved} item${moved === 1 ? '' : 's'}`, 'success');
    reload();
  } });
}

export async function remove(paths, reload) {
  if (!paths.length) return;
  let ins;
  try { ins = await A.api.post('/library/inspect', { paths }); } catch (err) { fail(err); return; }
  if (!ins.files && !ins.folders) { toast('Nothing to delete: the items are already gone.', 'secondary'); reload(); return; }
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

// appendToPlaylist adds files and reports it.
export async function appendToPlaylist(id, files) {
  const pl = await A.api.put(`/playlists/${id}`, { append: files });
  toast(`Added ${files.length} track${files.length === 1 ? '' : 's'} to ${pl.name}`, 'success');
  return pl;
}

// addToPlaylist offers the existing playlists or a new one.
export async function addToPlaylist(files) {
  if (!files.length) return;
  let lists;
  try { lists = (await A.api.get('/playlists')).playlists; } catch (err) { fail(err); return; }
  const list = h('div.list-group.mb-3');
  for (const pl of lists) {
    list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: async () => {
      try { await appendToPlaylist(pl.id, files); dlg.hide(); } catch (err) { fail(err); }
    } }, icon('list-ul', 'me-2'), pl.name, h('span.text-body-secondary.small.ms-2', `${pl.count} tracks`)));
  }
  if (!lists.length) list.append(h('div.list-group-item.text-body-secondary', 'No playlists yet.'));
  const name = h('input.form-control', { type: 'text', placeholder: 'New playlist name', required: true, maxlength: 100 });
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
