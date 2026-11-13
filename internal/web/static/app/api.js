// fetch wrapper for the CalmFit API (D51). Errors are ApiError with the HTTP status and the
// server's {"error": code}; status 0 / code "network" means the request never got an answer.
// A 401 also fires "calmfit:signedout" so the app can show the sign-in screen.

export class ApiError extends Error {
  constructor(status, code, message) {
    super(message || code);
    this.status = status;
    this.code = code;
  }
}

export async function api(method, path, body) {
  const opts = { method, headers: {}, credentials: 'same-origin' };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  let r;
  try {
    r = await fetch(path, opts);
  } catch (e) {
    throw new ApiError(0, 'network', 'network error');
  }
  if (r.status === 204) return null;
  const data = await r.json().catch(() => ({}));
  if (!r.ok) {
    const code = (data && data.error) || 'http_' + r.status;
    if (r.status === 401) window.dispatchEvent(new Event('calmfit:signedout'));
    throw new ApiError(r.status, code, `${method} ${path} → ${r.status} ${code}`);
  }
  return data;
}

// track posts a client analytics event (D48: only next_target_viewed is accepted). Best effort:
// it never blocks or breaks a screen.
export function track(name) {
  fetch('/events', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }), credentials: 'same-origin' }).catch(() => {});
}
