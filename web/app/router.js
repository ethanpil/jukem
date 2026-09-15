// Hash router. Routes are '#/section/rest', and the rest is passed to the
// view unparsed so a view can carry a path such as a library folder.

const routes = [];
let current = null;

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

export async function dispatch() {
  const hash = location.hash || '#/';
  const main = document.getElementById('main');
  const path = hash.slice(1);
  let match = null;
  for (const r of routes) {
    if (path === r.prefix || path.startsWith(r.prefix + '/') || (r.prefix === '/' && path === '/')) {
      if (!match || r.prefix.length > match.prefix.length) match = r;
    }
  }
  if (current && current.destroy) {
    try { current.destroy(); } catch (e) { console.error(e); }
  }
  current = null;
  main.scrollTop = 0;
  window.scrollTo(0, 0);
  if (!match) {
    location.hash = '#/';
    return;
  }
  const rest = path.length > match.prefix.length ? decodeURIComponent(path.slice(match.prefix.length + 1)) : '';
  current = await match.view(main, rest);
  document.dispatchEvent(new CustomEvent('route', { detail: { section: currentSection() } }));
}

export function start() {
  window.addEventListener('hashchange', dispatch);
  dispatch();
}

// refreshCurrent asks the active view to refetch after an event.
export function refreshCurrent(type) {
  if (current && current.onEvent) current.onEvent(type);
}
