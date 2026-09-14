// Renders the maintenance page from /api/v1/health. No framework: the page
// shows the problem at the usual address when the install is broken.
async function load() {
  const reason = document.getElementById('reason');
  const fix = document.getElementById('fix');
  try {
    const r = await fetch('/api/v1/health', { cache: 'no-store' });
    const h = await r.json();
    reason.textContent = h.reason || 'jukem is in maintenance mode.';
    fix.textContent = h.fix || '';
  } catch (e) {
    reason.textContent = 'Could not read the health report: ' + e;
  }
}
load();
