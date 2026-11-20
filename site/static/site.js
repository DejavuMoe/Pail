// pail.download: theme, language, menu, header, search palette and copy
// buttons. Every page loads it; the landing page adds home.js.
(function () {
  'use strict';
  var d = document.documentElement;
  var S = {};
  try { S = JSON.parse(document.getElementById('site-strings').textContent); } catch (e) {}
  var reduce = window.matchMedia('(prefers-reduced-motion: reduce)');
  var darkMQ = window.matchMedia('(prefers-color-scheme: dark)');
  function store(k, v) { try { localStorage.setItem(k, v); } catch (e) {} }
  function unhide(el) { if (el) el.hidden = false; }
  window.PailSite = { strings: S, reduce: reduce };

  // ---- language: once chosen, the English home page stops redirecting to /zh/
  var langLink = document.querySelector('[data-lang-switch]');
  if (langLink) {
    langLink.addEventListener('click', function () { store('pail-lang', langLink.getAttribute('data-lang-switch')); });
  }

  // ---- theme: System, Light, Dark
  var themeBtn = document.querySelector('[data-theme-toggle]');
  function applyTheme(pref) {
    var dark = pref === 'dark' || (pref === 'system' && darkMQ.matches);
    d.setAttribute('data-theme', dark ? 'dark' : 'light');
    d.setAttribute('data-theme-pref', pref);
    document.querySelectorAll('[data-dark-source]').forEach(function (s) {
      s.media = pref === 'system' ? '(prefers-color-scheme: dark)' : (pref === 'dark' ? 'all' : 'not all');
    });
    if (themeBtn) {
      var label = themeBtn.getAttribute('data-label-' + pref);
      themeBtn.setAttribute('aria-label', label);
      themeBtn.title = label;
    }
    document.dispatchEvent(new CustomEvent('pail:theme'));
  }
  var pref = d.getAttribute('data-theme-pref') || 'system';
  applyTheme(pref);
  if (themeBtn) {
    unhide(themeBtn);
    themeBtn.addEventListener('click', function () {
      pref = { system: 'light', light: 'dark', dark: 'system' }[pref] || 'system';
      store('pail-theme', pref);
      if (document.startViewTransition && !reduce.matches) {
        d.classList.add('is-theming');
        document.startViewTransition(function () { applyTheme(pref); }).finished.finally(function () { d.classList.remove('is-theming'); });
      }
      else applyTheme(pref);
    });
  }
  var onSystem = function () { if (pref === 'system') applyTheme('system'); };
  if (darkMQ.addEventListener) darkMQ.addEventListener('change', onSystem); else if (darkMQ.addListener) darkMQ.addListener(onSystem);

  // ---- menu for narrow screens
  var menuBtn = document.querySelector('.menu-btn');
  var nav = document.getElementById('site-nav');
  if (menuBtn && nav) {
    unhide(menuBtn);
    var setMenu = function (open) {
      menuBtn.setAttribute('aria-expanded', String(open));
      nav.classList.toggle('is-open', open);
    };
    menuBtn.addEventListener('click', function () { setMenu(menuBtn.getAttribute('aria-expanded') !== 'true'); });
    nav.addEventListener('click', function (e) { if (e.target.closest('a')) setMenu(false); });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && menuBtn.getAttribute('aria-expanded') === 'true') { setMenu(false); menuBtn.focus(); }
    });
  }

  // ---- header: a hairline once the page has scrolled
  var bar = document.querySelector('.bar');
  var onScroll = function () { if (bar) bar.classList.toggle('is-stuck', window.scrollY > 8); };
  window.addEventListener('scroll', onScroll, { passive: true });
  onScroll();

  // ---- shortcut labels
  var mac = /Mac|iPhone|iPad/.test((navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || '');
  document.querySelectorAll('kbd[data-mod]').forEach(function (k) { k.textContent = mac ? '⌘K' : 'Ctrl K'; });

  // ---- copy buttons on code blocks
  function addCopy(pre) {
    if (!navigator.clipboard) return;
    var wrap = document.createElement('div');
    wrap.className = 'copy-wrap';
    pre.parentNode.insertBefore(wrap, pre);
    wrap.appendChild(pre);
    var b = document.createElement('button');
    b.type = 'button';
    b.className = 'copy-btn';
    b.innerHTML = '<svg class="ic" aria-hidden="true"><use href="#i-copy"/></svg><span></span>';
    var label = b.querySelector('span');
    label.textContent = S.copyCode || 'Copy';
    b.addEventListener('click', function () {
      var text = pre.innerText.replace(/[ \t]+#[^\n]*$/gm, '').replace(/\n+$/, '');
      navigator.clipboard.writeText(text).then(function () {
        b.classList.add('is-done');
        label.textContent = S.copiedCode || 'Copied';
        setTimeout(function () { b.classList.remove('is-done'); label.textContent = S.copyCode || 'Copy'; }, 1600);
      });
    });
    wrap.appendChild(b);
  }
  document.querySelectorAll('.prose pre, pre.code').forEach(addCopy);

  // ---- docs: on narrow screens the page list folds behind one button
  var docsMenu = document.querySelector('.docs-menu');
  var docsGroups = document.getElementById('docs-groups');
  if (docsMenu && docsGroups) {
    unhide(docsMenu);
    var setDocs = function (open) {
      docsMenu.setAttribute('aria-expanded', String(open));
      docsGroups.classList.toggle('is-open', open);
    };
    docsMenu.addEventListener('click', function () { setDocs(docsMenu.getAttribute('aria-expanded') !== 'true'); });
    document.addEventListener('click', function (e) {
      if (docsGroups.classList.contains('is-open') && !e.target.closest('.docs-nav')) setDocs(false);
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && docsGroups.classList.contains('is-open')) { setDocs(false); docsMenu.focus(); }
    });
  }

  // ---- docs: the section on screen in "On this page"
  var toc = document.getElementById('toc');
  if (toc) {
    var links = [].slice.call(toc.querySelectorAll('a[href^="#"]'));
    var heads = links.map(function (a) { return document.getElementById(decodeURIComponent(a.getAttribute('href').slice(1))); });
    var ticking = false;
    var update = function () {
      ticking = false;
      var line = (bar ? bar.offsetHeight : 60) + 90, cur = 0;
      for (var i = 0; i < heads.length; i++) if (heads[i] && heads[i].getBoundingClientRect().top <= line) cur = i;
      links.forEach(function (a, i) { a.classList.toggle('is-current', i === cur); if (i === cur) a.setAttribute('aria-current', 'location'); else a.removeAttribute('aria-current'); });
    };
    window.addEventListener('scroll', function () { if (!ticking) { ticking = true; requestAnimationFrame(update); } }, { passive: true });
    window.addEventListener('resize', update);
    update();
  }

  // ---- search palette over the documentation
  var dialog = document.getElementById('palette');
  var openers = document.querySelectorAll('[data-palette-open]');
  if (!dialog || typeof dialog.showModal !== 'function') return;
  openers.forEach(function (b) { unhide(b); b.addEventListener('click', openPalette); });
  var input = document.getElementById('palette-input');
  var list = document.getElementById('palette-list');
  var index = null, results = [], active = 0;

  function load() {
    if (index) return Promise.resolve(index);
    return fetch(dialog.getAttribute('data-index')).then(function (r) { return r.json(); }).then(function (j) { index = j; return j; });
  }
  function openPalette() {
    if (dialog.open) return;
    input.value = '';
    dialog.showModal();
    load().then(render).catch(function () {});
  }
  document.addEventListener('keydown', function (e) {
    var t = e.target;
    var typing = t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName));
    if ((e.key === 'k' || e.key === 'K') && (e.metaKey || e.ctrlKey) && !e.altKey) { e.preventDefault(); openPalette(); }
    else if (e.key === '/' && !typing && !dialog.open) { e.preventDefault(); openPalette(); }
  });
  dialog.addEventListener('click', function (e) { if (e.target === dialog) dialog.close(); });

  function norm(s) { return (s || '').toLowerCase(); }
  function search(q) {
    var terms = norm(q).split(/\s+/).filter(Boolean);
    if (!terms.length) return index.filter(function (e) { return !e.s; });
    var out = [];
    index.forEach(function (e, i) {
      var title = norm(e.s || e.p), page = norm(e.p), hay = title + ' ' + page + ' ' + norm(e.u);
      var score = 0;
      for (var n = 0; n < terms.length; n++) {
        var at = hay.indexOf(terms[n]);
        if (at < 0) return;
        if (at === 0) score += 6; else if (at < title.length) score += 3; else score += 1;
        if (title.indexOf(terms[n]) === 0) score += 2;
      }
      if (!e.s) score += 1;
      out.push({ e: e, score: score, i: i });
    });
    out.sort(function (a, b) { return b.score - a.score || a.i - b.i; });
    return out.slice(0, 60).map(function (o) { return o.e; });
  }
  function mark(text, q) {
    var frag = document.createDocumentFragment();
    var t = norm(q).split(/\s+/).filter(Boolean)[0];
    var at = t ? norm(text).indexOf(t) : -1;
    if (at < 0) { frag.appendChild(document.createTextNode(text)); return frag; }
    var m = document.createElement('mark');
    m.textContent = text.slice(at, at + t.length);
    frag.appendChild(document.createTextNode(text.slice(0, at)));
    frag.appendChild(m);
    frag.appendChild(document.createTextNode(text.slice(at + t.length)));
    return frag;
  }
  function render() {
    if (!index) return;
    var q = input.value.trim();
    results = search(q);
    active = 0;
    list.textContent = '';
    if (!results.length) {
      var li = document.createElement('li');
      li.className = 'palette-empty';
      li.textContent = (S.searchEmpty || 'Nothing matches “{q}”.').replace('{q}', q);
      list.appendChild(li);
      return;
    }
    results.forEach(function (e, n) {
      var li = document.createElement('li');
      var a = document.createElement('a');
      a.href = e.u;
      a.innerHTML = '<svg class="ic" aria-hidden="true"><use href="#i-' + (e.s ? 'hash' : 'page') + '"/></svg><span><span class="ph-title"></span><span class="ph-path"></span></span>';
      a.querySelector('.ph-title').appendChild(mark(e.s || e.p, q));
      a.querySelector('.ph-path').textContent = e.s ? e.g + ' › ' + e.p : e.g + ' › ' + (S.searchPageHit || 'Page');
      a.addEventListener('mousemove', function () { if (active !== n) select(n, false); });
      a.addEventListener('click', function () { dialog.close(); });
      li.appendChild(a);
      list.appendChild(li);
    });
    select(0, false);
  }
  function select(n, scroll) {
    var items = list.querySelectorAll('a');
    if (!items.length) return;
    active = (n + items.length) % items.length;
    items.forEach(function (a, i) { a.classList.toggle('is-active', i === active); });
    if (scroll) items[active].scrollIntoView({ block: 'nearest' });
  }
  function go(n) {
    var e = results[n];
    if (!e) return;
    dialog.close();
    location.href = e.u;
  }
  input.addEventListener('input', render);
  input.addEventListener('keydown', function (e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); select(active + 1, true); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); select(active - 1, true); }
    else if (e.key === 'Enter') { e.preventDefault(); go(active); }
  });
})();
