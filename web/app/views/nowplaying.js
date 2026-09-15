import * as A from '../api.js';
import { h, clear, icon, toast, fmtDuration, confirmDialog, spinner, errorBox } from '../dom.js';
import { state, refreshStatus } from '../main.js';

const PAGE = 200;

// nowPlayingView shows the track, the transport, the volume and the holds.
// Below them are the last tracks that played and the queue.
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
      // A hold keeps the music as it is now, playing or paused. The
      // schedule does not change it until the hold ends. A timed hold also
      // ends at the next scheduled start or end, when that comes first.
      const hold = h('div.btn-group.btn-group-sm', { role: 'group', 'aria-labelledby': 'hold-label' });
      for (const [label, minutes] of [['15 min', 15], ['1 hour', 60], ['Until next event', 0]]) {
        hold.append(h('button.btn.btn-outline-warning', { type: 'button', onclick: () => override(minutes) }, label));
      }
      optionsRow.append(h('div.d-flex.flex-wrap.align-items-center.justify-content-center.gap-2',
        h('span.small.text-body-secondary#hold-label', icon('pause-circle', 'me-1'), 'Hold the schedule:'), hold),
        h('div.w-100.text-center.small.text-body-secondary', 'A hold keeps the music as it is now. The schedule takes over again at the end of the hold or at its next start or end.'));
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
      const r = await A.createOverride(minutes ? { mode: 'timed', minutes } : { mode: 'until_next' });
      // The server gives the real end: the next scheduled event can come
      // before the chosen time.
      const until = r.ends_at ? new Date(r.ends_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '';
      toast(until ? `Schedule on hold until ${until}` : 'Schedule on hold until you resume it', 'warning');
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

  // Recently played shows the last tracks from the play history, oldest
  // first. The newest is then directly above the current track. The
  // history has the real play order, also with shuffle on.
  async function loadRecent() {
    let r;
    try { r = await A.history(0, 7); } catch { clear(recentBox); return; }
    const current = state.status?.player?.song;
    let rows = r.rows;
    if (current && rows.length && rows[0].file === current.file) rows = rows.slice(1);
    rows = rows.slice(0, 6).reverse();
    clear(recentBox);
    if (!rows.length) return;
    const list = h('div.list-group.row-list.mb-1');
    const today = new Date().toDateString();
    for (const row of rows) {
      const at = new Date(row.started_at);
      // A track from an earlier day also shows the weekday.
      const when = at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      list.append(h('div.list-group-item',
        h('span.small.mono.text-body-secondary.text-nowrap', { style: 'min-width: 5.5em' }, at.toDateString() === today ? when : `${at.toLocaleDateString([], { weekday: 'short' })} ${when}`),
        h('div.row-main', h('div.row-title.text-body-secondary', row.title || row.file), h('div.small.text-body-secondary.row-title', [row.artist, row.album].filter(Boolean).join(' · ') || row.file))));
    }
    recentBox.append(h('div.small.text-body-secondary.mb-1', 'Recently played'), list);
  }

  // Queue. The list starts at the current track and follows it. With
  // shuffle on, MPD picks any track next, so the list shows the whole page
  // that holds the current track. When a person pages to Earlier or Later,
  // the list stays there. Show current goes back to the current track.
  let offset = 0;
  let total = 0;
  let sortable = null;
  let dragging = false;
  let followCurrent = true;
  let queueGen = 0;
  async function loadQueue() {
    if (dragging) return;
    // Only the newest load renders, so an older answer cannot replace it.
    const gen = ++queueGen;
    if (followCurrent) {
      const pos = state.status?.player?.song?.pos;
      if (!Number.isInteger(pos)) offset = 0;
      else offset = state.status.player.shuffle ? Math.floor(pos / PAGE) * PAGE : pos;
    }
    let off = offset;
    try {
      let q = await A.queue(off, PAGE);
      if (!q.tracks.length && q.total > 0 && off >= q.total) {
        // The queue shrank below this page: show its last page.
        off = Math.max(0, q.total - PAGE);
        q = await A.queue(off, PAGE);
      }
      if (gen !== queueGen || dragging) return;
      offset = off;
      total = q.total;
      renderQueue(q, off);
    } catch (e) {
      clear(queueBox).append(e.status === 503 ? h('p.text-body-secondary', 'MPD is not running.') : errorBox(e));
    }
  }
  // renderQueue draws a list that starts at queue position off. A drag in
  // it counts positions from off.
  function renderQueue(q, off) {
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
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: off === 0, onclick: () => pageTo(off - PAGE) }, 'Earlier'),
      h('span.small.text-body-secondary', `${off + 1}–${off + q.tracks.length} of ${total} ${total === 1 ? 'track' : 'tracks'}`),
      !followCurrent && Number.isInteger(currentPos) ? h('button.btn.btn-sm.btn-outline-primary', { type: 'button', onclick: () => { followCurrent = true; loadQueue(); } }, 'Show current') : null,
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: off + q.tracks.length >= total, onclick: () => pageTo(off + PAGE) }, 'Later')));
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
          try { await A.moveQueueEntry(id, off + ev.newIndex); } catch (e) { toast(e.message, 'danger'); loadQueue(); }
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
  let shownShuffle = !!state.status?.player?.shuffle;
  return {
    onEvent(type) {
      if (type === 'queue') loadQueue();
      if (type === 'player') {
        const id = state.status?.player?.song?.id;
        const shuffle = !!state.status?.player?.shuffle;
        if (id !== shownSongId) { shownSongId = id; shownShuffle = shuffle; loadQueue(); loadRecent(); }
        else if (shuffle !== shownShuffle) { shownShuffle = shuffle; loadQueue(); }
      }
    },
    destroy() {
      clearInterval(tick);
      state.statusListeners.delete(listener);
      if (sortable) sortable.destroy();
    },
  };
}
