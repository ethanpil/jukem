import * as A from '../api.js';
import { h, clear, icon, toast, fmtDuration, confirmDialog, spinner, errorBox } from '../dom.js';
import { state, refreshStatus } from '../main.js';

const PAGE = 200;

// nowPlayingView shows the track, transport, volume, overrides, the
// tracks that played last and the paged queue from the current track on.
export async function nowPlayingView(main) {
  clear(main);
  const trackBox = h('div.text-center.mb-3');
  const transport = h('div.transport.d-flex.justify-content-center.align-items-center.gap-2.mb-3');
  const seekRow = h('div.d-flex.align-items-center.gap-2.mb-3');
  const volumeRow = h('div.d-flex.align-items-center.gap-2.mb-3');
  const optionsRow = h('div.d-flex.flex-wrap.justify-content-center.gap-2.mb-4');
  const recentBox = h('div');
  const queueBox = h('div');
  main.append(h('div.mx-auto', { style: 'max-width: 720px' }, trackBox, transport, seekRow, volumeRow, optionsRow, h('h2.h5', 'Queue'), recentBox, queueBox));

  let elapsedBase = 0;
  let elapsedAt = 0;
  let playing = false;
  let seeking = false;
  let volumeDragging = false;
  const seekInput = h('input.form-range.flex-grow-1', { type: 'range', min: 0, max: 100, value: 0, step: 1, 'aria-label': 'Position' });
  const elapsedLabel = h('span.small.mono', '0:00');
  const durationLabel = h('span.small.mono', '0:00');
  seekRow.append(elapsedLabel, seekInput, durationLabel);
  seekInput.addEventListener('input', () => { seeking = true; elapsedLabel.textContent = fmtDuration(Number(seekInput.value)); });
  seekInput.addEventListener('change', async () => {
    seeking = false;
    elapsedBase = Number(seekInput.value);
    elapsedAt = Date.now();
    try { await A.seek(elapsedBase); } catch (e) { toast(e.message, 'danger'); }
  });

  const volumeInput = h('input.form-range.flex-grow-1', { type: 'range', min: 0, max: 100, value: 50, step: 1, 'aria-label': 'Volume' });
  const volumeLabel = h('span.small.mono', { style: 'width: 3em' }, '');
  volumeRow.append(icon('volume-down'), volumeInput, icon('volume-up'), volumeLabel);
  let volumeTimer = null;
  volumeInput.addEventListener('input', () => {
    volumeDragging = true;
    volumeLabel.textContent = volumeInput.value;
    clearTimeout(volumeTimer);
    volumeTimer = setTimeout(sendVolume, 150);
  });
  volumeInput.addEventListener('change', () => { volumeDragging = false; clearTimeout(volumeTimer); sendVolume(); });
  async function sendVolume() {
    const sent = Number(volumeInput.value);
    try {
      const r = await A.setVolume(sent);
      if (r.volume !== sent && !volumeDragging) {
        volumeInput.value = r.volume;
        volumeLabel.textContent = r.volume;
        toast(`Volume limited to ${r.volume} by Settings > Playback`, 'warning');
      }
    } catch (e) { toast(e.message, 'danger'); }
  }

  function render(st) {
    clear(trackBox);
    clear(transport);
    clear(optionsRow);
    if (!st) {
      trackBox.append(h('p.text-body-secondary', 'jukem is not reachable.'));
      return;
    }
    const p = st.player;
    const song = p.song;
    playing = p.state === 'play';
    elapsedBase = p.elapsed || 0;
    elapsedAt = Date.now();
    if (song) {
      trackBox.append(
        h('div.h4.mb-1.text-break', song.title || song.file),
        h('div.text-body-secondary.text-break', [song.artist, song.album].filter(Boolean).join(' · ')),
        h('div.small.text-body-secondary.mono.text-break', song.file));
      seekInput.max = Math.max(1, Math.round(song.duration || 0));
      durationLabel.textContent = fmtDuration(song.duration);
      seekInput.disabled = !song.duration;
      if (!seeking) {
        seekInput.value = Math.round(elapsedBase);
        elapsedLabel.textContent = fmtDuration(elapsedBase);
      }
    } else {
      trackBox.append(h('div.h4.mb-1', st.mpd_running ? 'Nothing playing' : 'MPD is not running'),
        h('div.text-body-secondary', st.owner?.reason || ''));
      seekInput.max = 100; seekInput.value = 0; seekInput.disabled = true;
      durationLabel.textContent = '0:00';
    }
    trackBox.append(h('div.mt-2', h('span.badge.text-bg-light.border', st.owner?.reason || st.owner?.state || ''), st.owner?.warning ? h('span.badge.text-bg-warning.ms-1', st.owner.warning) : null));
    if (p.volume >= 0 && !volumeDragging) {
      volumeInput.value = p.volume;
      volumeLabel.textContent = p.volume;
      volumeInput.disabled = false;
    } else if (p.volume < 0) {
      volumeInput.disabled = true;
      volumeLabel.textContent = '';
    }
    const act = (action) => async () => {
      try { await A.playerAction(action); } catch (e) { toast(e.message, 'danger'); }
    };
    transport.append(
      h('button.btn.btn-lg.btn-outline-secondary', { type: 'button', onclick: act('previous'), 'aria-label': 'Previous' }, icon('skip-start-fill')),
      h('button.btn.btn-lg.btn-primary.btn-play.rounded-circle', { type: 'button', onclick: act(playing ? 'pause' : 'play'), 'aria-label': playing ? 'Pause' : 'Play' }, icon(playing ? 'pause-fill' : 'play-fill')),
      h('button.btn.btn-lg.btn-outline-secondary', { type: 'button', onclick: act('stop'), 'aria-label': 'Stop' }, icon('stop-fill')),
      h('button.btn.btn-lg.btn-outline-secondary', { type: 'button', onclick: act('next'), 'aria-label': 'Next' }, icon('skip-end-fill')));
    optionsRow.append(
      h('button.btn.btn-sm', { type: 'button', class: `btn btn-sm ${p.shuffle ? 'btn-secondary' : 'btn-outline-secondary'}`, onclick: async () => {
        try { await A.setOptions({ shuffle: !p.shuffle }); } catch (e) { toast(e.message, 'danger'); }
      } }, icon('shuffle', 'me-1'), 'Shuffle'));
    if (st.owner?.state !== 'MANUAL') {
      // A hold keeps what plays now (playing, paused or stopped) and stops
      // the schedule from changing it for the chosen time.
      const hold = h('div.btn-group.btn-group-sm', { role: 'group', 'aria-label': 'Hold the schedule' },
        h('span.input-group-text.small', { title: 'Keep the current playback and stop the schedule from changing it' }, icon('pause-circle', 'me-1'), 'Hold schedule'));
      for (const [label, minutes, tip] of [['15 min', 15, 'for 15 minutes'], ['1 hour', 60, 'for 1 hour'], ['Until next event', 0, 'until the next scheduled start or end']]) {
        hold.append(h('button.btn.btn-outline-warning', { type: 'button', title: `Hold the schedule ${tip}`, onclick: () => override(minutes) }, label));
      }
      optionsRow.append(hold);
      if (st.owner?.state === 'OVERRIDDEN') {
        optionsRow.append(h('button.btn.btn-sm.btn-warning', { type: 'button', onclick: resumeSchedule }, icon('calendar-check', 'me-1'), 'Resume schedule'));
      }
    }
    if (song) {
      optionsRow.append(h('button.btn.btn-sm.btn-outline-danger', { type: 'button', onclick: () => doNotPlay(song) }, icon('slash-circle', 'me-1'), 'Do not play'));
    }
  }

  async function override(minutes) {
    try {
      await A.createOverride(minutes ? { mode: 'timed', minutes } : { mode: 'until_next' });
      toast(minutes ? `Schedule on hold for ${minutes} minutes` : 'Schedule on hold until the next scheduled event', 'warning');
    } catch (e) { toast(e.message, 'danger'); }
  }
  async function resumeSchedule() {
    try { await A.clearOverride(); toast('Schedule resumed', 'success'); } catch (e) { toast(e.message, 'danger'); }
  }
  async function doNotPlay(song) {
    if (!await confirmDialog({ title: 'Do not play', body: `Keep "${song.title || song.file}" out of all future scheduled playback?`, confirmText: 'Do not play' })) return;
    try {
      await A.addDoNotPlay(song.file, song.title);
      await A.playerAction('next');
      toast('Added to the do-not-play list', 'success');
    } catch (e) { toast(e.message, 'danger'); }
  }

  // The elapsed counter runs locally between status refreshes.
  const tick = setInterval(() => {
    if (!playing || seeking) return;
    const el = elapsedBase + (Date.now() - elapsedAt) / 1000;
    seekInput.value = Math.min(Number(seekInput.max), Math.round(el));
    elapsedLabel.textContent = fmtDuration(el);
  }, 1000);

  // Recently played: the last tracks from the play history, oldest first,
  // so the newest sits just above the current track. The history holds
  // the real play order, also when shuffle is on.
  async function loadRecent() {
    let r;
    try { r = await A.api.get('/history?limit=7'); } catch { clear(recentBox); return; }
    const current = state.status?.player?.song;
    let rows = r.rows;
    if (current && rows.length && rows[0].file === current.file) rows = rows.slice(1);
    rows = rows.slice(0, 6).reverse();
    clear(recentBox);
    if (!rows.length) return;
    const list = h('div.list-group.row-list.mb-1');
    for (const row of rows) {
      list.append(h('div.list-group-item.queue-played',
        h('span.small.mono.text-body-secondary', { style: 'width: 3.5em' }, new Date(row.started_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })),
        h('div.row-main', h('div.row-title', row.title || row.file), h('div.small.text-body-secondary.row-title', [row.artist, row.album].filter(Boolean).join(' · ') || row.file))));
    }
    recentBox.append(h('div.small.text-body-secondary.mb-1', 'Recently played'), list);
  }

  // Queue. The list follows the current track: it starts at that entry,
  // until a person pages away. "Show current" goes back.
  let offset = 0;
  let total = 0;
  let sortable = null;
  let dragging = false;
  let followCurrent = true;
  async function loadQueue() {
    if (dragging) return;
    const pos = state.status?.player?.song?.pos;
    if (followCurrent && Number.isInteger(pos)) offset = pos;
    try {
      let q = await A.queue(offset, PAGE);
      if (!q.tracks.length && q.total > 0 && offset >= q.total) {
        // The queue shrank below this page: show its last page.
        offset = Math.max(0, q.total - PAGE);
        q = await A.queue(offset, PAGE);
      }
      total = q.total;
      renderQueue(q);
    } catch (e) {
      clear(queueBox).append(e.status === 503 ? h('p.text-body-secondary', 'MPD is not running.') : errorBox(e));
    }
  }
  function renderQueue(q) {
    clear(queueBox);
    const currentId = state.status?.player?.song?.id;
    if (!q.tracks.length) {
      queueBox.append(h('p.text-body-secondary', 'The queue is empty. Add tracks from the Library.'));
      return;
    }
    const list = h('div.list-group.row-list');
    for (const t of q.tracks) {
      list.append(h('div.list-group-item', { class: `list-group-item ${t.id === currentId ? 'queue-current' : ''}`, dataset: { id: t.id } },
        h('span.drag-handle', icon('grip-vertical')),
        h('span.text-body-secondary.small.mono', { style: 'width: 3em' }, t.pos + 1),
        h('div.row-main', h('div.row-title', t.title), h('div.small.text-body-secondary.row-title', [t.artist, t.album].filter(Boolean).join(' · ') || t.file)),
        t.prio ? h('span.badge.text-bg-warning', 'next') : null,
        h('span.small.mono.text-body-secondary', fmtDuration(t.duration)),
        h('div.dropdown',
          h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'data-bs-toggle': 'dropdown', 'aria-label': 'Actions' }, icon('three-dots-vertical')),
          h('ul.dropdown-menu.dropdown-menu-end',
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => playFrom(t) }, 'Play from here')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => move(t, t.pos - 1) }, 'Move up')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => move(t, t.pos + 1) }, 'Move down')),
            h('li', h('button.dropdown-item', { type: 'button', onclick: () => moveTo(t) }, 'Move to…')),
            h('li', h('hr.dropdown-divider')),
            h('li', h('button.dropdown-item.text-danger', { type: 'button', onclick: () => remove(t) }, 'Remove'))))));
    }
    if (state.status?.player?.shuffle) {
      queueBox.append(h('div.small.text-body-secondary.mb-1', 'Shuffle is on: this is the queue order, and MPD picks the next track at random.'));
    }
    queueBox.append(list);
    const pageTo = (to) => { followCurrent = false; offset = Math.max(0, to); loadQueue(); };
    const currentPos = state.status?.player?.song?.pos;
    queueBox.append(h('div.d-flex.justify-content-between.align-items-center.mt-2.gap-2',
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: offset === 0, onclick: () => pageTo(offset - PAGE) }, 'Earlier'),
      h('span.small.text-body-secondary', `${offset + 1}–${offset + q.tracks.length} of ${total} tracks`),
      !followCurrent && Number.isInteger(currentPos) ? h('button.btn.btn-sm.btn-outline-primary', { type: 'button', onclick: () => { followCurrent = true; loadQueue(); } }, 'Show current') : null,
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: offset + q.tracks.length >= total, onclick: () => pageTo(offset + PAGE) }, 'Later')));
    if (window.Sortable) {
      if (sortable) sortable.destroy();
      sortable = Sortable.create(list, {
        handle: '.drag-handle', animation: 150,
        // An event during a drag must not rebuild the list under the finger.
        onStart: () => { dragging = true; },
        onEnd: async (ev) => {
          dragging = false;
          if (ev.oldIndex === ev.newIndex) return;
          const id = Number(ev.item.dataset.id);
          try { await A.moveQueueEntry(id, offset + ev.newIndex); } catch (e) { toast(e.message, 'danger'); loadQueue(); }
        },
      });
    }
  }
  async function playFrom(t) {
    try { await A.playQueueEntry(t.id); } catch (e) { toast(e.message, 'danger'); }
  }
  async function move(t, to) {
    if (to < 0 || to >= total) return;
    try { await A.moveQueueEntry(t.id, to); } catch (e) { toast(e.message, 'danger'); }
  }
  async function moveTo(t) {
    const v = prompt(`Move to position (1-${total}):`, String(t.pos + 1));
    if (!v) return;
    const to = Number(v) - 1;
    if (Number.isNaN(to) || to < 0 || to >= total) return;
    try { await A.moveQueueEntry(t.id, to); } catch (e) { toast(e.message, 'danger'); }
  }
  async function remove(t) {
    try { await A.removeQueueEntry(t.id); } catch (e) { toast(e.message, 'danger'); }
  }

  queueBox.append(spinner());
  render(state.status);
  const listener = (st) => render(st);
  state.statusListeners.add(listener);
  loadRecent();
  await loadQueue();
  if (!state.status) refreshStatus();

  // The queue reloads on queue changes, and on a player event only when
  // the current entry moved, so a volume drag does not rebuild the list.
  let shownSongId = state.status?.player?.song?.id;
  return {
    onEvent(type) {
      if (type === 'queue') loadQueue();
      if (type === 'player') {
        const id = state.status?.player?.song?.id;
        if (id !== shownSongId) { shownSongId = id; loadQueue(); loadRecent(); }
      }
    },
    destroy() {
      clearInterval(tick);
      state.statusListeners.delete(listener);
      if (sortable) sortable.destroy();
    },
  };
}
