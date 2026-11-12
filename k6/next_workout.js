// GET /workouts/next: the engine runs on each exercise's history (docs/BENCHMARKS.md).
import http from 'k6/http';
import { BASE_URL, options as opts, userSubs, login, params, pick, ok } from './lib.js';

export const options = opts(100, 200);

export function setup() {
  return { tokens: login(userSubs()) };
}

export default function (data) {
  ok(http.get(`${BASE_URL}/workouts/next`, params(pick(data.tokens), 'GET /workouts/next')), 'next');
}
