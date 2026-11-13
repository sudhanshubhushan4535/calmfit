// One-tap Google sign-in (spec: no email/password forms).
import { api } from '../api.js';
import { el, screen, showError } from '../ui.js';

// waitForGoogle resolves with window.google once the GIS script has loaded, or null after 10 s.
function waitForGoogle() {
  return new Promise(resolve => {
    const t0 = Date.now();
    (function poll() {
      if (window.google && window.google.accounts && window.google.accounts.id) return resolve(window.google);
      if (Date.now() - t0 > 10000) return resolve(null);
      setTimeout(poll, 100);
    })();
  });
}

// GIS must be initialized once per page; the view can render several times (sign-out, retries).
let initialized = false;

export default async function signin(app) {
  const host = el('div', { class: 'gsi' });
  screen(el('section', { class: 'hero' },
    el('img', { src: '/static/icons/icon-192.png', alt: '', width: 64, height: 64, class: 'logo' }),
    el('h1', {}, 'CalmFit'),
    el('p', { class: 'lead' }, "You don't have to decide what to lift next time. CalmFit already knows."),
    host,
    el('p', { class: 'muted small' }, 'Sign in with your Google account. No forms, no passwords.')));

  const g = await waitForGoogle();
  if (!g) {
    host.append(el('p', { class: 'err' }, "Google sign-in didn't load. Check your connection, then reload."));
    return;
  }
  const first = !initialized;
  initialized = true;
  if (first) g.accounts.id.initialize({
    client_id: document.body.dataset.clientId,
    use_fedcm_for_prompt: true,
    callback: async resp => {
      try {
        await api('POST', '/auth/google', { credential: resp.credential });
        app.me = await api('GET', '/me');
        app.go('today');
      } catch (e) { showError(e); }
    },
  });
  g.accounts.id.renderButton(host, { theme: 'filled_blue', size: 'large', shape: 'pill', text: 'continue_with', width: 280 });
  if (first) g.accounts.id.prompt(); // One Tap, once per page load
}
