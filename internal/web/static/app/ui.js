// Small DOM helpers shared by the views.
import { friendly } from './format.js';

// el builds an element: el('button', {class: 'primary', onclick: fn}, 'Save').
// on* attributes become listeners; true/false set or drop boolean attributes (hidden, disabled).
export function el(tag, attrs = {}, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
    else if (v === true) n.setAttribute(k, '');
    else n.setAttribute(k, v);
  }
  for (const k of kids.flat(Infinity)) if (k !== null && k !== undefined && k !== false) n.append(k);
  return n;
}

let toastTimer;
// toast shows a short message at the bottom of the screen.
export function toast(msg) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 4000);
}

// showError toasts an API error in plain English.
export function showError(err) {
  console.warn(err);
  toast(friendly(err));
}

// busy disables a button while fn runs, so a double tap can't send twice.
export async function busy(btn, fn) {
  if (btn.disabled) return;
  btn.disabled = true;
  try { return await fn(); } finally { btn.disabled = false; }
}

// stepper: [−] value [+] with big tap targets; onStep(n) gets −1 or +1.
export function stepper(label, value, onStep) {
  return el('div', { class: 'stepper', role: 'group', 'aria-label': label },
    el('button', { type: 'button', class: 'step', 'aria-label': 'less ' + label, onclick: () => onStep(-1) }, '−'),
    el('span', { class: 'val' }, value),
    el('button', { type: 'button', class: 'step', 'aria-label': 'more ' + label, onclick: () => onStep(1) }, '+'));
}

// screen replaces the main view and scrolls to the top.
export function screen(...kids) {
  const main = document.getElementById('view');
  main.replaceChildren(...kids.flat(Infinity).filter(k => k !== null && k !== undefined && k !== false));
  window.scrollTo(0, 0);
  return main;
}
