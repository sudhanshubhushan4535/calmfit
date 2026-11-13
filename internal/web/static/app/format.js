// Pure formatting and stepper math (no DOM), shared by the views and tested with node --test.

// kg formats grams as kilograms without trailing zeros: 12500 → "12.5", 1250 → "1.25".
export function kg(g) {
  return String(Math.round(g) / 1000);
}

// target formats one set target: "20 kg × 12".
export function target(t) {
  return `${kg(t.weight_g)} kg × ${t.reps}`;
}

// sets formats several sets compactly: "20×12 · 22.5×10 · 25×8".
export function sets(list) {
  return list.map(s => `${kg(s.weight_g)}×${s.reps}`).join(' · ');
}

// dayName capitalises a template day: "pull" → "Pull".
export function dayName(day) {
  return day ? day[0].toUpperCase() + day.slice(1) : '';
}

// stepWeight moves a weight by n steps and never goes below one step (no 0 kg sets).
export function stepWeight(g, stepG, n) {
  return Math.max(stepG, g + n * stepG);
}

// stepReps moves reps by n within the API's accepted range 1–50.
export function stepReps(r, n) {
  return Math.min(50, Math.max(1, r + n));
}

// friendly maps API error codes to plain English for the user.
const MESSAGES = {
  network: "No connection. Your sets are kept on this phone and saved when you're back online.",
  onboarding_required: 'Pick your gym days first.',
  workout_completed: 'This workout is already finished.',
  no_sets_logged: 'Log at least one set before finishing.',
  unauthorized: 'Please sign in again.',
};
export function friendly(err) {
  if (err && MESSAGES[err.code]) return MESSAGES[err.code];
  if (err && err.status >= 500) return 'Something went wrong on our side. Please try again.';
  return (err && err.message) || 'Something went wrong.';
}

// weekday names a YYYY-MM-DD date ("2026-11-12" → "Thu") without timezone shifts.
const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
export function weekday(date) {
  const [y, m, d] = date.split('-').map(Number);
  return WEEKDAYS[new Date(Date.UTC(y, m - 1, d)).getUTCDay()];
}

// change formats a lift that went up (D14): "Lat Pulldown: 40 → 45 kg" or, at the same weight,
// "Hammer Curl: 10 kg, 8 → 12 reps".
export function change(c) {
  return c.to.weight_g > c.from.weight_g
    ? `${c.name}: ${kg(c.from.weight_g)} → ${kg(c.to.weight_g)} kg`
    : `${c.name}: ${kg(c.to.weight_g)} kg, ${c.from.reps} → ${c.to.reps} reps`;
}

// shiftDate moves a YYYY-MM-DD date by n days (UTC arithmetic, so no DST or zone surprises).
export function shiftDate(date, n) {
  const [y, m, d] = date.split('-').map(Number);
  return new Date(Date.UTC(y, m - 1, d + n)).toISOString().slice(0, 10);
}

// scale maps v in [lo, hi] onto [top, bottom] (SVG y grows downwards); a flat series sits mid-way.
export function scale(v, lo, hi, top, bottom) {
  if (hi === lo) return (top + bottom) / 2;
  return bottom - (v - lo) / (hi - lo) * (bottom - top);
}
