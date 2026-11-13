// Onboarding (spec step 2): pick gym days per week → routine assigned → the rotation explainer.
import { api } from '../api.js';
import { el, screen, showError, busy } from '../ui.js';

export function explainer() {
  return el('p', { class: 'explainer' },
    'Your workouts follow Push → Pull → Legs. Your next workout is whatever comes after your last one.');
}

export default async function onboarding(app) {
  const pick = days => async ev => busy(ev.currentTarget, async () => {
    try {
      await api('POST', '/onboarding', { days_per_week: days });
    } catch (e) { return showError(e); }
    screen(el('section', { class: 'card center' },
      el('h1', {}, "You're set"),
      explainer(),
      el('p', { class: 'muted' }, 'Missed a day? Nothing breaks. The next workout just waits for you.'),
      el('button', { type: 'button', class: 'primary', onclick: () => app.go('today') }, 'See your first workout')));
  });

  screen(el('section', { class: 'card center' },
    el('h1', {}, 'How many days a week do you go to the gym?'),
    el('div', { class: 'choices' },
      [2, 3, 4, 5, 6].map(d => el('button', { type: 'button', class: 'choice', onclick: pick(d) }, String(d)))),
    el('p', { class: 'muted small' }, 'You can change this later in Account.')));
}
