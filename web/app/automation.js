// The automation card says who chooses the music now, and gives the
// controls that change it: stop the scheduler for a time, stop it for good,
// or start it again. Now Playing and Schedule both show it.

import * as A from './api.js';
import { h, clear, icon, toast, confirmDialog } from './dom.js';
import { state } from './main.js';

// STOPS are the choices of the Stop Scheduler group. Minutes of 0 means
// "until the next start or end of a window".
const STOPS = [
  ['15 min', { mode: 'timed', minutes: 15 }, 'Stop the scheduler for 15 minutes'],
  ['1 hour', { mode: 'timed', minutes: 60 }, 'Stop the scheduler for 1 hour'],
  ['Until next event', { mode: 'until_next' }, 'Stop the scheduler until its next start or end'],
];

// automationCard returns {el, destroy}. On the Schedule page it carries the
// other title and names the rule that plays.
export function automationCard({ title = 'Automation', playingLabel = 'Scheduled' } = {}) {
  const body = h('div.auto-body');
  const el = h('div.panel.clip', h('div.panel-head', h('h2.panel-title', title), h('span.auto-state')), body);
  let settings = null;

  // fmt writes an instant in the zone of the appliance, because the
  // schedule is written in that zone.
  function fmt(iso) {
    if (!iso) return '';
    const opts = { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' };
    if (settings?.time_zone) opts.timeZone = settings.time_zone;
    const d = new Date(iso);
    const today = new Intl.DateTimeFormat('en-CA', { timeZone: opts.timeZone, year: 'numeric', month: '2-digit', day: '2-digit' });
    const sameDay = today.format(d) === today.format(new Date());
    return (sameDay ? '' : new Intl.DateTimeFormat([], { timeZone: opts.timeZone, weekday: 'short' }).format(d) + ' ') +
      new Intl.DateTimeFormat([], opts).format(d);
  }

  const line = (label, value) => h('div.auto-line', h('span.auto-label', label), h('b', value));

  async function setScheduler(on) {
    try {
      if (!settings) settings = await A.settings();
      settings = await A.saveSettings({ ...settings, scheduler_enabled: on });
      toast(on ? 'Scheduler on' : 'Scheduler off', on ? 'success' : 'warning');
    } catch (e) { toast(e.message, 'danger'); }
  }

  async function stopFor(body) {
    try {
      const r = await A.createOverride(body);
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

  function render(st) {
    clear(body);
    const chip = el.querySelector('.auto-state');
    const owner = st?.owner;
    const stopped = owner?.state === 'OVERRIDDEN';
    const off = owner?.state === 'MANUAL';
    clear(chip);
    if (!st) {
      chip.append(h('span.chip.chip-neutral', 'offline'));
      body.append(h('div.auto-line.muted', 'jukem is not reachable.'));
      return;
    }
    chip.append(h('span', { class: `chip ${off ? 'chip-neutral' : stopped ? 'chip-warn' : 'chip-accent'}` }, off ? 'Disabled' : stopped ? 'Stopped' : 'Active'));

    if (off) {
      body.append(h('div.auto-line.muted', 'The scheduler is off. Nothing plays on a schedule, and nothing stops the music you start yourself.'));
    } else if (stopped) {
      if (owner.program) body.append(line('Waiting rule:', owner.program));
      body.append(line('Scheduler starts again:', owner.until ? fmt(owner.until) : 'when you start it'));
    } else if (owner?.program) {
      body.append(line(`${playingLabel}:`, owner.program));
      if (owner.since && owner.until) body.append(line('Start Time:', `${fmt(owner.since)} to ${fmt(owner.until)}`));
    } else {
      body.append(h('div.auto-line.muted', owner?.reason || 'Nothing scheduled.'));
    }
    if (owner?.warning) body.append(h('div.auto-line', h('span.chip.chip-warn', icon('exclamation-triangle-fill'), owner.warning)));

    const actions = h('div.auto-actions');
    if (off) {
      actions.append(h('button.btn.btn-primary', { type: 'button', onclick: () => setScheduler(true) }, icon('play-circle'), 'Enable Scheduler'));
    } else {
      if (stopped) actions.append(h('button.btn.btn-soft', { type: 'button', onclick: resume }, icon('calendar-check'), 'Start the scheduler now'));
      const seg = h('div.seg', { role: 'group', 'aria-label': 'Stop the scheduler' }, h('span.seg-label', 'Stop Scheduler'));
      for (const [label, over, tip] of STOPS) seg.append(h('button', { type: 'button', title: tip, onclick: () => stopFor(over) }, label));
      seg.append(h('button.seg-danger', { type: 'button', title: 'Turn the scheduler off until you start it again', onclick: stopForGood }, 'Permanent Stop'));
      actions.append(seg);
    }
    body.append(actions);
  }

  const listener = (st) => render(st);
  state.statusListeners.add(listener);
  render(state.status);
  A.settings().then((s) => { settings = s; render(state.status); }).catch(() => { /* the times then use the browser zone */ });

  return {
    el,
    async onEvent(type) {
      if (type !== 'settings') return;
      try { settings = await A.settings(); } catch { /* keep the copy */ }
      render(state.status);
    },
    destroy() { state.statusListeners.delete(listener); },
  };
}
