import * as A from '../api.js';
import { h, clear, icon, confirmDialog, modal, spinner, errorBox } from '../dom.js';
import { libraryPicker, fail } from '../fileops.js';

const DAYS = [['Mon', 1], ['Tue', 2], ['Wed', 4], ['Thu', 8], ['Fri', 16], ['Sat', 32], ['Sun', 64]];

// scheduleView shows the week of expanded intervals, the rule list with
// enable switches, the rule editor, and the exceptions calendar.
export async function scheduleView(main) {
  clear(main);
  const box = h('div.mx-auto', { style: 'max-width: 1000px' }, spinner());
  main.append(box);
  const weekBox = h('div.mb-4');
  const rulesBox = h('div.mb-4');
  const excBox = h('div.mb-4');
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
    clear(box).append(
      h('div.d-flex.align-items-center.gap-2.mb-3', h('h1.h3.mb-0.me-auto', 'Schedule'),
        h('button.btn.btn-primary', { type: 'button', onclick: () => ruleEditor(null) }, icon('plus-lg', 'me-1'), 'New rule')),
      weekBox, rulesBox, excBox);
    await Promise.all([loadWeek(), loadRules(), loadExceptions()]);
  }

  // localDate gives the YYYY-MM-DD of an instant in the configured zone.
  function localDate(d, zone) {
    const p = Object.fromEntries(new Intl.DateTimeFormat('en-CA', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(d).map((x) => [x.type, x.value]));
    return `${p.year}-${p.month}-${p.day}`;
  }

  // mondayOf returns the Monday on or before a YYYY-MM-DD date, shifted by
  // weekOffset weeks. Day arithmetic runs in UTC on the date only.
  function mondayOf(date) {
    const d = new Date(date + 'T00:00:00Z');
    d.setUTCDate(d.getUTCDate() - ((d.getUTCDay() + 6) % 7) + weekOffset * 7);
    return d.toISOString().slice(0, 10);
  }

  // Week view: the server returns the local day boundaries of the week,
  // so every block is placed by minutes into its local day.
  async function loadWeek() {
    const gen = ++weekGen;
    clear(weekBox).append(spinner());
    let r;
    try {
      const monday = mondayOf(localDate(new Date(), tz));
      r = await A.api.get(`/schedules/intervals?week=${monday}`);
    } catch (e) { if (gen === weekGen) clear(weekBox).append(weekNav(), errorBox(e)); return; }
    if (gen !== weekGen) return;
    tz = r.time_zone;
    const fmt = new Intl.DateTimeFormat([], { timeZone: tz, weekday: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
    const minutesOfDay = new Intl.DateTimeFormat('en-US', { timeZone: tz, hour: 'numeric', minute: 'numeric', hourCycle: 'h23' });
    const minutes = (d) => { const p = Object.fromEntries(minutesOfDay.formatToParts(d).map((x) => [x.type, x.value])); return Number(p.hour) * 60 + Number(p.minute); };
    const days = r.days.map((d) => new Date(d));
    const from = new Date(r.from);
    const to = new Date(r.to);
    const grid = h('div.week-grid');
    const cols = days.map((d) => h('div.week-col', h('div.week-head', new Intl.DateTimeFormat([], { timeZone: tz, weekday: 'short', day: 'numeric' }).format(d)), h('div.week-body')));
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
        cols[i].lastChild.append(h('div', { class: `week-block ${iv.exception ? 'week-exception' : ''}`, style: `top:${top}%;height:${height}%`, title: `${iv.name}\n${fmt.format(new Date(iv.start))} to ${fmt.format(new Date(iv.end))}` }, h('span.week-block-text', iv.name)));
      }
    }
    for (const c of cols) grid.append(c);
    clear(weekBox).append(weekNav(days[0], days[days.length - 1]), grid);
    if (!r.intervals.length) weekBox.append(h('p.small.text-body-secondary.mt-2', 'Nothing scheduled this week.'));
  }

  function weekNav(first, last) {
    const fmt = new Intl.DateTimeFormat([], { timeZone: tz, day: 'numeric', month: 'short' });
    return h('div.d-flex.align-items-center.gap-2.mb-2',
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'aria-label': 'Previous week', onclick: () => { weekOffset--; loadWeek(); } }, icon('chevron-left')),
      h('span.small.text-body-secondary', first ? `${fmt.format(first)} – ${fmt.format(last)} · ${tz}` : tz),
      h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'aria-label': 'Next week', onclick: () => { weekOffset++; loadWeek(); } }, icon('chevron-right')),
      weekOffset ? h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => { weekOffset = 0; loadWeek(); } }, 'This week') : null);
  }

  async function loadRules() {
    clear(rulesBox).append(spinner());
    let r;
    try { r = await A.api.get('/schedules'); } catch (e) { clear(rulesBox).append(errorBox(e)); return; }
    clear(rulesBox).append(h('h2.h5', 'Weekly rules'));
    if (r.conflicts.length) rulesBox.append(h('div.alert.alert-warning.small', 'Rules overlap: ', r.conflicts.map((c) => `${c.rule_name} and ${c.other_name} (${c.at})`).join('; '), '. The window that started first plays.'));
    if (!r.schedules.length) { rulesBox.append(h('p.text-body-secondary', 'No rules yet. Add one to play music on a schedule.')); return; }
    const list = h('div.list-group.row-list');
    for (const s of r.schedules) {
      const sw = h('input.form-check-input', { type: 'checkbox', role: 'switch', checked: s.enabled, 'aria-label': `Enable ${s.name}`, onchange: async () => {
        try { await A.api.put(`/schedules/${s.id}`, { ...s, enabled: sw.checked }); } catch (err) { sw.checked = !sw.checked; fail(err); }
      } });
      list.append(h('div.list-group-item',
        h('div.form-check.form-switch.mb-0', sw),
        h('div.row-main', h('div.row-title', s.name), h('div.small.text-body-secondary', `${dayLabel(s.days)} · ${s.start_time}–${s.end_time}${s.end_time <= s.start_time ? ' (next day)' : ''} · ${sourceLabel(s)}${s.shuffle ? ' · shuffle' : ''}${s.volume != null ? ` · vol ${s.volume}` : ''}`)),
        h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'aria-label': `Edit ${s.name}`, onclick: () => ruleEditor(s) }, icon('pencil')),
        h('button.btn.btn-sm.btn-outline-danger', { type: 'button', 'aria-label': `Delete ${s.name}`, onclick: async () => {
          if (!await confirmDialog({ title: 'Delete rule', body: `Delete "${s.name}"?`, confirmText: 'Delete', danger: true })) return;
          try { await A.api.del(`/schedules/${s.id}`); } catch (err) { fail(err); }
        } }, icon('trash'))));
    }
    rulesBox.append(list);
  }

  function dayLabel(mask) {
    if (mask === 127) return 'Every day';
    if (mask === 31) return 'Mon–Fri';
    if (mask === 96) return 'Sat–Sun';
    return DAYS.filter(([, b]) => mask & b).map(([l]) => l).join(' ');
  }
  function sourceLabel(s) {
    if (s.source_type === 'playlist') { const pl = playlists.find((p) => String(p.id) === String(s.source_ref)); return `playlist ${pl ? pl.name : '#' + s.source_ref}`; }
    return `folder /${s.source_ref}`;
  }

  // sourceFields builds the shared source and options inputs.
  function sourceFields(init) {
    const type = h('select.form-select', h('option', { value: 'directory', selected: init.source_type !== 'playlist' }, 'Folder'), h('option', { value: 'playlist', selected: init.source_type === 'playlist' }, 'Playlist'));
    const dir = h('input.form-control', { type: 'text', value: init.source_type === 'playlist' ? '' : (init.source_ref || ''), placeholder: 'Whole library' });
    const pick = h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => libraryPicker({ title: 'Choose a folder', start: dir.value, onPick: (p) => { dir.value = p; } }) }, 'Browse…');
    const pl = h('select.form-select');
    for (const p of playlists) pl.append(h('option', { value: String(p.id), selected: String(p.id) === String(init.source_ref) }, p.name));
    if (!playlists.length) pl.append(h('option', { value: '' }, 'No playlists yet'));
    const dirRow = h('div.d-flex.gap-2', dir, pick);
    const sync = () => { dirRow.classList.toggle('d-none', type.value === 'playlist'); pl.classList.toggle('d-none', type.value !== 'playlist'); };
    type.addEventListener('change', sync);
    sync();
    const shuffle = h('input.form-check-input', { type: 'checkbox', checked: !!init.shuffle });
    const useVol = h('input.form-check-input', { type: 'checkbox', checked: init.volume != null });
    const vol = h('input.form-range', { type: 'range', min: 0, max: 100, value: init.volume ?? 60, disabled: init.volume == null });
    const volLabel = h('span.mono.small', String(init.volume ?? 60));
    vol.addEventListener('input', () => { volLabel.textContent = vol.value; });
    useVol.addEventListener('change', () => { vol.disabled = !useVol.checked; });
    const el = [
      h('label.form-label', 'Source'), h('div.row.g-2', h('div.col-sm-4', type), h('div.col-sm-8', dirRow, pl)),
      h('div.row.g-3.mt-1',
        h('div.col-sm-4', h('div.form-check', shuffle, h('label.form-check-label', 'Shuffle'))),
        h('div.col-sm-8', h('div.form-check', useVol, h('label.form-check-label', 'Set volume at the start: ', volLabel)), vol)),
    ];
    // value returns the source, or an error message when it is incomplete.
    const value = () => {
      const v = {
        source_type: type.value,
        source_ref: type.value === 'playlist' ? pl.value : dir.value.trim().replace(/^\/+|\/+$/g, ''),
        shuffle: shuffle.checked,
        volume: useVol.checked ? Number(vol.value) : null,
      };
      if (v.source_type === 'playlist' && !v.source_ref) return { error: 'Choose a playlist.' };
      return v;
    };
    return { el, value };
  }

  function ruleEditor(rule) {
    const init = rule || { name: '', enabled: true, days: 31, start_time: '09:00', end_time: '17:00', source_type: 'directory', source_ref: '', shuffle: defaultShuffle, volume: null };
    const name = h('input.form-control', { type: 'text', value: init.name, required: true, maxlength: 100 });
    const dayChecks = DAYS.map(([label, bit]) => { const c = h('input.btn-check', { type: 'checkbox', id: `day-${bit}`, checked: !!(init.days & bit), autocomplete: 'off' }); return [c, h('label.btn.btn-outline-secondary.btn-sm', { for: `day-${bit}` }, label)]; });
    const start = h('input.form-control', { type: 'time', value: init.start_time, required: true });
    const end = h('input.form-control', { type: 'time', value: init.end_time, required: true });
    const src = sourceFields(init);
    const err = h('div.text-danger.small.mt-2');
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
      h('label.form-label.mt-3', 'Days ', h('span.text-body-secondary.small', '(the day the window starts)')),
      h('div.d-flex.flex-wrap.gap-1', dayChecks.flat()),
      h('div.row.g-2.mt-1', h('div.col-6', h('label.form-label', 'Start'), start), h('div.col-6', h('label.form-label', 'End'), end)),
      h('div.form-text', 'An end earlier than the start runs past midnight. Music repeats until the window ends.'),
      h('div.mt-3', src.el), err);
    const dlg = modal({ title: rule ? 'Edit rule' : 'New rule', body: form, footer: h('button.btn.btn-primary', { type: 'button', onclick: () => form.requestSubmit() }, 'Save') });
  }

  // Exceptions: a list of upcoming dates with add and edit.
  async function loadExceptions() {
    clear(excBox).append(spinner());
    let r;
    try { r = await A.api.get('/schedule-exceptions'); } catch (e) { clear(excBox).append(errorBox(e)); return; }
    clear(excBox).append(h('div.d-flex.align-items-center.gap-2.mb-2', h('h2.h5.mb-0.me-auto', 'Date exceptions'),
      h('button.btn.btn-sm.btn-outline-primary', { type: 'button', onclick: () => exceptionEditor(null) }, icon('calendar-plus', 'me-1'), 'Add date')));
    const today = localDate(new Date(), tz);
    const upcoming = r.exceptions.filter((e) => e.date >= today);
    if (!upcoming.length) { excBox.append(h('p.text-body-secondary.small', 'No upcoming exceptions. Use them for holidays, closures and one-off events.')); return; }
    const list = h('div.list-group.row-list');
    for (const e of upcoming) {
      const what = e.kind === 'silent' ? 'Silent all day' : e.kind === 'hours' ? `${e.start_time}–${e.end_time} · ${sourceLabel({ source_type: e.source_type, source_ref: e.source_ref })}` : `Normal hours · ${sourceLabel({ source_type: e.source_type, source_ref: e.source_ref })}`;
      list.append(h('div.list-group-item',
        h('span.mono', e.date),
        h('div.row-main', h('div.row-title', e.note || what), e.note ? h('div.small.text-body-secondary', what) : null),
        h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', 'aria-label': `Edit ${e.date}`, onclick: () => exceptionEditor(e) }, icon('pencil')),
        h('button.btn.btn-sm.btn-outline-danger', { type: 'button', 'aria-label': `Delete ${e.date}`, onclick: async () => {
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
    const start = h('input.form-control', { type: 'time', value: init.start_time || '09:00' });
    const end = h('input.form-control', { type: 'time', value: init.end_time || '17:00' });
    const hoursRow = h('div.row.g-2.mt-1', h('div.col-6', h('label.form-label', 'Start'), start), h('div.col-6', h('label.form-label', 'End'), end));
    const src = sourceFields(init);
    const srcBox = h('div.mt-3', src.el);
    const sync = () => { hoursRow.classList.toggle('d-none', kind.value !== 'hours'); srcBox.classList.toggle('d-none', kind.value === 'silent'); };
    kind.addEventListener('change', sync);
    sync();
    const err = h('div.text-danger.small.mt-2');
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
    const dlg = modal({ title: exc ? 'Edit exception' : 'Add exception', body: form, footer: h('button.btn.btn-primary', { type: 'button', onclick: () => form.requestSubmit() }, 'Save') });
  }

  await loadAll();
  return {
    onEvent(type) { if (type === 'schedule' || type === 'settings') { loadWeek(); loadRules(); loadExceptions(); } },
  };
}
