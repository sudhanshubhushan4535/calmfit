// GET /progress/{exerciseID}: the last 50 set-1 points of one exercise (docs/BENCHMARKS.md).
import http from 'k6/http';
import { BASE_URL, options as opts, userSubs, login, params, pick, ok } from './lib.js';

export const options = opts(50, 100);

const EXERCISES = [
  'Incline_Dumbbell_Press', 'Butterfly', 'Dumbbell_Shoulder_Press', 'Side_Lateral_Raise', 'Triceps_Pushdown',
  'Close-Grip_Front_Lat_Pulldown', 'Seated_Cable_Rows', 'Upright_Barbell_Row', 'Reverse_Machine_Flyes', 'Hammer_Curls',
  'Two-Arm_Dumbbell_Preacher_Curl', 'Barbell_Squat', 'Barbell_Hip_Thrust', 'Seated_Leg_Curl', 'Leg_Extensions', 'Seated_Calf_Raise',
];

export function setup() {
  return { tokens: login(userSubs()) };
}

export default function (data) {
  const ex = pick(EXERCISES);
  ok(http.get(`${BASE_URL}/progress/${ex}`, params(pick(data.tokens), 'GET /progress/{exerciseID}')), 'history');
}
