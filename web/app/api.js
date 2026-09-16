// API client. Every call goes through here, so the CSRF token and the
// error handling are in one place.

const base = '/api/v1';
let csrfToken = '';

class ApiError extends Error {
  constructor(status, problem) {
    super(problem?.detail || problem?.title || `HTTP ${status}`);
    this.status = status;
    this.problem = problem || {};
  }
}

export function setCSRF(token) { csrfToken = token || ''; }

async function call(method, path, body, opts = {}, retried = false) {
  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (csrfToken && method !== 'GET') headers['X-CSRF-Token'] = csrfToken;
  const resp = await fetch(base + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    cache: 'no-store',
    signal: opts.signal,
  });
  if (resp.status === 204) return null;
  const text = await resp.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { data = { detail: text }; }
  }
  if (resp.status === 401 && path !== '/auth/login') {
    // The session is gone: the app shell shows the login.
    document.dispatchEvent(new CustomEvent('jukem-unauthorized'));
  }
  if (resp.status === 403 && !retried && /CSRF/.test(data?.detail || '')) {
    // Another tab signed in again and replaced the session; take its token.
    const sess = await fetch(base + '/auth/session', { cache: 'no-store' }).then((r) => r.json()).catch(() => null);
    if (sess?.csrf_token) {
      csrfToken = sess.csrf_token;
      return call(method, path, body, opts, true);
    }
  }
  if (!resp.ok) throw new ApiError(resp.status, data);
  return data;
}

export const api = {
  get: (path, opts) => call('GET', path, undefined, opts),
  post: (path, body) => call('POST', path, body ?? {}),
  put: (path, body) => call('PUT', path, body ?? {}),
  del: (path) => call('DELETE', path),
};

// Auth
export const session = () => api.get('/auth/session');
export const login = (password) => api.post('/auth/login', { password });
export const setup = (password) => api.post('/auth/setup', { password });
export const logout = () => api.post('/auth/logout');
export const changePassword = (current, password) => api.put('/auth/password', { current, password });

// Player
export const status = () => api.get('/status');
export const playerAction = (action) => api.post(`/player/${action}`);
export const setVolume = (volume) => api.put('/player/volume', { volume });
export const setOptions = (opts) => api.put('/player/options', opts);
export const seek = (position) => api.post('/player/seek', { position });
export const queue = (offset = 0, limit = 200) => api.get(`/queue?offset=${offset}&limit=${limit}`);
export const queueAction = (body) => api.post('/queue', body);
export const removeQueueEntry = (id) => api.del(`/queue/${id}`);
export const moveQueueEntry = (id, to) => api.post('/queue/move', { id, to });
export const playQueueEntry = (id) => api.post('/queue/play', { id });
export const createOverride = (body) => api.post('/override', body);
export const clearOverride = () => api.del('/override');
export const addDoNotPlay = (file, title) => api.post('/do-not-play', { file, title });

// Devices and settings
export const devices = () => api.get('/devices');
export const rescanDevices = () => api.post('/devices/rescan');
export const selectDevice = (key) => api.put('/devices/default', { key });
export const mixer = (key) => api.get(`/devices/${encodeURIComponent(key)}/mixer`);
export const setMixer = (key, body) => api.put(`/devices/${encodeURIComponent(key)}/mixer`, body);
export const settings = () => api.get('/settings');
export const saveSettings = (body) => api.put('/settings', body);
export const health = () => api.get('/health');
export const systemInfo = () => api.get('/system/info');
export const updateStatus = () => api.get('/system/update');
export const checkUpdate = () => api.post('/system/update/check');
export const alerts = () => api.get('/alerts');
export const dismissAlert = (id) => api.del(`/alerts/${id}`);
export const history = (beforeID, limit = 100) => api.get(`/history?limit=${limit}${beforeID ? '&before_id=' + beforeID : ''}`);
export const rescanLibrary = () => api.post('/library/rescan');
export const apiKeys = () => api.get('/api-keys');
export const createApiKey = (name, expires_at) => api.post('/api-keys', expires_at ? { name, expires_at } : { name });
export const deleteApiKey = (id) => api.del(`/api-keys/${id}`);

// csrf exposes the token for requests made outside this module, such as
// XMLHttpRequest uploads.
export function csrf() { return csrfToken; }
