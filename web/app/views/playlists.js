import * as A from '../api.js';
import { h, clear, icon, toast, confirmDialog, spinner, errorBox, dotsMenu, menuItem, menuDivider } from '../dom.js';
import { navigate } from '../router.js';
import { queueToast, libraryPicker, appendToPlaylist, fail, baseOf } from '../fileops.js';

// playlistsView lists playlists with the three queue actions, and shows
// one playlist with reorder, removal, and a library picker to add tracks.
export async function playlistsView(main, rest) {
  clear(main);
  const box = h('section.page.page-md', spinner());
  main.append(box);
  const id = rest ? Number(rest) : 0;
  let sortable = null;
  let dragging = false;

  async function queue(action, playlist) {
    try { queueToast(action, await A.queueAction({ action, playlist })); } catch (e) { toast(e.message, 'danger'); }
  }
  // queueButtons are Play Now with its label, then Play Next and Add to
  // Queue as icons. The large form gives every button its label.
  const queueButtons = (playlist, large = false) => h('div.row-actions',
    h('button.btn.btn-primary', { type: 'button', class: `btn btn-primary ${large ? '' : 'btn-sm'}`, onclick: () => queue('play_now', playlist) }, icon('play-fill'), 'Play Now'),
    large
      ? h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => queue('play_next', playlist) }, icon('skip-end-fill'), 'Play Next')
      : h('button.btn.btn-outline-secondary.btn-icon.s34', { type: 'button', title: 'Play Next', 'aria-label': 'Play Next', onclick: () => queue('play_next', playlist) }, icon('skip-end-fill')),
    large
      ? h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => queue('add', playlist) }, icon('plus-lg'), 'Add to Queue')
      : h('button.btn.btn-outline-secondary.btn-icon.s34', { type: 'button', title: 'Add to Queue', 'aria-label': 'Add to Queue', onclick: () => queue('add', playlist) }, icon('plus-lg')));

  async function loadList() {
    let lists;
    try { lists = (await A.api.get('/playlists')).playlists; } catch (e) { clear(box).append(errorBox(e)); return; }
    clear(box);
    const name = h('input.form-control', { type: 'text', placeholder: 'New playlist name', required: true, maxlength: 100, 'aria-label': 'New playlist name' });
    box.append(h('div.page-head', h('h1.page-title', 'Playlists'),
      h('form.tools', { onsubmit: async (e) => {
        e.preventDefault();
        try { const pl = await A.api.post('/playlists', { name: name.value.trim() }); navigate(`#/playlists/${pl.id}`); } catch (err) { fail(err); }
      } }, name, h('button.btn.btn-primary', { type: 'submit' }, icon('plus-lg'), 'Create'))));
    const card = h('div.panel.clip');
    box.append(card);
    if (!lists.length) {
      card.append(h('div.panel-empty', 'No playlists yet. Create one here, or use "Add to playlist" in the Library.'));
      return;
    }
    const list = h('div.rows');
    for (const pl of lists) {
      list.append(h('div.row-item.tall',
        h('div.row-icon-tile', icon('music-note-list')),
        h('div.row-main', h('div.row-title.fw-semibold', h('a', { href: `#/playlists/${pl.id}` }, pl.name)), h('div.row-sub', `${pl.count} track${pl.count === 1 ? '' : 's'}`)),
        h('div.only-desktop', queueButtons(pl.id)),
        dotsMenu(`More for ${pl.name}`, [
          h('li', h('h6.dropdown-header', pl.name)),
          // A phone has no room for the queue buttons, so the menu has them.
          ...[
            menuItem('play-fill', 'Play Now', () => queue('play_now', pl.id), 'accent'),
            menuItem('skip-end-fill', 'Play Next', () => queue('play_next', pl.id)),
            menuItem('plus-lg', 'Add to Queue', () => queue('add', pl.id)),
            menuDivider(),
          ].map((li) => { li.classList.add('only-mobile-menu'); return li; }),
          menuItem('pencil-square', 'Open and edit', () => navigate(`#/playlists/${pl.id}`)),
        ], 'btn-ghost s34')));
    }
    card.append(list);
    box.append(h('p.small-note.mt-3.mb-0', 'Tip: use "Add to playlist" in the Library to put tracks into any list.'));
  }

  async function loadDetail() {
    let pl;
    try { pl = await A.api.get(`/playlists/${id}`); } catch (e) { clear(box).append(e.status === 404 ? h('div.panel.panel-empty', 'No such playlist. ', h('a', { href: '#/playlists' }, 'Back to playlists')) : errorBox(e)); return; }
    clear(box);
    box.append(h('nav.crumbs.mb-2', { 'aria-label': 'Playlist' }, h('a', { href: '#/playlists' }, icon('arrow-left', 'me-1'), 'Playlists')));
    box.append(h('div.page-head',
      h('h1.page-title.text-break', pl.name),
      h('div.tools',
        queueButtons(pl.id, true),
        dotsMenu('Playlist actions', [
          menuItem('pencil', 'Rename', renamePl),
          menuDivider(),
          menuItem('trash', 'Delete', deletePl, 'text-danger'),
        ], 'btn-outline-secondary s36'))));
    const card = h('div.panel.clip');
    box.append(card);
    card.append(h('div.panel-head', h('h2.panel-title', 'Tracks'), h('span.queue-fig', `${pl.entries.length} track${pl.entries.length === 1 ? '' : 's'}`),
      h('div.tools', h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: addTracks }, icon('plus-lg'), 'Add tracks'))));
    if (pl.missing) card.append(h('div.strip.strip-warn', icon('exclamation-triangle-fill'), `${pl.missing} entr${pl.missing === 1 ? 'y points' : 'ies point'} at files that are gone. They are skipped when the playlist plays.`));
    if (!pl.entries.length) { card.append(h('div.panel-empty', 'This playlist is empty. Add tracks here or from the Library.')); return; }
    const list = h('div.rows');
    pl.entries.forEach((e, i) => {
      const name = baseOf(e.file);
      list.append(h('div', { class: `row-item plain ${e.missing ? 'warn' : ''}` },
        h('span.drag-handle', { title: 'Drag to move' }, icon('grip-vertical')),
        h('span.row-pos', i + 1),
        h('div.row-main', h('div.row-title', name), h('div.row-sub', e.missing ? 'Missing: ' + e.file : e.file)),
        e.missing ? h('span.chip.chip-warn', 'missing') : null,
        dotsMenu(`Actions for ${name}`, [
          h('li', h('h6.dropdown-header', name)),
          menuItem('arrow-up', 'Move up', () => reorder(i, i - 1)),
          menuItem('arrow-down', 'Move down', () => reorder(i, i + 1)),
          menuItem('arrow-left-right', 'Move to…', () => moveTo(i)),
          menuDivider(),
          menuItem('x-lg', 'Remove', () => removeEntry(i), 'text-danger'),
        ], 'btn-ghost s28')));
    });
    card.append(list);
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
