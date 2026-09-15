import * as A from '../api.js';
import { h, clear, icon, toast, spinner, errorBox, confirmDialog, copyText, fmtTime, modal } from '../dom.js';
import { signOut } from '../main.js';

// settingsView is one page with sections. Each section renders from the
// current settings and saves the whole object.
export async function settingsView(main, rest) {
  clear(main);
  const box = h('div.mx-auto', { style: 'max-width: 800px' }, spinner());
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

  const sectionsBox = h('div');
  clear(box).append(h('h1.h3.mb-3', 'Settings'), sectionsBox);
  const sections = [
    ['playback', 'Playback', renderPlayback],
    ['audio', 'Audio', renderAudio],
    ['schedule', 'Schedule', renderSchedule],
    ['library', 'Library', renderLibrary],
    ['security', 'Security', renderSecurity],
    ['system', 'System', renderSystem],
    ['maintenance', 'Maintenance', renderMaintenance],
  ];
  const bodies = {};
  for (const [id, title, render] of sections) {
    const body = h('div.card-body');
    bodies[id] = { body, render };
    sectionsBox.append(h('div.card.mb-3', { id: `settings-${id}` }, h('div.card-header.fw-semibold', title), body));
  }
  function renderAll() { for (const s of Object.values(bodies)) s.render(clear(s.body)); }
  renderAll();
  if (rest) document.getElementById(`settings-${rest}`)?.scrollIntoView();

  function renderPlayback(body) {
    const min = h('input.form-control', { type: 'number', min: 0, max: 100, value: settings.volume_min });
    const max = h('input.form-control', { type: 'number', min: 0, max: 100, value: settings.volume_max });
    const cross = h('input.form-control', { type: 'number', min: 0, max: 30, value: settings.crossfade });
    const fin = h('input.form-control', { type: 'number', min: 0, max: 30, value: settings.fade_in });
    const fout = h('input.form-control', { type: 'number', min: 0, max: 30, value: settings.fade_out });
    const shuffle = h('input.form-check-input', { type: 'checkbox', checked: settings.default_shuffle, id: 'set-shuffle' });
    body.append(h('form', { onsubmit: async (e) => {
      e.preventDefault();
      await save({ volume_min: Number(min.value), volume_max: Number(max.value), crossfade: Number(cross.value), fade_in: Number(fin.value), fade_out: Number(fout.value), default_shuffle: shuffle.checked });
    } },
      h('div.row.g-3',
        h('div.col-6.col-md-3', h('label.form-label', 'Volume floor'), min),
        h('div.col-6.col-md-3', h('label.form-label', 'Volume ceiling'), max),
        h('div.col-6.col-md-2', h('label.form-label', 'Crossfade (s)'), cross),
        h('div.col-6.col-md-2', h('label.form-label', 'Fade in (s)'), fin),
        h('div.col-6.col-md-2', h('label.form-label', 'Fade out (s)'), fout)),
      h('div.form-check.mt-3', shuffle, h('label.form-check-label', { for: 'set-shuffle' }, 'Shuffle new schedule rules by default')),
      h('div.form-text', 'The floor and ceiling apply to people and API clients. Fades bypass the floor.'),
      h('button.btn.btn-primary.mt-3', { type: 'submit' }, 'Save playback')));
  }

  async function renderAudio(body) {
    body.append(spinner());
    let d;
    try { d = await A.devices(); } catch (e) { clear(body).append(errorBox(e)); return; }
    clear(body);
    if (!d.sys_readable) body.append(h('div.alert.alert-warning.small', '/sys is not readable, so devices are matched by card ID only. Two identical DACs may swap after a reboot.'));
    if (d.saved && !d.present) body.append(h('div.alert.alert-danger', `The selected output "${d.saved.name}" is not present. Music resumes when it comes back.`));
    const list = h('div.list-group.mb-3');
    if (!d.devices.length) list.append(h('div.list-group-item.text-body-secondary', 'No playback devices found. Plug in a USB DAC or check the HAT overlay.'));
    for (const dev of d.devices) {
      list.append(h('div.list-group-item.d-flex.align-items-center.gap-2',
        h('div.flex-grow-1',
          h('div.fw-semibold', dev.name, dev.selected ? h('span.badge.text-bg-success.ms-2', 'selected') : null, dev.hidden ? h('span.badge.text-bg-secondary.ms-2', 'virtual') : null),
          h('div.small.text-body-secondary.mono', `card ${dev.card_index} · ${dev.card_id} · device ${dev.device}`, dev.serial ? ` · serial ${dev.serial}` : '', dev.vendor_id ? ` · usb ${dev.vendor_id}:${dev.product_id}` : '')),
        h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: () => mixerDialog(dev) }, 'Hardware level'),
        dev.selected ? null : h('button.btn.btn-sm.btn-primary', { type: 'button', onclick: async () => {
          try { await A.selectDevice(dev.key); toast(`Output: ${dev.name}`, 'success'); bodies.audio.render(clear(body)); } catch (e) { toast(e.message, 'danger'); }
        } }, 'Use')));
    }
    const showAll = h('input.form-check-input', { type: 'checkbox', checked: settings.show_all_devices, id: 'set-showall', onchange: async () => {
      if (await save({ show_all_devices: showAll.checked })) bodies.audio.render(clear(body));
    } });
    body.append(list,
      h('div.d-flex.align-items-center.gap-3',
        h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: async () => { try { await A.rescanDevices(); bodies.audio.render(clear(body)); } catch (e) { toast(e.message, 'danger'); } } }, icon('arrow-clockwise', 'me-1'), 'Rescan'),
        h('div.form-check.mb-0', showAll, h('label.form-check-label', { for: 'set-showall' }, 'Show loopback and dummy cards'))));
  }

  async function mixerDialog(dev) {
    let m;
    try { m = await A.mixer(dev.key); } catch (e) { toast(e.message, 'danger'); return; }
    const select = h('select.form-select');
    for (const c of m.controls) select.append(h('option', { value: c.name, selected: m.remembered?.control === c.name }, `${c.name} — ${c.percent}%${c.muted ? ' (muted)' : ''}`));
    const level = h('input.form-range', { type: 'range', min: 0, max: 100, value: m.remembered?.level ?? (m.controls[0]?.percent ?? 80) });
    const levelLabel = h('span.mono', level.value);
    level.addEventListener('input', () => { levelLabel.textContent = level.value; });
    const reapply = h('input.form-check-input', { type: 'checkbox', checked: !!m.remembered?.reapply, id: 'mix-reapply' });
    const apply = h('button.btn.btn-primary', { type: 'button', onclick: async () => {
      try {
        await A.setMixer(dev.key, { control: select.value, level: Number(level.value), reapply: reapply.checked });
        toast('Applied. Play something to confirm.', 'success');
        dlg.hide();
      } catch (e) { toast(e.message, 'danger'); }
    } }, 'Apply');
    const dlg = modal({ title: `Hardware level: ${dev.name}`, body: m.controls.length ? [
      h('p.small.text-body-secondary', 'A muted ALSA control is a common cause of silence. jukem only touches a control you choose.'),
      h('label.form-label', 'Control'), select,
      h('label.form-label.mt-3', 'Level ', levelLabel, '%'), level,
      h('div.form-check.mt-3', reapply, h('label.form-check-label', { for: 'mix-reapply' }, 'Reapply at every MPD start')),
    ] : h('p', 'This card exposes no playback volume controls.'), footer: m.controls.length ? apply : null });
  }

  function renderSchedule(body) {
    const enabled = h('input.form-check-input', { type: 'checkbox', checked: settings.scheduler_enabled, id: 'set-sched', onchange: async () => {
      await save({ scheduler_enabled: enabled.checked }, enabled.checked ? 'Scheduler on' : 'Scheduler off: manual mode');
    } });
    const tz = h('input.form-control', { type: 'text', value: settings.time_zone, list: 'tz-list', placeholder: 'Europe/London' });
    const tzList = h('datalist#tz-list');
    for (const z of (Intl.supportedValuesOf ? Intl.supportedValuesOf('timeZone') : [])) tzList.append(h('option', { value: z }));
    const clockBox = h('div.mt-3');
    loadClock(clockBox);
    body.append(
      h('div.form-check.form-switch.mb-3', enabled, h('label.form-check-label', { for: 'set-sched' }, 'Scheduler on. Off puts the appliance in manual mode: no schedule, no overrides, no dead-air alerts.')),
      h('form', { onsubmit: async (e) => { e.preventDefault(); await save({ time_zone: tz.value }); } },
        h('label.form-label', 'Time zone'), h('div.d-flex.gap-2', tz, h('button.btn.btn-primary', { type: 'submit' }, 'Save')), tzList,
        h('div.form-text', `Browser zone: ${Intl.DateTimeFormat().resolvedOptions().timeZone}`)),
      clockBox);
  }

  async function loadClock(clockBox) {
    let c;
    try { c = await A.api.get('/clock'); } catch { clear(clockBox).append(h('div.small.text-body-secondary', 'Clock status arrives with the scheduler.')); return; }
    clear(clockBox);
    const label = { ntp: 'Synchronized (NTP)', rtc: 'Hardware clock (RTC)', manual: `Set by hand ${fmtTime(c.set_at)}`, none: 'Clock not set' }[c.source] || c.source;
    clockBox.append(h('div.d-flex.align-items-center.gap-2', icon(c.source === 'none' ? 'x-circle-fill' : 'check-circle-fill', c.source === 'none' ? 'text-danger' : 'text-success'),
      h('span', `Clock: ${label}`), h('span.small.text-body-secondary.mono', c.now_local || '')));
    if (c.source === 'none' || c.source === 'manual') {
      const date = h('input.form-control', { type: 'date' });
      const time = h('input.form-control', { type: 'time' });
      clockBox.append(h('form.mt-2', { onsubmit: async (e) => {
        e.preventDefault();
        try {
          await A.api.put('/clock', { date: date.value, time: time.value, time_zone: settings.time_zone });
          toast('Clock set for this boot', 'success');
          loadClock(clockBox);
        } catch (ex) { toast(ex.message, 'danger'); }
      } },
        h('div.row.g-2', h('div.col-auto', date), h('div.col-auto', time), h('div.col-auto', h('button.btn.btn-outline-primary', { type: 'submit' }, 'Set clock'))),
        h('div.form-text', 'Without an RTC or NTP the time must be entered again after every reboot. To fix the system clock properly, on the console: date -s "YYYY-MM-DD HH:MM" and hwclock -w')));
    }
  }

  function renderLibrary(body) {
    const root = h('input.form-control', { type: 'text', value: settings.music_root });
    const maxMB = h('input.form-control', { type: 'number', min: 1, value: Math.round(settings.upload_max_bytes / 1048576) });
    const exts = h('input.form-control', { type: 'text', value: settings.allowed_extensions.join(' ') });
    const reserveMB = h('input.form-control', { type: 'number', min: 0, value: Math.round(settings.free_space_reserve / 1048576) });
    const hour = h('input.form-control', { type: 'number', min: 0, max: 23, value: settings.nightly_rescan_hour });
    body.append(h('form', { onsubmit: async (e) => {
      e.preventDefault();
      if (root.value !== settings.music_root && !await confirmDialog({ title: 'Change the music root?', body: 'MPD restarts and rescans. Playlist entries are relative to the root, so they only resolve if the new root has the same structure.', confirmText: 'Change' })) return;
      await save({ music_root: root.value, upload_max_bytes: Number(maxMB.value) * 1048576, allowed_extensions: exts.value.split(/[\s,]+/).filter(Boolean), free_space_reserve: Number(reserveMB.value) * 1048576, nightly_rescan_hour: Number(hour.value) });
    } },
      h('label.form-label', 'Music root'), h('div.d-flex.gap-2', root, h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => browseDir(root) }, 'Browse…')),
      h('div.row.g-3.mt-1',
        h('div.col-sm-4', h('label.form-label', 'Max upload (MB)'), maxMB),
        h('div.col-sm-4', h('label.form-label', 'Free space reserve (MB)'), reserveMB),
        h('div.col-sm-4', h('label.form-label', 'Nightly rescan hour'), hour),
        h('div.col-12', h('label.form-label', 'Allowed extensions'), exts)),
      h('button.btn.btn-primary.mt-3', { type: 'submit' }, 'Save library')));
  }

  async function browseDir(input) {
    let path = input.value || '/';
    const list = h('div.list-group');
    const crumb = h('div.mono.small.mb-2');
    async function load() {
      try {
        const d = await A.api.get(`/system/directories?path=${encodeURIComponent(path)}`);
        path = d.path;
        crumb.textContent = path;
        clear(list);
        if (d.parent !== undefined && d.parent !== null) list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { path = d.parent; load(); } }, icon('arrow-90deg-up', 'me-2'), '..'));
        for (const e of d.entries) list.append(h('button.list-group-item.list-group-item-action', { type: 'button', onclick: () => { path = e.path; load(); } }, icon('folder', 'me-2'), e.name, e.writable ? null : h('span.badge.text-bg-secondary.ms-2', 'read-only')));
      } catch (e) { clear(list).append(errorBox(e)); }
    }
    const dlg = modal({ title: 'Choose the music root', body: [crumb, list], footer: h('button.btn.btn-primary', { type: 'button', onclick: () => { input.value = path; dlg.hide(); } }, 'Use this folder') });
    load();
  }

  async function renderSecurity(body) {
    const cur = h('input.form-control', { type: 'password', autocomplete: 'current-password' });
    const nw = h('input.form-control', { type: 'password', autocomplete: 'new-password', minlength: 8 });
    body.append(h('form.mb-4', { onsubmit: async (e) => {
      e.preventDefault();
      try { await A.changePassword(cur.value, nw.value); toast('Password changed. Sign in again.', 'success'); signOut(); } catch (ex) { toast(ex.message, 'danger'); }
    } },
      h('h3.h6', 'Password'),
      h('div.row.g-2', h('div.col-sm-5', h('label.form-label', 'Current'), cur), h('div.col-sm-5', h('label.form-label', 'New (8+ characters)'), nw), h('div.col-sm-2.d-flex.align-items-end', h('button.btn.btn-primary.w-100', { type: 'submit' }, 'Change'))),
      h('div.form-text', 'Every signed-in device is signed out.')));
    const keysBox = h('div');
    body.append(h('h3.h6', 'API keys'), keysBox);
    async function loadKeys() {
      let keys;
      try { keys = (await A.apiKeys()).keys; } catch (e) { clear(keysBox).append(errorBox(e)); return; }
      clear(keysBox);
      const list = h('div.list-group.mb-2');
      for (const k of keys) {
        list.append(h('div.list-group-item.d-flex.align-items-center.gap-2',
          h('div.flex-grow-1', h('div.fw-semibold', k.name), h('div.small.text-body-secondary', `created ${fmtTime(k.created_at)}`, k.last_used_at ? ` · last used ${fmtTime(k.last_used_at)} from ${k.last_used_ip}` : ' · never used', k.expires_at ? ` · expires ${fmtTime(k.expires_at)}` : '')),
          h('button.btn.btn-sm.btn-outline-danger', { type: 'button', onclick: async () => {
            if (!await confirmDialog({ title: 'Revoke key', body: `Revoke "${k.name}"? The next request with it is rejected.`, confirmText: 'Revoke', danger: true })) return;
            try { await A.deleteApiKey(k.id); loadKeys(); } catch (e) { toast(e.message, 'danger'); }
          } }, 'Revoke')));
      }
      if (!keys.length) list.append(h('div.list-group-item.text-body-secondary', 'No API keys.'));
      const name = h('input.form-control', { type: 'text', placeholder: 'Counter tablet app', required: true });
      keysBox.append(list, h('form.d-flex.gap-2', { onsubmit: async (e) => {
        e.preventDefault();
        try {
          const k = await A.createApiKey(name.value);
          const keyField = h('input.form-control.mono', { type: 'text', readonly: true, value: k.key });
          modal({ title: 'New API key', body: [h('p', 'Copy it now. It is not shown again.'), h('div.d-flex.gap-2', keyField, h('button.btn.btn-outline-secondary', { type: 'button', onclick: () => { copyText(k.key); toast('Copied', 'success'); } }, icon('clipboard'))), h('p.small.text-body-secondary.mt-2', 'Send it as: Authorization: Bearer <key>')] });
          name.value = '';
          loadKeys();
        } catch (ex) { toast(ex.message, 'danger'); }
      } }, name, h('button.btn.btn-outline-primary', { type: 'submit' }, 'Create key')));
    }
    loadKeys();
    body.append(h('h3.h6.mt-4', 'HTTPS'), renderHTTPS());
  }

  function renderHTTPS() {
    const box = h('div');
    const enabled = h('input.form-check-input', { type: 'checkbox', checked: settings.https_enabled, id: 'set-https' });
    const cert = h('textarea.form-control.mono', { rows: 3, placeholder: '-----BEGIN CERTIFICATE-----' });
    const key = h('textarea.form-control.mono', { rows: 3, placeholder: '-----BEGIN PRIVATE KEY-----' });
    box.append(h('p.small.text-body-secondary', 'HTTP on the LAN is the default. For access away from home, use Tailscale or WireGuard rather than a forwarded port. Upload a certificate and key, or generate a self-signed one (browsers warn about it).'),
      h('div.row.g-2', h('div.col-md-6', h('label.form-label', 'Certificate (PEM)'), cert), h('div.col-md-6', h('label.form-label', 'Private key (PEM)'), key)),
      h('div.d-flex.gap-2.align-items-center.mt-2',
        h('button.btn.btn-outline-primary', { type: 'button', onclick: async () => {
          try { await A.api.put('/settings/tls', { certificate: cert.value, key: key.value }); toast('Certificate stored', 'success'); } catch (e) { toast(e.message, 'danger'); }
        } }, 'Upload'),
        h('button.btn.btn-outline-secondary', { type: 'button', onclick: async () => {
          try { await A.api.post('/settings/tls/self-signed'); toast('Self-signed certificate generated', 'success'); } catch (e) { toast(e.message, 'danger'); }
        } }, 'Generate self-signed'),
        h('div.form-check.form-switch.ms-3', enabled, h('label.form-check-label', { for: 'set-https' }, 'Serve HTTPS and redirect HTTP'))),
      h('div.form-text', 'The switch takes effect at the next service restart.'));
    enabled.addEventListener('change', () => save({ https_enabled: enabled.checked }, 'Saved. Restart the service to apply.'));
    return box;
  }

  async function renderSystem(body) {
    let info = null;
    try { info = await A.systemInfo(); } catch { /* shown as unknown */ }
    const url = h('input.form-control', { type: 'url', value: settings.alert_webhook_url, placeholder: 'https://ntfy.sh/my-jukebox' });
    const preset = h('select.form-select');
    for (const [v, l] of [['generic', 'Generic JSON'], ['ntfy', 'ntfy']]) preset.append(h('option', { value: v, selected: settings.alert_webhook_preset === v }, l));
    const days = h('input.form-control', { type: 'number', min: 1, value: settings.history_days });
    const rows = h('input.form-control', { type: 'number', min: 100, value: settings.history_rows });
    const adays = h('input.form-control', { type: 'number', min: 1, value: settings.alert_days });
    body.append(
      h('p', `Version ${info?.version ?? '?'} · schema ${info?.schema_version ?? '?'} · runtime ${info?.runtime ?? '?'}`),
      h('p', h('a', { href: '#/health' }, 'Health page'), ' · ', h('a', { href: '#/history' }, 'Play history'), ' · ', h('a', { href: '/api/v1/docs', target: '_blank', rel: 'noopener' }, 'API docs')),
      h('form', { onsubmit: async (e) => { e.preventDefault(); await save({ alert_webhook_url: url.value, alert_webhook_preset: preset.value, history_days: Number(days.value), history_rows: Number(rows.value), alert_days: Number(adays.value) }); } },
        h('h3.h6', 'Alerts'),
        h('div.row.g-2', h('div.col-md-8', h('label.form-label', 'Webhook URL (JSON POST)'), url), h('div.col-md-4', h('label.form-label', 'Format'), preset)),
        h('div.d-flex.gap-2.mt-2', h('button.btn.btn-sm.btn-outline-secondary', { type: 'button', onclick: async () => { try { await A.api.post('/alerts/test'); toast('Test alert sent', 'success'); } catch (ex) { toast(ex.message, 'danger'); } } }, 'Send test')),
        h('h3.h6.mt-3', 'Retention'),
        h('div.row.g-2', h('div.col-4', h('label.form-label', 'History days'), days), h('div.col-4', h('label.form-label', 'History rows'), rows), h('div.col-4', h('label.form-label', 'Dismissed alerts days'), adays)),
        h('button.btn.btn-primary.mt-3', { type: 'submit' }, 'Save system')),
      h('p.small.text-body-secondary.mt-3', 'Log file: /var/log/jukem/jukem.log (or stdout in Docker).'));
  }

  function renderMaintenance(body) {
    const btn = (label, fn, cls = 'btn-outline-secondary') => h('button.btn', { type: 'button', class: `btn ${cls}`, onclick: fn }, label);
    body.append(h('div.d-flex.flex-wrap.gap-2',
      btn('Rescan library', async () => { try { await A.api.post('/library/rescan'); toast('Rescan started', 'success'); } catch (e) { toast(e.message, 'danger'); } }),
      btn('Check library permissions', async () => {
        try {
          const r = await A.api.post('/library/permissions/check');
          modal({ title: 'Library permissions', body: r.problems?.length ? [h('p', `${r.problems.length} folders are not writable.`), h('pre.pre-wrap.small', r.problems.join('\n')), h('p.small', r.fix), h('pre.pre-wrap.small', r.command)] : h('p', 'Every folder is writable.') });
        } catch (e) { toast(e.message, 'danger'); }
      }),
      btn('Database snapshot', async () => { try { const r = await A.api.post('/system/snapshot'); toast(`Snapshot written: ${r.path}`, 'success'); } catch (e) { toast(e.message, 'danger'); } }),
      btn('Restart service', async () => {
        if (!await confirmDialog({ title: 'Restart jukem?', body: 'Playback stops for a few seconds and resumes on its own.', confirmText: 'Restart', danger: true })) return;
        try { await A.api.post('/system/restart'); toast('Restarting', 'warning'); } catch (e) { toast(e.message, 'danger'); }
      }, 'btn-outline-danger')),
      h('div.mt-3', h('button.btn.btn-outline-secondary', { type: 'button', onclick: signOut }, icon('box-arrow-right', 'me-1'), 'Sign out')));
  }

  // Another client can change settings while this page is open. A save
  // sends the whole object, so the copy here must stay current.
  return {
    async onEvent(type) {
      if (type === 'devices') bodies.audio.render(clear(bodies.audio.body));
      if (type === 'settings') {
        try { settings = await A.settings(); } catch { /* keep the copy */ }
      }
    },
  };
}
