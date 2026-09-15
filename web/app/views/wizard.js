import * as A from '../api.js';
import { h, clear, icon, toast, errorBox } from '../dom.js';
import { dirPicker } from '../dirpicker.js';
import { navigate } from '../router.js';

// The wizard runs on the first visit: password, time zone, music root,
// output device, a first schedule, and a track that must be heard. Setup
// is complete when sound came out, not when a row exists.
const STEPS = ['Password', 'Time zone', 'Music', 'Output', 'Schedule', 'Listen'];

export async function wizardView(main, rest, opts = {}) {
  clear(main);
  const box = h('div.mx-auto.mt-3', { style: 'max-width: 560px' });
  main.append(box);
  const firstRun = !!opts.firstRun;
  let settings = null;
  let step = firstRun ? 0 : 1;

  function frame(title, body, footer) {
    clear(box);
    const dots = h('div.d-flex.gap-1.mb-3');
    STEPS.forEach((name, i) => dots.append(h('span', { class: `badge rounded-pill ${i === step ? 'text-bg-primary' : i < step ? 'text-bg-success' : 'text-bg-secondary'}`, title: name }, `${i + 1}`)));
    box.append(h('h1.h4.mb-1', title), dots, body, h('div.d-flex.gap-2.mt-4', footer));
  }
  const next = (label = 'Continue') => h('button.btn.btn-primary', { type: 'submit' }, label);
  const skip = (fn) => h('button.btn.btn-link.ms-auto', { type: 'button', onclick: fn }, 'Skip');

  async function loadSettings() {
    if (!settings) settings = await A.settings();
    return settings;
  }
  async function save(patch) {
    settings = await A.saveSettings({ ...(await loadSettings()), ...patch });
  }

  function stepPassword() {
    const pw = h('input.form-control.form-control-lg', { type: 'password', autocomplete: 'new-password', minlength: 8, required: true, id: 'setup-password' });
    const pw2 = h('input.form-control.form-control-lg', { type: 'password', autocomplete: 'new-password', minlength: 8, required: true });
    const err = h('div.text-danger.small.mt-2');
    frame('Welcome to jukem', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      err.textContent = '';
      if (pw.value !== pw2.value) { err.textContent = 'The passwords differ.'; return; }
      try {
        // The app shell signs in and opens the wizard again at step two.
        await opts.onSignedIn(await A.setup(pw.value));
      } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.text-body-secondary', 'Set the password for this jukebox. One login controls everything.'),
      h('label.form-label', { for: 'setup-password' }, 'Password (8+ characters)'), pw,
      h('label.form-label.mt-3', 'Again'), pw2, err),
    [next()]);
    pw.focus();
  }

  async function stepTimeZone() {
    const set = await loadSettings();
    const browser = Intl.DateTimeFormat().resolvedOptions().timeZone;
    const tz = h('input.form-control', { type: 'text', value: set.time_zone === 'UTC' && browser ? browser : set.time_zone, list: 'wiz-tz', required: true });
    const list = h('datalist#wiz-tz');
    for (const z of (Intl.supportedValuesOf ? Intl.supportedValuesOf('timeZone') : [])) list.append(h('option', { value: z }));
    const err = h('div.text-danger.small.mt-2');
    frame('Time zone', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      try { await save({ time_zone: tz.value.trim() }); step = 2; render(); } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.text-body-secondary', 'Every schedule uses this zone. Daylight saving is handled for you.'),
      h('label.form-label', 'Zone'), tz, list, err),
    [next()]);
  }

  async function stepMusic() {
    const set = await loadSettings();
    const root = h('input.form-control', { type: 'text', value: set.music_root, required: true });
    const err = h('div.text-danger.small.mt-2');
    frame('Music', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      try { await save({ music_root: root.value.trim() }); step = 3; render(); } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.text-body-secondary', 'The folder MPD reads music from. A NAS mount or a USB drive is usually under /mnt or /media. You can also upload music later in Library.'),
      h('label.form-label', 'Music root'), h('div.d-flex.gap-2', root, h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => dirPicker({ start: root.value, onPick: (p) => { root.value = p; } }) }, 'Browse…')),
      h('div.form-text', 'Music copied here as root must be given to the jukem user: chown -R jukem:jukem <folder>'), err),
    [next()]);
  }

  async function stepOutput() {
    const body = h('div');
    frame('Output device', h('form#wiz', { onsubmit: (e) => { e.preventDefault(); step = 4; render(); } }, h('p.text-body-secondary', 'Choose the sound card the speakers are connected to.'), body), [next(), skip(() => { step = 4; render(); })]);
    async function load() {
      let d;
      try { d = await A.devices(); } catch (e) { clear(body).append(errorBox(e)); return; }
      clear(body);
      const list = h('div.list-group');
      if (!d.devices.length) list.append(h('div.list-group-item.text-body-secondary', 'No playback devices found. Plug in a USB DAC or check the HAT overlay, then rescan.'));
      for (const dev of d.devices) {
        list.append(h('button.list-group-item.list-group-item-action.d-flex.align-items-center.gap-2', { type: 'button', onclick: async () => {
          try { await A.selectDevice(dev.key); load(); } catch (e) { toast(e.message, 'danger'); }
        } },
          icon(dev.selected ? 'check-circle-fill' : 'circle', dev.selected ? 'text-success' : 'text-body-secondary'),
          h('div', h('div.fw-semibold', dev.name), h('div.small.text-body-secondary.mono', `card ${dev.card_index} · ${dev.card_id}`))));
      }
      body.append(list, h('button.btn.btn-sm.btn-outline-secondary.mt-2', { type: 'button', onclick: async () => { try { await A.rescanDevices(); load(); } catch (e) { toast(e.message, 'danger'); } } }, icon('arrow-clockwise', 'me-1'), 'Rescan'));
    }
    load();
  }

  function stepSchedule() {
    const name = h('input.form-control', { type: 'text', value: 'Opening hours', required: true, maxlength: 100 });
    const start = h('input.form-control', { type: 'time', value: '09:00', required: true });
    const end = h('input.form-control', { type: 'time', value: '17:00', required: true });
    const weekend = h('input.form-check-input', { type: 'checkbox', id: 'wiz-weekend' });
    const shuffle = h('input.form-check-input', { type: 'checkbox', id: 'wiz-shuffle', checked: true });
    const err = h('div.text-danger.small.mt-2');
    frame('First schedule', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      try {
        await A.api.post('/schedules', { name: name.value.trim(), enabled: true, days: weekend.checked ? 127 : 31, start_time: start.value, end_time: end.value, source_type: 'directory', source_ref: '', shuffle: shuffle.checked, volume: null });
        step = 5;
        render();
      } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.text-body-secondary', 'When should music play? The whole library plays during this window. Add more rules later in Schedule.'),
      h('label.form-label', 'Name'), name,
      h('div.row.g-2.mt-1', h('div.col-6', h('label.form-label', 'Start'), start), h('div.col-6', h('label.form-label', 'End'), end)),
      h('div.form-check.mt-3', weekend, h('label.form-check-label', { for: 'wiz-weekend' }, 'Also on Saturday and Sunday')),
      h('div.form-check', shuffle, h('label.form-check-label', { for: 'wiz-shuffle' }, 'Shuffle')), err),
    [next(), skip(() => { step = 5; render(); })]);
  }

  // firstTrack finds one playable file by walking the library.
  async function firstTrack(path = '', depth = 0) {
    const r = await A.api.get(`/library/browse?path=${encodeURIComponent(path)}`);
    const file = r.entries.find((e) => e.type === 'file');
    if (file) return file;
    if (depth >= 4) return null;
    for (const d of r.entries.filter((e) => e.type === 'directory')) {
      const f = await firstTrack(d.path, depth + 1);
      if (f) return f;
    }
    return null;
  }

  function stepListen() {
    const status = h('div.mt-3');
    const heard = h('div.mt-3.d-none',
      h('p', 'Do you hear it?'),
      h('div.d-flex.gap-2',
        h('button.btn.btn-success', { type: 'button', onclick: finish }, icon('check-lg', 'me-1'), 'Yes, I hear music'),
        h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => {
          clear(status).append(h('div.alert.alert-warning.small',
            h('div.fw-semibold', 'No sound? Check these, then play again.'),
            h('ul.mb-0.mt-1',
              h('li', 'Is the right output device selected? Go back one step.'),
              h('li', 'A muted hardware control is a common cause. Settings > Audio > Hardware level.'),
              h('li', 'Is the amplifier on and the volume up?'))));
        } }, 'No')));
    async function play() {
      clear(status).append(h('div.text-body-secondary', 'Looking for a track…'));
      let track;
      try { track = await firstTrack(); } catch (e) { clear(status).append(errorBox(e)); return; }
      if (!track) {
        clear(status).append(h('div.alert.alert-info.small', 'The library has no tracks yet, or MPD is still scanning. Upload music in Library, then come back to Settings to finish. You can finish now and test later.'));
        heard.classList.remove('d-none');
        return;
      }
      try {
        await A.queueAction({ action: 'play_now', files: [track.path] });
        clear(status).append(h('div', icon('music-note-beamed', 'me-1'), `Playing ${track.title || track.name}`));
        heard.classList.remove('d-none');
      } catch (e) { clear(status).append(errorBox(e)); }
    }
    async function finish() {
      try {
        await save({ setup_complete: true });
        toast('Setup complete', 'success');
        navigate('#/');
      } catch (e) { toast(e.message, 'danger'); }
    }
    frame('Listen', h('div',
      h('p.text-body-secondary', 'Setup is done when sound comes out of the speakers. Play a track from the library and confirm you hear it.'),
      h('button.btn.btn-primary.btn-lg', { type: 'button', onclick: play }, icon('play-fill', 'me-1'), 'Play a track'),
      status, heard),
    [h('button.btn.btn-link', { type: 'button', onclick: () => { step = 3; render(); } }, 'Back to output'), h('button.btn.btn-link.ms-auto', { type: 'button', onclick: finish }, 'Finish without a test')]);
  }

  async function render() {
    try {
      await [stepPassword, stepTimeZone, stepMusic, stepOutput, stepSchedule, stepListen][step]();
    } catch (e) {
      clear(box).append(errorBox(e));
    }
  }
  await render();
  return {};
}
