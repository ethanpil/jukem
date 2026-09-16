// The scheduler card says who chooses the music now, and gives the controls
// that change it: stop the scheduler for a time, stop it for good, or start
// it again. Now Playing and Schedule both show it.

import * as A from './api.js';
import { h, clear, icon, toast, confirmDialog } from './dom.js';
import { state } from './main.js';

// STOPS are the choices of the Stop Scheduler group.
const STOPS = [
  ['15 min', { mode: 'timed', minutes: 15 }, 'Stop the scheduler for 15 minutes'],
  ['1 hour', { mode: 'timed', minutes: 60 }, 'Stop the scheduler for 1 hour'],
  ['Until next event', { mode: 'until_next' }, 'Stop the scheduler until its next start or end'],
];

// formatters keeps one set of Intl formatters per zone. A zone the browser
// does not know falls back to the browser's own zone.
const formatters = new Map();
function formattersFor(zone) {
  const key = zone || '';
  if (formatters.has(key)) return formatters.get(key);
  let made;
  try {
    const opts = zone ? { timeZone: zone } : {};
    made = {
      time: new Intl.DateTimeFormat([], { ...opts, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }),
      weekday: new Intl.DateTimeFormat([], { ...opts, weekday: 'short' }),
      day: new Intl.DateTimeFormat('en-CA', { ...opts, year: 'numeric', month: '2-digit', day: '2-digit' }),
    };
  } catch {
    // An unknown zone name must not stop the card from drawing.
    made = formattersFor('');
  }
  formatters.set(key, made);
  return made;
}

// automationCard returns {el, onEvent, destroy}. timeZone is the zone of the
// appliance; a host that has it already passes it and saves a request.
export function automationCard({ title = 'Scheduler', playingLabel = 'Scheduled', timeZone = null } = {}) {
  const body = h('div.auto-body');
  const chip = h('span.auto-state');
  const el = h('div.panel.clip', h('div.panel-head', h('h2.panel-title', title), chip), body);
  let zone = timeZone;
  let schedulerOn = null; // null until the settings are known
  let busy = false;
  let destroyed = false;
  let shown = '';         // the card is drawn again only when it changes

  function fmt(iso) {
    if (!iso) return '';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    const f = formattersFor(zone);
    const sameDay = f.day.format(d) === f.day.format(new Date());
    return (sameDay ? '' : f.weekday.format(d) + ' ') + f.time.format(d);
  }

  const line = (label, value) => h('div.auto-line', h('span.auto-label', label), h('b', value));

  // act runs one request at a time, so a second tap cannot send it twice.
  async function act(fn) {
    if (busy) return;
    busy = true;
    draw(state.status, true);
    try { await fn(); } finally {
      busy = false;
      draw(state.status, true);
    }
  }

  // setScheduler reads the settings again before it writes them, because
  // the whole object goes back and another client may have changed it.
  async function setScheduler(on) {
    try {
      const fresh = await A.settings();
      zone = fresh.time_zone || zone;
      await A.saveSettings({ ...fresh, scheduler_enabled: on });
      schedulerOn = on;
      toast(on ? 'Scheduler on' : 'Scheduler off', on ? 'success' : 'warning');
    } catch (e) { toast(e.message, 'danger'); }
  }

  async function stopFor(over) {
    try {
      const r = await A.createOverride(over);
      // The server gives the real end: a scheduled event can come before
      // the chosen time.
      toast(r.ends_at ? `Scheduler stopped until ${fmt(r.ends_at)}` : 'Scheduler stopped until you start it', 'warning');
    } catch (e) { toast(e.message, 'danger'); }
  }

  async function stopForGood() {
    if (!await confirmDialog({ title: 'Stop the scheduler', body: 'Nothing plays on a schedule until you start the scheduler again. The music that plays now keeps playing.', confirmText: 'Stop scheduler', danger: true })) return;
    await setScheduler(false);
  }

  async function resume() {
    try { await A.clearOverride(); toast('Scheduler started', 'success'); } catch (e) { toast(e.message, 'danger'); }
  }

  // draw redraws only when something it shows changed, so a status every
  // few seconds does not take the focus off a button.
  function draw(st, force = false) {
    if (destroyed) return;
    const o = st?.owner;
    const key = JSON.stringify([o?.state, o?.reason, o?.program, o?.since, o?.until, o?.warning, schedulerOn, zone, busy, !!st]);
    if (!force && key === shown) return;
    shown = key;
    try {
      render(st);
    } catch (e) {
      // A card that cannot draw must not stop the rest of the page.
      clear(body).append(h('div.auto-line.muted', 'The scheduler state cannot be shown: ' + e.message));
    }
  }

  function render(st) {
    clear(body);
    clear(chip);
    if (!st) {
      chip.append(h('span.chip.chip-neutral', 'Offline'));
      body.append(h('div.auto-line.muted', 'jukem is not reachable.'));
      return;
    }
    const owner = st.owner || {};
    const stopped = owner.state === 'OVERRIDDEN';
    const unavailable = owner.state === 'UNAVAILABLE';
    // The switch in the settings is what says the scheduler is off. MANUAL
    // is its owner state, but a problem hides that state behind UNAVAILABLE.
    const off = schedulerOn === false || owner.state === 'MANUAL';

    const look = off ? ['chip-neutral', 'Disabled'] : unavailable ? ['chip-danger', 'Unavailable'] : stopped ? ['chip-warn', 'Stopped'] : ['chip-accent', 'Active'];
    chip.append(h('span', { class: `chip ${look[0]}` }, look[1]));

    if (unavailable) {
      body.append(h('div.auto-line', icon('exclamation-triangle-fill'), h('span', owner.reason || 'The scheduler cannot act.')));
    } else if (off) {
      body.append(h('div.auto-line.muted', 'The scheduler is off. Nothing plays on a schedule, and nothing stops the music you start yourself.'));
    } else if (stopped) {
      if (owner.reason) body.append(h('div.auto-line.muted', owner.reason));
      if (owner.program) body.append(line('Rule when it starts again:', owner.program));
      body.append(line('Scheduler starts again:', owner.until ? fmt(owner.until) : 'when you start it'));
    } else if (owner.program) {
      body.append(line(`${playingLabel}:`, owner.program));
      if (owner.since && owner.until) body.append(line('Start Time:', `${fmt(owner.since)} to ${fmt(owner.until)}`));
    } else {
      body.append(h('div.auto-line.muted', owner.reason || 'Nothing scheduled.'));
    }
    if (owner.warning && !unavailable) body.append(h('div.auto-line', h('span.chip.chip-warn', icon('exclamation-triangle-fill'), owner.warning)));

    const actions = h('div.auto-actions');
    if (off) {
      actions.append(h('button.btn.btn-primary', { type: 'button', disabled: busy, onclick: () => act(() => setScheduler(true)) }, icon('play-circle'), 'Enable Scheduler'));
    } else {
      if (stopped) actions.append(h('button.btn.btn-soft', { type: 'button', disabled: busy, onclick: () => act(resume) }, icon('calendar-check'), 'Start the scheduler now'));
      // The scheduler cannot be stopped for a time while it cannot act at
      // all; only the switch below helps then.
      if (!unavailable) {
        const seg = h('div.seg', { role: 'group', 'aria-label': 'Stop the scheduler' }, h('span.seg-label', 'Stop Scheduler'));
        for (const [label, over, tip] of STOPS) seg.append(h('button', { type: 'button', title: tip, disabled: busy, onclick: () => act(() => stopFor(over)) }, label));
        seg.append(h('button.seg-danger', { type: 'button', title: 'Turn the scheduler off until you start it again', disabled: busy, onclick: () => act(stopForGood) }, 'Permanent Stop'));
        actions.append(seg);
      } else {
        actions.append(h('button.btn.btn-outline-danger', { type: 'button', disabled: busy, onclick: () => act(stopForGood) }, icon('power'), 'Turn the scheduler off'));
      }
    }
    body.append(actions);
    body.append(h('p.auto-note', 'A stopped scheduler leaves the music as it is. It takes over again at the end of the stop, or at its next start or end.'));
  }

  const listener = (st) => draw(st);
  state.statusListeners.add(listener);
  draw(state.status, true);
  loadSettings();

  // loadSettings learns the zone and the switch. The card draws without
  // them, with the times in the zone of this browser.
  async function loadSettings() {
    let set;
    try { set = await A.settings(); } catch { return; }
    if (destroyed) return;
    zone = set.time_zone || zone;
    schedulerOn = set.scheduler_enabled;
    draw(state.status, true);
  }

  return {
    el,
    onEvent(type) {
      if (type !== 'settings') return;
      loadSettings();
    },
    destroy() {
      destroyed = true;
      state.statusListeners.delete(listener);
    },
  };
}
