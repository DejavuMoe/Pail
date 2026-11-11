// The landing page demos must follow the reduced-motion preference live, not only
// when an IntersectionObserver fires. app.js runs here against a minimal DOM stub.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');

function load(rel, { legacyListener = false } = {}) {
  const src = fs.readFileSync(path.join(root, rel), 'utf8');
  const listeners = [];
  const mq = {
    matches: false,
    addEventListener: legacyListener ? undefined : (_, f) => listeners.push(f),
    addListener: legacyListener ? (f) => listeners.push(f) : undefined,
  };
  const observers = [];
  class IO { constructor(cb) { this.cb = cb; observers.push(this); } observe() {} unobserve() {} disconnect() {} }
  const timers = new Map();
  let nextTimer = 1;
  const el = () => ({
    classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
    style: {}, setAttribute() {}, getAttribute: () => null, hasAttribute: () => false,
    addEventListener() {}, querySelector: () => null, querySelectorAll: () => [], textContent: '', innerHTML: '',
  });
  const versions = el();
  const termCode = el();
  const term = el();
  const tab = Object.assign(el(), { getAttribute: (k) => (k === 'data-term' ? 'ls' : null) });
  term.querySelector = (s) => (s === '.term-body code' ? termCode : null);
  term.querySelectorAll = (s) => (s === '[data-term]' ? [tab] : []);
  const cliDemo = { textContent: JSON.stringify({ examples: [{ id: 'ls', lines: [{ kind: 'cmd', text: 'pail ls' }, { kind: 'out', text: 'a' }] }] }) };
  const pick = { '.mini-versions': versions, '[data-demo="terminal"]': term, '#pail-cli-demo': cliDemo };
  const document = {
    documentElement: Object.assign(el(), { lang: '' }), hidden: false,
    querySelector: (s) => pick[s] || null, querySelectorAll: () => [], addEventListener() {},
  };
  const win = {
    matchMedia: (q) => (q.includes('reduced-motion') ? mq : { matches: false, addEventListener() {}, addListener() {} }),
    IntersectionObserver: IO, addEventListener() {}, document,
    fetch: () => Promise.resolve({ json: () => Promise.resolve(JSON.parse(cliDemo.textContent)) }),
    setInterval: (f) => { const id = nextTimer++; timers.set(id, f); return id; },
    clearInterval: (id) => timers.delete(id),
    setTimeout: () => 0, clearTimeout() {}, requestAnimationFrame: () => 0,
    localStorage: { getItem: () => null, setItem() {} }, location: { hash: '' }, navigator: { userAgent: '', platform: '' },
  };
  win.window = win;
  vm.runInNewContext(src, win);
  return { mq, listeners, observers, timers, termCode };
}

for (const rel of ['designs/pail-site/app.js', 'site/static/app.js']) {
  for (const legacy of [false, true]) {
    test(`${rel}: reduced-motion change is applied live (${legacy ? 'addListener' : 'addEventListener'})`, async () => {
      const p = load(rel, { legacyListener: legacy });
      await new Promise((r) => setImmediate(r)); // the prototype loads cli-demo.json first
      assert.equal(p.listeners.length, 1, 'a change listener is registered');
      // Make the versions demo visible: its interval starts.
      p.observers.forEach((o) => o.cb([{ isIntersecting: true, target: { classList: { add() {} } } }]));
      assert.ok(p.timers.size > 0, 'demo interval running while visible');
      // OS switches to reduce while the page is open: everything stops at once.
      p.mq.matches = true;
      p.listeners[0]({ matches: true });
      assert.equal(p.timers.size, 0, 'intervals cleared on switching to reduce');
      assert.match(p.termCode.innerHTML, /t-prompt/, 'terminal shows its static final state');
      // Switching back resumes by visibility (still in view).
      p.mq.matches = false;
      p.listeners[0]({ matches: false });
      assert.ok(p.timers.size > 0, 'demos resume when reduce is turned off');
    });
  }
}
