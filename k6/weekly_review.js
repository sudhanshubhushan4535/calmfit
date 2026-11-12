// GET /review/weekly: half current week, half last week (a full Mon–Sun) (docs/BENCHMARKS.md).
import http from 'k6/http';
import { BASE_URL, options as opts, userSubs, login, params, pick, ok } from './lib.js';

export const options = opts(150, 300);

// lastSunday is the Sunday before today (UTC date; the API's zone is IST, close enough for a
// past full week: either way the date is inside last week).
function lastSunday() {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() - ((d.getUTCDay() + 7) % 7 || 7));
  return d.toISOString().slice(0, 10);
}

export function setup() {
  return { tokens: login(userSubs()), lastWeek: lastSunday() };
}

export default function (data) {
  const q = Math.random() < 0.5 ? '' : `?date=${data.lastWeek}`;
  ok(http.get(`${BASE_URL}/review/weekly${q}`, params(pick(data.tokens), 'GET /review/weekly')), 'review');
}
