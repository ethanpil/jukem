import * as A from '../api.js';
import { h, clear, logo } from '../dom.js';

// loginView asks for the password. onSignedIn receives the session.
export function loginView(main, onSignedIn) {
  clear(main);
  const input = h('input.form-control.form-control-lg', { type: 'password', autocomplete: 'current-password', required: true, autofocus: true, id: 'login-password' });
  const err = h('div.form-error');
  const btn = h('button.btn.btn-primary.btn-lg.w-100.mt-3.raised', { type: 'submit' }, 'Sign in');
  const form = h('form', {
    onsubmit: async (e) => {
      e.preventDefault();
      btn.disabled = true;
      err.textContent = '';
      let sess;
      try {
        sess = await A.login(input.value);
      } catch (ex) {
        err.textContent = ex.status === 401 ? 'Wrong password.' : ex.status === 429 ? 'Too many attempts. Wait a few minutes.' : ex.status ? `Sign-in failed: ${ex.message}` : 'jukem is not reachable.';
        btn.disabled = false;
        input.select();
        return;
      }
      await onSignedIn(sess);
    },
  },
    h('label.form-label', { for: 'login-password' }, 'Password'),
    input, err, btn);
  main.append(h('div.auth-wrap',
    h('div.panel.auth-card',
      h('div.auth-brand', logo(), h('h1', 'jukem')),
      form,
      h('p.auth-hint', 'Forgot it? On the console: ', h('code', 'jukem reset-password')))));
  input.focus();
}
