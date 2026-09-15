// App shell: authentication gate, navigation, the live event stream and
// the now-playing bar. Views render into <main>.

import * as A from './api.js';
import { h, clear, icon, toast } from './dom.js';
import { route, start, stop, navigate, currentSection, refreshCurrent } from './router.js';
import { loginView } from './views/login.js';
import { nowPlayingView } from './views/nowplaying.js';
import { libraryView } from './views/library.js';
import { playlistsView } from './views/playlists.js';
import { scheduleView } from './views/schedule.js';
import { settingsView } from './views/settings.js';
import { healthView } from './views/health.js';
import { wizardView } from './views/wizard.js';
import * as upload from './upload.js';

export const state = {
  session: null,
  status: null,
  statusListeners: new Set(),
};

const sections = [
  { id: '', title: 'Now Playing', icon: 'music-note-beamed', hash: '#/' },
  { id: 'library', title: 'Library', icon: 'folder2', hash: '#/library' },
  { id: 'playlists', title: 'Playlists', icon: 'list-ul', hash: '#/playlists' },
  { id: 'schedule', title: 'Schedule', icon: 'calendar-week', hash: '#/schedule' },
  { id: 'settings', title: 'Settings', icon: 'gear', hash: '#/settings' },
];

// Bootstrap has no "auto" theme; follow the device and its changes.
const darkQuery = window.matchMedia('(prefers-color-scheme: dark)');
function applyTheme() {
  document.documentElement.dataset.bsTheme = darkQuery.matches ? 'dark' : 'light';
}
darkQuery.addEventListener('change', applyTheme);
applyTheme();

function renderNav() {
  const top = clear(document.getElementById('topnav-links'));
  const tabs = clear(document.getElementById('tab-bar'));
  for (const s of sections) {
    top.append(h('li.nav-item', h('a.nav-link', { href: s.hash, dataset: { section: s.id } }, s.title)));
    tabs.append(h('a', { href: s.hash, dataset: { section: s.id } }, icon(s.icon), h('span', s.title)));
  }
  markActive();
}

function markActive() {
  const sec = currentSection();
  for (const a of document.querySelectorAll('[data-section]')) {
    a.classList.toggle('active', a.dataset.section === sec);
  }
}
document.addEventListener('route', markActive);

// File operations the Library view delegates to the shell.
document.addEventListener('library-fileop', async (e) => {
  const { kind, paths, path, reload } = e.detail;
  if (kind === 'upload') {
    e.preventDefault();
    upload.open(paths?.[0] ?? path);
  } else if (kind === 'new_folder') {
    e.preventDefault();
    const name = prompt('New folder name:');
    if (!name) return;
    try {
      await A.api.post('/library/folders', { path: (path ? path + '/' : '') + name.trim() });
      toast(`Folder "${name.trim()}" created`, 'success');
      reload();
    } catch (err) {
      const e = err.problem?.errors?.[0];
      toast(e?.message ? `${err.message} ${e.message}${e.value ? ` Command: ${e.value}` : ''}` : err.message, 'danger', 8000);
    }
  }
});

// The Library tab shows the upload progress while a batch runs.
document.addEventListener('upload-progress', (e) => {
  const { running, done, total } = e.detail;
  for (const a of document.querySelectorAll('[data-section="library"]')) {
    let badge = a.querySelector('.upload-badge');
    if (!running) { if (badge) badge.remove(); continue; }
    if (!badge) { badge = h('span.badge.text-bg-primary.upload-badge.ms-1'); a.append(badge); }
    badge.textContent = `${done}/${total}`;
  }
});

// The event stream says what changed; the client refetches. A slow poll
// stays as a fallback for a stream that silently died.
let es = null;
let statusTimer = null;

export async function refreshStatus() {
  if (!state.session?.authenticated) return;
  try {
    state.status = await A.status();
  } catch (e) {
    if (e.status === 401) { showLogin(); return; }
    state.status = null;
  }
  renderNowBar();
  for (const fn of state.statusListeners) fn(state.status);
}

function connectEvents() {
  if (es) es.close();
  es = new EventSource('/api/v1/events');
  es.onmessage = (m) => {
    let ev;
    try { ev = JSON.parse(m.data); } catch { return; }
    if (['player', 'devices', 'health', 'schedule', 'settings'].includes(ev.type)) refreshStatus();
    if (ev.type === 'upload') document.dispatchEvent(new CustomEvent('jukem-upload-done', { detail: ev }));
    refreshCurrent(ev.type);
  };
  es.onopen = () => refreshStatus();
}

function ownerClass(owner) {
  switch (owner?.state) {
    case 'SCHEDULED': return 'owner-scheduled';
    case 'OVERRIDDEN': return 'owner-overridden';
    case 'UNAVAILABLE': return 'owner-unavailable';
    default: return 'owner-manual';
  }
}

export function renderNowBar() {
  const st = state.status;
  const bar = document.getElementById('now-bar');
  const title = document.getElementById('now-bar-title');
  const sub = document.getElementById('now-bar-sub');
  const play = document.getElementById('now-bar-play');
  bar.classList.toggle('d-none', !state.session?.authenticated);
  for (const id of ['owner-badge-top', 'owner-badge-bar']) {
    const b = document.getElementById(id);
    for (const c of [...b.classList]) if (c.startsWith('owner-')) b.classList.remove(c);
    b.classList.add('owner-badge', ownerClass(st?.owner));
    let text = st ? (st.owner?.reason || st.owner?.state || '') : 'Offline';
    if (st?.owner?.warning) text += ` · ${st.owner.warning}`;
    b.textContent = text;
    b.onclick = () => navigate('#/health');
  }
  if (!st) {
    title.textContent = 'jukem is not reachable';
    sub.textContent = '';
    return;
  }
  const p = st.player;
  const song = p.song;
  if (song) {
    title.textContent = song.title || song.file;
    sub.textContent = [song.artist, song.album].filter(Boolean).join(' · ') || song.file;
  } else if (!st.mpd_running) {
    title.textContent = 'MPD is not running';
    sub.textContent = 'jukem restarts it on its own';
  } else {
    title.textContent = 'Nothing playing';
    sub.textContent = p.queue_length ? `${p.queue_length} tracks in the queue` : 'The queue is empty';
  }
  clear(play).append(icon(p.state === 'play' ? 'pause-fill' : 'play-fill'));
  play.onclick = async () => {
    try { await A.playerAction(p.state === 'play' ? 'pause' : 'play'); } catch (e) { toast(e.message, 'danger'); }
  };
  document.getElementById('now-bar-text').onclick = () => navigate('#/');
}

function showLogin() {
  state.session = { authenticated: false };
  state.status = null;
  stop();
  if (statusTimer) { clearInterval(statusTimer); statusTimer = null; }
  if (es) { es.close(); es = null; }
  document.getElementById('now-bar').classList.add('d-none');
  loginView(document.getElementById('main'), onSignedIn);
}

async function onSignedIn(sess) {
  state.session = sess;
  A.setCSRF(sess.csrf_token);
  renderNav();
  if (!routesRegistered) registerRoutes();
  connectEvents();
  await refreshStatus();
  if (!statusTimer) statusTimer = setInterval(refreshStatus, 60000);
  start();
}

let routesRegistered = false;
function registerRoutes() {
  routesRegistered = true;
  route('/', nowPlayingView);
  route('/library', libraryView);
  route('/playlists', playlistsView);
  route('/schedule', scheduleView);
  route('/settings', settingsView);
  route('/health', healthView);
  route('/setup', wizardView);
}

export async function signOut() {
  try { await A.logout(); } catch { /* the session may be gone already */ }
  showLogin();
}

async function boot() {
  let sess;
  try {
    sess = await A.session();
  } catch (e) {
    document.getElementById('main').append(h('div.alert.alert-danger', 'jukem is not reachable: ' + e.message));
    return;
  }
  if (sess.setup_required) {
    wizardView(document.getElementById('main'), '', { firstRun: true, onSignedIn });
    return;
  }
  if (!sess.authenticated || sess.kind !== 'session') {
    showLogin();
    return;
  }
  await onSignedIn(sess);
}

boot();
