// Shared setup for the CalmFit k6 benchmarks (docs/BENCHMARKS.md). Host-agnostic: BASE_URL.
// The API must run with APP_ENV=dev TEST_AUTH=1 (D39); users come from `make seed-synthetic`.
import http from 'k6/http';
import { check, fail } from 'k6';

export const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const USERS = parseInt(__ENV.USERS || '500', 10);
const SYNTHETIC_USERS = parseInt(__ENV.SYNTHETIC_USERS || '4000', 10);
const RATE = parseInt(__ENV.RATE || '50', 10);

// options returns a 15 s warm-up and a 60 s measured scenario at RATE req/s; thresholds apply to
// the measured scenario only.
export function options(p95, p99) {
  const base = { executor: 'constant-arrival-rate', rate: RATE, timeUnit: '1s', preAllocatedVUs: 50, maxVUs: 300 };
  return {
    setupTimeout: '180s',
    summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
    scenarios: {
      warmup: Object.assign({}, base, { duration: '15s', tags: { phase: 'warmup' } }),
      measure: Object.assign({}, base, { duration: '60s', startTime: '15s', tags: { phase: 'measure' } }),
    },
    thresholds: {
      'http_req_duration{scenario:measure}': [`p(95)<${p95}`, `p(99)<${p99}`],
      'http_req_failed{scenario:measure}': ['rate<0.001'],
    },
  };
}

// userSubs spreads USERS over the synthetic range (every Nth), keeping the history-length mix.
export function userSubs() {
  const stride = Math.max(1, Math.floor(SYNTHETIC_USERS / USERS));
  const subs = [];
  for (let i = 0; i < USERS && i * stride < SYNTHETIC_USERS; i++) {
    subs.push('synthetic-' + String(i * stride).padStart(5, '0'));
  }
  return subs;
}

// login signs every user in through test auth, 50 at a time, and returns their session tokens.
export function login(subs) {
  const tokens = [];
  for (let i = 0; i < subs.length; i += 50) {
    const reqs = subs.slice(i, i + 50).map((sub) => ({
      method: 'POST',
      url: `${BASE_URL}/auth/google`,
      body: JSON.stringify({ credential: `test:${sub}` }),
      params: { headers: { 'Content-Type': 'application/json' }, tags: { name: 'setup_login' } },
    }));
    for (const res of http.batch(reqs)) {
      if (res.status !== 200 || !res.cookies.calmfit_session) {
        fail(`login failed (${res.status}): is the API running with APP_ENV=dev TEST_AUTH=1 and is make seed-synthetic loaded?`);
      }
      tokens.push(res.cookies.calmfit_session[0].value);
    }
  }
  return tokens;
}

export function params(token, name, extra) {
  return Object.assign({ headers: { Cookie: `calmfit_session=${token}` }, tags: { name } }, extra || {});
}

export function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

export function ok(res, name) {
  check(res, { [`${name} 200`]: (r) => r.status === 200 });
}
