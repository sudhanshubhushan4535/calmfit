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
