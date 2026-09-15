import * as A from '../api.js';
import { h, clear, icon, toast, errorBox, tzDatalist, logo } from '../dom.js';
import { dirPicker } from '../dirpicker.js';
import { navigate } from '../router.js';
import { state, refreshStatus } from '../main.js';

// The wizard runs on the first visit: password, time zone, music root,
// output device, a first schedule, and a track that must be heard. Setup
// is complete when sound came out, not when a row exists.
const STEPS = ['Password', 'Time zone', 'Music', 'Output', 'Schedule', 'Listen'];

export async function wizardView(main, _rest, opts = {}) {
  clear(main);
  const box = h('div.panel.panel-pad');
  main.append(h('section.page.page-sm.mt-2', box));
  let settings = null;
  let step = opts.firstRun ? 0 : 1;

  function frame(title, body, footer) {
    clear(box);
    const steps = h('div.steps', { 'aria-label': `Step ${step + 1} of ${STEPS.length}` });
    STEPS.forEach((name, i) => steps.append(h('div', { class: `step ${i === step ? 'current' : i < step ? 'done' : ''}`, title: name }, h('div.bar'), h('div.name', name))));
    box.append(h('div.wiz-head', logo(), h('h1', title), h('span.step-of', `${step + 1} / ${STEPS.length}`)), steps, body, h('div.wiz-foot', footer));
  }
  const go = (n) => { step = n; render(); };
  // The footer sits outside the form, so the button names it.
  const next = () => h('button.btn.btn-primary', { type: 'submit', form: 'wiz' }, 'Continue', icon('arrow-right'));
  const skip = (n) => h('button.btn.btn-link.ms-auto', { type: 'button', onclick: () => go(n) }, 'Skip');
  const field = (label, input, id) => [h('label.form-label', id ? { for: id } : null, label), input];

  async function loadSettings() {
    if (!settings) settings = await A.settings();
    return settings;
  }
  async function save(patch) {
    settings = await A.saveSettings({ ...(await loadSettings()), ...patch });
  }

  function stepPassword() {
    const pw = h('input.form-control.form-control-lg', { type: 'password', autocomplete: 'new-password', minlength: 8, required: true, id: 'setup-password' });
    const pw2 = h('input.form-control.form-control-lg', { type: 'password', autocomplete: 'new-password', minlength: 8, required: true, id: 'setup-password-again' });
    const err = h('div.form-error');
    frame('Welcome to jukem', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      err.textContent = '';
      if (pw.value !== pw2.value) { err.textContent = 'The passwords differ.'; return; }
      try {
        // The app shell signs in and opens the wizard again at step two.
        await opts.onSignedIn(await A.setup(pw.value));
      } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.wiz-lead', 'Set the password for this jukebox. One login controls everything.'),
      field('Password (8+ characters)', pw, 'setup-password'),
      h('div.mt-3', field('Again', pw2, 'setup-password-again')), err),
    [next()]);
    pw.focus();
  }

  async function stepTimeZone() {
    const set = await loadSettings();
    const browser = Intl.DateTimeFormat().resolvedOptions().timeZone;
    const tz = h('input.form-control.mono', { type: 'text', value: set.time_zone === 'UTC' && browser ? browser : set.time_zone, list: 'wiz-tz', required: true, id: 'wiz-zone' });
    const err = h('div.form-error');
    frame('Time zone', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      try { await save({ time_zone: tz.value.trim() }); go(2); } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.wiz-lead', 'Every schedule uses this zone. Daylight saving is handled for you.'),
      field('Zone', tz, 'wiz-zone'), tzDatalist('wiz-tz'), err),
    [next()]);
  }

  async function stepMusic() {
    const set = await loadSettings();
    const root = h('input.form-control.mono', { type: 'text', value: set.music_root, required: true, id: 'wiz-root' });
    const err = h('div.form-error');
    frame('Music', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      try { await save({ music_root: root.value.trim() }); go(3); } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.wiz-lead', 'The folder MPD reads music from. A NAS mount or a USB drive is usually under /mnt or /media. You can also upload music later in Library.'),
      h('label.form-label', { for: 'wiz-root' }, 'Music root'), h('div.input-row', root, h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => dirPicker({ start: root.value, onPick: (p) => { root.value = p; } }) }, 'Browse…')),
      h('div.form-text', 'Music copied here as root must be given to the jukem user: chown -R jukem:jukem <folder>'), err),
    [next()]);
  }

  async function stepOutput() {
    const body = h('div');
    frame('Output device', h('form#wiz', { onsubmit: (e) => { e.preventDefault(); go(4); } }, h('p.wiz-lead', 'Choose the sound card the speakers are connected to.'), body), [next(), skip(4)]);
    async function load() {
      let d;
      try { d = await A.devices(); } catch (e) { clear(body).append(errorBox(e)); return; }
      clear(body);
      if (!d.devices.length) body.append(h('div.device', icon('speaker'), h('div.flex-1.muted', 'No playback devices found. Plug in a USB DAC or check the HAT overlay, then rescan.')));
      for (const dev of d.devices) {
        body.append(h('button', { type: 'button', class: `device ${dev.selected ? 'selected' : ''}`, 'aria-pressed': String(!!dev.selected), onclick: async () => {
          try { await A.selectDevice(dev.key); load(); } catch (e) { toast(e.message, 'danger'); }
        } },
          icon(dev.selected ? 'check-circle-fill' : 'circle'),
          h('div.flex-1', h('div.name', dev.name, dev.selected ? h('span.chip.chip-accent', 'selected') : null), h('div.sub', `card ${dev.card_index} · ${dev.card_id}`))));
      }
      body.append(h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: async () => { try { await A.rescanDevices(); load(); } catch (e) { toast(e.message, 'danger'); } } }, icon('arrow-clockwise'), 'Rescan'));
    }
    load();
  }

  async function stepSchedule() {
    const set = await loadSettings();
    // A person who returns to the wizard has a rule already: nothing to add.
    if ((await A.api.get('/schedules')).schedules.length) { go(5); return; }
    const name = h('input.form-control', { type: 'text', value: 'Opening hours', required: true, maxlength: 100, id: 'wiz-name' });
    const start = h('input.form-control', { type: 'time', value: '09:00', required: true, id: 'wiz-start' });
    const end = h('input.form-control', { type: 'time', value: '17:00', required: true, id: 'wiz-end' });
    const weekend = h('input', { type: 'checkbox' });
    const shuffle = h('input', { type: 'checkbox', checked: set.default_shuffle });
    const err = h('div.form-error');
    frame('First schedule', h('form#wiz', { onsubmit: async (e) => {
      e.preventDefault();
      try {
        await A.api.post('/schedules', { name: name.value.trim(), enabled: true, days: weekend.checked ? 127 : 31, start_time: start.value, end_time: end.value, source_type: 'directory', source_ref: '', shuffle: shuffle.checked, volume: null });
        go(5);
      } catch (ex) { err.textContent = ex.message; }
    } },
      h('p.wiz-lead', 'When should music play? The whole library plays during this window. Add more rules later in Schedule.'),
      field('Name', name, 'wiz-name'),
      h('div.row.g-2.mt-2', h('div.col-6', field('Start', start, 'wiz-start')), h('div.col-6', field('End', end, 'wiz-end'))),
      h('label.check-line', weekend, 'Also on Saturday and Sunday'),
      h('label.check-line', shuffle, 'Shuffle'), err),
    [next(), skip(5)]);
  }

  // The last step plays the whole library and asks for a confirmation.
  // Only an empty library allows a finish without the test.
  function stepListen() {
    const status = h('div.mt-3');
    const heard = h('div.mt-3.hidden',
      h('p.fw-semibold.mb-2', 'Do you hear it?'),
      h('div.d-flex.flex-wrap.gap-2',
        h('button.btn.btn-primary', { type: 'button', onclick: finish }, icon('check-lg'), 'Yes, I hear music'),
        h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => {
          clear(status).append(h('div.alert.alert-warning', icon('exclamation-triangle-fill'),
            h('div',
              h('div.fw-semibold', 'No sound? Check these, then play again.'),
              h('ul.mb-0.mt-1.ps-3',
                h('li', 'Is the right output device selected? Go back one step.'),
                h('li', 'A muted hardware control is a common cause. Settings > Audio > Hardware level.'),
                h('li', 'Is the amplifier on and the volume up?')))));
        } }, 'No')));
    const noMusic = h('div.mt-3.hidden',
      h('div.alert.alert-info', icon('info-circle'), h('div', 'The library has no tracks yet, or MPD is still scanning it. Upload music in Library, then test the sound from Settings > Audio.')),
      h('button.btn.btn-outline-secondary', { type: 'button', onclick: finish }, 'Finish without a test'));
    async function play() {
      clear(status).append(h('div.muted', 'Starting…'));
      try {
        await A.queueAction({ action: 'play_now', folder: '/' });
      } catch (e) {
        if (e.status === 404) { clear(status); noMusic.classList.remove('hidden'); return; }
        clear(status).append(errorBox(e));
        return;
      }
      await refreshStatus();
      const song = state.status?.player?.song;
      clear(status).append(h('div.info-strip', icon('music-note-beamed'), song ? `Playing ${song.title || song.file}` : 'Playing'));
      heard.classList.remove('hidden');
    }
    async function finish() {
      try {
        await save({ setup_complete: true });
        toast('Setup complete', 'success');
        navigate('#/');
      } catch (e) { toast(e.message, 'danger'); }
    }
    frame('Listen', h('div',
      h('p.wiz-lead', 'Setup is done when sound comes out of the speakers. Play the library and confirm that you hear it.'),
      h('button.btn.btn-primary.btn-lg.raised', { type: 'button', onclick: play }, icon('play-fill'), 'Play'),
      status, heard, noMusic),
    [h('button.btn.btn-link.ps-0', { type: 'button', onclick: () => go(3) }, icon('arrow-left'), 'Back to output')]);
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
