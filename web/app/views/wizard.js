import * as A from '../api.js';
import { h, clear, icon, toast, errorBox, tzDatalist } from '../dom.js';
import { dirPicker } from '../dirpicker.js';
import { navigate } from '../router.js';
import { state, refreshStatus } from '../main.js';

// The wizard runs on the first visit: password, time zone, music root,
// output device, a first schedule, and a track that must be heard. Setup
// is complete when sound came out, not when a row exists.
const STEPS = ['Password', 'Time zone', 'Music', 'Output', 'Schedule', 'Listen'];

export async function wizardView(main, _rest, opts = {}) {
  clear(main);
  const box = h('div.mx-auto.mt-3', { style: 'max-width: 560px' });
  main.append(box);
  let settings = null;
  let step = opts.firstRun ? 0 : 1;

  function frame(title, body, footer) {
    clear(box);
    const dots = h('div.d-flex.gap-1.mb-3');
    STEPS.forEach((name, i) => dots.append(h('span', { class: `badge rounded-pill ${i === step ? 'text-bg-primary' : i < step ? 'text-bg-success' : 'text-bg-secondary'}`, title: name }, `${i + 1}`)));
    box.append(h('div.d-flex.align-items-center.gap-2.mb-1',
      h('picture', h('source', { srcset: '/app/logo-dark.svg', media: '(prefers-color-scheme: dark)' }), h('img', { src: '/app/logo.svg', alt: '', width: 36, height: 36 })),
      h('h1.h4.mb-0', title)), dots, body, h('div.d-flex.gap-2.mt-4', footer));
  }
  const go = (n) => { step = n; render(); };
  // The footer sits outside the form, so the button names it.
  const next = () => h('button.btn.btn-primary', { type: 'submit', form: 'wiz' }, 'Continue');
  const skip = (n) => h('button.btn.btn-link.ms-auto', { type: 'button', onclick: () => go(n) }, 'Skip');

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
    const err = h('div.text-danger.small.mt-2');
    frame('Time zone', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      try { await save({ time_zone: tz.value.trim() }); go(2); } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.text-body-secondary', 'Every schedule uses this zone. Daylight saving is handled for you.'),
      h('label.form-label', 'Zone'), tz, tzDatalist('wiz-tz'), err),
    [next()]);
  }

  async function stepMusic() {
    const set = await loadSettings();
    const root = h('input.form-control', { type: 'text', value: set.music_root, required: true });
    const err = h('div.text-danger.small.mt-2');
    frame('Music', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      try { await save({ music_root: root.value.trim() }); go(3); } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.text-body-secondary', 'The folder MPD reads music from. A NAS mount or a USB drive is usually under /mnt or /media. You can also upload music later in Library.'),
      h('label.form-label', 'Music root'), h('div.d-flex.gap-2', root, h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => dirPicker({ start: root.value, onPick: (p) => { root.value = p; } }) }, 'Browse…')),
      h('div.form-text', 'Music copied here as root must be given to the jukem user: chown -R jukem:jukem <folder>'), err),
    [next()]);
  }

  async function stepOutput() {
    const body = h('div');
    frame('Output device', h('form#wiz', { onsubmit: (e) => { e.preventDefault(); go(4); } }, h('p.text-body-secondary', 'Choose the sound card the speakers are connected to.'), body), [next(), skip(4)]);
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

  async function stepSchedule() {
    const set = await loadSettings();
    // A person who returns to the wizard has a rule already: nothing to add.
    if ((await A.api.get('/schedules')).schedules.length) { go(5); return; }
    const name = h('input.form-control', { type: 'text', value: 'Opening hours', required: true, maxlength: 100 });
    const start = h('input.form-control', { type: 'time', value: '09:00', required: true });
    const end = h('input.form-control', { type: 'time', value: '17:00', required: true });
    const weekend = h('input.form-check-input', { type: 'checkbox', id: 'wiz-weekend' });
    const shuffle = h('input.form-check-input', { type: 'checkbox', id: 'wiz-shuffle', checked: set.default_shuffle });
    const err = h('div.text-danger.small.mt-2');
    frame('First schedule', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      try {
        await A.api.post('/schedules', { name: name.value.trim(), enabled: true, days: weekend.checked ? 127 : 31, start_time: start.value, end_time: end.value, source_type: 'directory', source_ref: '', shuffle: shuffle.checked, volume: null });
        go(5);
      } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.text-body-secondary', 'When should music play? The whole library plays during this window. Add more rules later in Schedule.'),
      h('label.form-label', 'Name'), name,
      h('div.row.g-2.mt-1', h('div.col-6', h('label.form-label', 'Start'), start), h('div.col-6', h('label.form-label', 'End'), end)),
      h('div.form-check.mt-3', weekend, h('label.form-check-label', { for: 'wiz-weekend' }, 'Also on Saturday and Sunday')),
      h('div.form-check', shuffle, h('label.form-check-label', { for: 'wiz-shuffle' }, 'Shuffle')), err),
    [next(), skip(5)]);
  }

  // The last step plays the whole library and asks for a confirmation.
  // Only an empty library allows a finish without the test.
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
    const noMusic = h('div.mt-3.d-none',
      h('div.alert.alert-info.small', 'The library has no tracks yet, or MPD is still scanning it. Upload music in Library, then test the sound from Settings > Audio.'),
      h('button.btn.btn-outline-secondary', { type: 'button', onclick: finish }, 'Finish without a test'));
    async function play() {
      clear(status).append(h('div.text-body-secondary', 'Starting…'));
      try {
        await A.queueAction({ action: 'play_now', folder: '/' });
      } catch (e) {
        if (e.status === 404) { clear(status); noMusic.classList.remove('d-none'); return; }
        clear(status).append(errorBox(e));
        return;
      }
      await refreshStatus();
      const song = state.status?.player?.song;
      clear(status).append(h('div', icon('music-note-beamed', 'me-1'), song ? `Playing ${song.title || song.file}` : 'Playing'));
      heard.classList.remove('d-none');
    }
    async function finish() {
      try {
        await save({ setup_complete: true });
        toast('Setup complete', 'success');
        navigate('#/');
      } catch (e) { toast(e.message, 'danger'); }
    }
    frame('Listen', h('div',
      h('p.text-body-secondary', 'Setup is done when sound comes out of the speakers. Play the library and confirm that you hear it.'),
      h('button.btn.btn-primary.btn-lg', { type: 'button', onclick: play }, icon('play-fill', 'me-1'), 'Play'),
      status, heard, noMusic),
    [h('button.btn.btn-link', { type: 'button', onclick: () => go(3) }, 'Back to output')]);
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
