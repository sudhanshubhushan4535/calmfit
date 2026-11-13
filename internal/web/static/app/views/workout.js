// The workout (spec steps 3, 6, 9): one card per exercise, one tap ✓ per set with +/− steppers,
// the usual-weight question on a first-ever exercise, "Do later", how-to info, and Finish.
// Every set goes through the pending queue and is flushed in order, because the server only accepts
// set n after set n-1 (D52).
import { api } from '../api.js';
import { el, screen, stepper, toast, showError, busy } from '../ui.js';
import { kg, target, dayName, stepWeight, stepReps, friendly } from '../format.js';
import { Queue, safeStorage, memoryStorage, flush } from '../pending.js';
import { voicePanel } from './voice.js';

const SETS = 3;
let st = null; // {app, wo, queue, drafts, info, flushing}

function storage() {
  try { return safeStorage(window.localStorage); } catch { return memoryStorage(); }
}

const sendSet = s => api('POST', `/workouts/${st.wo.id}/sets`, s);

function replaceExercise(ex) {
  st.wo.exercises = st.wo.exercises.map(e => (e.id === ex.id ? ex : e));
}

// sync flushes the queue in order and re-renders. Safe to call any time; runs one at a time.
async function sync() {
  if (st.flushing || !st.queue.list().length) return render();
  st.flushing = true;
  render();
  try {
    const r = await flush(st.queue, sendSet);
    r.saved.forEach(replaceExercise);
    for (const { set, err } of r.rejected) toast(`Set ${set.set_number} wasn't saved: ${friendly(err)}`);
    if (r.error) toast(friendly(r.error));
  } finally {
    st.flushing = false;
    render();
  }
}

window.addEventListener('online', () => { if (st && document.body.dataset.route === 'workout') sync(); });

// targetFor is the target of set n: the plan for logged sets, the live remaining targets after them
// (the engine adjusts later sets in-session, R10).
function targetFor(ex, n) {
  const logged = ex.sets.length;
  return (n > logged && ex.remaining[n - 1 - logged]) || ex.plan.sets[n - 1];
}

function logSet(ex, n) {
  const key = ex.id + ':' + n;
  const d = st.drafts[key];
  st.queue.add({ exercise_id: ex.id, set_number: n, weight_g: d.weight_g, reps: d.reps }); // on the phone first
  delete st.drafts[key];
  for (let m = n + 1; m <= SETS; m++) delete st.drafts[ex.id + ':' + m]; // re-prefill from new targets
  sync();
}

function setRow(ex, n) {
  const key = ex.id + ':' + n;
  const logged = ex.sets.find(s => s.set_number === n);
  const pending = st.queue.get(ex.id, n);
  const t = targetFor(ex, n);

  if (pending && !st.drafts[key]) {
    return el('div', { class: 'set pending' },
      el('span', { class: 'set-n' }, 'Set ' + n),
      el('span', { class: 'set-val' }, target(pending)),
      st.flushing ? el('span', { class: 'set-note small' }, 'Saving…')
        : el('button', { type: 'button', class: 'ghost set-note', onclick: () => sync() }, 'Not saved yet · Tap to retry'));
  }
  if (logged && !st.drafts[key]) {
    return el('div', { class: 'set done' },
      el('span', { class: 'set-n' }, '✓ Set ' + n),
      el('span', { class: 'set-val' }, target(logged)),
      el('button', { type: 'button', class: 'ghost small', onclick: () => {
        st.drafts[key] = { weight_g: logged.weight_g, reps: logged.reps };
        render();
      } }, 'Edit'));
  }
  // The current set: the first one neither logged nor waiting, or one being edited.
  const firstOpen = [...Array(SETS).keys()].map(i => i + 1)
    .find(m => !ex.sets.some(s => s.set_number === m) && !st.queue.has(ex.id, m));
  if (!st.drafts[key] && n !== firstOpen) {
    return el('div', { class: 'set later muted' }, el('span', { class: 'set-n' }, 'Set ' + n), el('span', { class: 'set-val' }, target(t)));
  }
  const d = (st.drafts[key] ||= { weight_g: t.weight_g, reps: t.reps });
  const changed = d.weight_g !== t.weight_g || d.reps !== t.reps;
  return el('div', { class: 'set current' },
    el('div', { class: 'set-head' },
      el('span', { class: 'set-n' }, 'Set ' + n),
      el('span', { class: 'muted small' }, 'Target ' + target(t))),
    t.why && t.why !== ex.plan.summary ? el('div', { class: 'why' }, t.why) : null,
    el('div', { class: 'set-controls' },
      stepper('weight', kg(d.weight_g) + ' kg', s => { d.weight_g = stepWeight(d.weight_g, ex.weight_step_g, s); render(); }),
      stepper('reps', d.reps + ' reps', s => { d.reps = stepReps(d.reps, s); render(); })),
    el('button', { type: 'button', class: 'primary wide log', onclick: () => logSet(ex, n) },
      changed ? `✓ Log ${kg(d.weight_g)} kg × ${d.reps}` : '✓ Done as planned'));
}

function coldStart(ex) {
  const key = ex.id + ':usual';
  st.drafts[key] ||= ex.weight_step_g;
  const send = body => async ev => busy(ev.currentTarget, async () => {
    try {
      replaceExercise(await api('POST', `/workouts/${st.wo.id}/exercises/${ex.id}/cold-start`, body));
      delete st.drafts[key];
      render();
    } catch (e) { showError(e); }
  });
  return el('div', { class: 'cold' },
    el('p', {}, 'First time on this exercise. What weight do you usually lift?'),
    stepper('usual weight', kg(st.drafts[key]) + ' kg', s => { st.drafts[key] = stepWeight(st.drafts[key], ex.weight_step_g, s); render(); }),
    el('div', { class: 'row' },
      el('button', { type: 'button', class: 'primary', onclick: send({ usual_kg: st.drafts[key] / 1000 }) }, 'Use this weight'),
      el('button', { type: 'button', class: 'secondary', onclick: send({ dont_know: true }) }, "I don't know")),
    el('p', { class: 'muted small' }, '"I don\'t know" starts you at the lightest weight.'));
}

// howTo is a collapsible sheet with the 2 images, instructions and 2 cues, loaded on first open.
function howTo(ex) {
  const body = el('div', { class: 'howto-body' }, el('p', { class: 'muted' }, 'Loading…'));
  const d = el('details', { class: 'howto', open: st.info.get(ex.id)?.open || false },
    el('summary', {}, 'How to do it'), body);
  const fill = info => body.replaceChildren(
    el('div', { class: 'imgs' }, info.images.map(src => el('img', { src, alt: info.name, loading: 'lazy' }))),
    info.cues.length ? el('div', {},
      el('strong', {}, 'Cues'),
      el('ul', {}, info.cues.map(c => el('li', {}, c))),
      info.cues_status === 'review' ? el('p', { class: 'muted small' }, 'Cues not yet checked by a coach.') : null) : null,
    el('strong', {}, 'Steps'),
    el('ol', {}, info.instructions.map(i => el('li', {}, i))));
  const cached = st.info.get(ex.id);
  if (cached && cached.data) fill(cached.data);
  d.addEventListener('toggle', async () => {
    const c = st.info.get(ex.id) || {};
    c.open = d.open;
    st.info.set(ex.id, c);
    if (!d.open || c.data) return;
    try { c.data = await api('GET', '/exercises/' + encodeURIComponent(ex.id)); fill(c.data); }
    catch (e) { body.replaceChildren(el('p', { class: 'err' }, friendly(e))); }
  });
  return d;
}

function exerciseCard(ex) {
  const allDone = [1, 2, 3].every(n => ex.sets.some(s => s.set_number === n) || st.queue.has(ex.id, n));
  const card = el('section', { class: 'card exercise' + (allDone ? ' complete' : '') },
    el('div', { class: 'ex-head' },
      el('h2', {}, ex.name),
      allDone ? null : el('button', { type: 'button', class: 'ghost small', onclick: ev => busy(ev.currentTarget, async () => {
        try { st.wo = await api('POST', `/workouts/${st.wo.id}/exercises/${ex.id}/later`); render(); toast(ex.name + ' moved to the end.'); }
        catch (e) { showError(e); }
      }) }, 'Do later')),
    howTo(ex));
  if (ex.needs_usual_weight) {
    card.append(coldStart(ex));
    return card;
  }
  card.append(el('div', { class: 'why' }, ex.plan.summary));
  if (ex.plan.warmups.length) {
    card.append(el('div', { class: 'muted small' }, 'Warm-up (not logged): ' +
      ex.plan.warmups.map(w => `${kg(w.weight_g)} kg × ${w.reps}`).join(' · ')));
  }
  for (let n = 1; n <= SETS; n++) card.append(setRow(ex, n));
  return card;
}

function finishBar() {
  const logged = st.wo.exercises.reduce((a, ex) => a + ex.sets.length, 0);
  const waiting = st.queue.list().length;
  const skipped = st.wo.exercises.filter(ex => ex.sets.length === 0).length;
  const label = st.finishArmed ? `Finish with ${skipped} skipped? Tap again` : 'Finish workout';
  const btn = el('button', { type: 'button', class: 'primary wide', disabled: logged === 0 || waiting > 0 || st.flushing,
    onclick: ev => busy(ev.currentTarget, async () => {
      if (skipped > 0 && !st.finishArmed) { st.finishArmed = true; return render(); }
      try {
        st.app.summary = await api('POST', `/workouts/${st.wo.id}/complete`);
        st.app.go('next');
      } catch (e) { st.finishArmed = false; showError(e); render(); }
    }) }, label);
  const note = waiting > 0 ? `${waiting} set${waiting > 1 ? 's' : ''} not saved yet. They'll save when you're online.`
    : logged === 0 ? 'Log at least one set to finish.' : null;
  return el('section', { class: 'finish' }, btn, note ? el('p', { class: 'muted small center' }, note) : null);
}

function render() {
  if (!st || document.body.dataset.route !== 'workout') return;
  const total = st.wo.exercises.length * SETS;
  const logged = st.wo.exercises.reduce((a, ex) => a + ex.sets.length, 0) + st.queue.list().length;
  const y = window.scrollY;
  screen(
    el('header', { class: 'wo-head' },
      el('button', { type: 'button', class: 'ghost', onclick: () => st.app.go('today') }, '← Today'),
      el('div', {}, el('h1', {}, dayName(st.wo.day) + ' day'), el('div', { class: 'muted small' }, `${logged} of ${total} sets`))),
    st.app.me.voice_enabled ? voicePanel(st, sync, replaceExercise, render) : null,
    st.wo.exercises.map(exerciseCard),
    finishBar());
  window.scrollTo(0, y); // re-rendering must not jump the page while logging
}

export default async function workout(app) {
  const wo = await api('POST', '/workouts/start'); // resumes the in-progress workout if there is one
  st = { app, wo, queue: new Queue(storage(), wo.id), drafts: {}, info: new Map(), flushing: false, finishArmed: false };
  await sync(); // sets left unsaved before a refresh or crash go first
}
