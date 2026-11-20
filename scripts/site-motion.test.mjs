// The landing page's moving parts must follow the reduced-motion preference
// live, not only when an IntersectionObserver fires. home.js runs here against
// a minimal DOM stub; only the terminal is present.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');

class Node {
  constructor(tag, text) { this.tagName = tag; this.children = []; this.parentNode = null; this.text = text ?? ''; this.attrs = {}; this.className = ''; this.style = {}; }
  get classList() { const n = this; return { add(c) { n.className += ' ' + c; }, remove() {}, toggle() {}, contains: () => false }; }
  get lastChild() { return this.children[this.children.length - 1] || null; }
  appendChild(c) { if (c.parentNode) c.parentNode.removeChild(c); c.parentNode = this; this.children.push(c); return c; }
  removeChild(c) { this.children = this.children.filter((x) => x !== c); c.parentNode = null; return c; }
  get textContent() { return this.tagName === '#text' ? this.text : this.text + this.children.map((c) => c.textContent).join(''); }
  set textContent(v) { this.children.forEach((c) => { c.parentNode = null; }); this.children = []; this.text = String(v); }
  // Markup with classes, enough to tell a prompt and an output line apart.
  get html() { return this.tagName === '#text' ? this.text : `<${this.tagName} class="${this.className}">${this.text}${this.children.map((c) => c.html).join('')}</${this.tagName}>`; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  addEventListener() {}
  querySelector() { return null; }
  querySelectorAll() { return []; }
  focus() {}
}

function load({ legacyListener = false } = {}) {
  const src = fs.readFileSync(path.join(root, 'site/static/home.js'), 'utf8');
  const listeners = [];
  const mq = {
    matches: false,
    addEventListener: legacyListener ? undefined : (_, f) => listeners.push(f),
    addListener: legacyListener ? (f) => listeners.push(f) : undefined,
  };
  const observers = [];
  class IO { constructor(cb) { this.cb = cb; observers.push(this); } observe(el) { this.el = el; } unobserve() {} disconnect() {} }

  const code = new Node('code');
  const out = new Node('pre');
  out.querySelector = (s) => (s === 'code' ? code : null);
  const tab = new Node('button');
  tab.id = 'tab-ls';
  const term = new Node('div');
  term.querySelectorAll = (s) => (s === '[data-example]' ? [tab] : []);
  const demo = new Node('script');
  demo.text = JSON.stringify({ examples: [{ id: 'ls', command: 'pail ls', lines: [{ kind: 'cmd', text: 'pail ls' }, { kind: 'out', text: 'a' }] }] });
  const document = {
    documentElement: new Node('html'), hidden: false,
    querySelector: (s) => (s === '[data-term]' ? term : null),
    querySelectorAll: () => [],
    getElementById: (id) => ({ 'cli-demo': demo, 'term-out': out }[id] || null),
    createElement: (tag) => new Node(tag),
    createTextNode: (t) => new Node('#text', t),
    addEventListener() {},
  };
  const win = {
    matchMedia: (q) => (q.includes('reduced-motion') ? mq : { matches: false, addEventListener() {}, addListener() {} }),
    IntersectionObserver: IO, addEventListener() {}, document, getComputedStyle: () => ({ getPropertyValue: () => '' }),
    setInterval: () => 0, clearInterval() {}, setTimeout: () => 0, clearTimeout() {},
    requestAnimationFrame: () => 0, cancelAnimationFrame() {}, performance: { now: () => 0 },
    localStorage: { getItem: () => null, setItem() {} }, navigator: { userAgent: '', platform: '' },
  };
  win.window = win;
  vm.runInNewContext(src, win);
  return { mq, listeners, observers, term, code };
}

for (const legacy of [false, true]) {
  test(`home.js: reduced-motion change is applied live (${legacy ? 'addListener' : 'addEventListener'})`, () => {
    const p = load({ legacyListener: legacy });
    assert.ok(p.listeners.length >= 1, 'a change listener is registered');
    // The terminal comes into view and starts typing; setTimeout is stubbed, so
    // it stays before the first character of the command.
    const io = p.observers.find((o) => o.el === p.term);
    assert.ok(io, 'the terminal is observed');
    io.cb([{ isIntersecting: true }]);
    assert.doesNotMatch(p.code.html, />a</, 'the output is not shown while the command is typed');
    // The OS switches to reduced motion while the page is open: the finished example shows at once.
    p.mq.matches = true;
    p.listeners.forEach((f) => f({ matches: true }));
    assert.match(p.code.html, /t-prompt/, 'terminal shows its static final state');
    assert.match(p.code.html, /pail ls/, 'with the command');
    assert.match(p.code.html, />a</, 'and its output');
  });
}
