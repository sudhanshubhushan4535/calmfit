// node --test internal/web/*.test.cjs (run by make test). Tests the share card's model and layout
// without a browser: a fake 2D context measures text as 0.6 × font size per character.
const test = require('node:test');
const assert = require('node:assert/strict');
const card = require('./static/sharecard.js');

const review = (over = {}) => ({
  week_start: '2026-11-09', week_end: '2026-11-15', done: 3, planned: 3,
  went_up: [], stalled: [], plate: { good_days: 4, days: 5, streak: 2 }, tip: '', ...over,
});
const up = (name, fw, fr, tw, tr) => ({ exercise_id: name, name, from: { weight_g: fw, reps: fr }, to: { weight_g: tw, reps: tr } });

function fakeCtx() {
  let size = 10;
  return {
    set font(f) { size = Number(/(\d+)px/.exec(f)[1]); },
    get font() { return size + 'px'; },
    measureText: t => ({ width: [...t].length * size * 0.6 }),
    fillRect() {}, fillText() {}, fillStyle: '', textBaseline: '',
  };
}

test('week label within and across months', () => {
  assert.equal(card.weekLabel('2026-11-09', '2026-11-15'), '9–15 Nov');
  assert.equal(card.weekLabel('2026-10-26', '2026-11-01'), '26 Oct – 1 Nov');
});

test('stats: workouts, good plate days of logged days, lifts up', () => {
  const m = card.cardModel(review({ went_up: [up('Hammer Curl', 10000, 10, 12500, 12)] }));
  assert.deepEqual(m.stats.map(s => s.value + ' ' + s.label), ['3/3 workouts', '4/5 good plate days', '1 lift went up']);
  assert.equal(m.week, '9–15 Nov');
});

test('no plate days logged shows a dash, never 0', () => {
  const m = card.cardModel(review({ plate: { good_days: 0, days: 0, streak: 0 } }));
  assert.equal(m.stats[1].value, '–');
});

test('lifts: biggest weight gain first, then reps; at most 3 plus a count', () => {
  const m = card.cardModel(review({ went_up: [
    up('Leg Extension', 30000, 12, 30000, 13),
    up('Barbell Squat', 40000, 12, 42500, 10),
    up('Lat Pulldown', 40000, 12, 45000, 9),
    up('Hammer Curl', 10000, 8, 10000, 12),
    up('Seated Cable Row', 35000, 12, 37500, 12),
  ] }));
  assert.deepEqual(m.lifts, [
    'Lat Pulldown: 40 → 45 kg',
    'Seated Cable Row: 35 → 37.5 kg', // ties Squat on +2.5 kg; more reps kept wins
    'Barbell Squat: 40 → 42.5 kg',
    '+2 more',
  ]);
  assert.equal(m.strength, 'Strength gained');
  assert.equal(m.stats[2].value, '5');
});

test('same weight, more reps', () => {
  const m = card.cardModel(review({ went_up: [up('Hammer Curl', 10000, 8, 10000, 12)] }));
  assert.deepEqual(m.lifts, ['Hammer Curl: 10 kg, 8 → 12 reps']);
});

test('nothing went up: no guilt, no zero-shaming', () => {
  const m = card.cardModel(review({ done: 0, planned: 3 }));
  assert.equal(m.strength, 'Building strength');
  assert.equal(m.lifts.length, 1);
  assert.equal(m.stats[2].label, 'lifts went up');
});

test('fitText shrinks, then ellipsizes', () => {
  const measure = (t, s) => t.length * s;
  assert.deepEqual(card.fitText('abcd', 95, 24, 10, measure), { text: 'abcd', size: 22 });
  assert.deepEqual(card.fitText('abcdefghijklmnop', 100, 20, 10, measure), { text: 'abcdefghi…', size: 10 });
});

test('every drawn text stays inside the card and the story safe zone', () => {
  const long = 'Two-Arm Dumbbell Preacher Curl With An Absurdly Long Name';
  const m = card.cardModel(review({ done: 12, planned: 12, plate: { good_days: 7, days: 7 },
    went_up: [up(long, 100000, 12, 102500, 12), up(long + ' 2', 5000, 8, 5000, 12), up('x', 1, 1, 2, 1), up('y', 1, 1, 3, 1)] }));
  const drawn = card.drawCard(fakeCtx(), m);
  assert.ok(drawn.length >= 12, `drew ${drawn.length} texts`);
  for (const d of drawn) {
    assert.ok(d.x >= 0 && d.x + d.width <= card.W - 64, `"${d.text}" is too wide: ${d.x}+${d.width}`);
    assert.ok(d.y - d.size >= 250 && d.y <= card.H - 250, `"${d.text}" is outside the story safe zone at y=${d.y}`);
  }
  assert.ok(drawn.some(d => d.text === 'Stop counting grams. Start getting stronger.'));
});
