// node --test (make test): the app's pure modules, without a browser.
import test from 'node:test';
import assert from 'node:assert/strict';
import { kg, target, sets, dayName, stepWeight, stepReps, friendly } from './static/app/format.js';
import { parse } from './static/app/router.js';

test('kg drops trailing zeros', () => {
  assert.equal(kg(20000), '20');
  assert.equal(kg(12500), '12.5');
  assert.equal(kg(1250), '1.25');
});

test('targets and set lists', () => {
  assert.equal(target({ weight_g: 22500, reps: 10 }), '22.5 kg × 10');
  assert.equal(sets([{ weight_g: 20000, reps: 12 }, { weight_g: 22500, reps: 10 }]), '20×12 · 22.5×10');
  assert.equal(dayName('pull'), 'Pull');
  assert.equal(dayName(''), '');
});

test('weight stepper never goes below one step', () => {
  assert.equal(stepWeight(10000, 2500, 1), 12500);
  assert.equal(stepWeight(2500, 2500, -1), 2500);
  assert.equal(stepWeight(5000, 2500, -1), 2500);
});

test('rep stepper stays within 1-50', () => {
  assert.equal(stepReps(1, -1), 1);
  assert.equal(stepReps(50, 1), 50);
  assert.equal(stepReps(12, -1), 11);
});

test('friendly error text', () => {
  assert.match(friendly({ code: 'network', status: 0 }), /kept on this phone/);
  assert.match(friendly({ code: 'x', status: 503 }), /our side/);
  assert.equal(friendly({ code: 'no_sets_logged', status: 409 }), 'Log at least one set before finishing.');
});

test('router parses hashes, defaulting to today', () => {
  assert.deepEqual(parse('#/workout'), { name: 'workout' });
  assert.deepEqual(parse('#/plate/'), { name: 'plate' });
  assert.deepEqual(parse(''), { name: 'today' });
  assert.deepEqual(parse('#/../x'), { name: 'today' });
});

import { Queue, memoryStorage, safeStorage, flush, retryable } from './static/app/pending.js';

const set = (n, reps = 12, ex = 'Squat') => ({ exercise_id: ex, set_number: n, weight_g: 40000, reps });
const err = status => Object.assign(new Error('x'), { status });

test('queue persists per workout and replaces the same set', () => {
  const s = memoryStorage();
  const q = new Queue(s, 'w1');
  q.add(set(1)); q.add(set(2)); q.add(set(1, 10));
  assert.deepEqual(q.list().map(x => [x.set_number, x.reps]), [[2, 12], [1, 10]]);
  assert.equal(new Queue(s, 'w1').list().length, 2, 'survives a reload (same storage)');
  assert.equal(new Queue(s, 'w2').list().length, 0, 'other workouts are separate');
  q.remove(set(2)); q.remove(set(1));
  assert.equal(s.getItem('calmfit.pending.w1'), null, 'empty queue leaves nothing behind');
});

test('flush sends in order and stops at the first retryable failure', async () => {
  const q = new Queue(memoryStorage(), 'w');
  [1, 2, 3].forEach(n => q.add(set(n)));
  const sent = [];
  const r = await flush(q, async s => { sent.push(s.set_number); if (s.set_number === 2) throw err(0); return { id: s.set_number }; });
  assert.deepEqual(sent, [1, 2], 'set 3 is never sent before set 2');
  assert.deepEqual(q.list().map(x => x.set_number), [2, 3]);
  assert.equal(r.saved.length, 1);
  assert.equal(r.error.status, 0);
});

test('flush drops a rejected set (4xx) and keeps going; 5xx is retryable', async () => {
  const q = new Queue(memoryStorage(), 'w');
  [1, 2].forEach(n => q.add(set(n)));
  const r = await flush(q, async s => { if (s.set_number === 1) throw err(400); return {}; });
  assert.equal(r.rejected.length, 1);
  assert.equal(r.saved.length, 1);
  assert.equal(q.list().length, 0);
  assert.equal(retryable(err(503)), true);
  assert.equal(retryable(err(409)), false);
});

test('safeStorage falls back to memory when storage throws', () => {
  const broken = { getItem() { throw new Error('denied'); }, setItem() { throw new Error('denied'); }, removeItem() { throw new Error('denied'); } };
  const q = new Queue(safeStorage(broken), 'w');
  q.add(set(1));
  assert.equal(q.list().length, 1);
});

import { weekday } from './static/app/format.js';
test('weekday names a local date without timezone shift', () => {
  assert.equal(weekday('2026-11-12'), 'Thu');
  assert.equal(weekday('2026-11-16'), 'Mon');
});

import { change, shiftDate, scale } from './static/app/format.js';
test('review lines, week shifting and chart scaling', () => {
  assert.equal(change({ name: 'Lat Pulldown', from: { weight_g: 40000, reps: 13 }, to: { weight_g: 45000, reps: 12 } }), 'Lat Pulldown: 40 → 45 kg');
  assert.equal(change({ name: 'Hammer Curl', from: { weight_g: 10000, reps: 8 }, to: { weight_g: 10000, reps: 12 } }), 'Hammer Curl: 10 kg, 8 → 12 reps');
  assert.equal(shiftDate('2026-11-09', -1), '2026-11-08');
  assert.equal(shiftDate('2026-12-31', 1), '2027-01-01');
  assert.equal(scale(10, 0, 10, 20, 120), 20, 'max at the top');
  assert.equal(scale(0, 0, 10, 20, 120), 120, 'min at the bottom');
  assert.equal(scale(5, 5, 5, 20, 120), 70, 'flat series in the middle');
});
