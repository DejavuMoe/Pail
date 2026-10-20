(function () {
  'use strict';

  var root = document.documentElement;
  root.classList.add('js');

  var reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
  var darkMQ = window.matchMedia('(prefers-color-scheme: dark)');
  var GH = 'https://github.com/DejavuMoe/Pail';

  function $(sel, ctx) { return (ctx || document).querySelector(sel); }
  function $$(sel, ctx) { return Array.prototype.slice.call((ctx || document).querySelectorAll(sel)); }
  function store(key, value) { try { localStorage.setItem(key, value); } catch (e) {} }

  // Page text is rendered per language; only strings the script builds at
  // runtime are embedded, for this page's language.
  var dict = {};
  try { dict = JSON.parse($('#pail-strings').textContent); } catch (e) {}
  function lang() { return root.getAttribute('data-lang') === 'zh' ? 'zh' : 'en'; }
  function t(key, vars) {
    var s = dict[key] || key;
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
  var isMac = /Mac|iPhone|iPad/i.test(platform);
  var OS = (function () {
    if (/Android|iPhone|iPad|iPod/i.test(ua)) return null;
    if (/Win/i.test(platform)) return 'windows';
    if (/Mac/i.test(platform)) return 'macos';
    if (/Linux|X11|CrOS/i.test(platform) || /Linux/i.test(ua)) return 'linux';
    return null;
  })();
  var OS_NAMES = { windows: 'Windows', macos: 'macOS', linux: 'Linux' };

  if (OS) {
    var card = $('.os-card[data-os="' + OS + '"]');
    if (card) card.classList.add('is-current');
  }

  /* ---------- Language ---------- */
  $$('[data-mod]').forEach(function (k) { k.textContent = isMac ? '⌘' : 'Ctrl'; });
  var ctaLabel = $('[data-os-cta-label]');
  if (ctaLabel && OS) ctaLabel.textContent = t('heroDownloadFor', { os: OS_NAMES[OS] });

  // The language control is a plain link to the other page; remember the
  // choice so the English page does not redirect a Chinese browser back.
  var langLink = $('[data-lang-target]');
  function rememberLang() { if (langLink) store('pail-lang', langLink.getAttribute('data-lang-target')); }
  function switchLang() {
    if (!langLink) return;
    rememberLang();
    location.href = langLink.getAttribute('href') + location.hash;
  }

  /* ---------- Theme ---------- */
  var themeRadios = $$('.theme-seg input[name="theme"]');
  function resolve(pref) { return pref === 'dark' || (pref === 'system' && darkMQ.matches) ? 'dark' : 'light'; }
  function applyTheme(pref) {
    root.setAttribute('data-theme-pref', pref);
    root.setAttribute('data-theme', resolve(pref));
    themeRadios.forEach(function (r) { r.checked = r.value === pref; });
    var bg = getComputedStyle(root).getPropertyValue('--bg').trim();
    $$('meta[name="theme-color"]').forEach(function (m) {
      if (pref === 'system') m.setAttribute('content', /dark/.test(m.getAttribute('media')) ? '#151513' : '#FBFAF8');
      else m.setAttribute('content', bg);
    });
    keys.recolor();
  }
  function setTheme(pref, origin) {
    store('pail-theme', pref);
    var changes = resolve(pref) !== root.getAttribute('data-theme');
    if (!changes || !document.startViewTransition || reduced.matches) { applyTheme(pref); return; }
    var x = origin ? origin.x : window.innerWidth - 120;
    var y = origin ? origin.y : 32;
    var r = Math.hypot(Math.max(x, window.innerWidth - x), Math.max(y, window.innerHeight - y));
    root.classList.add('vt-theme');
    var tr = document.startViewTransition(function () { applyTheme(pref); });
    tr.ready.then(function () {
      root.animate(
        { clipPath: ['circle(0px at ' + x + 'px ' + y + 'px)', 'circle(' + r + 'px at ' + x + 'px ' + y + 'px)'] },
        { duration: 700, easing: 'cubic-bezier(.2,.7,.1,1)', pseudoElement: '::view-transition-new(root)' }
      );
    });
    tr.finished.then(function () { root.classList.remove('vt-theme'); });
  }
  // Native radios: arrow keys and labels work without extra handlers.
  themeRadios.forEach(function (r) {
    r.addEventListener('change', function () {
      if (!r.checked) return;
      var rect = r.parentElement.getBoundingClientRect();
      setTheme(r.value, { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 });
    });
  });
  darkMQ.addEventListener('change', function () {
    if (root.getAttribute('data-theme-pref') === 'system') applyTheme('system');
  });

  /* ---------- Header, nav, reveal, tilt ---------- */
  // The hero window eases toward a scroll-derived target, so the tilt follows
  // the page with a little inertia instead of snapping per scroll event.
  var header = $('.site-header');
  var win = $('[data-tilt]');
  var tilt = { cur: null, target: 0, raf: 0 };
  function tiltTarget() {
    var rect = win.getBoundingClientRect();
    var vh = window.innerHeight;
    var p = Math.min(1, Math.max(0, (vh - rect.top) / (vh * 0.95)));
    return 1 - p;
  }
  function tiltFrame() {
    tilt.cur += (tilt.target - tilt.cur) * 0.14;
    if (Math.abs(tilt.target - tilt.cur) < 0.001) tilt.cur = tilt.target;
    win.style.setProperty('--tilt', (tilt.cur * 14).toFixed(2) + 'deg');
    win.style.setProperty('--scale', (1 - tilt.cur * 0.05).toFixed(4));
    tilt.raf = tilt.cur === tilt.target ? 0 : requestAnimationFrame(tiltFrame);
  }
  function onScroll() {
    header.classList.toggle('is-scrolled', window.scrollY > 8);
    if (!win || reduced.matches) return;
    tilt.target = tiltTarget();
    if (tilt.cur === null) tilt.cur = tilt.target;
    if (!tilt.raf) tilt.raf = requestAnimationFrame(tiltFrame);
  }
  window.addEventListener('scroll', onScroll, { passive: true });
  window.addEventListener('resize', onScroll);
  onScroll();

  var navLinks = $$('.nav a[href^="#"]');
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

  var revealObserver = new IntersectionObserver(function (entries) {
    entries.forEach(function (en) {
      if (en.isIntersecting) { en.target.classList.add('is-in'); revealObserver.unobserve(en.target); }
    });
  }, { threshold: 0.12 });
  $$('.reveal').forEach(function (el) {
    var siblings = el.parentElement ? $$(':scope > .reveal', el.parentElement) : [el];
    el.style.setProperty('--i', String(Math.max(0, siblings.indexOf(el)) % 4));
    revealObserver.observe(el);
  });

  /* ---------- Demo loops (run only while visible) ---------- */
  function loop(el, fn, interval) {
    var timer = null;
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (en.isIntersecting && !reduced.matches) { if (!timer) { fn(); timer = setInterval(fn, interval); } }
        else if (timer) { clearInterval(timer); timer = null; }
      });
    });
    if (el) io.observe(el);
  }

  // Transfers
  (function () {
    var tile = $('.mini-transfers');
    if (!tile) return;
    var rows = $$('.tq-row[data-total]', tile).map(function (row, i) {
      return { row: row, total: +row.getAttribute('data-total'), p: i ? 0.66 : 0.64, v: i ? 0.006 : 0.011, bytes: $('.tq-bytes', row) };
    });
    function paint(r) {
      r.row.style.setProperty('--p', r.p.toFixed(3));
      r.bytes.textContent = humanSize(Math.round(r.total * r.p)) + ' / ' + humanSize(r.total);
    }
    rows.forEach(paint);
    loop(tile, function () {
      rows.forEach(function (r) {
        r.p += r.v * (0.6 + Math.random() * 0.8);
        if (r.p >= 1) r.p = 0.04;
        paint(r);
      });
    }, 120);
    var chips = $$('[data-cycle] .chip', tile), k = 0;
    loop(tile, function () {
      chips[k].classList.remove('is-on');
      k = (k + 1) % chips.length;
      chips[k].classList.add('is-on');
    }, 2200);
  })();

  // Palette demo
  var paletteDemo = (function () {
    var box = $('.mini-palette');
    var state = { restart: function () {} };
    if (!box) return state;
    var input = $('.mp-input', box), query = $('.mp-query', box), items = $$('.mp-list li', box);
    var queries = { en: ['upl', 'settings', 'key'], zh: ['上传', '设置', '复制'] };
    var timer = null, qi = 0, step = 0;
    function filter(q) {
      var first = true;
      items.forEach(function (li) {
        var hay = (li.textContent + ' ' + li.getAttribute('data-k')).toLowerCase();
        var match = !q || hay.indexOf(q.toLowerCase()) !== -1;
        li.classList.toggle('is-hidden', !match);
        li.classList.toggle('is-active', match && first && !!q);
        if (match && q) first = false;
      });
      if (!q) items[0].classList.add('is-active');
      input.classList.toggle('has-query', !!q);
    }
    function tick() {
      var list = queries[lang()];
      var q = list[qi % list.length];
      var chars = Array.from(q);
      step++;
      if (step <= chars.length) { query.textContent = chars.slice(0, step).join(''); }
      else if (step > chars.length + 16) { step = 0; qi++; query.textContent = ''; }
      filter(query.textContent);
    }
    filter('');
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (en.isIntersecting && !reduced.matches) { if (!timer) timer = setInterval(tick, 140); }
        else if (timer) { clearInterval(timer); timer = null; }
      });
    });
    io.observe(box);
    state.restart = function () { step = 0; qi = 0; query.textContent = ''; filter(''); };
    return state;
  })();

  // Bucket settings
  (function () {
    var chips = $$('.setting-chips li:not(.is-unsupported)');
    var k = 0;
    loop($('.mini-settings'), function () {
      chips.forEach(function (c) { c.classList.remove('is-lit'); });
      chips[k % chips.length].classList.add('is-lit');
      k += 5;
    }, 1100);
  })();

  // Versions
  (function () {
    var box = $('.mini-versions');
    loop(box, function () { box.classList.toggle('is-restored'); }, 2600);
  })();

  // Presigned links
  (function () {
    var box = $('.mini-links');
    if (!box) return;
    var segs = $$('.ml-seg span', box), text = $('.ml-text', box), copy = $('.ml-copy', box);
    var secs = [900, 3600, 86400, 604800], k = 1;
    loop(box, function () {
      segs[k].classList.remove('is-on');
      k = (k + 1) % segs.length;
      segs[k].classList.add('is-on');
      text.textContent = 'https://acme-assets.s3.us-east-1.amazonaws.com/reports/q3.csv?X-Amz-Expires=' + secs[k] + '&X-Amz-Signature=…';
      copy.classList.add('is-copied');
      copy.lastElementChild.textContent = t('miniCopied');
      setTimeout(function () { copy.classList.remove('is-copied'); copy.lastElementChild.textContent = t('miniCopy'); }, 1200);
    }, 2400);
  })();

  // Delete-bucket dialog
  (function () {
    var box = $('.dialog-demo');
    if (!box) return;
    var typed = $('.dlg-typed', box), name = 'acme-logs', i = 0, hold = 0;
    loop(box, function () {
      if (i < name.length) { i++; typed.textContent = name.slice(0, i); box.classList.toggle('is-armed', i === name.length); return; }
      if (++hold > 14) { i = 0; hold = 0; typed.textContent = ''; box.classList.remove('is-armed'); }
    }, 150);
  })();

  /* ---------- Terminal ---------- */
  var TERM = {
    ls: [
      ['cmd', 'pail ls s3://acme-assets/'],
      ['out', '                                PRE assets/'],
      ['out', '                                PRE backups/'],
      ['out', '                                PRE logs/'],
      ['out', '                                PRE reports/'],
      ['out', '                                PRE website/'],
      ['out', '2026-10-19 14:51:08        303 B  manifest.json'],
      ['out', '2026-10-19 14:51:08        445 B  README.txt']
    ],
    sync: [
      ['cmd', 'pail sync ./photos s3://acme-media/photos/ --mode mirror --dry-run'],
      ['up', 'upload   2026/04/IMG_2231.heic  (3.2 MB)  new'],
      ['up', 'upload   2026/04/cover.jpg  (2.9 MB)  size differs'],
      ['del', 'delete   tmp/draft.png  (1.1 MB)  not in the source'],
      ['out', 'plan: 2 uploads, 0 downloads, 1 deletes, 340 skipped, 6.1 MB to transfer'],
      ['dim', 'dry run: nothing was changed']
    ],
    presign: [
      ['cmd', 'pail presign s3://acme-assets/reports/q3.csv --expires 1h'],
      ['out', 'https://acme-assets.s3.us-east-1.amazonaws.com/reports/q3.csv?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=…&X-Amz-Date=20261019T145108Z&X-Amz-Expires=3600&X-Amz-SignedHeaders=host&X-Amz-Signature=…']
    ],
    json: [
      ['cmd', 'pail ls s3://acme-assets/reports/ --json'],
      ['out', '{"etag":"\\"9b2cf535f27731c974343645a3985328\\"","key":"reports/q3.csv","modified":"2026-10-19T14:51:08Z","size":18742,"storageClass":"STANDARD","type":"object"}'],
      ['out', '{"etag":"\\"1f3870be274f6c49b3e31a0c6728957f\\"","key":"reports/q2.csv","modified":"2026-07-02T09:12:40Z","size":17311,"storageClass":"STANDARD_IA","type":"object"}']
    ]
  };
  var term = $('[data-demo="terminal"]');
  var termCode = term ? $('.term-body code', term) : null;
  var termTabs = term ? $$('[data-term]', term) : [];
  var termTimer = null;
  function esc(s) { return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); }
  function lineHTML(kind, text) {
    if (kind === 'cmd') return '<span class="t-prompt">$</span> <span class="t-cmd">' + esc(text) + '</span>';
    var cls = { out: 't-out', up: 't-ok', del: 't-del', dim: 't-dim' }[kind] || 't-out';
    return '<span class="' + cls + '">' + esc(text) + '</span>';
  }
  function renderTerminal(name, animate) {
    if (!termCode) return;
    clearTimeout(termTimer);
    var lines = TERM[name];
    if (!animate || reduced.matches) {
      termCode.innerHTML = lines.map(function (l) { return lineHTML(l[0], l[1]); }).join('\n') + '\n<span class="t-prompt">$</span> <span class="t-caret"></span>';
      return;
    }
    var cmd = lines[0][1], n = 0;
    (function type() {
      n++;
      termCode.innerHTML = '<span class="t-prompt">$</span> <span class="t-cmd">' + esc(cmd.slice(0, n)) + '</span><span class="t-caret"></span>';
      if (n < cmd.length) { termTimer = setTimeout(type, 18 + Math.random() * 30); return; }
      var shown = 1;
      (function out() {
        shown++;
        termCode.innerHTML = lines.slice(0, shown).map(function (l) { return lineHTML(l[0], l[1]); }).join('\n') +
          (shown >= lines.length ? '\n<span class="t-prompt">$</span> <span class="t-caret"></span>' : '');
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
  if (term) {
    renderTerminal('ls', false);
    var termSeen = false;
    new IntersectionObserver(function (entries) {
      if (entries[0].isIntersecting && !termSeen) { termSeen = true; renderTerminal('ls', true); }
    }, { threshold: 0.4 }).observe(term);
  }

  /* ---------- Falling keys ---------- */
  var keys = (function () {
    var canvas = $('.hero-keys');
    var api = { recolor: function () {} };
    if (!canvas) return api;
    var ctx = canvas.getContext('2d');
    var KEYS = [
      'photos/2026/04/IMG_2231.heic', 'backups/2026-10-19.tar.gz', 'logs/app-2026-10-19.log.gz', 'reports/q3.csv',
      'website/index.html', 'manifest.json', 'tmp/upload-7f3a.part', 'videos/launch.mp4', 'invoices/2026-09.pdf',
      'data/events/2026/10/19/part-0001.parquet', 'site/assets/hero.webp', 'website/robots.txt', 'models/v3/weights.bin',
      'exports/users.csv.gz', 'releases/0.1.0/SHA256SUMS', 'fonts/inter.woff2', 'db/dump-2026-10-18.sql.zst'
    ];
    // Object keys drift down and are drawn into the window, where they fade as
    // they land. Motion is time-based so speed does not depend on refresh rate.
    var FONT = '500 11px ui-monospace, "SF Mono", SFMono-Regular, "Cascadia Mono", Menlo, Consolas, monospace';
    var dpr = 1, W = 0, H = 0, drops = [], raf = 0, last = 0, visible = true, colors = {}, avoid = null, land = null;
    function readColors() {
      var cs = getComputedStyle(root);
      colors.base = cs.getPropertyValue('--keys-color').trim().split(/\s+/).join(',');
      colors.accent = cs.getPropertyValue('--accent').trim();
    }
    function make(col, cols, initial, lane) {
      var top = land ? land.t : H * 0.8;
      var key = KEYS[(Math.random() * KEYS.length) | 0];
      return {
        x0: (col + 0.1 + Math.random() * 0.6) * (W / cols),
        y: initial ? (lane * top / 2 + Math.random() * top / 3) : -20 - Math.random() * 160,
        lane: lane,
        v: 16 + Math.random() * 18,
        key: key,
        w: ctx.measureText(key).width,
        col: col,
        hi: Math.random() < 0.12
      };
    }
    function measure() {
      var cr = canvas.getBoundingClientRect();
      var copy = $('.hero-copy'), shot = $('.window');
      if (copy) {
        var r = copy.getBoundingClientRect();
        avoid = { l: r.left - cr.left - 32, r: r.right - cr.left + 32, t: r.top - cr.top - 24, b: r.bottom - cr.top + 24 };
      }
      if (shot) {
        var s = shot.getBoundingClientRect();
        land = { l: s.left - cr.left + 24, r: s.right - cr.left - 24, t: s.top - cr.top + 8 };
      }
    }
    function resize() {
      dpr = Math.min(2, window.devicePixelRatio || 1);
      W = canvas.clientWidth; H = canvas.clientHeight;
      canvas.width = W * dpr; canvas.height = H * dpr;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.font = FONT;
      measure();
      var cols = Math.max(3, Math.floor(W / 180));
      drops = [];
      for (var c = 0; c < cols; c++) { drops.push(make(c, cols, true, 0)); drops.push(make(c, cols, true, 1)); }
      drops.cols = cols;
      readColors();
      if (reduced.matches) draw();
    }
    function draw() {
      ctx.clearRect(0, 0, W, H);
      ctx.font = FONT;
      ctx.textBaseline = 'middle';
      var floor = land ? land.t : H * 0.8;
      for (var i = 0; i < drops.length; i++) {
        var d = drops[i];
        var p = Math.max(0, Math.min(1, d.y / floor));
        // Ease the last third of the fall toward the window's horizontal span.
        var pull = Math.max(0, (p - 0.66) / 0.34);
        pull = pull * pull * (3 - 2 * pull);
        var x = d.x0;
        if (land) {
          var tx = Math.min(Math.max(d.x0, land.l), Math.max(land.l, land.r - d.w));
          x = d.x0 + (tx - d.x0) * pull;
        }
        x = Math.min(x, W - d.w - 8);
        var a = Math.min(1, (d.y + 20) / 110) * (1 - pull * pull);
        if (avoid && x + d.w > avoid.l && x < avoid.r && d.y > avoid.t && d.y < avoid.b) a *= 0.1;
        if (a <= 0.01) continue;
        if (d.hi) { ctx.globalAlpha = a * 0.8; ctx.fillStyle = colors.accent; }
        else { ctx.globalAlpha = 1; ctx.fillStyle = 'rgba(' + colors.base + ',' + (a * 0.5).toFixed(3) + ')'; }
        ctx.fillText(d.key, x, d.y);
      }
      ctx.globalAlpha = 1;
    }
    function step(now) {
      var dt = last ? Math.min(0.05, (now - last) / 1000) : 0;
      last = now;
      var floor = land ? land.t : H * 0.8;
      for (var i = 0; i < drops.length; i++) {
        var d = drops[i];
        d.y += d.v * dt;
        if (d.y >= floor) drops[i] = make(d.col, drops.cols, false, d.lane);
      }
      draw();
      raf = visible && !document.hidden ? requestAnimationFrame(step) : 0;
      if (!raf) last = 0;
    }
    function start() { if (!raf && visible && !reduced.matches && !document.hidden) raf = requestAnimationFrame(step); }
    new IntersectionObserver(function (entries) {
      visible = entries[0].isIntersecting;
      if (visible) start();
    }).observe(canvas);
    document.addEventListener('visibilitychange', start);
    var resizeTimer = 0;
    window.addEventListener('resize', function () { clearTimeout(resizeTimer); resizeTimer = setTimeout(resize, 150); });
    resize();
    start();
    api.recolor = function () { readColors(); if (reduced.matches) draw(); };
    api.relayout = function () { measure(); };
    return api;
  })();

  /* ---------- Command palette ---------- */
  var palette = (function () {
    var el = $('[data-palette]');
    var input = $('input', el), list = $('.palette-list', el), empty = $('.palette-empty', el);
    var state = { open: false, items: [], active: 0, opener: null };
    function go(id) { return function () { var target = document.getElementById(id); if (target) target.scrollIntoView({ behavior: reduced.matches ? 'auto' : 'smooth' }); }; }
    function link(url) { return function () { window.open(url, '_blank', 'noopener'); }; }
    function catalog() {
      var pref = root.getAttribute('data-theme-pref');
      var items = [
        { g: 'pgGo', icon: 'i-arrow-right', label: t('navFeatures'), k: 'features 功能', run: go('features') },
        { g: 'pgGo', icon: 'i-terminal', label: t('navCli'), k: 'command line cli terminal 命令行 终端', run: go('cli') },
        { g: 'pgGo', icon: 'i-lock', label: t('navPrivacy'), k: 'privacy safety security 隐私 安全', run: go('privacy') },
        { g: 'pgGo', icon: 'i-download', label: t('navDownload'), k: 'download install 下载 安装', run: go('download') },
        { g: 'pgGo', icon: 'i-arrow-up', label: t('pcTop'), k: 'top home 顶部 首页', run: go('top') }
      ];
      if (OS) items.push({ g: 'pgActions', icon: 'i-download', label: t('pcDownload', { os: OS_NAMES[OS] }), k: 'download 下载 ' + OS, run: go('download') });
      items.push(
        { g: 'pgActions', icon: 'i-monitor', label: t('pcThemeSystem'), k: 'theme system auto 主题 系统 自动', hint: pref === 'system', run: function () { setTheme('system'); } },
        { g: 'pgActions', icon: 'i-sun', label: t('pcThemeLight'), k: 'theme light 主题 浅色 亮色', hint: pref === 'light', run: function () { setTheme('light'); } },
        { g: 'pgActions', icon: 'i-moon', label: t('pcThemeDark'), k: 'theme dark 主题 深色 暗色', hint: pref === 'dark', run: function () { setTheme('dark'); } },
        { g: 'pgActions', icon: 'i-globe', label: t('pcLang'), k: 'language english chinese 语言 中文 英文', run: switchLang },
        { g: 'pgLinks', icon: 'i-github', label: t('pcGithub'), k: 'github source repository 源码 仓库', run: link(GH) },
        { g: 'pgLinks', icon: 'i-package', label: t('pcReleases'), k: 'releases versions 版本 发布', run: link(GH + '/releases') },
        { g: 'pgLinks', icon: 'i-scroll', label: t('pcDocs'), k: 'docs documentation readme 文档', run: link(GH + '#readme') }
      );
      return items;
    }
    function renderPalette() {
      var q = input.value.trim().toLowerCase();
      state.items = catalog().filter(function (it) { return !q || (it.label + ' ' + it.k).toLowerCase().indexOf(q) !== -1; });
      if (state.active >= state.items.length) state.active = 0;
      // listbox > group (labelled) > option, per the WAI-ARIA listbox pattern.
      var html = '', lastGroup = null;
      state.items.forEach(function (it, i) {
        if (it.g !== lastGroup) {
          if (lastGroup) html += '</ul></li>';
          html += '<li role="group" aria-labelledby="pg-' + it.g + '"><div class="palette-group" id="pg-' + it.g + '">' + t(it.g) + '</div><ul role="presentation">';
          lastGroup = it.g;
        }
        html += '<li class="palette-item" role="option" id="pi-' + i + '" data-index="' + i + '" aria-selected="' + (i === state.active) + '">' +
          '<svg class="i" aria-hidden="true" focusable="false"><use href="#' + it.icon + '"></use></svg><span>' + it.label + '</span>' +
          (it.hint ? '<svg class="i pi-hint" aria-hidden="true" focusable="false"><use href="#i-check"></use></svg>' : '') + '</li>';
      });
      if (lastGroup) html += '</ul></li>';
      list.innerHTML = html;
      empty.hidden = state.items.length > 0;
      input.setAttribute('aria-activedescendant', state.items.length ? 'pi-' + state.active : '');
    }
    function move(d) {
      if (!state.items.length) return;
      state.active = (state.active + d + state.items.length) % state.items.length;
      $$('.palette-item', list).forEach(function (li) { li.setAttribute('aria-selected', String(+li.getAttribute('data-index') === state.active)); });
      input.setAttribute('aria-activedescendant', 'pi-' + state.active);
      var cur = $('#pi-' + state.active, list);
      if (cur) cur.scrollIntoView({ block: 'nearest' });
    }
    function runActive() {
      var it = state.items[state.active];
      if (!it) return;
      close();
      it.run();
    }
    function open() {
      if (state.open) return;
      state.opener = document.activeElement;
      state.open = true;
      el.hidden = false;
      input.value = '';
      state.active = 0;
      renderPalette();
      setInert(true);
      input.focus();
      document.body.style.overflow = 'hidden';
    }
    function setInert(on) {
      $$('body > :not([data-palette]):not(script):not(.sprite)').forEach(function (n) { n.inert = on; });
    }
    function close() {
      if (!state.open) return;
      state.open = false;
      el.hidden = true;
      setInert(false);
      document.body.style.overflow = '';
      if (state.opener && state.opener.focus) state.opener.focus();
    }
    input.addEventListener('input', function () { state.active = 0; renderPalette(); });
    input.addEventListener('keydown', function (e) {
      if (e.key === 'ArrowDown') { e.preventDefault(); move(1); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); move(-1); }
      else if (e.key === 'Enter') { e.preventDefault(); runActive(); }
      else if (e.key === 'Escape') { e.preventDefault(); close(); }
      else if (e.key === 'Tab') { e.preventDefault(); }
    });
    list.addEventListener('mousemove', function (e) {
      var li = e.target.closest('.palette-item');
      if (li && +li.getAttribute('data-index') !== state.active) move(+li.getAttribute('data-index') - state.active);
    });
    list.addEventListener('click', function (e) {
      var li = e.target.closest('.palette-item');
      if (!li) return;
      state.active = +li.getAttribute('data-index');
      runActive();
    });
    document.addEventListener('keydown', function (e) {
      if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        if (state.open) close(); else open();
      }
    });
    state.show = open;
    state.hide = close;
    state.render = renderPalette;
    return state;
  })();

  /* ---------- Actions ---------- */
  document.addEventListener('click', function (e) {
    var trigger = e.target.closest('[data-action]');
    if (!trigger) return;
    var action = trigger.getAttribute('data-action');
    if (action === 'palette') palette.show();
    else if (action === 'palette-close') palette.hide();
    else if (action === 'lang') rememberLang();
  });

  applyTheme(root.getAttribute('data-theme-pref') || 'system');
  // Re-measure the landing area once the hero entrance has settled.
  window.addEventListener('load', function () { setTimeout(keys.relayout, reduced.matches ? 0 : 1800); });
})();
