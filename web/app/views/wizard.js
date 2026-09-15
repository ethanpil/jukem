import * as A from '../api.js';
import { h, clear } from '../dom.js';

// wizardView is completed in build step 11. Until then, the first run only
// sets the password and signs in.
export function wizardView(main, rest, opts = {}) {
  clear(main);
  const pw = h('input.form-control.form-control-lg', { type: 'password', autocomplete: 'new-password', minlength: 8, required: true, id: 'setup-password' });
  const pw2 = h('input.form-control.form-control-lg', { type: 'password', autocomplete: 'new-password', minlength: 8, required: true });
  const err = h('div.text-danger.small.mt-2.d-none');
  main.append(h('div.mx-auto.mt-5', { style: 'max-width: 420px' },
    h('h1.h3.mb-2', 'Welcome to jukem'),
    h('p.text-body-secondary', 'Set the password for this jukebox. One login controls everything.'),
    h('form', { onsubmit: async (e) => {
      e.preventDefault();
      err.classList.add('d-none');
      if (pw.value !== pw2.value) { err.textContent = 'The passwords differ.'; err.classList.remove('d-none'); return; }
      try {
        const sess = await A.setup(pw.value);
        await opts.onSignedIn(sess);
      } catch (ex) { err.textContent = ex.message; err.classList.remove('d-none'); }
    } },
      h('label.form-label', { for: 'setup-password' }, 'Password (8+ characters)'), pw,
      h('label.form-label.mt-3', 'Again'), pw2, err,
      h('button.btn.btn-primary.btn-lg.w-100.mt-3', { type: 'submit' }, 'Continue'))));
  pw.focus();
  return {};
}
