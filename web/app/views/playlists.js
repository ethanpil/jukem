import * as A from '../api.js';
import { h, clear, icon, toast, confirmDialog, modal, spinner, errorBox, fmtDuration } from '../dom.js';
import { navigate } from '../router.js';

// playlistsView lists playlists with the three queue actions, and shows
// one playlist with reorder, removal, and a library picker to add tracks.
export async function playlistsView(main, rest) {
  clear(main);
  const box = h('div.mx-auto', { style: 'max-width: 800px' }, spinner());
  main.append(box);
  const id = rest ? Number(rest) : 0;
  let sortable = null;

  const actionText = { play_now: 'Playing', play_next: 'Playing next', add: 'Added to the queue' };
  async function queue(action, playlist) {
    try {
      const r = await A.queueAction({ action, playlist });
      toast(`${actionText[action]}: ${r.added} track${r.added === 1 ? '' : 's'}${r.truncated ? ' (first 20,000 only)' : ''}`, 'success');
    } catch (e) { toast(e.message, 'danger'); }
  }
  const queueButtons = (playlist, small = true) => h('div.btn-group', { role: 'group' },
    h('button', { type: 'button', class: `btn ${small ? 'btn-sm' : ''} btn-primary`, onclick: () => queue('play_now', playlist), 'aria-label': 'Play Now' }, icon('play-fill'), small ? null : ' Play Now'),
    h('button', { type: 'button', class: `btn ${small ? 'btn-sm' : ''} btn-outline-primary`, onclick: () => queue('play_next', playlist), 'aria-label': 'Play Next' }, icon('skip-end'), small ? null : ' Play Next'),
    h('button', { type: 'button', class: `btn ${small ? 'btn-sm' : ''} btn-outline-primary`, onclick: () => queue('add', playlist), 'aria-label': 'Add to Queue' }, icon('plus-lg'), small ? null : ' Add to Queue'));

  async function loadList() {
    let lists;
    try { lists = (await A.api.get('/playlists')).playlists; } catch (e) { clear(box).append(errorBox(e)); return; }
    clear(box);
    const name = h('input.form-control', { type: 'text', placeholder: 'New playlist name', required: true, maxlength: 100 });
    box.append(h('div.d-flex.align-items-center.justify-content-between.mb-3', h('h1.h3.mb-0', 'Playlists')),
      h('form.d-flex.gap-2.mb-3', { onsubmit: async (e) => {
        e.preventDefault();
        try { const pl = await A.api.post('/playlists', { name: name.value.trim() }); navigate(`#/playlists/${pl.id}`); } catch (err) { toast(err.message, 'danger'); }
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
      const name = e.file.slice(e.file.lastIndexOf('/') + 1);
      list.append(h('div.list-group-item', { class: `list-group-item ${e.missing ? 'list-group-item-warning' : ''}`, dataset: { index: i } },
        h('span.drag-handle', icon('grip-vertical')),
        h('span.text-body-secondary.small.mono', { style: 'width: 3em' }, i + 1),
        h('div.row-main', h('div.row-title', name), h('div.small.text-body-secondary.row-title', e.missing ? 'Missing: ' + e.file : e.file)),
        h('div.dropdown',
          h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'data-bs-toggle': 'dropdown', 'aria-label': `Actions for ${name}` }, icon('three-dots-vertical')),
          h('ul.dropdown-menu.dropdown-menu-end',
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => reorder(pl, i, i - 1) }, 'Move up')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => reorder(pl, i, i + 1) }, 'Move down')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => moveTo(pl, i) }, 'Move to…')),
            h('li', h('hr.dropdown-divider')),
            h('li', h('button.dropdown-item.text-danger', { type: 'button', onclick: () => removeEntry(pl, i) }, 'Remove'))))));
    });
    box.append(list);
    if (window.Sortable) {
      if (sortable) sortable.destroy();
      sortable = Sortable.create(list, { handle: '.drag-handle', animation: 150, onEnd: (ev) => { if (ev.oldIndex !== ev.newIndex) reorder(pl, ev.oldIndex, ev.newIndex); } });
    }

    async function save(entries) {
      try { await A.api.put(`/playlists/${id}`, { entries }); loadDetail(); } catch (err) { toast(err.message, 'danger'); loadDetail(); }
    }
    function reorder(pl, from, to) {
      if (to < 0 || to >= pl.entries.length) return;
      const entries = pl.entries.map((e) => e.file);
      const [item] = entries.splice(from, 1);
      entries.splice(to, 0, item);
      save(entries);
    }
    function moveTo(pl, from) {
      const v = prompt(`Move to position (1-${pl.entries.length}):`, String(from + 1));
      if (!v) return;
      reorder(pl, from, Number(v) - 1);
    }
    function removeEntry(pl, i) {
      save(pl.entries.filter((_, j) => j !== i).map((e) => e.file));
    }
    async function renamePl() {
      const name = prompt('New name:', pl.name);
      if (!name || name.trim() === pl.name) return;
      try { await A.api.put(`/playlists/${id}`, { name: name.trim() }); loadDetail(); } catch (err) { toast(err.message, 'danger'); }
    }
    async function deletePl() {
      if (!await confirmDialog({ title: 'Delete playlist', body: `Delete "${pl.name}"? Schedule rules that use it are deleted too.`, confirmText: 'Delete', danger: true })) return;
      try { await A.api.del(`/playlists/${id}`); toast('Playlist deleted', 'success'); navigate('#/playlists'); } catch (err) { toast(err.message, 'danger'); }
    }
    function addTracks() {
      trackPicker(async (files) => {
        try { await A.api.put(`/playlists/${id}`, { append: files }); toast(`Added ${files.length} track${files.length === 1 ? '' : 's'}`, 'success'); loadDetail(); } catch (err) { toast(err.message, 'danger'); }
      });
    }
  }

  // trackPicker browses the library inside a dialog and returns the chosen
  // files or a whole folder's files.
  function trackPicker(onPick) {
    let path = '';
    const selected = new Set();
    const crumb = h('div.mono.small.mb-2');
    const list = h('div.list-group');
    const count = h('span.text-body-secondary.small');
    const addBtn = h('button.btn.btn-primary', { type: 'button', disabled: true, onclick: () => { dlg.hide(); onPick([...selected]); } }, 'Add selected');
    const addFolder = h('button.btn.btn-outline-primary', { type: 'button', onclick: async () => {
      try {
        const files = [];
        let page = 0;
        for (;;) {
          const pg = await A.api.get(`/library/browse?path=${encodeURIComponent(path)}&page=${page}`);
          for (const e of pg.entries) if (e.type === 'file') files.push(e.path);
          if (++page >= pg.pages) break;
        }
        dlg.hide();
        onPick(files);
      } catch (e) { toast(e.message, 'danger'); }
    } }, 'Add all tracks here');
    function refreshCount() { count.textContent = `${selected.size} selected`; addBtn.disabled = !selected.size; }
    async function load() {
      clear(list).append(spinner());
      try {
        const pg = await A.api.get(`/library/browse?path=${encodeURIComponent(path)}&page=0`);
        crumb.textContent = '/' + path;
        clear(list);
        if (path) list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { path = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : ''; load(); } }, icon('arrow-90deg-up', 'me-2'), '..'));
        for (const e of pg.entries) {
          if (e.type === 'directory') {
            list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { path = e.path; load(); } }, icon('folder', 'me-2'), e.name));
          } else {
            const sel = selected.has(e.path);
            list.append(h('button', { type: 'button', class: `list-group-item list-group-item-action ${sel ? 'active' : ''}`, onclick: (ev) => { if (selected.has(e.path)) selected.delete(e.path); else selected.add(e.path); ev.currentTarget.classList.toggle('active'); refreshCount(); } },
              icon(sel ? 'check-square-fill' : 'square', 'me-2'), e.title || e.name, h('span.small.ms-2.mono', fmtDuration(e.duration))));
          }
        }
        if (pg.pages > 1) list.append(h('div.list-group-item.small.text-body-secondary', `Showing the first ${pg.entries.length} of ${pg.total}. Use "Add all tracks here" for the whole folder.`));
      } catch (e) { clear(list).append(errorBox(e)); }
    }
    const dlg = modal({ title: 'Add tracks', body: [crumb, list], footer: [count, addFolder, addBtn], size: 'modal-lg' });
    refreshCount();
    load();
  }

  if (id) await loadDetail(); else await loadList();
  return {
    onEvent(type) { if (type === 'library') (id ? loadDetail() : loadList()); },
    destroy() { if (sortable) sortable.destroy(); },
  };
}
