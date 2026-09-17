// The announcements card shows the next announcement time and the names
// that play at that time. Now Playing shows it above the queue, because an
// announcement is in the queue only while it plays.

import * as A from './api.js';
import { h, clear } from './dom.js';
import { formatWhen } from './automation.js';

// nextAnnouncementCard returns {el, onEvent, destroy}. The card stays hidden
// when no announcement plays in the coming week.
export function nextAnnouncementCard() {
  const body = h('div.auto-body');
  const el = h('div.panel.clip.d-none', h('div.panel-head', h('h2.panel-title', 'Announcements')), body);
  let zone = null;
  let timer = null;
  let destroyed = false;
  let gen = 0;

  async function load() {
    const mine = ++gen;
    clearTimeout(timer);
    let next;
    try {
      next = await A.api.get('/announcements/next');
    } catch {
      // The card is a hint. When it cannot load, it is not shown.
      if (mine === gen && !destroyed) el.classList.add('d-none');
      return;
    }
    if (mine !== gen || destroyed) return;
    if (!next.at || !next.announcements.length) {
      el.classList.add('d-none');
      return;
    }
    clear(body).append(h('div.auto-line',
      h('span.auto-label', 'Next'),
      h('b', formatWhen(next.at, zone)),
      h('span', next.announcements.map((a) => a.name).join(' · '))));
    el.classList.remove('d-none');
    // After the time passes, the card shows the time after it. The card
    // loads again at least every six hours, so "today" stays correct.
    const wait = new Date(next.at).getTime() - Date.now() + 5000;
    timer = setTimeout(load, Math.min(Math.max(wait, 5000), 6 * 3600 * 1000));
  }

  // The zone of the appliance comes from the settings. Until it is known,
  // the time is in the zone of this browser.
  async function loadZone() {
    try {
      const set = await A.settings();
      if (destroyed) return;
      zone = set.time_zone || null;
    } catch { /* the browser zone stays */ }
    load();
  }

  loadZone();
  return {
    el,
    onEvent(type) {
      // A change of an announcement and the end of a play both send a
      // schedule event. A zone change sends a settings event.
      if (type === 'schedule') load();
      if (type === 'settings') loadZone();
    },
    destroy() {
      destroyed = true;
      clearTimeout(timer);
    },
  };
}
