// POST /workouts/{id}/sets: the write path (row lock, in-session check, upsert, response view).
// Setup starts or resumes a workout per user and logs sets 1–2 of its first planned exercise,
// so any set 1–3 is a valid upsert afterwards (docs/BENCHMARKS.md).
import http from 'k6/http';
import { fail } from 'k6';
import { BASE_URL, options as opts, userSubs, login, params, pick, ok } from './lib.js';

export const options = opts(50, 100);

function logSet(token, t, n) {
  const s = t.plan.sets[n - 1];
  return http.post(`${BASE_URL}/workouts/${t.workout}/sets`,
    JSON.stringify({ exercise_id: t.exercise, set_number: n, weight_g: s.weight_g, reps: s.reps }),
    params(token, 'POST /workouts/{id}/sets', { headers: { Cookie: `calmfit_session=${token}`, 'Content-Type': 'application/json' } }));
}

export function setup() {
  const tokens = login(userSubs());
  const targets = [];
  for (const token of tokens) {
    const res = http.post(`${BASE_URL}/workouts/start`, null, params(token, 'setup_start'));
    if (res.status !== 200 && res.status !== 201) fail(`start: ${res.status} ${res.body}`);
    const w = res.json();
    const ex = w.exercises.find((e) => e.plan);
    if (!ex) continue; // no planned exercise (shouldn't happen with synthetic history)
    const t = { token, workout: w.id, exercise: ex.id, plan: ex.plan };
    for (const n of [1, 2]) {
      const r = logSet(token, t, n);
      if (r.status !== 200) fail(`setup log set ${n}: ${r.status} ${r.body}`);
    }
    targets.push(t);
  }
  return { targets };
}

export default function (data) {
  const t = pick(data.targets);
  ok(logSet(t.token, t, 1 + Math.floor(Math.random() * 3)), 'log set');
}
