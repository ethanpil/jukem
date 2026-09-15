import * as A from '../api.js';
import { h, clear, icon, toast, fmtDuration, confirmDialog, spinner, errorBox, dotsMenu, menuItem, menuDivider } from '../dom.js';
import { state, refreshStatus } from '../main.js';

const PAGE = 200;

// nowPlayingView shows the player card: the track, the transport, the
// position, the volume and the holds. Below it is the queue card, with the
// last tracks that played above the queue.
export async function nowPlayingView(main) {
  clear(main);
  const trackBox = h('div.player-track');
  const transport = h('div.transport');
  const seekRow = h('div.seek-row');
  const volumeRow = h('div.volume-row');
  const optionsRow = h('div.options-row');
  const ownerRow = h('div.owner-row');
  const queueFig = h('span.queue-fig');
  const recentBox = h('div');
  const queueBox = h('div');
  main.append(h('section.page.page-narrow.stack',
    h('div.panel.panel-pad', trackBox, transport, seekRow, volumeRow, ownerRow, optionsRow),
    h('div.panel.clip',
      h('div.panel-head', h('h2.panel-title', 'Queue'), queueFig),
      recentBox, queueBox)));

  let elapsedBase = 0;
  let elapsedAt = 0;
  let playing = false;
  let seeking = false;
  let volumeDragging = false;
  const seekInput = h('input.form-range.flex-1', { type: 'range', min: 0, max: 100, value: 0, step: 1, 'aria-label': 'Position' });
  const elapsedLabel = h('span.fig', '0:00');
  const durationLabel = h('span.fig', '0:00');
  seekRow.append(elapsedLabel, seekInput, durationLabel);
  seekInput.addEventListener('input', () => { seeking = true; elapsedLabel.textContent = fmtDuration(Number(seekInput.value)); });
  seekInput.addEventListener('change', async () => {
    seeking = false;
    elapsedBase = Number(seekInput.value);
    elapsedAt = Date.now();
    try { await A.seek(elapsedBase); } catch (e) { toast(e.message, 'danger'); }
  });

  const volumeInput = h('input.form-range.flex-1', { type: 'range', min: 0, max: 100, value: 50, step: 1, 'aria-label': 'Volume' });
  const volumeLabel = h('span.fig', '');
  volumeRow.append(icon('volume-down'), volumeInput, icon('volume-up'), volumeLabel);
  const showVolume = (v) => { volumeLabel.textContent = v === '' ? '' : `${v}%`; };
  let volumeTimer = null;
  volumeInput.addEventListener('input', () => {
    volumeDragging = true;
    showVolume(volumeInput.value);
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
        showVolume(r.volume);
        toast(`Volume limited to ${r.volume} by Settings > Playback`, 'warning');
      }
    } catch (e) { toast(e.message, 'danger'); }
  }

  function render(st) {
    clear(trackBox);
    clear(transport);
    clear(optionsRow);
    if (!st) {
      clear(ownerRow);
      trackBox.append(h('div.art', icon('wifi-off')), h('div.flex-1', h('div.track-title', 'jukem is not reachable'), h('div.track-sub', 'The page tries again on its own.')));
      return;
    }
    const p = st.player;
    const song = p.song;
    playing = p.state === 'play';
    elapsedBase = p.elapsed || 0;
    elapsedAt = Date.now();
    if (song) {
      trackBox.append(h('div.art', icon('vinyl-fill')),
        h('div.flex-1',
          h('div.track-title', { title: song.title || song.file }, song.title || song.file),
          h('div.track-sub', [song.artist, song.album].filter(Boolean).join(' · ')),
          h('div.track-file', { title: song.file }, song.file)));
      seekInput.max = Math.max(1, Math.round(song.duration || 0));
      durationLabel.textContent = fmtDuration(song.duration);
      seekInput.disabled = !song.duration;
      if (!seeking) {
        seekInput.value = Math.round(elapsedBase);
        elapsedLabel.textContent = fmtDuration(elapsedBase);
      }
    } else {
      trackBox.append(h('div.art', icon(st.mpd_running ? 'vinyl' : 'exclamation-triangle')),
        h('div.flex-1',
          h('div.track-title', st.mpd_running ? 'Nothing playing' : 'MPD is not running'),
          h('div.track-sub', st.mpd_running ? (p.queue_length ? `${p.queue_length} tracks in the queue` : 'The queue is empty. Add tracks from the Library.') : 'jukem restarts it on its own')));
      seekInput.max = 100; seekInput.value = 0; seekInput.disabled = true;
      elapsedLabel.textContent = '0:00';
      durationLabel.textContent = '0:00';
    }
    if (p.volume >= 0 && !volumeDragging) {
      volumeInput.value = p.volume;
      showVolume(p.volume);
      volumeInput.disabled = false;
    } else if (p.volume < 0) {
      volumeInput.disabled = true;
      showVolume('');
    }
    const act = (action) => async () => {
      try { await A.playerAction(action); } catch (e) { toast(e.message, 'danger'); }
    };
    const round = (ic, label, action) => h('button.btn.btn-outline-secondary.btn-round', { type: 'button', onclick: act(action), 'aria-label': label, title: label }, icon(ic));
    transport.append(
      round('skip-start-fill', 'Previous', 'previous'),
      h('button.btn.btn-primary.btn-round.btn-play', { type: 'button', onclick: act(playing ? 'pause' : 'play'), 'aria-label': playing ? 'Pause' : 'Play' }, icon(playing ? 'pause-fill' : 'play-fill')),
      round('stop-fill', 'Stop', 'stop'),
      round('skip-end-fill', 'Next', 'next'));

    optionsRow.append(
      h('button', { type: 'button', class: `btn btn-sm ${p.shuffle ? 'btn-soft' : 'btn-outline-secondary'}`, 'aria-pressed': String(!!p.shuffle), onclick: async () => {
        try { await A.setOptions({ shuffle: !p.shuffle }); } catch (e) { toast(e.message, 'danger'); }
      } }, icon('shuffle'), 'Shuffle'));
    if (st.owner?.state !== 'MANUAL') {
      // A hold keeps the music as it is now, playing or paused. The
      // schedule does not change it until the hold ends. A timed hold also
      // ends at the next scheduled start or end, when that comes first.
      const why = 'A hold keeps the music as it is now. The schedule takes over again at the end of the hold or at its next start or end.';
      const hold = h('div.seg', { role: 'group', 'aria-label': 'Hold the schedule', title: why }, h('span.seg-label', 'Hold schedule'));
      for (const [label, minutes, tip] of [['15 min', 15, 'Hold the schedule for 15 minutes'], ['1 hour', 60, 'Hold the schedule for 1 hour'], ['Until next event', 0, 'Hold the schedule until its next start or end']]) {
        hold.append(h('button', { type: 'button', title: tip, onclick: () => override(minutes) }, label));
      }
      optionsRow.append(hold);
      if (st.owner?.state === 'OVERRIDDEN') {
        optionsRow.append(h('button.btn.btn-sm.btn-warning', { type: 'button', onclick: resumeSchedule }, icon('calendar-check'), 'Resume schedule'));
      }
      // The buttons need the sentence beside them, because a phone shows no
      // tooltip.
      optionsRow.append(h('p.options-note', why));
    }
    if (song) {
      optionsRow.append(h('button.btn.btn-sm.btn-outline-danger.push', { type: 'button', onclick: () => doNotPlay(song) }, icon('slash-circle'), 'Do not play'));
    }
    // The owner line says who plays the music and what is wrong with it. The
    // top bar holds the same words, but it is one word wide on a phone.
    clear(ownerRow);
    if (st.owner?.reason) ownerRow.append(h('span.owner-reason', icon(ownerIcon(st.owner.state)), st.owner.reason));
    if (st.owner?.warning) ownerRow.append(h('span.chip.chip-warn', icon('exclamation-triangle-fill'), st.owner.warning));
  }

  function ownerIcon(state) {
    switch (state) {
      case 'SCHEDULED': return 'calendar-week';
      case 'OVERRIDDEN': return 'pause-circle';
      case 'UNAVAILABLE': return 'exclamation-triangle';
      default: return 'hand-index';
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
    recentBox.append(h('div.section-label', 'Recently played'));
    const today = new Date().toDateString();
    // The time column is wider when a row also shows the weekday.
    const older = rows.some((row) => new Date(row.started_at).toDateString() !== today);
    for (const row of rows) {
      const at = new Date(row.started_at);
      // A track from an earlier day also shows the weekday.
      const when = at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      const label = at.toDateString() === today ? when : `${at.toLocaleDateString([], { weekday: 'short' })} ${when}`;
      recentBox.append(h('div.row-item.plain.dim',
        h('span', { class: `row-time nowrap ${older ? 'wide' : ''}` }, label),
        h('div.row-main', h('div.row-title', row.title || row.file), h('div.row-sub', [row.artist, row.album].filter(Boolean).join(' · ') || row.file))));
    }
    recentBox.append(h('div.divider'));
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
      queueFig.textContent = '';
      clear(queueBox).append(e.status === 503 ? h('div.panel-empty', 'MPD is not running.') : h('div.panel-body', errorBox(e)));
    }
  }
  // renderQueue draws a list that starts at queue position off. A drag in
  // it counts positions from off.
  function renderQueue(q, off) {
    clear(queueBox);
    queueFig.textContent = `${total} ${total === 1 ? 'track' : 'tracks'}`;
    const currentId = state.status?.player?.song?.id;
    if (!q.tracks.length) {
      queueBox.append(h('div.panel-empty', 'The queue is empty. Add tracks from the Library.'));
      return;
    }
    const list = h('div.rows');
    for (const t of q.tracks) {
      const isCurrent = t.id === currentId;
      list.append(h('div', { class: `row-item plain ${isCurrent ? 'current' : ''}`, dataset: { id: t.id } },
        h('span.drag-handle', { title: 'Drag to move' }, icon('grip-vertical')),
        h('span.row-pos', t.pos + 1),
        h('div.row-main', h('div.row-title', t.title), h('div.row-sub', [t.artist, t.album].filter(Boolean).join(' · ') || t.file)),
        isCurrent ? h('span.chip.chip-accent.only-desktop', 'now playing') : null,
        t.prio ? h('span.chip.chip-warn', 'next') : null,
        h('span.row-fig', fmtDuration(t.duration)),
        dotsMenu(`Actions for ${t.title}`, [
          h('li', h('h6.dropdown-header', t.title)),
          menuItem('play-fill', 'Play from here', () => playFrom(t)),
          menuItem('arrow-up', 'Move up', () => move(t, t.pos - 1)),
          menuItem('arrow-down', 'Move down', () => move(t, t.pos + 1)),
          menuItem('arrow-left-right', 'Move to…', () => moveTo(t)),
          menuDivider(),
          menuItem('x-lg', 'Remove', () => remove(t), 'text-danger'),
        ], 'btn-ghost s28')));
    }
    queueBox.append(list);
    if (state.status?.player?.shuffle) {
      queueBox.append(h('div.panel-note', icon('shuffle'), 'Shuffle is on: this is the queue order, and MPD picks the next track at random.'));
    }
    const pageTo = (to) => { followCurrent = false; offset = Math.max(0, to); loadQueue(); };
    const currentPos = state.status?.player?.song?.pos;
    queueBox.append(h('div.pager',
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', disabled: off === 0, onclick: () => pageTo(off - PAGE) }, 'Earlier'),
      h('span.fig', `${off + 1}–${off + q.tracks.length} of ${total} ${total === 1 ? 'track' : 'tracks'}`),
      !followCurrent && Number.isInteger(currentPos) ? h('button.btn.btn-sm.btn-soft', { type: 'button', onclick: () => { followCurrent = true; loadQueue(); } }, 'Show current') : null,
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
