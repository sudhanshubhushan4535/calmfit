// Voice logging (D31, D35): speech-to-text in the browser, parse on the server, then an editable
// confirm card. Nothing is saved until Confirm, and confirmed sets go through the same pending queue
// as tapped sets. Shown only when GET /me says voice_enabled.
import { api } from '../api.js';
import { el, stepper, showError, busy } from '../ui.js';
import { kg, stepWeight, stepReps } from '../format.js';

const SR = window.SpeechRecognition || window.webkitSpeechRecognition;

// hint is the exercise the user is most likely talking about: the first one with an open set.
function hint(st) {
  const open = st.wo.exercises.find(ex => !ex.needs_usual_weight && ex.sets.length < 3) || st.wo.exercises[0];
  return open ? open.id : '';
}

export function voicePanel(st, sync, replaceExercise, render) {
  if (!SR) return null; // no keyboard fallback in the pilot UI; the steppers cover it
  const v = (st.voice ||= { listening: false, parsed: null });

  if (v.parsed) {
    const p = v.parsed;
    const ex = st.wo.exercises.find(e => e.id === p.exercise_id);
    const step = ex ? ex.weight_step_g : 2500;
    return el('section', { class: 'card voice' },
      el('h2', {}, p.exercise_name),
      p.usual_weight_g != null ? el('p', { class: 'small' }, "First time on this exercise: set 1's weight becomes your usual weight.") : null,
      p.warnings.map(w => el('p', { class: 'warn small' }, '⚠ ' + w)),
      p.sets.map(s => el('div', { class: 'set current' },
        el('span', { class: 'set-n' }, 'Set ' + s.set_number),
        el('div', { class: 'set-controls' },
          stepper('weight', kg(s.weight_g) + ' kg', d => { s.weight_g = stepWeight(s.weight_g, step, d); render(); }),
          stepper('reps', s.reps + ' reps', d => { s.reps = stepReps(s.reps, d); render(); })))),
      el('div', { class: 'row' },
        el('button', { type: 'button', class: 'primary', onclick: ev => busy(ev.currentTarget, async () => {
          try {
            if (p.usual_weight_g != null) {
              replaceExercise(await api('POST', `/workouts/${st.wo.id}/exercises/${p.exercise_id}/cold-start`, { usual_kg: p.sets[0].weight_g / 1000 }));
            }
            for (const s of p.sets) st.queue.add({ exercise_id: p.exercise_id, set_number: s.set_number, weight_g: s.weight_g, reps: s.reps });
            v.parsed = null;
            await sync();
          } catch (e) { showError(e); }
        }) }, 'Confirm'),
        el('button', { type: 'button', class: 'ghost', onclick: () => { v.parsed = null; render(); } }, 'Cancel')));
  }

  return el('section', { class: 'voice-bar' },
    el('button', { type: 'button', class: 'secondary wide', disabled: v.listening, onclick: () => {
      const rec = new SR();
      rec.lang = 'en-IN';
      rec.interimResults = false;
      rec.maxAlternatives = 1;
      v.listening = true;
      render();
      rec.onresult = async e => {
        try {
          v.parsed = await api('POST', `/workouts/${st.wo.id}/voice/parse`, { transcript: e.results[0][0].transcript, exercise_id: hint(st) });
        } catch (err) { showError(err); }
        render();
      };
      rec.onerror = e => showError({ message: "Didn't catch that (" + e.error + '). Try again or use the buttons.' });
      rec.onend = () => { v.listening = false; render(); };
      rec.start();
    } }, v.listening ? '🎤 Listening…' : '🎤 Say your sets, e.g. "incline 20, 12 10 8"'));
}
