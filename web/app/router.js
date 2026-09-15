// Hash router. Routes are '#/section/rest'. The rest goes to the view
// unparsed, so a view can carry a path such as a library folder.

const routes = [];
let current = null;
let generation = 0;
let started = false;

export function route(prefix, view) {
  routes.push({ prefix, view });
}

export function navigate(hash) {
  if (location.hash === hash) dispatch();
  else location.hash = hash;
}

export function currentSection() {
  const hash = location.hash || '#/';
  const m = /^#\/([^/]*)/.exec(hash);
  return m ? m[1] : '';
}

function destroyCurrent() {
  if (current && current.destroy) {
    try { current.destroy(); } catch (e) { console.error(e); }
  }
  current = null;
}

async function dispatch() {
  if (!started) return;
  const hash = location.hash || '#/';
  const main = document.getElementById('main');
  const path = hash.slice(1);
  let match = null;
  for (const r of routes) {
    if (path === r.prefix || path.startsWith(r.prefix + '/') || (r.prefix === '/' && path === '/')) {
      if (!match || r.prefix.length > match.prefix.length) match = r;
    }
  }
  destroyCurrent();
  window.scrollTo(0, 0);
  if (!match) {
    location.hash = '#/';
    return;
  }
  let rest = '';
  if (path.length > match.prefix.length) {
    try { rest = decodeURIComponent(path.slice(match.prefix.length + 1)); } catch { rest = path.slice(match.prefix.length + 1); }
  }
  // A view can await network calls. Only the newest dispatch keeps its
  // result; an older one that resolves late is destroyed at once.
  const gen = ++generation;
  const view = await match.view(main, rest);
  if (gen !== generation) {
    if (view && view.destroy) view.destroy();
    return;
  }
  current = view;
  document.dispatchEvent(new CustomEvent('route', { detail: { section: currentSection() } }));
}

// start enables dispatching. The hashchange listener is added once.
export function start() {
  if (!started) {
    started = true;
    window.addEventListener('hashchange', dispatch);
  }
  dispatch();
}

// stop destroys the current view and ignores hash changes until start.
export function stop() {
  started = false;
  generation++;
  destroyCurrent();
}

// refreshCurrent asks the active view to refetch after an event.
export function refreshCurrent(type) {
  if (current && current.onEvent) current.onEvent(type);
}
