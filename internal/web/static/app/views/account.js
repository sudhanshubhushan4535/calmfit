// Account: gym days, reminders on/off, data export, log out, delete account (D53).
import { api } from '../api.js';
import { el, screen, toast, showError, busy } from '../ui.js';
import { pushSupported, currentSubscription, subscribe, unsubscribe } from '../reminders.js';

const DECLINED = 'calmfit.reminders.declined';
const wasOffered = () => { try { return localStorage.getItem(DECLINED) === '1'; } catch { return false; } };

// clearLocal removes everything this app keeps on the phone. Only after deletion: a logout keeps
// any sets still waiting to be saved (D52), so signing back in sends them.
function clearLocal() {
  try {
    for (const k of Object.keys(localStorage)) if (k.startsWith('calmfit.')) localStorage.removeItem(k);
  } catch {}
}

function signedOut(app, message, clear = false) {
  if (clear) clearLocal();
  if (window.google && google.accounts && google.accounts.id) google.accounts.id.disableAutoSelect();
  app.me = null;
  app.notice = message || null; // shown by the sign-in screen (a toast would vanish on navigation)
  app.go('signin');
}

async function remindersSection() {
  const box = el('section', { class: 'card' }, el('h2', {}, 'Workout-day reminders'));
  const ios = /iPhone|iPad|iPod/.test(navigator.userAgent);
  const standalone = navigator.standalone === true || window.matchMedia('(display-mode: standalone)').matches;
  const line = text => el('p', { class: 'muted' }, text);
  const refresh = async () => box.replaceChildren(box.firstChild, ...(await body()));
  async function body() {
    if (ios && !standalone) return [line('On iPhone, reminders work only in the app on your Home Screen (Share → Add to Home Screen).')];
    if (!pushSupported()) return [line("This browser can't show reminders.")];
    if (Notification.permission === 'denied') return [line('Notifications are blocked for CalmFit. Allow them in your browser settings to get reminders.')];
    const sub = await currentSubscription().catch(() => null);
    if (sub) {
      return [line("On for this phone: one short message on workout days."),
        el('button', { type: 'button', class: 'secondary', onclick: ev => busy(ev.currentTarget, async () => {
          try { await unsubscribe(); toast('Reminders off.'); } catch (e) { showError(e); }
          refresh();
        }) }, 'Turn off')];
    }
    // The spec asks for permission only after the first completed workout; that offer comes on the
    // "next time" screen. Here it can only be turned back on after it was declined or turned off.
    if (!wasOffered()) return [line("Reminders are offered on the screen after you finish a workout.")];
    return [line('Off.'), el('button', { type: 'button', class: 'secondary', onclick: ev => busy(ev.currentTarget, async () => {
      try { toast(await subscribe()); } catch (e) { showError(e); }
      refresh();
    }) }, 'Turn on')];
  }
  box.append(...(await body()));
  return box;
}

function daysSection() {
  const status = el('p', { class: 'muted small' }, 'Changing this never resets your progress.');
  return el('section', { class: 'card' },
    el('h2', {}, 'Gym days per week'),
    el('div', { class: 'choices left' }, [2, 3, 4, 5, 6].map(d => el('button', { type: 'button', class: 'choice', onclick: ev => {
      const btn = ev.currentTarget; // currentTarget is null once the handler has awaited
      return busy(btn, async () => {
      try {
        await api('POST', '/onboarding', { days_per_week: d });
        for (const b of btn.parentNode.children) b.classList.toggle('on', b === btn);
        status.textContent = `Saved: ${d} days a week.`;
      } catch (e) { showError(e); }
      });
    } }, String(d)))),
    status);
}

function deleteSection(app) {
  const input = el('input', { type: 'text', autocomplete: 'off', autocapitalize: 'characters', 'aria-label': 'Type DELETE to confirm', placeholder: 'DELETE' });
  const btn = el('button', { type: 'button', class: 'secondary danger', disabled: true, onclick: ev => busy(ev.currentTarget, async () => {
    try {
      await api('DELETE', '/me');
      signedOut(app, 'Your account and data were deleted.', true);
    } catch (e) { showError(e); }
  }) }, 'Delete my account');
  input.addEventListener('input', () => { btn.disabled = input.value.trim().toUpperCase() !== 'DELETE'; });
  return el('details', { class: 'card' },
    el('summary', { class: 'danger' }, 'Delete account'),
    el('p', {}, 'This permanently deletes your workouts, plate checks, reminders and sign-in. It cannot be undone. Download your data first if you want a copy.'),
    el('label', { class: 'small' }, 'Type DELETE to confirm', input),
    btn);
}

export default async function account(app) {
  const me = app.me;
  screen(
    el('section', { class: 'card' },
      el('div', { class: 'eyebrow' }, 'Signed in'),
      el('h1', {}, me.name || me.email),
      me.name ? el('p', { class: 'muted' }, me.email) : null),
    daysSection(),
    await remindersSection(),
    el('section', { class: 'card' },
      el('h2', {}, 'Your data'),
      el('p', { class: 'muted small' }, 'Everything CalmFit stores about you, as one JSON file.'),
      el('a', { class: 'secondary btnlink', href: '/me/export', download: 'calmfit-export.json' }, 'Download my data')),
    el('section', { class: 'card' },
      el('button', { type: 'button', class: 'secondary wide', onclick: ev => busy(ev.currentTarget, async () => {
        try { await api('POST', '/auth/logout'); signedOut(app); } catch (e) { showError(e); }
      }) }, 'Log out'),
      el('p', { class: 'muted small center' }, el('a', { href: '/guide/protein' }, 'Veg protein guide'))),
    deleteSection(app));
}
