(function () {
  'use strict';

  var root = document.documentElement;
  root.classList.add('js');

  var dict = window.PAIL_I18N || null;
  var reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
  var darkMQ = window.matchMedia('(prefers-color-scheme: dark)');

  function $(sel, ctx) { return (ctx || document).querySelector(sel); }
  function $$(sel, ctx) { return Array.prototype.slice.call((ctx || document).querySelectorAll(sel)); }
  function store(key, value) { try { localStorage.setItem(key, value); } catch (e) {} }
  function read(key) { try { return localStorage.getItem(key); } catch (e) { return null; } }
  function onChange(mq, fn) { if (mq.addEventListener) mq.addEventListener('change', fn); else mq.addListener(fn); }
  function lang() { return root.getAttribute('data-lang') === 'zh' ? 'zh' : 'en'; }
  function t(key, vars) {
    var s = (dict && ((dict[lang()] && dict[lang()][key]) || dict.en[key])) || key;
    if (vars) Object.keys(vars).forEach(function (k) { s = s.replace('{' + k + '}', vars[k]); });
    return s;
  }
  function humanSize(n) {
    if (n < 1024) return n + ' B';
    var units = ['KB', 'MB', 'GB', 'TB'], i = -1;
    do { n /= 1024; i++; } while (n >= 1024 && i < units.length - 1);
    return n.toFixed(1) + ' ' + units[i];
  }

  /* ---------- Platform ---------- */
  var platform = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || '';
  var ua = navigator.userAgent || '';
  var OS = (function () {
    if (/Android|iPhone|iPad|iPod/i.test(ua) || (/Mac/i.test(platform) && navigator.maxTouchPoints > 1)) return null;
    if (/Win/i.test(platform)) return 'windows';
    if (/Mac/i.test(platform)) return 'macos';
    if (/Linux|X11|CrOS/i.test(platform) || /Linux/i.test(ua)) return 'linux';
    return null;
  })();
  var OS_NAMES = { windows: 'Windows', macos: 'macOS', linux: 'Linux' };

  // The visitor's platform moves to the top of the download list and gets the primary button.
  if (OS) {
    var row = $('.dl-row[data-os="' + OS + '"]');
    if (row) {
      row.classList.add('is-current');
      row.parentNode.insertBefore(row, row.parentNode.firstElementChild);
    }
  }

  /* ---------- Download source ---------- */
  var dl = $('.dl');
  var sourceRadios = $$('input[name="source"]');
  function applySource(source) {
    if (!dl) return;
    var base = dl.getAttribute(source === 'mirror' ? 'data-mirror-base' : 'data-github-base');
    $$('[data-asset]', dl).forEach(function (a) { a.href = base + a.getAttribute('data-asset'); });
    sourceRadios.forEach(function (r) { r.checked = r.value === source; });
    var note = $('[data-source-note]', dl);
    if (note) {
      note.setAttribute('data-i18n', source === 'mirror' ? 'sourceMirrorNote' : 'sourceGithubNote');
      note.textContent = t(note.getAttribute('data-i18n'));
    }
  }
  if (dl && !dl.hasAttribute('data-mirror-base')) {
    // This version is not on the mirror yet: offer GitHub only.
    $$('.source-seg, [data-source-note], .source-fallback', dl).forEach(function (el) { el.hidden = true; });
    var head = $('.dl-source', dl);
    if (head) head.hidden = true;
  } else if (dl) {
    sourceRadios.forEach(function (r) {
      r.addEventListener('change', function () {
        if (!r.checked) return;
        store('pail-source', r.value);
        applySource(r.value);
      });
    });
    applySource(read('pail-source') === 'mirror' ? 'mirror' : 'github');
  }

  /* ---------- i18n ---------- */
  function applyLang() {
    if (!dict) return;
    $$('[data-i18n]').forEach(function (el) {
      var vars = el.getAttribute('data-i18n-vars');
      el.textContent = t(el.getAttribute('data-i18n'), vars ? JSON.parse(vars) : null);
      if (el.hasAttribute('data-label')) el.setAttribute('data-label', el.textContent);
    });
    $$('[data-i18n-aria-label], [data-i18n-title], [data-i18n-alt], [data-i18n-content], [data-i18n-href]').forEach(function (el) {
      ['aria-label', 'title', 'alt', 'content', 'href'].forEach(function (attr) {
        var key = el.getAttribute('data-i18n-' + attr);
        if (key) el.setAttribute(attr, t(key));
      });
    });
    // The window screenshots are in the page's language, light and dark.
    var shotLang = root.getAttribute('data-lang') === 'zh' ? 'zh' : 'en';
    $$('[data-shot]').forEach(function (el) {
      var file = 'assets/main-' + el.getAttribute('data-shot') + '-' + shotLang + '.webp';
      if (el.tagName === 'SOURCE') el.srcset = file; else el.src = file;
    });
    var cta = $('[data-os-cta-label]');
    if (cta) cta.textContent = OS ? t('heroDownloadFor', { os: OS_NAMES[OS] }) : t('heroDownload');
  }
  function setLang(next) {
    store('pail-lang', next);
    root.setAttribute('data-lang', next);
    root.lang = next === 'zh' ? 'zh-Hans' : 'en';
    applyLang();
  }

  /* ---------- Theme ---------- */
  var themeRadios = $$('input[name="theme"]');
  var darkShot = $('[data-shot-dark]');
  function resolve(pref) { return pref === 'dark' || (pref === 'system' && darkMQ.matches) ? 'dark' : 'light'; }
  function applyTheme(pref) {
    root.setAttribute('data-theme-pref', pref);
    root.setAttribute('data-theme', resolve(pref));
    themeRadios.forEach(function (r) { r.checked = r.value === pref; });
    if (darkShot) darkShot.media = pref === 'system' ? '(prefers-color-scheme: dark)' : pref === 'dark' ? 'all' : 'not all';
    $$('meta[name="theme-color"]').forEach(function (m) {
      var dark = /dark/.test(m.getAttribute('media'));
      m.setAttribute('content', pref === 'system' ? (dark ? '#151513' : '#FBFAF8') : (resolve(pref) === 'dark' ? '#151513' : '#FBFAF8'));
    });
  }
  themeRadios.forEach(function (r) {
    r.addEventListener('change', function () {
      if (!r.checked) return;
      store('pail-theme', r.value);
      applyTheme(r.value);
    });
  });
  onChange(darkMQ, function () {
    if (root.getAttribute('data-theme-pref') === 'system') applyTheme('system');
  });

  /* ---------- Header, current section, reveal ---------- */
  var header = $('.site-header');
  function onScroll() { if (header) header.classList.toggle('is-scrolled', window.scrollY > 8); }
  window.addEventListener('scroll', onScroll, { passive: true });
  onScroll();

  var hasIO = 'IntersectionObserver' in window;
  var navLinks = $$('.nav a[href^="#"], .mobile-nav a[href^="#"]');
  if (hasIO && navLinks.length) {
    var sectionObserver = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (!en.isIntersecting) return;
        navLinks.forEach(function (a) {
          var on = a.getAttribute('href') === '#' + en.target.id;
          a.classList.toggle('is-current', on);
          if (on) a.setAttribute('aria-current', 'location'); else a.removeAttribute('aria-current');
        });
      });
    }, { rootMargin: '-45% 0px -50% 0px' });
    $$('main section[id]').forEach(function (s) { sectionObserver.observe(s); });
  }

  var reveals = $$('.reveal');
  if (hasIO) {
    var revealObserver = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (en.isIntersecting) { en.target.classList.add('is-in'); revealObserver.unobserve(en.target); }
      });
    }, { threshold: 0.12 });
    reveals.forEach(function (el) {
      var siblings = $$(':scope > .reveal', el.parentElement);
      el.style.transitionDelay = (Math.max(0, siblings.indexOf(el)) % 3) * 60 + 'ms';
      revealObserver.observe(el);
    });
  } else {
    reveals.forEach(function (el) { el.classList.add('is-in'); });
  }

  /* ---------- Demos (run only while visible) ---------- */
  var motionSync = []; // re-evaluate every demo when the reduced-motion preference changes
  function whenVisible(el, start, stop) {
    if (!el || !hasIO) return;
    var inView = false;
    function sync() { if (inView && !reduced.matches) start(); else stop(); }
    motionSync.push(sync);
    new IntersectionObserver(function (entries) {
      inView = entries[entries.length - 1].isIntersecting;
      sync();
    }).observe(el);
  }
  function loop(el, fn, interval) {
    var timer = null;
    whenVisible(el, function () { if (!timer) { fn(); timer = setInterval(fn, interval); } },
      function () { if (timer) { clearInterval(timer); timer = null; } });
  }

  // Transfers: progress is time-based with a gentle speed ripple; the fill is scaled, never resized.
  (function () {
    var box = $('.mini-transfers');
    if (!box) return;
    var DURATIONS = [9000, 16000];
    var rows = $$('.tq-row[data-total]', box).map(function (row, i) {
      return {
        total: +row.getAttribute('data-total'), p: i ? 0.66 : 0.64, dur: DURATIONS[i] || 16000,
        phase: i * 1.7, state: 'run', until: 0, lastText: -1e9,
        fill: $('.tq-bar i', row), bytes: $('.tq-bytes', row)
      };
    });
    function paint(r, now, force) {
      r.fill.style.transform = 'scaleX(' + r.p.toFixed(4) + ')';
      if (!force && now - r.lastText < 160) return;
      r.lastText = now;
      r.bytes.textContent = t('miniBytesOf', { done: humanSize(Math.round(r.total * r.p)), total: humanSize(r.total) });
    }
    rows.forEach(function (r) { paint(r, 0, true); });
    var raf = 0, last = 0, visible = false;
    function frame(now) {
      var dt = last ? Math.min(100, now - last) : 0;
      last = now;
      rows.forEach(function (r) {
        if (r.state === 'run') {
          r.p = Math.min(1, r.p + (dt / r.dur) * (1 + 0.2 * Math.sin(now / 1900 + r.phase)));
          paint(r, now);
          if (r.p >= 1) { r.state = 'hold'; r.until = now + 1200; paint(r, now, true); }
        } else if (r.state === 'hold' && now >= r.until) {
          r.state = 'fade'; r.until = now + 320; r.fill.classList.add('is-fading');
        } else if (r.state === 'fade' && now >= r.until) {
          r.p = 0; paint(r, now, true); r.state = 'back'; r.until = now + 60;
        } else if (r.state === 'back' && now >= r.until) {
          r.fill.classList.remove('is-fading'); r.state = 'run';
        }
      });
      raf = visible && !document.hidden ? requestAnimationFrame(frame) : 0;
      if (!raf) last = 0;
    }
    function start() { if (!raf && visible && !document.hidden) raf = requestAnimationFrame(frame); }
    whenVisible(box, function () { visible = true; start(); }, function () { visible = false; });
    document.addEventListener('visibilitychange', start);

    var chips = $$('[data-cycle] .chip', box), k = 0;
    loop(box, function () {
      chips[k].classList.remove('is-on');
      k = (k + 1) % chips.length;
      chips[k].classList.add('is-on');
    }, 2200);
  })();

  // Versions
  (function () {
    var box = $('.mini-versions');
    if (box) loop(box, function () { box.classList.toggle('is-restored'); }, 2600);
  })();

  /* ---------- Terminal ---------- */
  // The examples are the CLI's own output, recorded by TestSiteCLIDemo in
  // cmd/pail (site/cli-demo.json); nothing here is typed by hand.
  var TERM = {};
  function termKind(l) {
    if (l.kind === 'cmd') return 'cmd';
    if (l.kind === 'err') return 'dim';
    if (/^(upload|download)\s/.test(l.text)) return 'up';
    if (/^delete\s/.test(l.text)) return 'del';
    if (/^dry run:/.test(l.text)) return 'dim';
    return 'out';
  }
  function setDemo(data) {
    data.examples.forEach(function (e) {
      TERM[e.id] = e.lines.map(function (l) { return [termKind(l), l.text]; });
    });
  }
  var term = $('[data-demo="terminal"]');
  var termCode = term ? $('.term-body code', term) : null;
  var termTabs = term ? $$('[data-term]', term) : [];
  var termTimer = null, termName = null;
  function esc(s) { return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); }
  function lineHTML(kind, text) {
    var wrap = /\S {2,}\S/.test(text) ? '' : ' t-wrap'; // aligned columns scroll, other lines wrap
    if (kind === 'cmd') return '<span class="t-prompt">$</span> <span class="t-cmd' + wrap + '">' + esc(text) + '</span>';
    var cls = { out: 't-out', up: 't-ok', del: 't-del', dim: 't-dim' }[kind] || 't-out';
    return '<span class="' + cls + wrap + '">' + esc(text) + '</span>';
  }
  var PROMPT = '\n<span class="t-prompt">$</span> <span class="t-caret"></span>';
  function renderTerminal(name, animate) {
    if (!termCode) return;
    clearTimeout(termTimer);
    var lines = TERM[name];
    if (lines) termName = name;
    if (!animate || reduced.matches) {
      termCode.innerHTML = lines.map(function (l) { return lineHTML(l[0], l[1]); }).join('\n') + PROMPT;
      return;
    }
    var cmd = lines[0][1], n = 0;
    (function type() {
      n++;
      termCode.innerHTML = '<span class="t-prompt">$</span> <span class="t-cmd t-wrap">' + esc(cmd.slice(0, n)) + '</span><span class="t-caret"></span>';
      if (n < cmd.length) { termTimer = setTimeout(type, 18 + Math.random() * 30); return; }
      var shown = 1;
      (function out() {
        shown++;
        termCode.innerHTML = lines.slice(0, shown).map(function (l) { return lineHTML(l[0], l[1]); }).join('\n') +
          (shown >= lines.length ? PROMPT : '');
        if (shown < lines.length) termTimer = setTimeout(out, 70);
      })();
    })();
  }
  termTabs.forEach(function (tab, i) {
    tab.addEventListener('click', function () {
      termTabs.forEach(function (x) { x.setAttribute('aria-selected', String(x === tab)); x.tabIndex = x === tab ? 0 : -1; });
      $('#term-panel').setAttribute('aria-labelledby', tab.id);
      renderTerminal(tab.getAttribute('data-term'), true);
    });
    tab.addEventListener('keydown', function (e) {
      var d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
      if (!d) return;
      var next = termTabs[(i + d + termTabs.length) % termTabs.length];
      next.focus();
      next.click();
    });
    tab.tabIndex = i ? -1 : 0;
  });
  // The prototype is served as plain files, so it reads the recording next to
  // it; the production page embeds the same file in its HTML.
  if (term && window.fetch) {
    fetch('cli-demo.json').then(function (r) { return r.json(); }).then(function (data) {
      setDemo(data);
      renderTerminal('ls', false);
      if (hasIO) {
        var termIO = new IntersectionObserver(function (entries) {
          if (entries[0].isIntersecting) { termIO.disconnect(); renderTerminal('ls', true); }
        }, { threshold: 0.4 });
        termIO.observe(term);
      }
    }).catch(function () { /* the first example stays as written in the HTML */ });
  }

  // The preference can change while the page is open: stop what runs and show the
  // finished terminal at once, or let the demos resume by their normal visibility.
  function motionChanged() {
    motionSync.forEach(function (sync) { sync(); });
    if (reduced.matches && termName) renderTerminal(termName, false);
  }
  if (reduced.addEventListener) reduced.addEventListener('change', motionChanged);
  else if (reduced.addListener) reduced.addListener(motionChanged); // Safari < 14

  /* ---------- Mobile navigation ---------- */
  var menuBtn = $('.menu-btn');
  var mobileNav = $('#mobile-nav');
  function setMenu(open, restoreFocus) {
    mobileNav.hidden = !open;
    menuBtn.setAttribute('aria-expanded', String(open));
    if (!open && restoreFocus) menuBtn.focus();
  }
  if (menuBtn && mobileNav) {
    menuBtn.addEventListener('click', function () { setMenu(mobileNav.hidden, false); });
    mobileNav.addEventListener('click', function (e) { if (e.target.closest('a')) setMenu(false, false); });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && !mobileNav.hidden) { e.preventDefault(); setMenu(false, true); }
    });
    var wideMQ = window.matchMedia('(min-width: 961px)');
    onChange(wideMQ, function () { if (wideMQ.matches) setMenu(false, false); });
  }

  /* ---------- Language ---------- */
  document.addEventListener('click', function (e) {
    var trigger = e.target.closest('[data-action="lang"]');
    if (!trigger) return;
    if (trigger.tagName === 'A') {
      // Docs pages: the control links to the same page in the other language.
      store('pail-lang', trigger.getAttribute('data-lang-target') === 'zh' ? 'zh' : 'en');
      if (location.hash) trigger.setAttribute('href', trigger.getAttribute('href').split('#')[0] + location.hash);
      return;
    }
    setLang(lang() === 'zh' ? 'en' : 'zh');
  });

  applyTheme(root.getAttribute('data-theme-pref') || 'system');
  applyLang();
})();
