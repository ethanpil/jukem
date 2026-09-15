import * as A from '../api.js';
import { h, clear, icon, toast, fmtDuration, spinner, errorBox } from '../dom.js';

// libraryView browses folders and tracks, searches, and applies the three
// queue actions to rows, folders or a selection. The hash carries the
// folder and, after "?q=", the search, so Back and Reload keep them.
export async function libraryView(main, rest) {
  clear(main);
  let path = rest || '';
  let query = '';
  const qi = path.indexOf('?q=');
  if (qi >= 0) { query = path.slice(qi + 3); path = path.slice(0, qi); }
  const header = h('div.d-flex.flex-wrap.align-items-center.gap-2.mb-3');
  const crumbs = h('nav', { 'aria-label': 'breadcrumb' }, h('ol.breadcrumb.mb-0'));
  const searchInput = h('input.form-control', { type: 'search', value: query, placeholder: 'Search titles, artists, albums, file names', 'aria-label': 'Search' });
  const searchForm = h('form.d-flex.gap-1.flex-grow-1', { onsubmit: (e) => { e.preventDefault(); location.hash = hashFor(path, searchInput.value.trim()); } },
    searchInput, h('button.btn.btn-outline-secondary', { type: 'submit', 'aria-label': 'Search' }, icon('search')));
  const tools = h('div.d-flex.gap-2');
  const listBox = h('div');
  const pager = h('div');
  const selectBar = h('div.flex-wrap.align-items-center.gap-2.mb-2.p-2.rounded.bg-body-tertiary');
  main.append(header, selectBar, listBox, pager);
  header.append(crumbs, searchForm, tools);

  let page = 0;
  let current = null;       // last page or search result
  let selectMode = false;
  const selected = new Set();
  let storage = null;
  let preview = null;

  function hashFor(p, q) {
    return '#/library' + (p ? '/' + encodeURIComponent(p) : '') + (q ? (p ? '' : '/') + '?q=' + encodeURIComponent(q) : '');
  }

  function renderCrumbs() {
    const ol = clear(crumbs.firstChild);
    ol.append(h('li.breadcrumb-item', h('a', { href: '#/library' }, icon('house'), ' Library')));
    let acc = '';
    for (const seg of path.split('/').filter(Boolean)) {
      acc = acc ? `${acc}/${seg}` : seg;
      ol.append(h('li.breadcrumb-item', h('a', { href: hashFor(acc, '') }, seg)));
    }
    if (query) ol.append(h('li.breadcrumb-item.active', `Search: ${query}`));
  }

  // menuItems builds the action list for a target: {files} or {folder}.
  function menuItems(target, entryName) {
    const isDir = !!target.folder;
    const run = (fn) => (ev) => { ev.preventDefault(); fn(); };
    const item = (ic, label, fn, cls = '') => h('li', h('button', { type: 'button', class: `dropdown-item ${cls}`.trim(), onclick: run(fn) }, icon(ic, 'me-2'), label));
    const items = [
      entryName ? h('li', h('h6.dropdown-header.text-truncate', entryName)) : null,
      item('play-fill', 'Play Now', () => queueTarget('play_now', target)),
      item('skip-end', 'Play Next', () => queueTarget('play_next', target)),
      item('plus-lg', 'Add to Queue', () => queueTarget('add', target)),
      isDir ? null : item('list-ul', 'Add to playlist', () => fileOps('add_to_playlist', target.files)),
    ];
    if (storage && !storage.read_only && target.paths) {
      items.push(h('li', h('hr.dropdown-divider')),
        item('pencil', 'Rename', () => fileOps('rename', target.paths)),
        item('folder-symlink', 'Move', () => fileOps('move', target.paths)),
        item('trash', 'Delete', () => fileOps('delete', target.paths), 'text-danger'));
    }
    return items;
  }

  function menu(target, entryName, label) {
    return h('div.dropdown',
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'data-bs-toggle': 'dropdown', 'aria-label': label }, icon('three-dots-vertical')),
      h('ul.dropdown-menu.dropdown-menu-end', menuItems(target, entryName)));
  }

  function renderTools() {
    clear(tools);
    tools.append(
      h('button', { type: 'button', class: `btn ${selectMode ? 'btn-secondary' : 'btn-outline-secondary'}`, onclick: toggleSelect }, icon('check2-square', 'me-1'), 'Select'),
      query ? null : h('div.dropdown',
        h('button.btn.btn-outline-secondary', { type: 'button', 'data-bs-toggle': 'dropdown', 'aria-label': 'Folder actions' }, icon('three-dots')),
        h('ul.dropdown-menu.dropdown-menu-end',
          menuItems({ folder: path || '/' }, path || 'Whole library'),
          storage && !storage.read_only ? [
            h('li', h('hr.dropdown-divider')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => fileOps('new_folder', [path]) }, icon('folder-plus', 'me-2'), 'New folder')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => fileOps('upload', [path]) }, icon('upload', 'me-2'), 'Upload')),
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
    selectBar.classList.toggle('d-flex', selectMode);
    if (!selectMode) return;
    const files = () => [...selected];
    const btn = (label, cls, fn) => h('button', { type: 'button', class: `btn btn-sm ${cls}`, disabled: !selected.size, onclick: fn }, label);
    selectBar.append(
      h('span.me-2', `${selected.size} selected`),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => { for (const e of current?.entries || []) if (e.type === 'file') selected.add(e.path); renderSelectBar(); renderList(current); } }, 'All on this page'),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => { selected.clear(); renderSelectBar(); renderList(current); } }, 'None'),
      h('span.vr'),
      btn('Play Now', 'btn-primary', () => queueTarget('play_now', { files: files() })),
      btn('Play Next', 'btn-outline-primary', () => queueTarget('play_next', { files: files() })),
      btn('Add to Queue', 'btn-outline-primary', () => queueTarget('add', { files: files() })),
      btn('Add to playlist', 'btn-outline-secondary', () => fileOps('add_to_playlist', files())),
      storage && !storage.read_only ? [
        btn('Move', 'btn-outline-secondary', () => fileOps('move', files())),
        btn('Delete', 'btn-outline-danger', () => fileOps('delete', files())),
      ] : null);
  }

  async function loadStorage() {
    if (!storage) storage = await A.api.get('/library/storage').catch(() => null);
  }

  // load fetches the folder or the search and swaps the list in one step,
  // so a burst of library events does not flicker a spinner.
  async function load(showSpinner = true) {
    if (showSpinner) { clear(listBox).append(spinner()); clear(pager); }
    try {
      await loadStorage();
      if (query) {
        const r = await A.api.get(`/library/search?q=${encodeURIComponent(query)}`);
        current = { entries: r.entries, search: query, limited: r.limited };
      } else {
        current = await A.api.get(`/library/browse?path=${encodeURIComponent(path)}&page=${page}`);
      }
      renderCrumbs();
      renderTools();
      renderList(current);
      renderPager(current);
    } catch (e) {
      clear(listBox).append(e.status === 503 ? h('p.text-body-secondary', 'MPD is not running, so the library cannot be read.') : errorBox(e));
    }
  }

  function renderList(pg) {
    clear(listBox);
    if (!pg.entries.length) {
      listBox.append(h('p.text-body-secondary', pg.search ? 'No tracks match.' : storage?.problem ? `${storage.problem} Check Settings > Library.` : 'This folder is empty. Upload music or copy it into the music root and rescan.'));
      return;
    }
    if (storage?.read_only && !storage.problem && !pg.search && !path) {
      listBox.append(h('div.alert.alert-secondary.small.py-2', 'The music root is read-only. Upload and file operations are hidden.'));
    }
    const list = h('div.list-group.row-list');
    for (const e of pg.entries) {
      if (e.type === 'directory') {
        // The row is a div with a link inside, so the menu button is not
        // nested in the anchor.
        list.append(h('div.list-group-item',
          icon('folder-fill', 'text-warning'),
          h('a.row-main.text-decoration-none.text-body', { href: hashFor(e.path, '') }, h('div.row-title', e.name)),
          menu({ folder: e.path, paths: [e.path] }, e.name, `Actions for ${e.name}`)));
      } else if (selectMode) {
        const isSel = selected.has(e.path);
        list.append(h('button', { type: 'button', class: `list-group-item list-group-item-action text-start ${isSel ? 'active' : ''}`, role: 'checkbox', 'aria-checked': String(isSel),
          onclick: () => { if (isSel) selected.delete(e.path); else selected.add(e.path); renderSelectBar(); renderList(pg); } },
          icon(isSel ? 'check-square-fill' : 'square'),
          rowText(e, pg),
          h('span.small.mono', fmtDuration(e.duration))));
      } else {
        const previewBtn = h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'aria-label': `Preview ${e.name}` }, icon(preview?.path === e.path ? 'stop-fill' : 'headphones'));
        previewBtn.onclick = () => togglePreview(e, previewBtn);
        if (preview?.path === e.path) preview.button = previewBtn;
        list.append(h('div.list-group-item',
          previewBtn,
          rowText(e, pg),
          h('span.small.mono.text-body-secondary', fmtDuration(e.duration)),
          menu({ files: [e.path], paths: [e.path] }, e.title || e.name, `Actions for ${e.name}`)));
      }
    }
    listBox.append(list);
  }

  function rowText(e, pg) {
    return h('div.row-main', h('div.row-title', e.title || e.name), h('div.small.text-body-secondary.row-title', [e.artist, e.album].filter(Boolean).join(' · ') || (pg.search ? e.path : e.name)));
  }

  function renderPager(pg) {
    clear(pager);
    if (pg.search) {
      if (pg.limited) pager.append(h('p.small.text-body-secondary', 'Only the first 500 matches are shown. Narrow the search.'));
      return;
    }
    if (pg.pages <= 1) return;
    pager.append(h('div.d-flex.justify-content-between.align-items-center.mt-2',
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: pg.page === 0, onclick: () => { page--; load(); } }, 'Previous'),
      h('span.small.text-body-secondary', `${pg.page + 1} / ${pg.pages} · ${pg.total} entries`),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: pg.page >= pg.pages - 1, onclick: () => { page++; load(); } }, 'Next')));
  }

  const actionText = { play_now: 'Playing', play_next: 'Playing next', add: 'Added to the queue' };
  async function queueTarget(action, target) {
    const body = { action };
    if (target.folder) body.folder = target.folder;
    else if (target.files?.length) body.files = target.files;
    else return;
    try {
      const r = await A.queueAction(body);
      let msg = `${actionText[action]}: ${r.added} track${r.added === 1 ? '' : 's'}`;
      if (r.truncated) msg += ' (first 20,000 only)';
      if (r.shuffle && action === 'play_next') msg += ' · shuffle is on, so they play next in random order';
      toast(msg, 'success');
    } catch (e) { toast(e.message, 'danger'); }
  }

  // fileOps is completed by the upload and file operation steps.
  function fileOps(kind, paths) {
    const ev = new CustomEvent('library-fileop', { detail: { kind, paths, path, reload: () => load(false) }, cancelable: true });
    document.dispatchEvent(ev);
    if (!ev.defaultPrevented) toast('This action arrives in a later build step.', 'secondary');
  }

  // Preview plays through the browser, never the speakers.
  function togglePreview(e, button) {
    if (preview && preview.path === e.path) { stopPreview(); return; }
    stopPreview();
    const audio = new Audio(`/api/v1/library/preview?path=${encodeURIComponent(e.path)}`);
    preview = { path: e.path, audio, button };
    clear(button).append(icon('stop-fill'));
    audio.addEventListener('ended', stopPreview);
    // A pause before the first byte rejects play() with AbortError, which
    // is not a failure.
    audio.play().catch((err) => { if (err.name !== 'AbortError') toast(`Preview failed: ${err.message}`, 'danger'); });
  }
  function stopPreview() {
    if (!preview) return;
    const p = preview;
    preview = null;
    p.audio.pause();
    p.audio.removeAttribute('src');
    p.audio.load();
    if (p.button.isConnected) clear(p.button).append(icon('headphones'));
  }

  await load();
  return {
    onEvent(type) {
      if (type === 'settings') storage = null;
      if (type === 'library' || type === 'settings') load(false);
    },
    destroy() { stopPreview(); },
  };
}
