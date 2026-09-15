import * as A from '../api.js';
import { h, clear, icon, toast, fmtDuration, spinner, errorBox } from '../dom.js';
import { navigate } from '../router.js';

// libraryView browses folders and tracks, searches, and applies the three
// queue actions to rows, folders or a selection.
export async function libraryView(main, rest) {
  clear(main);
  const path = rest || '';
  const header = h('div.d-flex.flex-wrap.align-items-center.gap-2.mb-3');
  const crumbs = h('nav', { 'aria-label': 'breadcrumb' }, h('ol.breadcrumb.mb-0'));
  const searchInput = h('input.form-control', { type: 'search', placeholder: 'Search titles, artists, albums, file names', 'aria-label': 'Search' });
  const searchForm = h('form.d-flex.gap-1.flex-grow-1', { onsubmit: (e) => { e.preventDefault(); runSearch(searchInput.value); } },
    searchInput, h('button.btn.btn-outline-secondary', { type: 'submit', 'aria-label': 'Search' }, icon('search')));
  const tools = h('div.d-flex.gap-2');
  const listBox = h('div');
  const pager = h('div');
  const selectBar = h('div.d-none.d-flex.flex-wrap.align-items-center.gap-2.mb-2.p-2.rounded.bg-body-tertiary');
  main.append(header, selectBar, listBox, pager);
  header.append(crumbs, searchForm, tools);

  let page = 0;
  let current = null;       // last page or search result
  let selectMode = false;
  const selected = new Set();
  let storage = null;
  let preview = null;

  function renderCrumbs(p) {
    const ol = clear(crumbs.firstChild);
    ol.append(h('li.breadcrumb-item', h('a', { href: '#/library' }, icon('house'), ' Library')));
    let acc = '';
    for (const seg of p.split('/').filter(Boolean)) {
      acc = acc ? `${acc}/${seg}` : seg;
      ol.append(h('li.breadcrumb-item', h('a', { href: '#/library/' + encodeURIComponent(acc) }, seg)));
    }
  }

  function renderTools() {
    clear(tools);
    tools.append(
      h('button.btn.btn-outline-secondary', { type: 'button', class: `btn ${selectMode ? 'btn-secondary' : 'btn-outline-secondary'}`, onclick: toggleSelect }, icon('check2-square', 'me-1'), 'Select'),
      h('div.dropdown',
        h('button.btn.btn-outline-secondary', { type: 'button', 'data-bs-toggle': 'dropdown', 'aria-label': 'Folder actions' }, icon('three-dots')),
        h('ul.dropdown-menu.dropdown-menu-end',
          h('li', h('h6.dropdown-header', path || 'Whole library')),
          h('li', h('button.dropdown-item', { type: 'button', onclick: () => queueFolder('play_now') }, icon('play-fill', 'me-2'), 'Play Now')),
          h('li', h('button.dropdown-item', { type: 'button', onclick: () => queueFolder('play_next') }, icon('skip-end', 'me-2'), 'Play Next')),
          h('li', h('button.dropdown-item', { type: 'button', onclick: () => queueFolder('add') }, icon('plus-lg', 'me-2'), 'Add to Queue')),
          storage && !storage.read_only ? [
            h('li', h('hr.dropdown-divider')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => fileOps('new_folder') }, icon('folder-plus', 'me-2'), 'New folder')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => fileOps('upload') }, icon('upload', 'me-2'), 'Upload')),
          ] : null)));
  }

  function toggleSelect() {
    selectMode = !selectMode;
    selected.clear();
    renderTools();
    renderSelectBar();
    if (current) renderList(current);
  }

  function renderSelectBar() {
    clear(selectBar);
    selectBar.classList.toggle('d-none', !selectMode);
    if (!selectMode) return;
    const files = () => [...selected];
    selectBar.append(
      h('span.me-2', `${selected.size} selected`),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => { for (const e of current?.entries || []) if (e.type === 'file') selected.add(e.path); renderSelectBar(); renderList(current); } }, 'All'),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => { selected.clear(); renderSelectBar(); renderList(current); } }, 'None'),
      h('span.vr'),
      h('button.btn.btn-sm.btn-primary', { type: 'button', disabled: !selected.size, onclick: () => queueFiles('play_now', files()) }, 'Play Now'),
      h('button.btn.btn-sm.btn-outline-primary', { type: 'button', disabled: !selected.size, onclick: () => queueFiles('play_next', files()) }, 'Play Next'),
      h('button.btn.btn-sm.btn-outline-primary', { type: 'button', disabled: !selected.size, onclick: () => queueFiles('add', files()) }, 'Add to Queue'),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: !selected.size, onclick: () => fileOps('add_to_playlist', files()) }, 'Add to playlist'),
      storage && !storage.read_only ? [
        h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: !selected.size, onclick: () => fileOps('move', files()) }, 'Move'),
        h('button.btn.btn-sm.btn-outline-danger', { type: 'button', disabled: !selected.size, onclick: () => fileOps('delete', files()) }, 'Delete'),
      ] : null);
  }

  async function load() {
    clear(listBox).append(spinner());
    clear(pager);
    try {
      const [pg, st] = await Promise.all([A.api.get(`/library/browse?path=${encodeURIComponent(path)}&page=${page}`), storage ? Promise.resolve(storage) : A.api.get('/library/storage').catch(() => null)]);
      storage = st;
      current = pg;
      renderCrumbs(pg.path);
      renderTools();
      renderList(pg);
      renderPager(pg);
    } catch (e) {
      clear(listBox).append(e.status === 503 ? h('p.text-body-secondary', 'MPD is not running, so the library cannot be read.') : errorBox(e));
    }
  }

  async function runSearch(q) {
    q = q.trim();
    if (!q) { load(); return; }
    clear(listBox).append(spinner());
    clear(pager);
    try {
      const r = await A.api.get(`/library/search?q=${encodeURIComponent(q)}`);
      current = { entries: r.entries, search: q };
      renderList(current);
      if (r.limited) pager.append(h('p.small.text-body-secondary', 'Only the first 500 matches are shown. Narrow the search.'));
    } catch (e) { clear(listBox).append(errorBox(e)); }
  }

  function renderList(pg) {
    clear(listBox);
    if (!pg.entries.length) {
      listBox.append(h('p.text-body-secondary', pg.search ? 'No tracks match.' : storage?.missing ? 'The music root does not exist. Check Settings > Library.' : 'This folder is empty. Upload music or copy it into the music root and rescan.'));
      return;
    }
    if (storage?.read_only && !storage.missing && !pg.search && !path) {
      listBox.append(h('div.alert.alert-secondary.small.py-2', 'The music root is read-only. Upload and file operations are hidden.'));
    }
    const list = h('div.list-group.row-list');
    for (const e of pg.entries) {
      if (e.type === 'directory') {
        list.append(h('a.list-group-item.list-group-item-action', { href: '#/library/' + encodeURIComponent(e.path) },
          icon('folder-fill', 'text-warning'),
          h('div.row-main', h('div.row-title', e.name)),
          rowMenu(e)));
      } else {
        const isSel = selected.has(e.path);
        list.append(h('div.list-group-item', { class: `list-group-item ${isSel ? 'active' : ''}`, onclick: selectMode ? () => { if (isSel) selected.delete(e.path); else selected.add(e.path); renderSelectBar(); renderList(pg); } : null },
          selectMode ? icon(isSel ? 'check-square-fill' : 'square') : h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'aria-label': 'Preview', onclick: (ev) => { ev.stopPropagation(); togglePreview(e, ev.currentTarget); } }, icon('headphones')),
          h('div.row-main', h('div.row-title', e.title || e.name), h('div.small.text-body-secondary.row-title', [e.artist, e.album].filter(Boolean).join(' · ') || (pg.search ? e.path : e.name))),
          h('span.small.mono.text-body-secondary', fmtDuration(e.duration)),
          selectMode ? null : rowMenu(e)));
      }
    }
    listBox.append(list);
  }

  function rowMenu(e) {
    const isDir = e.type === 'directory';
    const act = (action) => (ev) => { ev.preventDefault(); ev.stopPropagation(); isDir ? queueFolder(action, e.path) : queueFiles(action, [e.path]); };
    const items = [
      h('li', h('button.dropdown-item', { type: 'button', onclick: act('play_now') }, icon('play-fill', 'me-2'), 'Play Now')),
      h('li', h('button.dropdown-item', { type: 'button', onclick: act('play_next') }, icon('skip-end', 'me-2'), 'Play Next')),
      h('li', h('button.dropdown-item', { type: 'button', onclick: act('add') }, icon('plus-lg', 'me-2'), 'Add to Queue')),
      isDir ? null : h('li', h('button.dropdown-item', { type: 'button', onclick: (ev) => { ev.preventDefault(); ev.stopPropagation(); fileOps('add_to_playlist', [e.path]); } }, icon('list-ul', 'me-2'), 'Add to playlist')),
    ];
    if (storage && !storage.read_only) {
      items.push(h('li', h('hr.dropdown-divider')),
        h('li', h('button.dropdown-item', { type: 'button', onclick: (ev) => { ev.preventDefault(); ev.stopPropagation(); fileOps('rename', [e.path]); } }, icon('pencil', 'me-2'), 'Rename')),
        h('li', h('button.dropdown-item', { type: 'button', onclick: (ev) => { ev.preventDefault(); ev.stopPropagation(); fileOps('move', [e.path]); } }, icon('folder-symlink', 'me-2'), 'Move')),
        h('li', h('button.dropdown-item.text-danger', { type: 'button', onclick: (ev) => { ev.preventDefault(); ev.stopPropagation(); fileOps('delete', [e.path]); } }, icon('trash', 'me-2'), 'Delete')));
    }
    return h('div.dropdown', { onclick: (ev) => { ev.preventDefault(); ev.stopPropagation(); } },
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'data-bs-toggle': 'dropdown', 'aria-label': 'Actions' }, icon('three-dots-vertical')),
      h('ul.dropdown-menu.dropdown-menu-end', items));
  }

  function renderPager(pg) {
    clear(pager);
    if (pg.pages <= 1) return;
    pager.append(h('div.d-flex.justify-content-between.align-items-center.mt-2',
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: pg.page === 0, onclick: () => { page--; load(); } }, 'Previous'),
      h('span.small.text-body-secondary', `${pg.page + 1} / ${pg.pages} · ${pg.total} entries`),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: pg.page >= pg.pages - 1, onclick: () => { page++; load(); } }, 'Next')));
  }

  const actionText = { play_now: 'Playing', play_next: 'Playing next', add: 'Added to the queue' };
  async function queueResult(promise, what) {
    try {
      const r = await promise;
      let msg = `${actionText[r.action] || actionText[what.action]}: ${r.added} track${r.added === 1 ? '' : 's'}`;
      if (r.truncated) msg += ' (first 20,000 only)';
      if (r.shuffle && what.action === 'play_next') msg += ' · shuffle is on, so they play next in random order';
      toast(msg, 'success');
    } catch (e) { toast(e.message, 'danger'); }
  }
  function queueFiles(action, files) {
    if (!files.length) return;
    queueResult(A.queueAction({ action, files }), { action });
  }
  function queueFolder(action, folder = path) {
    queueResult(A.queueAction({ action, folder: folder || '/' }), { action });
  }

  // fileOps arrives with the upload and file operation steps.
  function fileOps(kind, files) {
    document.dispatchEvent(new CustomEvent('library-fileop', { detail: { kind, files, path, reload: load } }));
    if (!window.jukemFileOps) toast('This action arrives in a later build step.', 'secondary');
  }

  // Preview plays through the browser, never the speakers.
  function togglePreview(e, button) {
    if (preview && preview.path === e.path) { stopPreview(); return; }
    stopPreview();
    const audio = new Audio(`/api/v1/library/preview?path=${encodeURIComponent(e.path)}`);
    audio.play().catch((err) => toast(`Preview failed: ${err.message}`, 'danger'));
    preview = { path: e.path, audio, button };
    clear(button).append(icon('stop-fill'));
    audio.addEventListener('ended', stopPreview);
  }
  function stopPreview() {
    if (!preview) return;
    preview.audio.pause();
    preview.audio.src = '';
    clear(preview.button).append(icon('headphones'));
    preview = null;
  }

  await load();
  return {
    onEvent(type) { if (type === 'library' && !current?.search) { storage = null; load(); } },
    destroy() { stopPreview(); },
  };
}
