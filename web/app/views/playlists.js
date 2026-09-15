import * as A from '../api.js';
import { h, clear, icon, toast, confirmDialog, spinner, errorBox } from '../dom.js';
import { navigate } from '../router.js';
import { queueToast, libraryPicker, appendToPlaylist, fail, baseOf } from '../fileops.js';

// playlistsView lists playlists with the three queue actions, and shows
// one playlist with reorder, removal, and a library picker to add tracks.
export async function playlistsView(main, rest) {
  clear(main);
  const box = h('div.mx-auto', { style: 'max-width: 800px' }, spinner());
  main.append(box);
  const id = rest ? Number(rest) : 0;
  let sortable = null;
  let dragging = false;

  async function queue(action, playlist) {
    try { queueToast(action, await A.queueAction({ action, playlist })); } catch (e) { toast(e.message, 'danger'); }
  }
  const queueButtons = (playlist, small = true) => {
    const size = small ? 'btn-sm' : '';
    const btn = (cls, action, ic, label) => h('button', { type: 'button', class: `btn ${size} ${cls}`, onclick: () => queue(action, playlist), 'aria-label': label }, icon(ic), small ? null : ` ${label}`);
    return h('div.btn-group', { role: 'group' },
      btn('btn-primary', 'play_now', 'play-fill', 'Play Now'),
      btn('btn-outline-primary', 'play_next', 'skip-end', 'Play Next'),
      btn('btn-outline-primary', 'add', 'plus-lg', 'Add to Queue'));
  };

  async function loadList() {
    let lists;
    try { lists = (await A.api.get('/playlists')).playlists; } catch (e) { clear(box).append(errorBox(e)); return; }
    clear(box);
    const name = h('input.form-control', { type: 'text', placeholder: 'New playlist name', required: true, maxlength: 100 });
    box.append(h('div.d-flex.align-items-center.justify-content-between.mb-3', h('h1.h3.mb-0', 'Playlists')),
      h('form.d-flex.gap-2.mb-3', { onsubmit: async (e) => {
        e.preventDefault();
        try { const pl = await A.api.post('/playlists', { name: name.value.trim() }); navigate(`#/playlists/${pl.id}`); } catch (err) { fail(err); }
      } }, name, h('button.btn.btn-outline-primary', { type: 'submit' }, icon('plus-lg', 'me-1'), 'Create')));
    if (!lists.length) { box.append(h('p.text-body-secondary', 'No playlists yet. Create one here, or use "Add to playlist" in the Library.')); return; }
    const list = h('div.list-group.row-list');
    for (const pl of lists) {
      list.append(h('div.list-group-item',
        icon('list-ul', 'text-body-secondary'),
        h('a.row-main.text-decoration-none.text-body', { href: `#/playlists/${pl.id}` }, h('div.row-title', pl.name), h('div.small.text-body-secondary', `${pl.count} track${pl.count === 1 ? '' : 's'}`)),
        queueButtons(pl.id)));
    }
    box.append(list);
  }

  async function loadDetail() {
    let pl;
    try { pl = await A.api.get(`/playlists/${id}`); } catch (e) { clear(box).append(e.status === 404 ? h('p', 'No such playlist. ', h('a', { href: '#/playlists' }, 'Back to playlists')) : errorBox(e)); return; }
    clear(box);
    box.append(h('nav', { 'aria-label': 'breadcrumb' }, h('ol.breadcrumb', h('li.breadcrumb-item', h('a', { href: '#/playlists' }, 'Playlists')), h('li.breadcrumb-item.active', pl.name))));
    box.append(h('div.d-flex.flex-wrap.align-items-center.gap-2.mb-3',
      h('h1.h3.mb-0.me-auto', pl.name),
      queueButtons(pl.id, false),
      h('div.dropdown',
        h('button.btn.btn-outline-secondary', { type: 'button', 'data-bs-toggle': 'dropdown', 'aria-label': 'Playlist actions' }, icon('three-dots')),
        h('ul.dropdown-menu.dropdown-menu-end',
          h('li', h('button.dropdown-item', { type: 'button', onclick: addTracks }, icon('plus-lg', 'me-2'), 'Add tracks')),
          h('li', h('button.dropdown-item', { type: 'button', onclick: renamePl }, icon('pencil', 'me-2'), 'Rename')),
          h('li', h('hr.dropdown-divider')),
          h('li', h('button.dropdown-item.text-danger', { type: 'button', onclick: deletePl }, icon('trash', 'me-2'), 'Delete'))))));
    if (pl.missing) box.append(h('div.alert.alert-warning', `${pl.missing} entr${pl.missing === 1 ? 'y points' : 'ies point'} at files that are gone. They are skipped when the playlist plays.`));
    if (!pl.entries.length) { box.append(h('p.text-body-secondary', 'This playlist is empty. Add tracks here or from the Library.')); return; }
    const list = h('div.list-group.row-list');
    pl.entries.forEach((e, i) => {
      const name = baseOf(e.file);
      list.append(h('div', { class: `list-group-item ${e.missing ? 'list-group-item-warning' : ''}` },
        h('span.drag-handle', icon('grip-vertical')),
        h('span.text-body-secondary.small.mono', { style: 'width: 3em' }, i + 1),
        h('div.row-main', h('div.row-title', name), h('div.small.text-body-secondary.row-title', e.missing ? 'Missing: ' + e.file : e.file)),
        h('div.dropdown',
          h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'data-bs-toggle': 'dropdown', 'aria-label': `Actions for ${name}` }, icon('three-dots-vertical')),
          h('ul.dropdown-menu.dropdown-menu-end',
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => reorder(i, i - 1) }, 'Move up')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => reorder(i, i + 1) }, 'Move down')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => moveTo(i) }, 'Move to…')),
            h('li', h('hr.dropdown-divider')),
            h('li', h('button.dropdown-item.text-danger', { type: 'button', onclick: () => removeEntry(i) }, 'Remove'))))));
    });
    box.append(list);
    if (window.Sortable) {
      if (sortable) sortable.destroy();
      sortable = Sortable.create(list, {
        handle: '.drag-handle', animation: 150,
        onStart: () => { dragging = true; },
        onEnd: (ev) => { dragging = false; if (ev.oldIndex !== ev.newIndex) reorder(ev.oldIndex, ev.newIndex); },
      });
    }

    async function save(entries) {
      try { await A.api.put(`/playlists/${id}`, { entries }); } catch (err) { fail(err); }
      loadDetail();
    }
    function reorder(from, to) {
      if (!Number.isInteger(to) || to < 0 || to >= pl.entries.length || from === to) return;
      const entries = pl.entries.map((e) => e.file);
      const [item] = entries.splice(from, 1);
      entries.splice(to, 0, item);
      save(entries);
    }
    function moveTo(from) {
      const v = prompt(`Move to position (1-${pl.entries.length}):`, String(from + 1));
      if (v === null) return;
      const to = Number(v.trim()) - 1;
      if (!Number.isInteger(to) || to < 0 || to >= pl.entries.length) { toast(`Enter a number from 1 to ${pl.entries.length}.`, 'warning'); return; }
      reorder(from, to);
    }
    function removeEntry(i) {
      save(pl.entries.filter((_, j) => j !== i).map((e) => e.file));
    }
    async function renamePl() {
      const name = prompt('New name:', pl.name);
      if (!name || name.trim() === pl.name) return;
      try { await A.api.put(`/playlists/${id}`, { name: name.trim() }); loadDetail(); } catch (err) { fail(err); }
    }
    async function deletePl() {
      if (!await confirmDialog({ title: 'Delete playlist', body: `Delete "${pl.name}"? Schedule rules that use it are deleted too.`, confirmText: 'Delete', danger: true })) return;
      try { await A.api.del(`/playlists/${id}`); toast('Playlist deleted', 'success'); navigate('#/playlists'); } catch (err) { fail(err); }
    }
    function addTracks() {
      libraryPicker({ title: 'Add tracks', files: true, onPick: async (files) => {
        if (!files.length) return;
        try { await appendToPlaylist(id, files); loadDetail(); } catch (err) { fail(err); }
      } });
    }
  }

  if (id) await loadDetail(); else await loadList();
  return {
    // The list only changes through this UI. The detail refreshes its
    // missing-file flags after a library change, unless a drag is on.
    onEvent(type) { if (type === 'library' && id && !dragging) loadDetail(); },
    destroy() { if (sortable) sortable.destroy(); },
  };
}
