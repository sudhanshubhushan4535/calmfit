// Progress (spec step 7, 8 and the charts): the weekly review with the share card, workouts per
// week, and set-1 weight over time for one lift.
import { api } from '../api.js';
import { el, screen, showError, busy } from '../ui.js';
import { change, shiftDate } from '../format.js';
import { setOneChart, weeksChart } from '../charts.js';

let st = null; // {date, currentStart, review, weeks, lifts, lift, liftData}

function stat(value, label) {
  return el('div', { class: 'stat' }, el('div', { class: 'stat-val' }, value), el('div', { class: 'muted small' }, label));
}

// shareSection draws the story image (sharecard.js, D46) and offers Share and Download. The made
// card lives in st.card, so re-rendering (e.g. picking another lift) keeps it.
function shareSection(rv) {
  const c = st.card;
  const make = el('button', { type: 'button', class: 'secondary wide', onclick: ev => busy(ev.currentTarget, async () => {
    const C = window.CalmFitCard;
    const canvas = document.createElement('canvas');
    canvas.width = C.W;
    canvas.height = C.H;
    C.drawCard(canvas.getContext('2d'), C.cardModel(rv));
    const blob = await new Promise(res => canvas.toBlob(res, 'image/png'));
    const name = `calmfit-week-${rv.week_start}.png`;
    const file = new File([blob], name, { type: 'image/png' });
    dropCard();
    st.card = { url: URL.createObjectURL(blob), file, name, canShare: !!(navigator.canShare && navigator.canShare({ files: [file] })) };
    render();
  }) }, c ? 'Make it again' : 'Make my weekly story card');
  return el('section', { class: 'card' }, el('h2', {}, 'Share your week'),
    el('p', { class: 'muted small' }, 'An Instagram story image of this week. Made on your phone; nothing is uploaded.'),
    c ? [el('img', { src: c.url, alt: 'Your weekly CalmFit card', class: 'share-img' }),
      el('div', { class: 'row center-row' },
        c.canShare ? el('button', { type: 'button', class: 'primary', onclick: async () => {
          try { await navigator.share({ files: [c.file], title: 'My CalmFit week' }); }
          catch (e) { if (e.name !== 'AbortError') showError(e); }
        } }, 'Share') : null,
        el('a', { class: 'secondary btnlink', href: c.url, download: c.name }, 'Download'))] : null,
    make);
}

function dropCard() {
  if (st.card) URL.revokeObjectURL(st.card.url);
  st.card = null;
}

function reviewSection() {
  const rv = st.review;
  const label = window.CalmFitCard ? window.CalmFitCard.weekLabel(rv.week_start, rv.week_end) : rv.week_start;
  const isCurrent = rv.week_start === st.currentStart;
  return el('section', { class: 'card' },
    el('div', { class: 'week-nav' },
      el('button', { type: 'button', class: 'ghost', 'aria-label': 'previous week', onclick: () => go(shiftDate(rv.week_start, -1)) }, '←'),
      el('div', { class: 'center' }, el('div', { class: 'eyebrow' }, isCurrent ? 'This week' : 'Week'), el('h1', {}, label)),
      el('button', { type: 'button', class: 'ghost', 'aria-label': 'next week', disabled: isCurrent,
        onclick: () => { const d = shiftDate(rv.week_end, 1); go(d >= st.currentStart ? null : d); } }, '→')),
    el('div', { class: 'stats' },
      stat(`${rv.done}/${rv.planned}`, 'workouts'),
      stat(rv.plate.days ? `${rv.plate.good_days}/${rv.plate.days}` : '–', 'good plate days'),
      stat(String(rv.went_up.length), rv.went_up.length === 1 ? 'lift went up' : 'lifts went up')),
    rv.went_up.length ? [el('h2', {}, 'Went up'), el('ul', { class: 'plain' }, rv.went_up.map(c => el('li', {}, '↑ ' + change(c))))] : null,
    rv.stalled.length ? [el('h2', {}, 'Stalled'), el('ul', { class: 'plain' }, rv.stalled.map(s => el('li', {}, s.name))),
      el('p', { class: 'muted small' }, 'The plan has already adjusted these targets.')] : null,
    el('p', { class: 'explainer' }, rv.tip));
}

function chartsSection() {
  const picker = el('div', { class: 'chips' }, st.lifts.map(l => el('button', { type: 'button',
    class: 'chip' + (l.id === st.lift ? ' on' : ''), 'aria-pressed': String(l.id === st.lift),
    onclick: async () => { st.lift = l.id; st.liftData = null; render(); await loadLift(); } }, l.name)));
  return [
    el('section', { class: 'card' }, el('h2', {}, 'Workouts per week'), weeksChart(st.weeks),
      el('p', { class: 'muted small' }, 'Green: done. Grey: planned.')),
    el('section', { class: 'card' }, el('h2', {}, 'Set 1 weight'), picker,
      st.liftData ? setOneChart(st.liftData) : el('p', { class: 'muted' }, 'Loading…'),
      el('p', { class: 'muted small' }, 'Each point is one session; ×12 means 12 reps on set 1.')),
  ];
}

function render() {
  if (!st || document.body.dataset.route !== 'progress') return;
  const y = window.scrollY;
  screen(reviewSection(), shareSection(st.review), chartsSection());
  window.scrollTo(0, y);
}

async function loadLift() {
  if (!st.lift) return;
  try { st.liftData = await api('GET', '/progress/' + encodeURIComponent(st.lift)); }
  catch (e) { showError(e); }
  render();
}

// go shows the week containing date (null = this week).
async function go(date) {
  try {
    dropCard(); // the card belongs to the week it was made for
    st.date = date;
    st.review = await api('GET', '/review/weekly' + (date ? '?date=' + date : ''));
    render();
  } catch (e) { showError(e); }
}

export default async function progress() {
  const [review, weekly, next] = await Promise.all([
    api('GET', '/review/weekly'),
    api('GET', '/progress/weekly?weeks=8'),
    api('GET', '/workouts/next'),
  ]);
  // Lifts to chart: this week's movers first, then the next workout's exercises.
  const lifts = [];
  for (const l of [...review.went_up.map(c => ({ id: c.exercise_id, name: c.name })), ...review.stalled.map(s => ({ id: s.exercise_id, name: s.name })),
    ...next.exercises.map(e => ({ id: e.id, name: e.name }))]) {
    if (!lifts.some(x => x.id === l.id)) lifts.push(l);
  }
  st = { date: null, currentStart: review.week_start, // the server's current week (Asia/Kolkata)
    review, weeks: weekly.weeks, lifts, lift: lifts[0] && lifts[0].id, liftData: null, card: null };
  render();
  await loadLift();
}
