import * as A from '../api.js';
import { h, clear, icon, confirmDialog, modal, spinner, errorBox } from '../dom.js';
import { libraryPicker, fail } from '../fileops.js';
import { automationCard } from '../automation.js';
import { announcementsPanel } from '../announcements.js';
import { fmtHM, hour12, timeField } from '../time.js';

// The week starts on Sunday. The bits are the API's: Monday is 1, Sunday is 64.
const DAYS = [['Sun', 64], ['Mon', 1], ['Tue', 2], ['Wed', 4], ['Thu', 8], ['Fri', 16], ['Sat', 32]];

// scheduleView shows the week of expanded intervals, the rule list with
// enable switches, the rule editor, and the exceptions calendar.
export async function scheduleView(main) {
  clear(main);
  const box = h('section.page.page-wide', spinner());
  main.append(box);
  // The status card says whether the scheduler runs, and what it plays now.
  // It is made when the zone is known, so it asks for no settings of its own.
  let status = null;
  const announcements = announcementsPanel();
  const weekBox = h('div.panel.panel-pad');
  const rulesBox = h('div.panel.clip');
  const excBox = h('div.panel.clip');
  let weekOffset = 0; // weeks from the current one
  let playlists = [];
  let tz = 'UTC';
  let defaultShuffle = false;
  let weekGen = 0;

  async function loadAll() {
    try { playlists = (await A.api.get('/playlists')).playlists; } catch { playlists = []; }
    try {
      const set = await A.settings();
      tz = set.time_zone;
      defaultShuffle = set.default_shuffle;
    } catch { /* the zone arrives with the intervals */ }
    status = automationCard({ title: 'Scheduler Status', playingLabel: 'Currently Playing', timeZone: tz });
    clear(box).append(
      h('div.page-head', h('h1.page-title', 'Schedule'),
        h('div.tools', h('button.btn.btn-primary.raised', { type: 'button', onclick: () => ruleEditor(null) }, icon('plus-lg'), 'New rule'))),
      h('div.stack', status.el, weekBox, rulesBox, excBox, announcements.el));
    await Promise.all([loadWeek(), loadRules(), loadExceptions(), announcements.load()]);
  }

  // localDate gives the YYYY-MM-DD of an instant in the configured zone.
  function localDate(d, zone) {
    const p = Object.fromEntries(new Intl.DateTimeFormat('en-CA', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(d).map((x) => [x.type, x.value]));
    return `${p.year}-${p.month}-${p.day}`;
  }

  // sundayOf returns the Sunday on or before a YYYY-MM-DD date, shifted by
  // weekOffset weeks. Day arithmetic runs in UTC on the date only.
  function sundayOf(date) {
    const d = new Date(date + 'T00:00:00Z');
    d.setUTCDate(d.getUTCDate() - d.getUTCDay() + weekOffset * 7);
    return d.toISOString().slice(0, 10);
  }

  // Week view: the server returns the local day boundaries of the week,
  // so every block is placed by minutes into its local day.
  async function loadWeek() {
    const gen = ++weekGen;
    clear(weekBox).append(spinner());
    let r;
    try {
      const sunday = sundayOf(localDate(new Date(), tz));
      r = await A.api.get(`/schedules/intervals?week=${sunday}`);
    } catch (e) { if (gen === weekGen) clear(weekBox).append(weekNav(), errorBox(e)); return; }
    if (gen !== weekGen) return;
    tz = r.time_zone;
    const clock = hour12() ? { hour: 'numeric', minute: '2-digit', hour12: true } : { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' };
    const fmt = new Intl.DateTimeFormat([], { timeZone: tz, weekday: 'short', ...clock });
    const hm = new Intl.DateTimeFormat([], { timeZone: tz, ...clock });
    const weekday = new Intl.DateTimeFormat([], { timeZone: tz, weekday: 'short' });
    const dayNum = new Intl.DateTimeFormat([], { timeZone: tz, day: 'numeric' });
    // minutesOfDay gives the place of a block in the column, so it stays
    // on the 24 hour clock whatever the setting says.
    const minutesOfDay = new Intl.DateTimeFormat('en-US', { timeZone: tz, hour: 'numeric', minute: 'numeric', hourCycle: 'h23' });
    const minutes = (d) => { const p = Object.fromEntries(minutesOfDay.formatToParts(d).map((x) => [x.type, x.value])); return Number(p.hour) * 60 + Number(p.minute); };
    const days = r.days.map((d) => new Date(d));
    const from = new Date(r.from);
    const to = new Date(r.to);
    const today = localDate(new Date(), tz);
    const grid = h('div.week-grid');
    // The week starts on Sunday, so the first and the last column are the
    // weekend.
    const cols = days.map((d, i) => h('div',
      h('div', { class: `week-head ${i === 0 || i === days.length - 1 ? 'weekend' : ''}` }, weekday.format(d), ' ', h('span.date', dayNum.format(d))),
      h('div', { class: `week-body ${localDate(d, tz) === today ? 'today' : ''}` })));
    for (const iv of r.intervals) {
      // Clip to the week, then draw one block per local day the interval touches.
      const start = new Date(Math.max(new Date(iv.start).getTime(), from.getTime()));
      const end = new Date(Math.min(new Date(iv.end).getTime(), to.getTime()));
      for (let i = 0; i < days.length; i++) {
        const dayStart = days[i];
        const dayEnd = i + 1 < days.length ? days[i + 1] : to;
        if (end <= dayStart || start >= dayEnd) continue;
        const dayMinutes = (dayEnd.getTime() - dayStart.getTime()) / 60000;
        const s = start > dayStart ? minutes(start) : 0;
        const e = end < dayEnd ? minutes(end) : dayMinutes;
        const top = (s / dayMinutes) * 100;
        const height = Math.max(1.5, ((e - s) / dayMinutes) * 100);
        cols[i].lastChild.append(h('div', { class: `week-block ${iv.exception ? 'week-exception' : ''}`, style: `top:${top}%;height:${height}%`, title: `${iv.name}\n${fmt.format(new Date(iv.start))} to ${fmt.format(new Date(iv.end))}` },
          h('div.name', iv.name),
          height > 7 ? h('div.time', `${hm.format(new Date(iv.start))}–${hm.format(new Date(iv.end))}`) : null));
      }
    }
    for (const c of cols) grid.append(c);
    clear(weekBox).append(weekNav(days[0], days[days.length - 1]), grid,
      h('div.week-legend',
        h('span', h('span.swatch'), 'Scheduled window'),
        h('span', h('span.swatch.exc'), 'Date exception'),
        r.intervals.length ? null : h('span', 'Nothing scheduled this week.')));
  }

  function weekNav(first, last) {
    const fmt = new Intl.DateTimeFormat([], { timeZone: tz, day: 'numeric', month: 'short' });
    return h('div.week-nav',
      h('button.btn.btn-outline-secondary.btn-icon', { type: 'button', 'aria-label': 'Previous week', onclick: () => { weekOffset--; loadWeek(); } }, icon('chevron-left')),
      h('span.week-range', first ? `${fmt.format(first)} – ${fmt.format(last)} ` : '', h('span.tz', `· ${tz}`)),
      h('button.btn.btn-outline-secondary.btn-icon', { type: 'button', 'aria-label': 'Next week', onclick: () => { weekOffset++; loadWeek(); } }, icon('chevron-right')),
      weekOffset ? h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => { weekOffset = 0; loadWeek(); } }, 'This week') : null);
  }

  async function loadRules() {
    const head = h('div.panel-head', h('h2.panel-title', 'Weekly rules'));
    clear(rulesBox).append(head, spinner());
    let r;
    try { r = await A.api.get('/schedules'); } catch (e) { clear(rulesBox).append(head, h('div.panel-body', errorBox(e))); return; }
    clear(rulesBox).append(head);
    if (r.conflicts.length) rulesBox.append(h('div.strip.strip-warn', icon('exclamation-triangle-fill'), h('span', 'Rules overlap: ', r.conflicts.map((c) => `${c.rule_name} and ${c.other_name} (${c.at})`).join('; '), '. The window that started first plays.')));
    if (!r.schedules.length) { rulesBox.append(h('div.panel-empty', 'No rules yet. Add one to play music on a schedule.')); return; }
    const list = h('div.rows');
    for (const s of r.schedules) {
      const sw = h('input.form-check-input', { type: 'checkbox', role: 'switch', checked: s.enabled, 'aria-label': `Enable ${s.name}`, onchange: async () => {
        try { await A.api.put(`/schedules/${s.id}`, { ...s, enabled: sw.checked }); } catch (err) { sw.checked = !sw.checked; fail(err); }
      } });
      const summary = `${dayLabel(s.days)} · ${fmtHM(s.start_time)}–${fmtHM(s.end_time)}${s.end_time <= s.start_time ? ' (next day)' : ''} · ${sourceLabel(s)}${s.shuffle ? ' · shuffle' : ''}${s.volume != null ? ` · vol ${s.volume}` : ''}`;
      list.append(h('div.row-item.tall',
        h('div.form-check.form-switch.switch-only', sw),
        h('div.row-main', h('div.row-title.fw-semibold', s.name), h('div.row-sub.text-wrap', { title: summary }, summary)),
        h('button.btn.btn-outline-secondary.btn-icon', { type: 'button', 'aria-label': `Edit ${s.name}`, title: 'Edit', onclick: () => ruleEditor(s) }, icon('pencil')),
        h('button.btn.btn-outline-danger.btn-icon', { type: 'button', 'aria-label': `Delete ${s.name}`, title: 'Delete', onclick: async () => {
          if (!await confirmDialog({ title: 'Delete rule', body: `Delete "${s.name}"?`, confirmText: 'Delete', danger: true })) return;
          try { await A.api.del(`/schedules/${s.id}`); } catch (err) { fail(err); }
        } }, icon('trash'))));
    }
    rulesBox.append(list);
  }

  function dayLabel(mask) {
    if (mask === 127) return 'Every day';
    if (mask === 31) return 'Mon–Fri';
    if (mask === 96) return 'Weekends';
    return DAYS.filter(([, b]) => mask & b).map(([l]) => l).join(' ');
  }
  function sourceLabel(s) {
    if (s.source_type === 'playlist') { const pl = playlists.find((p) => String(p.id) === String(s.source_ref)); return `playlist ${pl ? pl.name : '#' + s.source_ref}`; }
    if (s.source_type === 'stream') return `stream ${s.source_ref}`;
    return `folder /${s.source_ref}`;
  }

  // sourceFields builds the shared source and options inputs.
  function sourceFields(init) {
    const kind = init.source_type === 'playlist' || init.source_type === 'stream' ? init.source_type : 'directory';
    const type = h('select.form-select',
      h('option', { value: 'directory', selected: kind === 'directory' }, 'Folder'),
      h('option', { value: 'playlist', selected: kind === 'playlist' }, 'Playlist'),
      h('option', { value: 'stream', selected: kind === 'stream' }, 'Stream'));
    const dir = h('input.form-control.mono', { type: 'text', value: kind === 'directory' ? (init.source_ref || '') : '', placeholder: 'Whole library' });
    const pick = h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => libraryPicker({ title: 'Choose a folder', start: dir.value, onPick: (p) => { dir.value = p; } }) }, 'Browse…');
    const pl = h('select.form-select');
    for (const p of playlists) pl.append(h('option', { value: String(p.id), selected: String(p.id) === String(init.source_ref) }, p.name));
    if (!playlists.length) pl.append(h('option', { value: '' }, 'No playlists yet'));
    const stream = h('input.form-control.mono', { type: 'url', value: kind === 'stream' ? (init.source_ref || '') : '', placeholder: 'https://stream.example.com/live.mp3', 'aria-label': 'Stream address' });
    const dirRow = h('div.input-row', dir, pick);
    const shuffle = h('input', { type: 'checkbox', checked: !!init.shuffle });
    // A stream is one address that plays until the window ends, so it has
    // nothing to shuffle.
    const shuffleLine = h('label.check-line', shuffle, 'Shuffle');
    const sync = () => {
      dirRow.classList.toggle('hidden', type.value !== 'directory');
      pl.classList.toggle('hidden', type.value !== 'playlist');
      stream.classList.toggle('hidden', type.value !== 'stream');
      shuffleLine.classList.toggle('hidden', type.value === 'stream');
    };
    type.addEventListener('change', sync);
    sync();
    const useVol = h('input', { type: 'checkbox', checked: init.volume != null });
    const vol = h('input.form-range.mt-1', { type: 'range', min: 0, max: 100, value: init.volume ?? 60, disabled: init.volume == null, 'aria-label': 'Start volume' });
    const volLabel = h('span.mono', String(init.volume ?? 60));
    vol.addEventListener('input', () => { volLabel.textContent = vol.value; });
    useVol.addEventListener('change', () => { vol.disabled = !useVol.checked; });
    const el = [
      h('label.form-label', 'Source'), h('div.row.g-2', h('div.col-sm-4', type), h('div.col-sm-8', dirRow, pl, stream)),
      shuffleLine,
      h('label.check-line', useVol, 'Set volume at the start:', volLabel),
      vol,
    ];
    // value returns the source, or an error message when it is incomplete.
    const value = () => {
      const ref = { playlist: () => pl.value, stream: () => stream.value.trim() }[type.value]
        || (() => dir.value.trim().replace(/^\/+|\/+$/g, ''));
      const v = {
        source_type: type.value,
        source_ref: ref(),
        shuffle: type.value === 'stream' ? false : shuffle.checked,
        volume: useVol.checked ? Number(vol.value) : null,
      };
      if (v.source_type === 'playlist' && !v.source_ref) return { error: 'Choose a playlist.' };
      if (v.source_type === 'stream' && !/^https?:\/\/.+/.test(v.source_ref)) return { error: 'Give the stream address, starting with http:// or https://' };
      return v;
    };
    return { el, value };
  }

  // editorFooter is Cancel and Save for a dialog form.
  const editorFooter = (form) => [
    h('button.btn.btn-outline-secondary', { type: 'button', 'data-bs-dismiss': 'modal' }, 'Cancel'),
    h('button.btn.btn-primary', { type: 'button', onclick: () => form.requestSubmit() }, 'Save'),
  ];

  function ruleEditor(rule) {
    const init = rule || { name: '', enabled: true, days: 31, start_time: '09:00', end_time: '17:00', source_type: 'directory', source_ref: '', shuffle: defaultShuffle, volume: null };
    const name = h('input.form-control', { type: 'text', value: init.name, required: true, maxlength: 100, placeholder: 'Opening hours' });
    const dayChecks = DAYS.map(([label, bit]) => { const c = h('input.btn-check', { type: 'checkbox', id: `day-${bit}`, checked: !!(init.days & bit), autocomplete: 'off' }); return [c, h('label.btn.btn-sm', { for: `day-${bit}` }, label)]; });
    const start = timeField({ value: init.start_time, label: 'Start', required: true });
    const end = timeField({ value: init.end_time, label: 'End', required: true });
    const src = sourceFields(init);
    const err = h('div.form-error');
    const form = h('form', { onsubmit: async (e) => {
      e.preventDefault();
      const days = dayChecks.reduce((m, [c], i) => m | (c.checked ? DAYS[i][1] : 0), 0);
      if (!days) { err.textContent = 'Choose at least one day.'; return; }
      const source = src.value();
      if (source.error) { err.textContent = source.error; return; }
      const body = { name: name.value.trim(), enabled: init.enabled, days, start_time: start.value, end_time: end.value, ...source };
      try {
        if (rule) await A.api.put(`/schedules/${rule.id}`, body); else await A.api.post('/schedules', body);
        dlg.hide();
      } catch (ex) { err.textContent = ex.message; }
    } },
      h('label.form-label', 'Name'), name,
      h('label.form-label.mt-3', 'Days ', h('span.muted.fw-normal', '(the day the window starts)')),
      h('div.d-flex.flex-wrap.gap-1', dayChecks.flat()),
      h('div.row.g-2.mt-2', h('div.col-6', h('label.form-label', 'Start'), start.el), h('div.col-6', h('label.form-label', 'End'), end.el)),
      h('div.form-text', 'An end earlier than the start runs past midnight. Music repeats until the window ends.'),
      h('div.mt-3', src.el), err);
    const dlg = modal({ title: rule ? 'Edit rule' : 'New rule', body: form, footer: editorFooter(form) });
  }

  // Exceptions: a list of upcoming dates with add and edit.
  async function loadExceptions() {
    const head = h('div.panel-head', h('h2.panel-title', 'Date exceptions'),
      h('div.tools', h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => exceptionEditor(null) }, icon('plus-lg'), 'Add date')));
    clear(excBox).append(head, spinner());
    let r;
    try { r = await A.api.get('/schedule-exceptions'); } catch (e) { clear(excBox).append(head, h('div.panel-body', errorBox(e))); return; }
    clear(excBox).append(head);
    const today = localDate(new Date(), tz);
    const upcoming = r.exceptions.filter((e) => e.date >= today);
    if (!upcoming.length) { excBox.append(h('div.panel-empty', 'No upcoming exceptions. Use them for holidays, closures and one-off events.')); return; }
    const list = h('div.rows');
    for (const e of upcoming) {
      const what = e.kind === 'silent' ? 'Silent all day' : e.kind === 'hours' ? `${fmtHM(e.start_time)}–${fmtHM(e.end_time)} · ${sourceLabel({ source_type: e.source_type, source_ref: e.source_ref })}` : `Normal hours · ${sourceLabel({ source_type: e.source_type, source_ref: e.source_ref })}`;
      list.append(h('div.row-item.tall',
        h('span.row-date', e.date),
        h('div.row-main', h('div.row-title.fw-semibold', e.note || what), e.note ? h('div.row-sub', what) : null),
        h('button.btn.btn-outline-secondary.btn-icon', { type: 'button', 'aria-label': `Edit ${e.date}`, title: 'Edit', onclick: () => exceptionEditor(e) }, icon('pencil')),
        h('button.btn.btn-outline-danger.btn-icon', { type: 'button', 'aria-label': `Delete ${e.date}`, title: 'Delete', onclick: async () => {
          if (!await confirmDialog({ title: 'Remove exception', body: `Remove the exception on ${e.date}?`, confirmText: 'Remove', danger: true })) return;
          try { await A.api.del(`/schedule-exceptions/${e.date}`); } catch (err) { fail(err); }
        } }, icon('trash'))));
    }
    excBox.append(list);
  }

  function exceptionEditor(exc) {
    const init = exc || { date: localDate(new Date(), tz), kind: 'silent', note: '', start_time: '09:00', end_time: '17:00', source_type: 'directory', source_ref: '', shuffle: false, volume: null };
    const date = h('input.form-control', { type: 'date', value: init.date, required: true, disabled: !!exc });
    const note = h('input.form-control', { type: 'text', value: init.note || '', maxlength: 200, placeholder: 'Closed for the holiday' });
    const kind = h('select.form-select',
      h('option', { value: 'silent', selected: init.kind === 'silent' }, 'Silent all day'),
      h('option', { value: 'hours', selected: init.kind === 'hours' }, 'Different hours'),
      h('option', { value: 'source', selected: init.kind === 'source' }, 'Different source, normal hours'));
    const start = timeField({ value: init.start_time || '09:00', label: 'Start' });
    const end = timeField({ value: init.end_time || '17:00', label: 'End' });
    const hoursRow = h('div.row.g-2.mt-2', h('div.col-6', h('label.form-label', 'Start'), start.el), h('div.col-6', h('label.form-label', 'End'), end.el));
    const src = sourceFields(init);
    const srcBox = h('div.mt-3', src.el);
    const sync = () => { hoursRow.classList.toggle('hidden', kind.value !== 'hours'); srcBox.classList.toggle('hidden', kind.value === 'silent'); };
    kind.addEventListener('change', sync);
    sync();
    const err = h('div.form-error');
    const form = h('form', { onsubmit: async (e) => {
      e.preventDefault();
      const body = { date: date.value, kind: kind.value, note: note.value.trim() };
      if (kind.value === 'hours') Object.assign(body, { start_time: start.value, end_time: end.value });
      if (kind.value !== 'silent') {
        const source = src.value();
        if (source.error) { err.textContent = source.error; return; }
        Object.assign(body, source);
      }
      try {
        if (exc) await A.api.put(`/schedule-exceptions/${exc.date}`, body); else await A.api.post('/schedule-exceptions', body);
        dlg.hide();
      } catch (ex) { err.textContent = ex.message; }
    } },
      h('div.row.g-2', h('div.col-sm-5', h('label.form-label', 'Date'), date), h('div.col-sm-7', h('label.form-label', 'Note'), note)),
      h('label.form-label.mt-3', 'What happens'), kind,
      h('div.form-text', 'An exception governs its whole day. A window from the day before stops at midnight.'),
      hoursRow, srcBox, err);
    const dlg = modal({ title: exc ? 'Edit exception' : 'Add exception', body: form, footer: editorFooter(form) });
  }

  await loadAll();
  return {
    onEvent(type) {
      status?.onEvent(type);
      announcements.onEvent(type);
      if (type === 'schedule' || type === 'settings') { loadWeek(); loadRules(); loadExceptions(); }
    },
    destroy() { status?.destroy(); announcements.destroy(); },
  };
}
