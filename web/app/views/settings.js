import * as A from '../api.js';
import { h, clear, icon, toast, spinner, errorBox, confirmDialog, copyText, fmtTime, modal, tzDatalist } from '../dom.js';
import { signOut } from '../main.js';
import { dirPicker } from '../dirpicker.js';
import { timeField } from '../time.js';

// settingsView is one page with sections and a section list beside them.
// Each section renders from the current settings and saves the whole object.
export async function settingsView(main, rest) {
  clear(main);
  const box = h('section.page', spinner());
  main.append(box);
  let settings;
  try {
    settings = await A.settings();
  } catch (e) {
    clear(box).append(errorBox(e));
    return {};
  }

  async function save(patch, message = 'Saved') {
    try {
      settings = await A.saveSettings({ ...settings, ...patch });
      toast(message, 'success');
      return true;
    } catch (e) {
      toast(e.message, 'danger');
      return false;
    }
  }

  // Every section starts with a heading, and most hold labelled fields.
  const heading = (title, tools) => h('div.set-card-head', h('h2', title), tools ? h('div.tools', tools) : null);
  const field = (label, input) => h('label.d-block', h('span.form-label.d-block', label), input);

  const sections = [
    ['playback', 'Playback', 'sliders', renderPlayback],
    ['audio', 'Audio', 'speaker', renderAudio],
    ['schedule', 'Schedule', 'calendar-week', renderSchedule],
    ['library', 'Library', 'folder2', renderLibrary],
    ['security', 'Security', 'shield-lock', renderSecurity],
    ['system', 'System', 'hdd-stack', renderSystem],
    ['maintenance', 'Maintenance', 'tools', renderMaintenance],
  ];
  const nav = h('nav.set-nav', { 'aria-label': 'Settings sections' });
  const cards = h('div.stack');
  clear(box).append(h('h1.page-title.mb-3', 'Settings'), h('div.set-grid', nav, cards));
  const bodies = {};
  const links = {};
  for (const [id, title, ic, render] of sections) {
    const body = h('div');
    const card = h('div.set-card', { id: `settings-${id}` }, body);
    bodies[id] = { body, render, title };
    cards.append(card);
    // The router owns the hash, so a section link scrolls instead of
    // changing it.
    links[id] = h('a', { href: `#/settings/${id}`, onclick: (e) => { e.preventDefault(); history.replaceState(null, '', `#/settings/${id}`); card.scrollIntoView({ behavior: 'smooth' }); markNav(id); } }, icon(ic), title);
    nav.append(links[id]);
  }
  function markNav(id) {
    for (const [k, a] of Object.entries(links)) a.classList.toggle('active', k === id);
    if (matchMedia('(max-width: 900px)').matches) links[id].scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }
  // A section that fetches returns a promise. A link to a section waits for
  // all of them, because a section that fills later moves the one below it.
  function renderAll() { return Promise.all(Object.values(bodies).map((s) => s.render(clear(s.body)))); }
  const rendered = renderAll();
  markNav(rest && bodies[rest] ? rest : 'playback');
  if (rest && bodies[rest]) {
    await rendered;
    // The jump is instant: a smooth scroll that starts before the page is
    // ready is interrupted by the browser.
    const toSection = () => document.getElementById(`settings-${rest}`)?.scrollIntoView({ behavior: 'auto' });
    toSection();
    // A page that still loads is put back at the top by the browser, because
    // the address has no element with that name. The section is shown again
    // when the page is ready.
    if (document.readyState !== 'complete') window.addEventListener('load', toSection, { once: true });
  }

  // The section list follows the scroll: the active section is the last
  // card whose top has passed the upper part of the window.
  let scrollFrame = 0;
  const onScroll = () => {
    if (scrollFrame) return;
    scrollFrame = requestAnimationFrame(() => {
      scrollFrame = 0;
      let active = sections[0][0];
      for (const [id] of sections) {
        if (document.getElementById(`settings-${id}`)?.getBoundingClientRect().top < 160) active = id;
      }
      if (innerHeight + scrollY >= document.documentElement.scrollHeight - 4) active = sections[sections.length - 1][0];
      if (!links[active].classList.contains('active')) markNav(active);
    });
  };
  window.addEventListener('scroll', onScroll, { passive: true });

  function renderPlayback(body) {
    const min = h('input.form-control', { type: 'number', min: 0, max: 100, value: settings.volume_min });
    const max = h('input.form-control', { type: 'number', min: 0, max: 100, value: settings.volume_max });
    const cross = h('input.form-control', { type: 'number', min: 0, max: 30, value: settings.crossfade });
    const fin = h('input.form-control', { type: 'number', min: 0, max: 30, value: settings.fade_in });
    const fout = h('input.form-control', { type: 'number', min: 0, max: 30, value: settings.fade_out });
    const shuffle = h('input', { type: 'checkbox', checked: settings.default_shuffle });
    body.append(heading('Playback'), h('p.set-desc', 'The floor and ceiling apply to people and API clients. Fades bypass the floor.'),
      h('form', { onsubmit: async (e) => {
        e.preventDefault();
        await save({ volume_min: Number(min.value), volume_max: Number(max.value), crossfade: Number(cross.value), fade_in: Number(fin.value), fade_out: Number(fout.value), default_shuffle: shuffle.checked });
      } },
        h('div.field-grid',
          field('Volume floor', min),
          field('Volume ceiling', max),
          field('Crossfade (s)', cross),
          field('Fade in (s)', fin),
          field('Fade out (s)', fout)),
        h('label.check-line', shuffle, 'Shuffle new schedule rules by default'),
        h('div.set-actions', h('button.btn.btn-primary', { type: 'submit' }, 'Save playback'))));
  }

  async function renderAudio(body) {
    const rescan = h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: async () => { try { await A.rescanDevices(); bodies.audio.render(clear(body)); } catch (e) { toast(e.message, 'danger'); } } }, icon('arrow-clockwise'), 'Rescan');
    body.append(heading('Audio', rescan), spinner());
    let d;
    try { d = await A.devices(); } catch (e) { clear(body).append(heading('Audio', rescan), errorBox(e)); return; }
    clear(body).append(heading('Audio', rescan));
    if (!d.sys_readable) body.append(h('div.alert.alert-warning', icon('exclamation-triangle-fill'), h('div', '/sys is not readable, so devices are matched by card ID only. Two identical DACs may swap after a reboot.')));
    if (d.saved && !d.present) body.append(h('div.alert.alert-danger', icon('x-circle-fill'), h('div', `The selected output "${d.saved.name}" is not present. Music resumes when it comes back.`)));
    if (!d.devices.length) body.append(h('div.device', icon('speaker'), h('div.flex-1.muted', 'No playback devices found. Plug in a USB DAC or check the HAT overlay.')));
    for (const dev of d.devices) {
      body.append(h('div', { class: `device ${dev.selected ? 'selected' : ''}` },
        icon(dev.selected ? 'check-circle-fill' : 'circle'),
        h('div.flex-1',
          h('div.name', dev.name, dev.selected ? h('span.chip.chip-accent', 'selected') : null, dev.hidden ? h('span.chip.chip-neutral', 'virtual') : null),
          h('div.sub', `card ${dev.card_index} · ${dev.card_id} · device ${dev.device}`, dev.serial ? ` · serial ${dev.serial}` : '', dev.vendor_id ? ` · usb ${dev.vendor_id}:${dev.product_id}` : '')),
        h('div.row-actions',
          h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => mixerDialog(dev) }, 'Hardware level'),
          dev.selected ? null : h('button.btn.btn-sm.btn-primary', { type: 'button', onclick: async () => {
            try { await A.selectDevice(dev.key); toast(`Output: ${dev.name}`, 'success'); bodies.audio.render(clear(body)); } catch (e) { toast(e.message, 'danger'); }
          } }, 'Use'))));
    }
    const showAll = h('input', { type: 'checkbox', checked: settings.show_all_devices, onchange: async () => {
      if (await save({ show_all_devices: showAll.checked })) bodies.audio.render(clear(body)); else showAll.checked = !showAll.checked;
    } });
    body.append(h('label.check-line', showAll, 'Show loopback and dummy cards'));
  }

  async function mixerDialog(dev) {
    let m;
    try { m = await A.mixer(dev.key); } catch (e) { toast(e.message, 'danger'); return; }
    const select = h('select.form-select');
    m.controls = m.controls || [];
    for (const c of m.controls) select.append(h('option', { value: c.name, selected: m.remembered?.control === c.name }, `${c.name} — ${c.percent}%${c.muted ? ' (muted)' : ''}`));
    const level = h('input.form-range', { type: 'range', min: 0, max: 100, value: m.remembered?.level ?? (m.controls[0]?.percent ?? 80), 'aria-label': 'Level' });
    const levelLabel = h('span.mono', level.value);
    level.addEventListener('input', () => { levelLabel.textContent = level.value; });
    const reapply = h('input', { type: 'checkbox', checked: !!m.remembered?.reapply });
    const apply = h('button.btn.btn-primary', { type: 'button', onclick: async () => {
      try {
        await A.setMixer(dev.key, { control: select.value, level: Number(level.value), reapply: reapply.checked });
        toast('Applied. Play something to confirm.', 'success');
        dlg.hide();
      } catch (e) { toast(e.message, 'danger'); }
    } }, 'Apply');
    const dlg = modal({ title: `Hardware level: ${dev.name}`, body: m.controls.length ? [
      h('p.small-note', 'A muted ALSA control is a common cause of silence. jukem only touches a control you choose.'),
      h('label.form-label', 'Control'), select,
      h('label.form-label.mt-3', 'Level ', levelLabel, '%'), level,
      h('label.check-line', reapply, 'Reapply at every MPD start'),
    ] : h('p.mb-0', 'This card exposes no playback volume controls.'), footer: m.controls.length ? [h('button.btn.btn-outline-secondary', { type: 'button', 'data-bs-dismiss': 'modal' }, 'Cancel'), apply] : null });
  }

  function renderSchedule(body) {
    const enabled = h('input.form-check-input', { type: 'checkbox', role: 'switch', checked: settings.scheduler_enabled, id: 'set-sched', onchange: async () => {
      if (!await save({ scheduler_enabled: enabled.checked }, enabled.checked ? 'Scheduler on' : 'Scheduler off: manual mode')) enabled.checked = !enabled.checked;
    } });
    const tz = h('input.form-control.mono', { type: 'text', value: settings.time_zone, list: 'tz-list', placeholder: 'Europe/London' });
    const tzList = tzDatalist('tz-list');
    const format = h('select.form-select', { id: 'set-timefmt', onchange: async () => {
      if (!await save({ time_format: format.value }, 'Time format saved')) format.value = settings.time_format || '12h';
    } },
      h('option', { value: '12h', selected: (settings.time_format || '12h') === '12h' }, '12 hours (1:05 PM)'),
      h('option', { value: '24h', selected: settings.time_format === '24h' }, '24 hours (13:05)'));
    const clockBox = h('div.mt-3');
    loadClock(clockBox);
    body.append(heading('Schedule'),
      h('div.switch-line', h('div.form-check.form-switch.switch-only', enabled), h('label', { for: 'set-sched' }, h('b', 'Scheduler on.'), ' Off puts the appliance in manual mode: no schedule, no holds, no dead-air alerts.')),
      h('form.mt-3', { onsubmit: async (e) => { e.preventDefault(); await save({ time_zone: tz.value }); } },
        h('div.field-row', h('label.grow', h('span.form-label.d-block', 'Time zone'), tz), h('button.btn.btn-outline-secondary', { type: 'submit' }, 'Save')), tzList,
        h('div.form-text', `Browser zone: ${Intl.DateTimeFormat().resolvedOptions().timeZone}`)),
      h('div.mt-3',
        h('label.form-label', { for: 'set-timefmt' }, 'Time format'),
        format,
        h('div.form-text', 'jukem shows every time this way, and it asks for a time this way.')),
      clockBox);
  }

  async function loadClock(clockBox) {
    let c;
    try { c = await A.api.get('/clock'); } catch { clear(clockBox).append(h('div.small-note', 'Clock status arrives with the scheduler.')); return; }
    clear(clockBox);
    const label = { ntp: 'Synchronized (NTP)', rtc: 'Hardware clock (RTC)', manual: `Set by hand ${fmtTime(c.set_at)}`, none: 'Clock not set' }[c.source] || c.source;
    const bad = c.source === 'none';
    clockBox.append(h('div', { class: `info-strip ${bad ? 'bad' : ''}` }, icon(bad ? 'x-circle-fill' : 'clock'), h('span', `Clock: ${label}`), h('span.mono', c.now_local || '')));
    if (c.source === 'none' || c.source === 'manual') {
      // The form starts at the date and the time of this browser. A
      // person who knows them keeps them, and the clock does not go to a
      // time that nobody chose.
      const local = new Date();
      const pad = (n) => String(n).padStart(2, '0');
      const date = h('input.form-control', { type: 'date', 'aria-label': 'Date', value: `${local.getFullYear()}-${pad(local.getMonth() + 1)}-${pad(local.getDate())}` });
      const time = timeField({ value: `${pad(local.getHours())}:${pad(local.getMinutes())}`, label: 'Time' });
      clockBox.append(h('form.mt-3', { onsubmit: async (e) => {
        e.preventDefault();
        try {
          await A.api.put('/clock', { date: date.value, time: time.value, time_zone: settings.time_zone });
          toast('Clock set for this boot', 'success');
          loadClock(clockBox);
        } catch (ex) { toast(ex.message, 'danger'); }
      } },
        h('div.field-row', date, time.el, h('button.btn.btn-outline-secondary', { type: 'submit' }, 'Set clock')),
        h('div.form-text', 'Without an RTC or NTP the time must be entered again after every reboot. To fix the system clock properly, on the console: date -s "YYYY-MM-DD HH:MM" and hwclock -w')));
    }
  }

  function renderLibrary(body) {
    const root = h('input.form-control.mono', { type: 'text', value: settings.music_root, 'aria-label': 'Music root' });
    const maxMB = h('input.form-control', { type: 'number', min: 1, value: Math.round(settings.upload_max_bytes / 1048576) });
    const exts = h('input.form-control.mono', { type: 'text', value: settings.allowed_extensions.join(' ') });
    const reserveMB = h('input.form-control', { type: 'number', min: 0, value: Math.round(settings.free_space_reserve / 1048576) });
    const hour = h('input.form-control', { type: 'number', min: 0, max: 23, value: settings.nightly_rescan_hour });
    body.append(heading('Library'), h('form', { onsubmit: async (e) => {
      e.preventDefault();
      if (root.value !== settings.music_root && !await confirmDialog({ title: 'Change the music root?', body: 'MPD restarts and rescans. Playlist entries are relative to the root, so they only resolve if the new root has the same structure.', confirmText: 'Change' })) return;
      await save({ music_root: root.value, upload_max_bytes: Number(maxMB.value) * 1048576, allowed_extensions: exts.value.split(/[\s,]+/).filter(Boolean), free_space_reserve: Number(reserveMB.value) * 1048576, nightly_rescan_hour: Number(hour.value) });
    } },
      h('span.form-label.d-block', 'Music root'), h('div.input-row', root, h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => dirPicker({ start: root.value, onPick: (p) => { root.value = p; } }) }, 'Browse…')),
      h('div.field-grid.mt-3',
        field('Max upload (MB)', maxMB),
        field('Free space reserve (MB)', reserveMB),
        field('Nightly rescan hour', hour)),
      h('div.mt-3', field('Allowed extensions', exts)),
      h('div.set-actions', h('button.btn.btn-primary', { type: 'submit' }, 'Save library'))));
    const dnpBox = h('div.set-sub');
    body.append(dnpBox);
    renderDoNotPlay(dnpBox);
  }

  // renderDoNotPlay lists the excluded tracks, each with a way back.
  async function renderDoNotPlay(box) {
    let r;
    try { r = await A.api.get('/do-not-play'); } catch (e) { clear(box).append(errorBox(e)); return; }
    clear(box).append(h('h3', 'Do not play'));
    if (!r.entries.length) { box.append(h('p.small-note.mb-0', 'No track is excluded from scheduled playback.')); return; }
    const list = h('div.set-list');
    for (const e of r.entries) {
      list.append(h('div.set-list-item',
        h('div.flex-1', h('div.row-title', e.title || e.file), e.title ? h('div.row-sub.mono', e.file) : null),
        h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: async () => {
          try { await A.api.del(`/do-not-play?file=${encodeURIComponent(e.file)}`); renderDoNotPlay(box); } catch (ex) { toast(ex.message, 'danger'); }
        } }, 'Allow again')));
    }
    box.append(list);
  }

  async function renderSecurity(body) {
    const cur = h('input.form-control', { type: 'password', autocomplete: 'current-password' });
    const nw = h('input.form-control', { type: 'password', autocomplete: 'new-password', minlength: 8 });
    body.append(heading('Security'), h('form', { onsubmit: async (e) => {
      e.preventDefault();
      try { await A.changePassword(cur.value, nw.value); toast('Password changed. Sign in again.', 'success'); signOut(); } catch (ex) { toast(ex.message, 'danger'); }
    } },
      h('h3', 'Password'),
      h('div.field-row',
        h('label.grow', h('span.form-label.d-block', 'Current'), cur),
        h('label.grow', h('span.form-label.d-block', 'New (8+ characters)'), nw),
        h('button.btn.btn-outline-secondary', { type: 'submit' }, 'Change')),
      h('div.form-text', 'Every signed-in device is signed out.')));
    const keysBox = h('div');
    body.append(h('div.set-sub', h('h3', 'API keys'), keysBox));
    async function loadKeys() {
      let keys;
      try { keys = (await A.apiKeys()).keys; } catch (e) { clear(keysBox).append(errorBox(e)); return; }
      clear(keysBox);
      const list = h('div.set-list');
      for (const k of keys) {
        list.append(h('div.set-list-item',
          h('div.flex-1', h('div.row-title.fw-semibold', k.name), h('div.row-sub.mono.text-wrap', `created ${fmtTime(k.created_at)}`, k.last_used_at ? ` · last used ${fmtTime(k.last_used_at)} from ${k.last_used_ip}` : ' · never used', k.expires_at ? ` · expires ${fmtTime(k.expires_at)}` : '')),
          h('button.btn.btn-sm.btn-outline-danger', { type: 'button', onclick: async () => {
            if (!await confirmDialog({ title: 'Revoke key', body: `Revoke "${k.name}"? The next request with it is rejected.`, confirmText: 'Revoke', danger: true })) return;
            try { await A.deleteApiKey(k.id); loadKeys(); } catch (e) { toast(e.message, 'danger'); }
          } }, 'Revoke')));
      }
      if (!keys.length) list.append(h('p.small-note.mb-0', 'No API keys.'));
      const name = h('input.form-control', { type: 'text', placeholder: 'Counter tablet app', required: true, 'aria-label': 'Key name' });
      keysBox.append(list, h('form.input-row.mt-3', { onsubmit: async (e) => {
        e.preventDefault();
        try {
          const k = await A.createApiKey(name.value);
          const keyField = h('input.form-control.mono', { type: 'text', readonly: true, value: k.key, 'aria-label': 'API key' });
          modal({ title: 'New API key', body: [h('p', 'Copy it now. It is not shown again.'), h('div.input-row', keyField, h('button.btn.btn-outline-secondary.btn-icon.s36', { type: 'button', 'aria-label': 'Copy', title: 'Copy', onclick: () => { copyText(k.key); toast('Copied', 'success'); } }, icon('clipboard'))), h('p.small-note.mt-2.mb-0', 'Send it as: Authorization: Bearer <key>')] });
          name.value = '';
          loadKeys();
        } catch (ex) { toast(ex.message, 'danger'); }
      } }, name, h('button.btn.btn-primary', { type: 'submit' }, 'Create key')));
    }
    loadKeys();
  }

  async function renderSystem(body) {
    let info = null;
    try { info = await A.systemInfo(); } catch { /* shown as unknown */ }
    const url = h('input.form-control.mono', { type: 'url', value: settings.alert_webhook_url, placeholder: 'https://ntfy.sh/my-jukebox', 'aria-label': 'Webhook URL' });
    const preset = h('select.form-select', { 'aria-label': 'Format' });
    for (const [v, l] of [['generic', 'Generic JSON'], ['ntfy', 'ntfy']]) preset.append(h('option', { value: v, selected: settings.alert_webhook_preset === v }, l));
    const days = h('input.form-control', { type: 'number', min: 1, value: settings.history_days });
    const rows = h('input.form-control', { type: 'number', min: 100, value: settings.history_rows });
    const adays = h('input.form-control', { type: 'number', min: 1, value: settings.alert_days });
    const upd = h('div.upd');
    body.append(heading('System'),
      h('div.meta-line', `jukem ${info?.version ?? '?'} · schema ${info?.schema_version ?? '?'} · runtime ${info?.runtime ?? '?'}`),
      h('div.link-line', h('a', { href: '#/health' }, 'Health page'), h('a', { href: '#/history' }, 'Play history'), h('a', { href: '/api/v1/docs', target: '_blank', rel: 'noopener' }, 'API docs ↗')),
      upd,
      h('form', { onsubmit: async (e) => { e.preventDefault(); await save({ alert_webhook_url: url.value, alert_webhook_preset: preset.value, history_days: Number(days.value), history_rows: Number(rows.value), alert_days: Number(adays.value) }); } },
        h('h3', 'Alerts'),
        h('span.form-label.d-block', 'Webhook URL (JSON POST)'),
        h('div.field-row', h('div.grow', url), preset,
          h('button.btn.btn-outline-secondary', { type: 'button', onclick: async () => { try { await A.api.post('/alerts/test'); toast('Test alert sent', 'success'); } catch (ex) { toast(ex.message, 'danger'); } } }, 'Send test')),
        h('h3.mt-4', 'Retention'),
        h('div.field-grid',
          field('History days', days),
          field('History rows', rows),
          field('Dismissed alerts days', adays)),
        h('div.set-actions', h('button.btn.btn-primary', { type: 'submit' }, 'Save system'))),
      h('div.set-sub',
        h('p.small-note.mb-1', 'Log file: /var/log/jukem/jukem.log (or stdout in Docker).'),
        h('p.small-note.mb-0', 'Logo: "Music Library 2" from the Solar icon set by 480 Design, CC BY 4.0. jukem changed the colours.')));
    drawUpdate(upd, null, true);
    A.updateStatus().then((u) => drawUpdate(upd, u)).catch((e) => drawUpdate(upd, { error: e.message }));
  }

  // drawUpdate shows the result of the last check for a new release. jukem
  // does not install the package: the commands below do that.
  function drawUpdate(box, u, loading) {
    clear(box);
    box.classList.toggle('new', !!u?.available);
    if (loading) { box.append(h('div.upd-line', 'Looking for a new version…')); return; }
    if (u?.available) {
      box.append(h('div.upd-line', icon('arrow-up-circle'),
        h('span', h('b', `Version ${u.version} is available.`), ` This appliance has ${u.current}.`),
        u.url ? h('a', { href: u.url, target: '_blank', rel: 'noopener' }, 'Release notes ↗') : null));
      const cmd = installCommands(u.version);
      box.append(h('p.small-note.mb-0.mt-2', 'Install it as root over SSH:'), h('pre.pre-wrap', cmd));
    } else {
      box.append(h('div.upd-line', icon('check2-circle'),
        u?.version ? `Version ${u.version} is the newest one. This appliance is up to date.` : 'No release has been found yet.'));
    }
    box.append(h('p.small-note.mb-0.mt-2',
      u?.checked_at ? `Last checked: ${fmtTime(u.checked_at)}` : 'Not checked yet.'));
    if (u?.error) box.append(h('p.small-note.mb-0', `The last check failed: ${u.error}`));

    const auto = h('input.form-check-input', { type: 'checkbox', checked: settings.update_check, onchange: async () => {
      if (!await save({ update_check: auto.checked })) auto.checked = !auto.checked;
    } });
    box.append(h('div.set-actions',
      h('button.btn.btn-outline-secondary', { type: 'button', onclick: async (e) => {
        const btn = e.currentTarget;
        btn.disabled = true;
        drawUpdate(box, null, true);
        try { drawUpdate(box, await A.checkUpdate()); } catch (ex) { toast(ex.message, 'danger'); drawUpdate(box, u); }
      } }, icon('arrow-repeat'), 'Check now'),
      u?.available ? h('button.btn.btn-outline-secondary', { type: 'button', onclick: async () => {
        try { await copyText(installCommands(u.version)); toast('Commands copied', 'success'); } catch (ex) { toast(ex.message, 'danger'); }
      } }, icon('clipboard'), 'Copy commands') : null,
      h('label.form-check.form-switch.switch-only.ms-auto', auto, h('span.ms-2', 'Check every day'))));
  }

  // installCommands is the same procedure as the readme, with the version
  // filled in.
  function installCommands(version) {
    return `ARCH=$(apk --print-arch)
`
      + `wget https://github.com/ethanpil/jukem/releases/download/v${version}/jukem-${version}-$ARCH.apk
`
      + `apk add --allow-untrusted ./jukem-${version}-$ARCH.apk`;
  }

  function renderMaintenance(body) {
    const btn = (ic, label, fn, cls = 'btn-outline-secondary') => h('button.btn', { type: 'button', class: `btn ${cls}`, onclick: fn }, icon(ic), label);
    body.append(heading('Maintenance'), h('div.tool-row',
      btn('arrow-clockwise', 'Rescan library', async () => { try { await A.rescanLibrary(); toast('Library scan started. The Library shows its progress.', 'success'); } catch (e) { toast(e.message, 'danger'); } }),
      btn('check2-circle', 'Check library permissions', async () => {
        try {
          const r = await A.api.post('/library/permissions/check');
          modal({ title: 'Library permissions', body: r.problems?.length ? [h('p', `${r.problems.length} folders are not writable.`), h('pre.pre-wrap', r.problems.join('\n')), h('p.small-note', r.fix), h('pre.pre-wrap.mb-0', r.command)] : h('p.mb-0', 'Every folder is writable.') });
        } catch (e) { toast(e.message, 'danger'); }
      }),
      btn('database', 'Database snapshot', async () => { try { const r = await A.api.post('/system/snapshot'); toast(`Snapshot written: ${r.path}`, 'success'); } catch (e) { toast(e.message, 'danger'); } }),
      btn('power', 'Restart service', async () => {
        if (!await confirmDialog({ title: 'Restart jukem?', body: 'Playback stops for a few seconds and resumes on its own.', confirmText: 'Restart', danger: true })) return;
        try { await A.api.post('/system/restart'); toast('Restarting', 'warning'); } catch (e) { toast(e.message, 'danger'); }
      }, 'btn-outline-danger')),
    h('div.set-sub', btn('box-arrow-right', 'Sign out', signOut)));
  }

  // Another client can change settings while this page is open. A save sends
  // the whole object and reads it from the inputs, so the copy and the
  // inputs must both follow the change. The sections are drawn again only
  // when a value really differs, so a save from this page does not clear a
  // field that somebody types in.
  return {
    async onEvent(type) {
      if (type === 'devices') bodies.audio.render(clear(bodies.audio.body));
      if (type === 'settings') {
        let fresh;
        try { fresh = await A.settings(); } catch { return; /* keep the copy */ }
        const changed = JSON.stringify(fresh) !== JSON.stringify(settings);
        settings = fresh;
        if (changed) renderAll();
      }
    },
    destroy() {
      window.removeEventListener('scroll', onScroll);
      cancelAnimationFrame(scrollFrame);
    },
  };
}
