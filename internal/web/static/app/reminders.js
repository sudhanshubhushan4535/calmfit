// Workout-day reminders (D47). Offered only after a completed workout, only while the browser
// hasn't been asked, and the permission prompt opens only from a tap (iPhone requires it).
import { api } from './api.js';
import { el } from './ui.js';

const DECLINED = 'calmfit.reminders.declined';

function b64ToBytes(s) {
  const b = atob((s + '='.repeat((4 - s.length % 4) % 4)).replace(/-/g, '+').replace(/_/g, '/'));
  return Uint8Array.from(b, c => c.charCodeAt(0));
}

const ios = () => /iPhone|iPad|iPod/.test(navigator.userAgent);
const standalone = () => navigator.standalone === true || window.matchMedia('(display-mode: standalone)').matches;
export const pushSupported = () => 'Notification' in window && 'serviceWorker' in navigator && 'PushManager' in window;

function declined() {
  try { return localStorage.getItem(DECLINED) === '1'; } catch { return false; }
}
function setDeclined(v) {
  try { v ? localStorage.setItem(DECLINED, '1') : localStorage.removeItem(DECLINED); } catch {}
}

// subscribe asks for permission and registers this browser. Returns a message for the user.
export async function subscribe() {
  const r = await fetch('/push/key', { credentials: 'same-origin' });
  if (!r.ok) return 'Reminders are not available yet.';
  const { public_key } = await r.json();
  if (await Notification.requestPermission() !== 'granted') {
    return 'Reminders are off. You can allow notifications for CalmFit in your browser settings.';
  }
  const reg = await navigator.serviceWorker.ready;
  const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64ToBytes(public_key) });
  await api('POST', '/push/subscribe', sub.toJSON());
  setDeclined(false);
  return "Reminders on. You'll get one short message on workout days.";
}

// currentSubscription is this browser's push subscription, or null.
export async function currentSubscription() {
  if (!pushSupported()) return null;
  const reg = await navigator.serviceWorker.getRegistration('/');
  return reg ? reg.pushManager.getSubscription() : null;
}

// unsubscribe turns reminders off for this browser, on the server and in the browser.
export async function unsubscribe() {
  const sub = await currentSubscription();
  if (!sub) return;
  await api('DELETE', '/push/subscribe', { endpoint: sub.endpoint });
  await sub.unsubscribe();
  setDeclined(true);
}

// offerCard returns the "Get a reminder on workout days?" card, or null when it shouldn't show.
export async function offerCard() {
  if (declined()) return null;
  if (ios() && !standalone()) {
    return el('section', { class: 'card' },
      el('h2', {}, 'Want workout-day reminders?'),
      el('p', { class: 'muted' }, 'On iPhone, add CalmFit to your Home Screen first (Share → Add to Home Screen), then open it from there.'));
  }
  if (!pushSupported() || Notification.permission !== 'default') return null;
  const r = await fetch('/push/key', { credentials: 'same-origin' });
  if (!r.ok) return null; // reminders aren't configured on this server

  const card = el('section', { class: 'card' });
  const done = text => card.replaceChildren(el('p', {}, text));
  card.append(
    el('h2', {}, 'Get a reminder on workout days?'),
    el('p', { class: 'muted' }, 'One short message in the morning, like "Pull day today. Beat 45 kg on the lat pulldown."'),
    el('div', { class: 'row' },
      el('button', { type: 'button', class: 'primary', onclick: async () => {
        try { done(await subscribe()); } catch (e) { done("Couldn't turn on reminders: " + e.message); }
      } }, 'Yes, remind me'),
      el('button', { type: 'button', class: 'ghost', onclick: () => { setDeclined(true); card.remove(); } }, 'Not now')));
  return card;
}
