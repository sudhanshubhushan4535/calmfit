// The spec's 1-2 strength charts as inline SVG (no chart library). Colours come from CSS classes.
import { kg, scale } from './format.js';

const NS = 'http://www.w3.org/2000/svg';
const W = 340, H = 180, PAD = { l: 34, r: 12, t: 22, b: 26 };

function s(tag, attrs = {}, text) {
  const n = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
  if (text !== undefined) n.textContent = text;
  return n;
}

function frame(label) {
  const svg = s('svg', { viewBox: `0 0 ${W} ${H}`, class: 'chart', role: 'img', 'aria-label': label });
  svg.append(s('line', { x1: PAD.l, y1: H - PAD.b, x2: W - PAD.r, y2: H - PAD.b, class: 'axis' }));
  return svg;
}

// setOneChart: set-1 weight per session for one exercise; each point labelled with its reps.
export function setOneChart(p) {
  const svg = frame(`${p.name}: set 1 weight per session`);
  const pts = p.points.slice(-12); // the last 12 sessions stay readable on a phone
  if (!pts.length) {
    svg.append(s('text', { x: W / 2, y: H / 2, 'text-anchor': 'middle', class: 'label' }, 'No sessions yet'));
    return svg;
  }
  const ws = pts.map(q => q.weight_g);
  const lo = Math.min(...ws), hi = Math.max(...ws);
  const x = i => PAD.l + (pts.length === 1 ? (W - PAD.l - PAD.r) / 2 : i * (W - PAD.l - PAD.r) / (pts.length - 1));
  const y = g => scale(g, lo, hi, PAD.t, H - PAD.b);
  svg.append(s('text', { x: 2, y: y(hi) + 4, class: 'label' }, kg(hi)), s('text', { x: 2, y: y(lo) + 4, class: 'label' }, kg(lo)));
  svg.append(s('polyline', { points: pts.map((q, i) => `${x(i)},${y(q.weight_g)}`).join(' '), class: 'line' }));
  pts.forEach((q, i) => {
    svg.append(s('circle', { cx: x(i), cy: y(q.weight_g), r: 3.5, class: 'pt' }));
    svg.append(s('text', { x: x(i), y: y(q.weight_g) - 7, 'text-anchor': 'middle', class: 'label' }, '×' + q.reps));
  });
  svg.append(s('text', { x: x(0), y: H - 8, 'text-anchor': 'start', class: 'label' }, pts[0].date.slice(5)));
  if (pts.length > 1) svg.append(s('text', { x: x(pts.length - 1), y: H - 8, 'text-anchor': 'end', class: 'label' }, pts.at(-1).date.slice(5)));
  return svg;
}

// weeksChart: workouts done (green) vs planned (grey) per week.
export function weeksChart(weeks) {
  const svg = frame('Workouts per week, done vs planned');
  const hi = Math.max(1, ...weeks.map(w => Math.max(w.done, w.planned)));
  const slot = (W - PAD.l - PAD.r) / weeks.length, bw = Math.min(14, slot / 3);
  const y = v => scale(v, 0, hi, PAD.t, H - PAD.b);
  weeks.forEach((w, i) => {
    const cx = PAD.l + slot * i + slot / 2;
    svg.append(s('rect', { x: cx - bw - 1, y: y(w.planned), width: bw, height: H - PAD.b - y(w.planned), class: 'bar-planned' }));
    svg.append(s('rect', { x: cx + 1, y: y(w.done), width: bw, height: H - PAD.b - y(w.done), class: 'bar-done' }));
    svg.append(s('text', { x: cx, y: H - 8, 'text-anchor': 'middle', class: 'label' }, w.week_start.slice(8) + '/' + w.week_start.slice(5, 7)));
  });
  return svg;
}
