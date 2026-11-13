// "Never lose a set" (spec item 9; D52). Every set is saved the moment it is tapped. If the request
// gets no answer (offline) or a 5xx, the set waits here, in localStorage, until it can be re-sent.
// The server upserts by (exercise, set number), so re-sending is always safe.
// Pure apart from the injected storage, so it is tested with node --test.

// memoryStorage stands in when localStorage is unavailable (private mode, blocked site data).
export function memoryStorage() {
  const m = new Map();
  return { getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k) };
}

// safeStorage wraps a Storage so a throwing accessor never breaks logging.
export function safeStorage(s) {
  const mem = memoryStorage();
  const tryOr = (fn, fallback) => { try { return fn(); } catch { return fallback(); } };
  return {
    getItem: k => tryOr(() => s.getItem(k), () => mem.getItem(k)),
    setItem: (k, v) => tryOr(() => s.setItem(k, v), () => mem.setItem(k, v)),
    removeItem: k => tryOr(() => s.removeItem(k), () => mem.removeItem(k)),
  };
}

const same = (a, b) => a.exercise_id === b.exercise_id && a.set_number === b.set_number;

// Queue holds the unsaved sets of one workout, oldest first.
export class Queue {
  constructor(storage, workoutId) {
    this.storage = storage;
    this.key = 'calmfit.pending.' + workoutId;
  }
  list() {
    try { return JSON.parse(this.storage.getItem(this.key) || '[]'); } catch { return []; }
  }
  save(list) {
    if (list.length) this.storage.setItem(this.key, JSON.stringify(list));
    else this.storage.removeItem(this.key);
  }
  // add stores a set; a newer value for the same set replaces the older one.
  add(set) {
    const list = this.list().filter(s => !same(s, set));
    list.push({ exercise_id: set.exercise_id, set_number: set.set_number, weight_g: set.weight_g, reps: set.reps });
    this.save(list);
  }
  remove(set) {
    this.save(this.list().filter(s => !same(s, set)));
  }
  has(exerciseId, setNumber) {
    return this.list().some(s => s.exercise_id === exerciseId && s.set_number === setNumber);
  }
  get(exerciseId, setNumber) {
    return this.list().find(s => s.exercise_id === exerciseId && s.set_number === setNumber);
  }
}

// retryable: no answer at all, or the server failed. A 4xx means the set itself was rejected.
export function retryable(err) {
  return !err || err.status === 0 || err.status >= 500;
}

// flush re-sends every queued set in order with send(set) → updated exercise. It stops at the first
// retryable failure (still offline) and keeps that set and the rest. A rejected set (4xx) is dropped
// and reported. Returns {saved: [exercise...], rejected: [{set, err}], error}.
export async function flush(queue, send) {
  const out = { saved: [], rejected: [], error: null };
  for (const set of queue.list()) {
    try {
      out.saved.push(await send(set));
      queue.remove(set);
    } catch (err) {
      if (retryable(err)) { out.error = err; break; }
      queue.remove(set);
      out.rejected.push({ set, err });
    }
  }
  return out;
}
