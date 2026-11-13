// CalmFit app (D51): boot, routing and the shared app state. Each view is a function (app) that
// renders into <main id="view">; the server is the source of truth, so views re-fetch what they show.
import { api } from './api.js';
import * as router from './router.js';
import { el, screen } from './ui.js';
import { friendly } from './format.js';
import signin from './views/signin.js';
import onboarding from './views/onboarding.js';
import today from './views/today.js';
import workout from './views/workout.js';
import next from './views/next.js';
import plate from './views/plate.js';
import progress from './views/progress.js';

// Routes without the tab bar are full-screen.
const FULL = new Set(['signin', 'onboarding', 'workout', 'next']);

export const views = { signin, onboarding, today, workout, next, plate, progress };

export const app = {
  me: null,
  summary: null, // the last "next time" summary, shown once by #/next
  go: router.go,
  startWorkout: () => router.go('workout'),
  render: () => renderRoute(router.parse(location.hash).name),
};

async function renderRoute(name) {
  if (!app.me) name = 'signin';
  else if (name === 'signin' || !views[name]) name = 'today';
  document.body.dataset.route = name;
  document.getElementById('toast').hidden = true; // a toast belongs to the screen it was shown on
  document.body.classList.toggle('full', FULL.has(name));
  for (const a of document.querySelectorAll('.tabs a')) {
    a.toggleAttribute('aria-current', a.dataset.tab === name);
  }
  try {
    await views[name](app);
  } catch (e) {
    if (e.code === 'onboarding_required') return app.go('onboarding');
    if (e.status === 401) return; // the signedout handler shows sign-in
    screen(el('section', { class: 'card center' },
      el('h1', {}, "Can't load this right now"),
      el('p', { class: 'muted' }, friendly(e)),
      el('button', { type: 'button', class: 'primary', onclick: () => app.render() }, 'Try again')));
  }
}

window.addEventListener('calmfit:signedout', () => {
  app.me = null;
  app.render();
});

let started = false;

async function boot() {
  try {
    app.me = await api('GET', '/me');
  } catch (e) {
    app.me = null;
    if (e.status !== 401) {
      // Offline or server down: don't send a signed-in user to the sign-in screen.
      document.body.dataset.route = 'loading';
      screen(el('section', { class: 'card center' },
        el('h1', {}, "Can't reach CalmFit"),
        el('p', { class: 'muted' }, friendly(e)),
        el('button', { type: 'button', class: 'primary', onclick: boot }, 'Try again')));
      return;
    }
  }
  if (started) return app.render();
  started = true;
  router.start(renderRoute);
}

boot();
