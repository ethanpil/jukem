// Every time in the interface comes from here, so one setting decides how
// a time looks and how a person gives one. Settings > General holds the
// choice: 12 hours with AM and PM, or 24 hours.

import { h } from './dom.js';

let is12 = true;

// setTimeFormat takes the value of the settings ("12h" or "24h").
export function setTimeFormat(format) { is12 = format !== '24h'; }

// hour12 reports the current choice, for a formatter that needs it.
export function hour12() { return is12; }

// timeOpts are the Intl options for a time of day.
function timeOpts() {
  return is12 ? { hour: 'numeric', minute: '2-digit', hour12: true } : { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' };
}

// formatters keeps one set per zone and format. A zone the browser does
// not know falls back to the zone of the browser.
const cache = new Map();
function formatters(zone) {
  const key = `${is12 ? 12 : 24}|${zone || ''}`;
  if (cache.has(key)) return cache.get(key);
  const zoneOpts = zone ? { timeZone: zone } : {};
  let made;
  try {
    made = {
      time: new Intl.DateTimeFormat([], { ...zoneOpts, ...timeOpts() }),
      weekday: new Intl.DateTimeFormat([], { ...zoneOpts, weekday: 'short' }),
      date: new Intl.DateTimeFormat([], { ...zoneOpts, dateStyle: 'medium' }),
      day: new Intl.DateTimeFormat('en-CA', { ...zoneOpts, year: 'numeric', month: '2-digit', day: '2-digit' }),
    };
  } catch {
    made = formatters('');
  }
  cache.set(key, made);
  return made;
}

// toDate accepts an instant, a date or a time string.
function toDate(value) {
  if (value instanceof Date) return value;
  if (!value) return null;
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

// fmtTimeOfDay writes the time of an instant, for example "1:05 PM".
export function fmtTimeOfDay(value, zone = null) {
  const d = toDate(value);
  return d ? formatters(zone).time.format(d) : '';
}

// fmtWhen writes the time of an instant, with the weekday when the instant
// is not on the day of the browser.
export function fmtWhen(value, zone = null) {
  const d = toDate(value);
  if (!d) return '';
  const f = formatters(zone);
  const sameDay = f.day.format(d) === f.day.format(new Date());
  return (sameDay ? '' : f.weekday.format(d) + ' ') + f.time.format(d);
}

// fmtStamp writes a date and a time, for a log line or a row.
export function fmtStamp(value, zone = null) {
  const d = toDate(value);
  if (!d) return '';
  const f = formatters(zone);
  return `${f.date.format(d)}, ${f.time.format(d)}`;
}

// fmtHM writes a stored wall-clock time such as "09:00". The day does not
// matter, so it takes any day.
export function fmtHM(hhmm) {
  const parts = /^(\d{1,2}):(\d{2})/.exec(hhmm || '');
  if (!parts) return hhmm || '';
  const [hh, mm] = [Number(parts[1]), Number(parts[2])];
  if (!is12) return `${String(hh).padStart(2, '0')}:${parts[2]}`;
  const period = hh < 12 ? 'AM' : 'PM';
  const hour = hh % 12 === 0 ? 12 : hh % 12;
  return `${hour}:${String(mm).padStart(2, '0')} ${period}`;
}

// timeField is the control that asks for a time of day. It follows the
// setting, because a browser gives a native time input the format of its
// own language. The value is read and written as "HH:MM", the form the
// API uses.
export function timeField({ value = '09:00', label = 'Time', required = false } = {}) {
  const hours = h('select.form-select.time-part', { 'aria-label': `${label}, hour`, required });
  const minutes = h('select.form-select.time-part', { 'aria-label': `${label}, minute`, required });
  const period = h('select.form-select.time-part', { 'aria-label': `${label}, AM or PM` });
  for (let i = 0; i < (is12 ? 12 : 24); i++) {
    const n = is12 ? (i === 0 ? 12 : i) : i;
    hours.append(h('option', { value: String(n) }, is12 ? String(n) : String(n).padStart(2, '0')));
  }
  for (let m = 0; m < 60; m++) {
    minutes.append(h('option', { value: String(m) }, String(m).padStart(2, '0')));
  }
  period.append(h('option', { value: 'AM' }, 'AM'), h('option', { value: 'PM' }, 'PM'));

  const el = h('div.time-field', hours, h('span.time-sep', ':'), minutes);
  if (is12) el.append(period);

  // set takes "HH:MM" and puts it in the parts.
  function set(hhmm) {
    const parts = /^(\d{1,2}):(\d{2})/.exec(hhmm || '') || ['', '9', '00'];
    const hh = Math.min(23, Math.max(0, Number(parts[1])));
    minutes.value = String(Number(parts[2]));
    if (!is12) {
      hours.value = String(hh);
      return;
    }
    hours.value = String(hh % 12 === 0 ? 12 : hh % 12);
    period.value = hh < 12 ? 'AM' : 'PM';
  }
  set(value);

  return {
    el,
    // value gives the time as "HH:MM".
    get value() {
      let hh = Number(hours.value);
      if (is12) {
        hh %= 12;
        if (period.value === 'PM') hh += 12;
      }
      return `${String(hh).padStart(2, '0')}:${String(Number(minutes.value)).padStart(2, '0')}`;
    },
    set value(hhmm) { set(hhmm); },
  };
}

// setDisabled turns every part of a time field on or off.
export function setDisabled(el, disabled) {
  for (const part of el.querySelectorAll('select')) part.disabled = disabled;
}
