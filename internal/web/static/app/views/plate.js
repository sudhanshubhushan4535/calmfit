// Plate check (spec "Plate check"): after each of 3 meals, tap Protein ✓ and Fiber ✓. Shown as a
// daily score %, with the streak of good plate days. No grams, no calories, no guilt.
import { api } from '../api.js';
import { el, screen, showError } from '../ui.js';
import { weekday } from '../format.js';

const MEALS = [['breakfast', 'Breakfast'], ['lunch', 'Lunch'], ['dinner', 'Dinner']];

let st = null; // {summary}

function toggle(meal, field) {
  const today = st.summary.today;
  const before = today.meals[meal] || { protein: false, fiber: false };
  const after = { ...before, [field]: !before[field] };
  today.meals[meal] = after; // show the tap at once
  render();
  api('PUT', `/plate/${today.date}/${meal}`, after)
    .then(async () => { st.summary = await api('GET', '/plate'); render(); }) // score + streak from the server
    .catch(e => { today.meals[meal] = before; render(); showError(e); });
}

function mealRow([meal, label]) {
  const m = st.summary.today.meals[meal] || { protein: false, fiber: false };
  const btn = (field, text) => el('button', { type: 'button', class: 'check' + (m[field] ? ' on' : ''),
    'aria-pressed': String(!!m[field]), onclick: () => toggle(meal, field) }, (m[field] ? '✓ ' : '') + text);
  return el('div', { class: 'meal' }, el('span', { class: 'meal-name' }, label),
    el('div', { class: 'meal-checks' }, btn('protein', 'Protein'), btn('fiber', 'Fiber')));
}

function render() {
  if (!st || document.body.dataset.route !== 'plate') return;
  const { today, streak, days, good_day_min } = st.summary;
  screen(
    el('section', { class: 'card' },
      el('div', { class: 'eyebrow' }, 'Plate check'),
      el('div', { class: 'score' },
        el('span', { class: 'big' }, today.logged ? today.percent + '%' : '–'),
        el('span', { class: 'muted' }, today.good ? 'Good plate day ✓' : today.logged ? 'today' : 'Tap after each meal')),
      MEALS.map(mealRow),
      el('p', { class: 'muted small' }, `Protein matters most: ${good_day_min} of ${today.max} points makes a good plate day.`)),
    el('section', { class: 'card' },
      el('h2', {}, streak > 0 ? `${streak} good plate ${streak === 1 ? 'day' : 'days'} in a row` : 'Your week'),
      el('div', { class: 'dots' }, days.map(d => el('div', { class: 'dot' + (d.good ? ' good' : d.logged ? ' logged' : '') },
        el('span', { class: 'dot-day' }, weekday(d.date)),
        el('span', { class: 'dot-val' }, d.logged ? d.percent + '%' : '·')))),
      el('p', { class: 'muted small' }, 'A day you skip doesn\'t break your streak.')),
    el('a', { class: 'card tile', href: '/guide/protein' },
      el('div', { class: 'tile-main' }, 'What counts as protein? →'),
      el('div', { class: 'muted small' }, 'Paneer, soya, dal, curd and whey: practical portions.')));
}

export default async function plate() {
  st = { summary: await api('GET', '/plate') };
  render();
}
