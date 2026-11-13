// Hash router: "#/workout" → {name: "workout"}. parse is pure (tested); start wires it to the page.

export const TABS = ['today', 'plate', 'progress', 'account'];

export function parse(hash) {
  const m = /^#\/([a-z]+)\/?$/.exec(hash || '');
  return { name: m ? m[1] : 'today' };
}

export function go(name) {
  if (location.hash !== '#/' + name) location.hash = '#/' + name;
  else window.dispatchEvent(new HashChangeEvent('hashchange'));
}

// start calls render(name) now and on every hash change.
export function start(render) {
  const run = () => render(parse(location.hash).name);
  window.addEventListener('hashchange', run);
  run();
}
