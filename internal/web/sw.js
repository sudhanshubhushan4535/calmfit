// CalmFit service worker (D44). It caches the app shell only. API responses are never cached:
// requests outside SHELL get no respondWith, so they go to the network as if there were no worker.
// Sets are saved by POST /workouts/{id}/sets the moment they are logged, and this worker never
// touches a POST, so a refresh or an update can never lose a logged set.
const VERSION = {{.Version}};
const SHELL = {{.Shell}};
const CACHE = 'calmfit-shell-' + VERSION;

self.addEventListener('install', e => {
  // cache: 'reload' skips the HTTP cache, so a new version never precaches yesterday's files.
  e.waitUntil(caches.open(CACHE)
    .then(c => c.addAll(SHELL.map(p => new Request(p, { cache: 'reload' }))))
    .then(() => self.skipWaiting()));
});

self.addEventListener('activate', e => {
  e.waitUntil(caches.keys()
    .then(keys => Promise.all(keys.filter(k => k.startsWith('calmfit-shell-') && k !== CACHE).map(k => caches.delete(k))))
    .then(() => self.clients.claim()));
});

self.addEventListener('fetch', e => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin || !SHELL.includes(url.pathname)) return;
  if (req.mode === 'navigate') {
    // Pages: network first so a deploy shows up at once; the cached copy only when offline.
    e.respondWith(fetch(req)
      .then(res => {
        if (res.ok) { const copy = res.clone(); caches.open(CACHE).then(c => c.put(url.pathname, copy)); }
        return res;
      })
      .catch(() => caches.match(url.pathname)));
    return;
  }
  // Icons, manifest, script: cache first (the cache name changes with every shell change).
  e.respondWith(caches.match(url.pathname).then(hit => hit || fetch(req)));
});

// Workout-day reminders (D47). The payload is {title, body, url}, sent by cmd/reminders.
self.addEventListener('push', e => {
  let msg = { title: 'CalmFit', body: 'Your next workout is ready.', url: '/app' };
  try { msg = { ...msg, ...e.data.json() }; } catch (_) {}
  e.waitUntil(self.registration.showNotification(msg.title, {
    body: msg.body, icon: '/static/icons/icon-192.png', badge: '/static/icons/icon-192.png',
    tag: 'workout-reminder', data: { url: msg.url },
  }));
});

self.addEventListener('notificationclick', e => {
  e.notification.close();
  let target = new URL((e.notification.data && e.notification.data.url) || '/app', self.location.origin);
  if (target.origin !== self.location.origin) target = new URL('/app', self.location.origin); // never open another site
  target = target.href;
  e.waitUntil(self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(wins => {
    const open = wins.find(w => w.url === target);
    return open ? open.focus() : self.clients.openWindow(target);
  }));
});
