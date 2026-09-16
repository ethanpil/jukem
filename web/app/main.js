// App shell: authentication gate, navigation, the live event stream and
// the now-playing bar. Views render into <main>.

import * as A from './api.js';
import { h, clear, icon, toast, logo } from './dom.js';
import { route, start, stop, navigate, currentSection, refreshCurrent } from './router.js';
import { loginView } from './views/login.js';
import { nowPlayingView } from './views/nowplaying.js';
import { libraryView } from './views/library.js';
import { playlistsView } from './views/playlists.js';
import { scheduleView } from './views/schedule.js';
import { settingsView } from './views/settings.js';
import { healthView, historyView } from './views/health.js';
import { wizardView } from './views/wizard.js';

export const state = {
  session: null,
  status: null,
  statusListeners: new Set(),
  // scanSince is when this page first saw the current library scan.
  scanSince: 0,
};

const sections = [
  { id: '', title: 'Now Playing', short: 'Now', icon: 'music-note-beamed', hash: '#/' },
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

for (const el of document.querySelectorAll('[data-logo]')) logo(el);

function renderNav() {
  const top = clear(document.getElementById('topnav-links'));
  const tabs = clear(document.getElementById('tab-bar'));
  for (const s of sections) {
    top.append(h('a.navlink', { href: s.hash, dataset: { section: s.id } }, icon(s.icon), s.title));
    tabs.append(h('a', { href: s.hash, dataset: { section: s.id } }, icon(s.icon), h('span', s.short || s.title)));
  }
  markActive();
}

// showShell shows the navigation and the now-playing bar only to a
// signed-in person.
function showShell(on) {
  for (const id of ['topnav', 'mobilenav', 'now-bar', 'tab-bar']) document.getElementById(id).classList.toggle('hidden', !on);
}

function markActive() {
  const sec = currentSection();
  for (const a of document.querySelectorAll('[data-section]')) {
    a.classList.toggle('active', a.dataset.section === sec);
  }
}
document.addEventListener('route', markActive);

// The Library tab shows the upload progress while a batch runs.
document.addEventListener('upload-progress', (e) => {
  const { running, done, total } = e.detail;
  for (const a of document.querySelectorAll('[data-section="library"]')) {
    let badge = a.querySelector('.upload-badge');
    if (!running) { if (badge) badge.remove(); continue; }
    if (!badge) { badge = h('span.upload-badge'); a.append(badge); }
    badge.textContent = `${done}/${total}`;
  }
});

// Active alerts show as a banner on every screen, with the count in the
// top bar. The Health page has the detail and the dismiss buttons.
async function refreshAlerts() {
  if (!state.session?.authenticated) return;
  let alerts = [];
  try { alerts = (await A.alerts()).alerts; } catch { return; }
  const banner = document.getElementById('alert-banner');
  clear(banner);
  banner.classList.toggle('hidden', !alerts.length);
  for (const id of ['alert-badge', 'alert-badge-mobile']) {
    const bell = document.getElementById(id);
    bell.classList.toggle('hidden', !alerts.length);
    bell.title = `${alerts.length} active alert${alerts.length === 1 ? '' : 's'}`;
    bell.querySelector('.count-dot').textContent = String(alerts.length);
  }
  if (!alerts.length) return;
  const first = alerts[0];
  banner.append(icon('exclamation-triangle-fill'), h('span.msg', first.message),
    h('a', { href: '#/health' }, alerts.length > 1 ? `${alerts.length} alerts` : 'Details'));
}

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
  const updating = !!state.status?.player?.updating;
  if (updating && !state.scanSince) state.scanSince = Date.now();
  if (!updating) state.scanSince = 0;
  renderNowBar();
  // A listener that throws must not stop the others, or the page stops
  // following the player.
  for (const fn of state.statusListeners) {
    try { fn(state.status); } catch (e) { console.error(e); }
  }
}

function connectEvents() {
  if (es) es.close();
  es = new EventSource('/api/v1/events');
  // Events are handled one after the other, and the status is refetched
  // before the view hears about the event, so the view reads fresh state.
  let chain = Promise.resolve();
  es.onmessage = (m) => {
    let ev;
    try { ev = JSON.parse(m.data); } catch { return; }
    chain = chain.then(async () => {
      // A queue edit moves the current track, and a library event starts
      // or ends a scan. The views read both from the status.
      if (['player', 'queue', 'library', 'devices', 'health', 'schedule', 'settings'].includes(ev.type)) await refreshStatus();
      if (ev.type === 'alerts') await refreshAlerts();
      if (ev.type === 'upload') document.dispatchEvent(new CustomEvent('jukem-upload-done', { detail: ev }));
      refreshCurrent(ev.type);
    }).catch(() => {});
  };
  es.onopen = () => refreshStatus();
}

// ownerShort is the one-word state for the small chip on a phone.
function ownerShort(owner) {
  switch (owner?.state) {
    case 'SCHEDULED': return 'scheduled';
    case 'OVERRIDDEN': return 'stopped';
    case 'UNAVAILABLE': return 'unavailable';
    case 'MANUAL': return 'manual';
    default: return owner?.state ? String(owner.state).toLowerCase() : 'offline';
  }
}

const ownerClasses = ['owner-scheduled', 'owner-overridden', 'owner-unavailable', 'owner-manual'];

function ownerClass(owner) {
  switch (owner?.state) {
    case 'SCHEDULED': return 'owner-scheduled';
    case 'OVERRIDDEN': return 'owner-overridden';
    case 'UNAVAILABLE': return 'owner-unavailable';
    default: return 'owner-manual';
  }
}

function renderNowBar() {
  const st = state.status;
  const bar = document.getElementById('now-bar');
  const title = document.getElementById('now-bar-title');
  const sub = document.getElementById('now-bar-sub');
  const play = document.getElementById('now-bar-play');
  bar.classList.toggle('hidden', !state.session?.authenticated);
  let text = st ? (st.owner?.reason || st.owner?.state || '') : 'Offline';
  if (st?.owner?.warning) text += ` · ${st.owner.warning}`;
  for (const id of ['owner-badge-top', 'owner-badge-bar']) {
    const b = document.getElementById(id);
    b.classList.remove(...ownerClasses);
    b.classList.add(ownerClass(st?.owner));
    const compact = b.classList.contains('compact');
    clear(b).append(h('span.dot'), h('span.label', compact ? (st ? ownerShort(st.owner) : 'offline') : text));
    b.title = `${text}. Open the health page.`;
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

document.addEventListener('jukem-unauthorized', () => { if (state.session?.authenticated) showLogin(); });

function showLogin() {
  state.session = { authenticated: false };
  state.status = null;
  stop();
  if (statusTimer) { clearInterval(statusTimer); statusTimer = null; }
  if (es) { es.close(); es = null; }
  showShell(false);
  document.getElementById('alert-banner').classList.add('hidden');
  loginView(document.getElementById('main'), onSignedIn);
}

// onSignedIn starts the app shell. An unfinished setup opens the wizard.
async function onSignedIn(sess) {
  state.session = sess;
  A.setCSRF(sess.csrf_token);
  renderNav();
  showShell(true);
  if (!routesRegistered) registerRoutes();
  // The hash changes before the router starts, so the change event that
  // follows is ignored and the wizard mounts once.
  if (sess.setup_complete === false && location.hash !== '#/setup') location.hash = '#/setup';
  connectEvents();
  await refreshStatus();
  refreshAlerts();
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
  route('/history', historyView);
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
    document.getElementById('main').append(h('div.page.page-sm', h('div.alert.alert-danger', icon('x-circle-fill'), h('div', 'jukem is not reachable: ' + e.message))));
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
