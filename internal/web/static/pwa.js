// Registers the service worker and drives the "Add to home screen" guide (D44).
(() => {
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(e => console.warn('service worker:', e));
  }
  const guide = document.getElementById('install');
  if (!guide) return;
  const standalone = window.matchMedia('(display-mode: standalone)').matches || navigator.standalone === true;
  if (standalone) { guide.hidden = true; return; }
  // Android Chrome: a one-tap install button when the browser offers it; the written steps stay as the fallback.
  const btn = document.getElementById('install-btn');
  let prompt = null;
  window.addEventListener('beforeinstallprompt', e => {
    e.preventDefault();
    prompt = e;
    btn.hidden = false;
  });
  btn.addEventListener('click', async () => {
    if (!prompt) return;
    prompt.prompt();
    await prompt.userChoice;
    prompt = null;
    btn.hidden = true;
  });
  window.addEventListener('appinstalled', () => { guide.hidden = true; });
  // Open the steps for this phone.
  const ios = /iPhone|iPad|iPod/.test(navigator.userAgent);
  const steps = guide.querySelectorAll('details');
  if (steps.length === 2) steps[ios ? 1 : 0].open = true;
})();
