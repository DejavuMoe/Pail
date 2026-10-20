/* Prototype-only review controls. Not part of the production site. */
(function () {
  'use strict';
  var root = document.documentElement;
  var KEY = 'pail-site-tweaks';
  var saved = {};
  try { saved = JSON.parse(localStorage.getItem(KEY) || '{}'); } catch (e) {}
  var state = {
    hero: saved.hero === 'split' ? 'split' : 'center',
    backdrop: saved.backdrop === 'plain' ? 'plain' : 'keys'
  };
  var options = {
    hero: [['center', 'Centered'], ['split', 'Split']],
    backdrop: [['keys', 'Object keys'], ['plain', 'Plain']]
  };
  var labels = { hero: 'Hero layout', backdrop: 'Hero backdrop' };

  function apply() {
    root.setAttribute('data-hero', state.hero);
    root.setAttribute('data-backdrop', state.backdrop);
    try { localStorage.setItem(KEY, JSON.stringify(state)); } catch (e) {}
    window.dispatchEvent(new Event('resize'));
  }

  var css = document.createElement('style');
  css.textContent =
    '.tw-toggle{position:fixed;right:16px;bottom:16px;z-index:70;height:34px;padding:0 14px;border:1px solid var(--border-strong);border-radius:999px;background:var(--surface);color:var(--text);font:500 13px var(--font-body);cursor:pointer;box-shadow:0 8px 24px -12px rgb(var(--shadow-color)/.4)}' +
    '.tw-panel{position:fixed;right:16px;bottom:60px;z-index:70;width:260px;padding:14px;border:1px solid var(--border-strong);border-radius:14px;background:var(--surface);box-shadow:0 24px 48px -24px rgb(var(--shadow-color)/.5);font:13px var(--font-body);display:grid;gap:12px}' +
    '.tw-panel[hidden]{display:none}.tw-row{display:grid;gap:6px}.tw-row>span{color:var(--muted);font-size:12px;font-weight:500}' +
    '.tw-seg{display:flex;flex-wrap:wrap;gap:4px}.tw-seg button{padding:4px 9px;border:1px solid var(--border);border-radius:7px;background:var(--bg);color:var(--muted);cursor:pointer;font-size:12px}' +
    '.tw-seg button[aria-pressed="true"]{border-color:var(--border-strong);background:var(--raised);color:var(--text);font-weight:600}';
  document.head.appendChild(css);

  var toggle = document.createElement('button');
  toggle.type = 'button';
  toggle.className = 'tw-toggle';
  toggle.textContent = 'Tweaks';
  toggle.setAttribute('aria-expanded', 'false');

  var panel = document.createElement('div');
  panel.className = 'tw-panel';
  panel.hidden = true;

  Object.keys(options).forEach(function (name) {
    var row = document.createElement('div');
    row.className = 'tw-row';
    var label = document.createElement('span');
    label.textContent = labels[name];
    var seg = document.createElement('div');
    seg.className = 'tw-seg';
    options[name].forEach(function (opt) {
      var b = document.createElement('button');
      b.type = 'button';
      b.textContent = opt[1];
      b.setAttribute('aria-pressed', String(state[name] === opt[0]));
      b.addEventListener('click', function () {
        state[name] = opt[0];
        Array.prototype.forEach.call(seg.children, function (x) { x.setAttribute('aria-pressed', String(x === b)); });
        apply();
      });
      seg.appendChild(b);
    });
    row.appendChild(label);
    row.appendChild(seg);
    panel.appendChild(row);
  });

  toggle.addEventListener('click', function () {
    panel.hidden = !panel.hidden;
    toggle.setAttribute('aria-expanded', String(!panel.hidden));
  });

  document.body.appendChild(panel);
  document.body.appendChild(toggle);
  apply();
})();
