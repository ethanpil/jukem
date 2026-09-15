import * as A from '../api.js';
import { h, clear } from '../dom.js';

// loginView asks for the password. onSignedIn receives the session.
export function loginView(main, onSignedIn) {
  clear(main);
  const input = h('input.form-control.form-control-lg', { type: 'password', autocomplete: 'current-password', required: true, autofocus: true, id: 'login-password' });
  const err = h('div.text-danger.small.mt-2.d-none');
  const btn = h('button.btn.btn-primary.btn-lg.w-100.mt-3', { type: 'submit' }, 'Sign in');
  const form = h('form', {
    onsubmit: async (e) => {
      e.preventDefault();
      btn.disabled = true;
      err.classList.add('d-none');
      try {
        const sess = await A.login(input.value);
        await onSignedIn(sess);
      } catch (ex) {
        err.textContent = ex.status === 429 ? 'Too many attempts. Wait a few minutes.' : 'Wrong password.';
        err.classList.remove('d-none');
        btn.disabled = false;
        input.select();
      }
    },
  },
    h('label.form-label', { for: 'login-password' }, 'Password'),
    input, err, btn);
  main.append(h('div.mx-auto.mt-5', { style: 'max-width: 360px' },
    h('h1.h3.mb-4.text-center', 'jukem'),
    form,
    h('p.text-body-secondary.small.mt-4', 'Forgot it? On the console: jukem reset-password')));
  input.focus();
}
