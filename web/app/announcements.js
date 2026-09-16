// Announcements are single files that play instead of the music at their
// own times: a message for customers, an offer, an advertisement. The panel
// lists them and holds the editor.

import * as A from './api.js';
import { h, clear, icon, toast, confirmDialog, modal, spinner, errorBox } from './dom.js';
import { libraryPicker, fail, baseOf } from './fileops.js';

// The week starts on Sunday, as everywhere else. The bits are the API's:
// Monday is 1, Sunday is 64.
const DAYS = [['Sun', 64], ['Mon', 1], ['Tue', 2], ['Wed', 4], ['Thu', 8], ['Fri', 16], ['Sat', 32]];

const KINDS = [
  ['file', 'This file', 'The same file plays every time.'],
  ['random', 'Any file of a folder', 'One file of the folder plays, chosen at random.'],
  ['cycle', 'The files of a folder in turn', 'The next file of the folder plays each time.'],
];

function dayLabel(mask) {
  if (mask === 127) return 'Every day';
  if (mask === 31) return 'Mon–Fri';
  if (mask === 96) return 'Weekends';
  return DAYS.filter(([, b]) => mask & b).map(([l]) => l).join(' ');
}

// summary is the one line under the name of an announcement.
function summary(a) {
  const when = a.mode === 'at'
    ? `at ${a.at_time}`
    : `every ${a.every_minutes} min from ${a.start_time} to ${a.end_time}`;
  const what = a.source_kind === 'file' ? baseOf(a.source_ref) : `${a.source_kind === 'random' ? 'any file' : 'files in turn'} of /${a.source_ref || ''}`;
  return `${dayLabel(a.days)} · ${when} · ${what}${a.volume != null ? ` · vol ${a.volume}` : ''}`;
}

// announcementsPanel returns {el, load, onEvent, destroy}.
export function announcementsPanel() {
  const body = h('div');
  const head = h('div.panel-head', h('h2.panel-title', 'Announcements'),
    h('div.tools', h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => editor(null) }, icon('plus-lg'), 'Add announcement')));
  const el = h('div.panel.clip', head, body);

  async function load() {
    clear(body).append(spinner());
    let list;
    try { list = (await A.api.get('/announcements')).announcements; } catch (e) { clear(body).append(h('div.panel-body', errorBox(e))); return; }
    clear(body);
    if (!list.length) {
      body.append(h('div.panel-empty', 'No announcements. Add one to play a message, an offer or an advertisement between the music.'));
      return;
    }
    const rows = h('div.rows');
    for (const a of list) {
      const sw = h('input.form-check-input', { type: 'checkbox', role: 'switch', checked: a.enabled, 'aria-label': `Enable ${a.name}`, onchange: async () => {
        try { await A.api.put(`/announcements/${a.id}`, { ...a, enabled: sw.checked }); } catch (err) { sw.checked = !sw.checked; fail(err); }
      } });
      rows.append(h('div.row-item.tall',
        h('div.form-check.form-switch.switch-only', sw),
        h('div.row-main', h('div.row-title.fw-semibold', a.name), h('div.row-sub.text-wrap', summary(a))),
        h('button.btn.btn-outline-secondary.btn-icon', { type: 'button', 'aria-label': `Play ${a.name} now`, title: 'Play it now. The music continues afterwards.', onclick: () => playNow(a) }, icon('megaphone')),
        h('button.btn.btn-outline-secondary.btn-icon', { type: 'button', 'aria-label': `Edit ${a.name}`, title: 'Edit', onclick: () => editor(a) }, icon('pencil')),
        h('button.btn.btn-outline-danger.btn-icon', { type: 'button', 'aria-label': `Delete ${a.name}`, title: 'Delete', onclick: async () => {
          if (!await confirmDialog({ title: 'Delete announcement', body: `Delete "${a.name}"?`, confirmText: 'Delete', danger: true })) return;
          try { await A.api.del(`/announcements/${a.id}`); load(); } catch (err) { fail(err); }
        } }, icon('trash'))));
    }
    body.append(rows);
  }

  async function playNow(a) {
    try {
      await A.api.post(`/announcements/${a.id}/play`);
      toast(`Playing ${a.name}`, 'success');
    } catch (err) { fail(err); }
  }

  function editor(ann) {
    const init = ann || { name: '', enabled: true, days: 127, mode: 'at', at_time: '10:15', start_time: '09:00', end_time: '17:00', every_minutes: 30, source_kind: 'file', source_ref: '', volume: null };
    const name = h('input.form-control', { type: 'text', value: init.name, required: true, maxlength: 100, placeholder: 'Closing time message' });
    const dayChecks = DAYS.map(([label, bit]) => {
      const c = h('input.btn-check', { type: 'checkbox', id: `ann-day-${bit}`, checked: !!(init.days & bit), autocomplete: 'off' });
      return [c, h('label.btn.btn-sm', { for: `ann-day-${bit}` }, label)];
    });

    const mode = h('select.form-select',
      h('option', { value: 'at', selected: init.mode !== 'every' }, 'Once a day, at a time'),
      h('option', { value: 'every', selected: init.mode === 'every' }, 'Again and again, between two times'));
    const atTime = h('input.form-control', { type: 'time', value: init.at_time || '10:15' });
    const startTime = h('input.form-control', { type: 'time', value: init.start_time || '09:00' });
    const endTime = h('input.form-control', { type: 'time', value: init.end_time || '17:00' });
    const every = h('input.form-control', { type: 'number', min: 1, max: 1440, value: init.every_minutes ?? 30 });
    const atRow = h('div.mt-2', h('label.form-label', 'Time'), atTime);
    const everyRow = h('div.row.g-2.mt-2',
      h('div.col-4', h('label.form-label', 'From'), startTime),
      h('div.col-4', h('label.form-label', 'To'), endTime),
      h('div.col-4', h('label.form-label', 'Every (min)'), every));

    const kind = h('select.form-select');
    for (const [value, label] of KINDS) kind.append(h('option', { value, selected: init.source_kind === value }, label));
    const kindNote = h('div.form-text');
    const ref = h('input.form-control.mono', { type: 'text', value: init.source_ref || '', placeholder: 'announcements/closing.mp3', 'aria-label': 'File or folder' });
    const browse = h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => {
      if (kind.value === 'file') {
        libraryPicker({ title: 'Choose the file', files: true, onPick: (files) => { if (files.length) ref.value = files[0]; } });
      } else {
        libraryPicker({ title: 'Choose the folder', start: ref.value, onPick: (p) => { ref.value = p; } });
      }
    } }, 'Browse…');

    const useVol = h('input', { type: 'checkbox', checked: init.volume != null });
    const vol = h('input.form-range.mt-1', { type: 'range', min: 0, max: 100, value: init.volume ?? 80, disabled: init.volume == null, 'aria-label': 'Announcement volume' });
    const volLabel = h('span.mono', String(init.volume ?? 80));
    vol.addEventListener('input', () => { volLabel.textContent = vol.value; });
    useVol.addEventListener('change', () => { vol.disabled = !useVol.checked; });

    const sync = () => {
      atRow.classList.toggle('hidden', mode.value !== 'at');
      everyRow.classList.toggle('hidden', mode.value !== 'every');
      ref.placeholder = kind.value === 'file' ? 'announcements/closing.mp3' : 'announcements';
      kindNote.textContent = KINDS.find(([v]) => v === kind.value)[2];
    };
    mode.addEventListener('change', sync);
    kind.addEventListener('change', sync);
    sync();

    const err = h('div.form-error');
    const form = h('form', { onsubmit: async (e) => {
      e.preventDefault();
      const days = dayChecks.reduce((m, [c], i) => m | (c.checked ? DAYS[i][1] : 0), 0);
      if (!days) { err.textContent = 'Choose at least one day.'; return; }
      const value = ref.value.trim().replace(/^\/+|\/+$/g, '');
      if (kind.value === 'file' && !value) { err.textContent = 'Choose the file to play.'; return; }
      if (kind.value !== 'file' && !value) { err.textContent = 'Choose the folder to play from.'; return; }
      const body = {
        name: name.value.trim(), enabled: init.enabled, days, mode: mode.value,
        source_kind: kind.value, source_ref: value,
        volume: useVol.checked ? Number(vol.value) : null,
      };
      if (mode.value === 'at') body.at_time = atTime.value;
      else Object.assign(body, { start_time: startTime.value, end_time: endTime.value, every_minutes: Number(every.value) });
      try {
        if (ann) await A.api.put(`/announcements/${ann.id}`, body);
        else await A.api.post('/announcements', body);
        dlg.hide();
        load();
      } catch (ex) { err.textContent = ex.message; }
    } },
      h('label.form-label', 'Name'), name,
      h('label.form-label.mt-3', 'Days'),
      h('div.d-flex.flex-wrap.gap-1', dayChecks.flat()),
      h('label.form-label.mt-3', 'When'), mode, atRow, everyRow,
      h('label.form-label.mt-3', 'What plays'), kind, kindNote,
      h('div.input-row.mt-2', ref, browse),
      h('label.check-line', useVol, 'Play it at its own volume:', volLabel), vol,
      h('div.form-text', 'The music stops for the announcement and starts again where it was.'),
      err);
    const dlg = modal({
      title: ann ? 'Edit announcement' : 'New announcement',
      body: form,
      footer: [
        h('button.btn.btn-outline-secondary', { type: 'button', 'data-bs-dismiss': 'modal' }, 'Cancel'),
        h('button.btn.btn-primary', { type: 'button', onclick: () => form.requestSubmit() }, 'Save'),
      ],
    });
  }

  return { el, load, onEvent(type) { if (type === 'schedule') load(); }, destroy() {} };
}
