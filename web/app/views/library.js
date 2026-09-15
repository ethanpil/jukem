import * as A from '../api.js';
import { h, clear, icon, toast, fmtDuration, spinner, errorBox, dotsMenu, menuItem, menuDivider } from '../dom.js';
import * as ops from '../fileops.js';
import { state, refreshStatus } from '../main.js';

// libraryView browses folders and tracks, searches, and applies the three
// queue actions to rows, folders or a selection. The hash carries the
// folder and, after "?q=", the search, so Back and Reload keep them.
export async function libraryView(main, rest) {
  clear(main);
  let path = rest || '';
  let query = '';
  const qi = path.indexOf('?q=');
  if (qi >= 0) { query = path.slice(qi + 3); path = path.slice(0, qi); }
  const crumbs = h('nav.crumbs', { 'aria-label': 'Folder' });
  const searchInput = h('input', { type: 'search', value: query, placeholder: 'Search titles, artists, albums, file names', 'aria-label': 'Search' });
  const searchForm = h('form.search', { role: 'search', onsubmit: (e) => { e.preventDefault(); location.hash = hashFor(path, searchInput.value.trim()); } },
    searchInput, h('button', { type: 'submit', 'aria-label': 'Search' }, icon('search')));
  // The search form stays outside the buttons, which are rebuilt on every
  // load. A reload in the background must not take the focus out of it.
  const tools = h('div.tools');
  const buttons = h('div.tools');
  const scanBox = h('div');
  const selectBar = h('div.select-bar.hidden');
  const listBox = h('div');
  tools.append(searchForm, buttons);
  main.append(h('section.page.page-wide',
    h('div.lib-head', crumbs, tools),
    scanBox, selectBar,
    h('div.panel.clip', listBox)));

  let page = 0;
  let current = null;       // last page or search result
  let selectMode = false;
  const selected = new Set();
  let storage = null;
  let preview = null;
  let scanning = false;
  let scanTicker = null;
  let reloadTimer = null;

  function hashFor(p, q) {
    return '#/library' + (p ? '/' + encodeURIComponent(p) : '') + (q ? (p ? '' : '/') + '?q=' + encodeURIComponent(q) : '');
  }

  function renderCrumbs() {
    clear(crumbs);
    const segs = path.split('/').filter(Boolean);
    const atRoot = !segs.length && !query;
    crumbs.append(h('a.home', { href: '#/library', 'aria-label': 'Library', title: 'Library' }, icon('house-door-fill')));
    if (atRoot) crumbs.append(h('span.here', 'Library'));
    let acc = '';
    segs.forEach((seg, i) => {
      acc = acc ? `${acc}/${seg}` : seg;
      crumbs.append(h('span.sep', '/'));
      crumbs.append(i === segs.length - 1 && !query ? h('span.here', seg) : h('a', { href: hashFor(acc, '') }, seg));
    });
    if (query) crumbs.append(h('span.sep', '/'), h('span.here', `Search: ${query}`));
  }

  const writable = () => storage && !storage.read_only;

  // menuItems builds the action list for a target: {files} or {folder}.
  // A file row also gets the browser preview, which never uses the
  // speakers.
  function menuItems(target, entryName, entry) {
    const isDir = !!target.folder;
    const run = (fn) => (ev) => { ev.preventDefault(); fn(); };
    const items = [
      entryName ? h('li', h('h6.dropdown-header', entryName)) : null,
      entry ? menuItem('headphones', preview?.path === entry.path ? 'Stop browser preview' : 'Browser preview', run(() => togglePreview(entry))) : null,
      entry ? menuDivider() : null,
      menuItem('play-fill', 'Play Now', run(() => queueTarget('play_now', target)), 'accent'),
      menuItem('skip-end-fill', 'Play Next', run(() => queueTarget('play_next', target))),
      menuItem('plus-lg', 'Add to Queue', run(() => queueTarget('add', target))),
      isDir ? null : menuItem('list-ul', 'Add to playlist…', run(() => fileOps('add_to_playlist', target.files))),
    ];
    if (writable() && target.paths) {
      items.push(menuDivider(),
        menuItem('pencil', 'Rename', run(() => fileOps('rename', target.paths))),
        menuItem('folder-symlink', 'Move', run(() => fileOps('move', target.paths))),
        menuItem('trash', 'Delete', run(() => fileOps('delete', target.paths)), 'text-danger'));
    }
    return items;
  }

  function renderTools() {
    clear(buttons);
    buttons.append(
      h('button.btn.btn-outline-secondary.btn-icon.s36', { type: 'button', onclick: rescan, 'aria-label': 'Rescan', title: 'Scan the whole music root for new, changed and removed files' }, icon('arrow-clockwise')),
      h('button', { type: 'button', class: `btn ${selectMode ? 'btn-soft' : 'btn-outline-secondary'}`, 'aria-pressed': String(selectMode), onclick: toggleSelect }, icon('check2-square'), 'Select'),
      query ? null : dotsMenu('Folder actions', () => [
        menuItems({ folder: path || '/' }, path || 'Whole library'),
        writable() ? [menuDivider(), menuItem('folder-plus', 'New folder', () => fileOps('new_folder', [path]))] : null,
      ], 'btn-outline-secondary s36'),
      writable() && !query ? h('button.btn.btn-primary', { type: 'button', onclick: () => fileOps('upload', [path]) }, icon('cloud-upload'), 'Upload') : null);
  }

  function toggleSelect() {
    selectMode = !selectMode;
    selected.clear();
    renderTools();
    refresh();
  }

  // refresh draws the select bar and the list again. The list is empty when
  // the folder could not be read, and it stays as it is then.
  function refresh() {
    renderSelectBar();
    if (current) renderList(current);
  }

  function renderSelectBar() {
    clear(selectBar);
    selectBar.classList.toggle('hidden', !selectMode);
    if (!selectMode) return;
    const files = () => [...selected];
    const btn = (label, cls, fn, ic) => h('button', { type: 'button', class: `btn btn-sm ${cls}`, disabled: !selected.size, onclick: fn }, ic ? icon(ic) : null, label);
    selectBar.append(
      h('span.count', `${selected.size} selected`),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => { for (const e of current?.entries || []) if (e.type === 'file') selected.add(e.path); refresh(); } }, 'All on this page'),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => { selected.clear(); refresh(); } }, 'None'),
      h('span.sep'),
      btn('Play Now', 'btn-primary', () => queueTarget('play_now', { files: files() }), 'play-fill'),
      btn('Play Next', 'btn-outline-secondary', () => queueTarget('play_next', { files: files() }), 'skip-end-fill'),
      btn('Add to Queue', 'btn-outline-secondary', () => queueTarget('add', { files: files() }), 'plus-lg'),
      btn('Add to playlist', 'btn-outline-secondary', () => fileOps('add_to_playlist', files()), 'list-ul'),
      writable() ? [
        btn('Move', 'btn-outline-secondary', () => fileOps('move', files()), 'folder-symlink'),
        btn('Delete', 'btn-outline-danger', () => fileOps('delete', files()), 'trash'),
      ] : null);
  }

  async function loadStorage() {
    if (!storage) storage = await A.api.get('/library/storage').catch(() => null);
  }

  // load fetches the folder or the search and swaps the list in one step,
  // so a burst of library events does not flicker a spinner.
  async function load(showSpinner = true) {
    if (showSpinner) clear(listBox).append(spinner());
    try {
      await loadStorage();
      // The toolbar does not need MPD, so uploads work while it restarts.
      renderCrumbs();
      renderTools();
      if (query) {
        const r = await A.api.get(`/library/search?q=${encodeURIComponent(query)}`);
        current = { entries: r.entries, search: query, limited: r.limited };
      } else {
        current = await A.api.get(`/library/browse?path=${encodeURIComponent(path)}&page=${page}`);
        // The server clamps the page when the folder shrank.
        page = current.page;
      }
      renderList(current);
    } catch (e) {
      clear(listBox).append(e.status === 503 ? h('div.panel-empty', 'MPD is not running, so the library cannot be read.') : h('div.panel-body', errorBox(e)));
    }
  }

  // previewButtons holds the row button of each path. The menu can then
  // start a preview, and the row button shows the stop icon.
  const previewButtons = new Map();

  function renderList(pg) {
    clear(listBox);
    previewButtons.clear();
    if (storage?.read_only && !pg.search && !path) {
      listBox.append(h('div.strip.strip-info', icon('lock'), storage.problem || 'The music root is read-only. Upload and file operations are hidden.'));
    }
    if (!pg.entries.length) {
      listBox.append(h('div.panel-empty', pg.search ? 'No tracks match.' : storage?.missing ? `${storage.problem} Check Settings > Library.` : 'This folder is empty. Upload music or copy it into the music root and rescan.'));
      renderPager(pg);
      return;
    }
    const list = h('div.rows');
    for (const e of pg.entries) {
      if (e.type === 'directory') {
        // The row is a div with a link inside, so the menu button is not
        // nested in the anchor.
        list.append(h('div.row-item',
          icon('folder-fill', 'row-icon folder'),
          h('div.row-main', h('div.row-title.fw-semibold', h('a.row-link', { href: hashFor(e.path, '') }, e.name))),
          dotsMenu(`Actions for ${e.name}`, () => menuItems({ folder: e.path, paths: [e.path] }, e.name))));
      } else if (selectMode) {
        const isSel = selected.has(e.path);
        list.append(h('button', { type: 'button', class: `row-item ${isSel ? 'selected' : ''}`, role: 'checkbox', 'aria-checked': String(isSel),
          onclick: () => { if (isSel) selected.delete(e.path); else selected.add(e.path); refresh(); } },
          icon(isSel ? 'check-square-fill' : 'square', 'sel-box'),
          rowText(e, pg),
          h('span.row-fig', fmtDuration(e.duration))));
      } else {
        // A row that is drawn again while its preview plays keeps the stop
        // icon and the accent, so both come from the preview state.
        const playing = preview?.path === e.path;
        const previewBtn = h('button', { type: 'button', class: `btn btn-icon s30 ${playing ? 'btn-soft' : 'btn-outline-secondary'}`, 'aria-pressed': String(playing), 'aria-label': `Browser preview of ${e.name}`, title: 'Browser preview: play in this browser, not on the speakers' }, icon(playing ? 'stop-fill' : 'headphones'));
        previewBtn.onclick = () => togglePreview(e);
        previewButtons.set(e.path, previewBtn);
        list.append(h('div.row-item',
          previewBtn,
          rowText(e, pg),
          h('span.row-fig', fmtDuration(e.duration)),
          dotsMenu(`Actions for ${e.name}`, () => menuItems({ files: [e.path], paths: [e.path] }, e.title || e.name, e))));
      }
    }
    listBox.append(list);
    renderPager(pg);
  }

  function rowText(e, pg) {
    return h('div.row-main', h('div.row-title.fw-medium', e.title || e.name), h('div.row-sub', [e.artist, e.album].filter(Boolean).join(' · ') || (pg.search ? e.path : e.name)));
  }

  function renderPager(pg) {
    if (pg.search) {
      if (pg.limited) listBox.append(h('div.panel-note', icon('info-circle'), 'Only the first 500 matches are shown. Narrow the search.'));
      return;
    }
    if (pg.pages <= 1) return;
    listBox.append(h('div.pager.tinted',
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: pg.page === 0, onclick: () => { page--; load(); } }, 'Previous'),
      h('span.fig', `${pg.page + 1} / ${pg.pages} · ${pg.total} entries`),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: pg.page >= pg.pages - 1, onclick: () => { page++; load(); } }, 'Next')));
  }

  async function queueTarget(action, target) {
    const body = { action };
    if (target.folder) body.folder = target.folder;
    else if (target.files?.length) body.files = target.files;
    else return;
    try { ops.queueToast(action, await A.queueAction(body)); } catch (e) { toast(e.message, 'danger'); }
  }

  // fileOps runs a file operation and reloads the list afterwards.
  function fileOps(kind, paths) {
    const reload = () => { selected.clear(); renderSelectBar(); load(false); };
    switch (kind) {
      case 'upload': ops.openUpload(path); break;
      case 'new_folder': ops.newFolder(path, reload); break;
      case 'rename': ops.rename(paths[0], reload); break;
      case 'move': ops.move(paths, reload); break;
      case 'delete': ops.remove(paths, reload); break;
      case 'add_to_playlist': ops.addToPlaylist(paths); break;
    }
  }

  // Preview plays through the browser, never the speakers.
  function togglePreview(e) {
    if (preview && preview.path === e.path) { stopPreview(); return; }
    stopPreview();
    const audio = new Audio(`/api/v1/library/preview?path=${encodeURIComponent(e.path)}`);
    preview = { path: e.path, audio };
    markPreview(previewButtons.get(e.path), true);
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
    markPreview(previewButtons.get(p.path), false);
  }

  // markPreview shows on the row button whether its track plays.
  function markPreview(button, playing) {
    if (!button) return;
    clear(button).append(icon(playing ? 'stop-fill' : 'headphones'));
    button.classList.toggle('btn-soft', playing);
    button.classList.toggle('btn-outline-secondary', !playing);
    button.setAttribute('aria-pressed', String(playing));
  }

  // rescan starts a full scan. The status then says a scan runs.
  async function rescan() {
    try {
      await A.rescanLibrary();
      toast('Library scan started', 'success');
      refreshStatus();
    } catch (e) { toast(e.message, 'danger'); }
  }

  // reload coalesces the list reloads that a scan end and its library
  // event ask for at the same time.
  function reload() {
    clearTimeout(reloadTimer);
    reloadTimer = setTimeout(() => load(false), 0);
  }

  // onStatus shows the scan bar while MPD scans. MPD reports no progress
  // figure for a scan, so the bar moves without a figure and shows the time.
  function onStatus(st) {
    const now = !!st?.player?.updating;
    if (now && !scanning) {
      const time = h('span.mono');
      clear(scanBox).append(h('div.panel.scan-bar',
        h('div.scan-text', h('span', icon('arrow-repeat', 'me-1'), 'Scanning the library'), time),
        h('div.progress', { role: 'progressbar', 'aria-label': 'Library scan in progress' },
          h('div.progress-bar.progress-bar-striped.progress-bar-animated.w-100'))));
      const tick = () => { time.textContent = fmtDuration(state.scanSince ? (Date.now() - state.scanSince) / 1000 : 0); };
      tick();
      scanTicker = setInterval(tick, 1000);
    } else if (!now && scanning) {
      clearInterval(scanTicker);
      scanTicker = null;
      clear(scanBox);
      reload();
    }
    scanning = now;
  }

  await load();
  onStatus(state.status);
  state.statusListeners.add(onStatus);
  return {
    onEvent(type) {
      if (type === 'settings') storage = null;
      // During a scan the list reloads once, at the end.
      if ((type === 'library' && !scanning) || type === 'settings') reload();
    },
    destroy() {
      stopPreview();
      state.statusListeners.delete(onStatus);
      clearInterval(scanTicker);
      clearTimeout(reloadTimer);
    },
  };
}
