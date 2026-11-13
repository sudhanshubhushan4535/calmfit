// "Next time" (spec step 4, the aha): after every workout, this session vs next session per
// exercise with the one-line why. Then the reminder offer (spec step 5).
import { track } from '../api.js';
import { el, screen } from '../ui.js';
import { dayName, sets } from '../format.js';
import { offerCard } from '../reminders.js';

function row(ex, day) {
  if (ex.skipped) {
    return el('section', { class: 'card' }, el('h2', {}, ex.name),
      el('p', { class: 'muted' }, `Skipped today. It's back on your next ${day} day.`));
  }
  return el('section', { class: 'card' },
    el('h2', {}, ex.name),
    el('div', { class: 'cmp' },
      el('span', { class: 'muted' }, 'Today'), el('span', {}, sets(ex.this_session.done)),
      el('span', { class: 'muted' }, 'Next time'), el('strong', { class: 'next-sets' }, sets(ex.next.sets))),
    el('p', { class: 'why' }, ex.next.summary));
}

export default async function next(app) {
  const sum = app.summary;
  if (!sum) return app.go('today'); // a reload lands here without a summary; Today shows the same targets
  track('next_target_viewed');
  const main = screen(
    el('header', { class: 'center' },
      el('div', { class: 'eyebrow' }, 'Workout saved'),
      el('h1', {}, dayName(sum.day) + ' day done ✓'),
      el('p', { class: 'lead' }, "Here's what to lift next time, and why. You don't have to remember it.")),
    sum.exercises.map(ex => row(ex, dayName(sum.day))));
  const offer = await offerCard().catch(() => null);
  if (offer) main.append(offer);
  main.append(el('button', { type: 'button', class: 'primary wide', onclick: () => { app.summary = null; app.go('today'); } }, 'Done'));
}
