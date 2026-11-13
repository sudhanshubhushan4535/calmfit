// Today: the next workout with every exercise's set-1 target and its "why", Start/Resume, and
// today's plate score.
import { api } from '../api.js';
import { el, screen } from '../ui.js';
import { dayName, target } from '../format.js';
import { explainer } from './onboarding.js';

function exerciseRow(ex) {
  const plan = ex.plan;
  return el('li', { class: 'ex-row' },
    el('div', { class: 'ex-name' }, ex.name),
    plan
      ? [el('div', { class: 'ex-target' }, 'Set 1: ' + target(plan.sets[0])), el('div', { class: 'why' }, plan.summary)]
      : el('div', { class: 'why' }, "First time: you'll pick a starting weight."));
}

function plateTile(plate) {
  if (!plate) return null;
  const t = plate.today;
  return el('a', { class: 'card tile', href: '#/plate' },
    el('div', { class: 'tile-main' }, t.logged ? `Plate today: ${t.percent}%` : 'Plate check: tap after each meal'),
    plate.streak > 0 ? el('div', { class: 'muted' }, `${plate.streak} good plate ${plate.streak === 1 ? 'day' : 'days'} in a row`) : null);
}

export default async function today(app) {
  const [next, plate] = await Promise.all([
    api('GET', '/workouts/next'),
    api('GET', '/plate').catch(() => null),
  ]);
  const resuming = !!next.in_progress_workout_id;
  const canStart = typeof app.startWorkout === 'function';
  screen(
    el('section', { class: 'card' },
      el('div', { class: 'eyebrow' }, resuming ? 'Workout in progress' : 'Your next workout'),
      el('h1', {}, dayName(next.day) + ' day'),
      el('ul', { class: 'ex-list' }, next.exercises.map(exerciseRow)),
      canStart ? el('button', { type: 'button', class: 'primary wide', onclick: () => app.startWorkout() },
        resuming ? 'Resume workout' : 'Start workout') : null),
    plateTile(plate),
    explainer());
}
