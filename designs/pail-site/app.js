(function () {
  'use strict';

  var root = document.documentElement;
  root.classList.add('js');

  var dict = window.PAIL_I18N;
  var reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
  var darkMQ = window.matchMedia('(prefers-color-scheme: dark)');
  var GH = 'https://github.com/DejavuMoe/Pail';

  function $(sel, ctx) { return (ctx || document).querySelector(sel); }
  function $$(sel, ctx) { return Array.prototype.slice.call((ctx || document).querySelectorAll(sel)); }
  function store(key, value) { try { localStorage.setItem(key, value); } catch (e) {} }
  function lang() { return root.getAttribute('data-lang') === 'zh' ? 'zh' : 'en'; }
  function t(key, vars) {
    var s = (dict[lang()] && dict[lang()][key]) || dict.en[key] || key;
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

  /* ---------- i18n ---------- */
  function applyLang() {
    $$('[data-i18n]').forEach(function (el) { el.textContent = t(el.getAttribute('data-i18n')); });
    $$('[data-i18n-aria-label], [data-i18n-title], [data-i18n-alt], [data-i18n-placeholder], [data-i18n-content]').forEach(function (el) {
      ['aria-label', 'title', 'alt', 'placeholder', 'content'].forEach(function (attr) {
        var key = el.getAttribute('data-i18n-' + attr);
        if (key) el.setAttribute(attr, t(key));
      });
    });
    $$('[data-mod]').forEach(function (k) { k.textContent = isMac ? '⌘' : 'Ctrl'; });
    var cta = $('[data-os-cta-label]');
    if (cta) cta.textContent = OS ? t('heroDownloadFor', { os: OS_NAMES[OS] }) : t('heroDownload');
    if (palette.open) palette.render();
    paletteDemo.restart();
  }

  function setLang(next) {
    store('pail-lang', next);
    var run = function () {
      root.setAttribute('data-lang', next);
      root.lang = next === 'zh' ? 'zh-Hans' : 'en';
      applyLang();
    };
    if (document.startViewTransition && !reduced.matches) document.startViewTransition(run);
    else run();
  }

  /* ---------- Theme ---------- */
  var themeButtons = $$('[data-theme-set]');
  function resolve(pref) { return pref === 'dark' || (pref === 'system' && darkMQ.matches) ? 'dark' : 'light'; }
  function applyTheme(pref) {
    root.setAttribute('data-theme-pref', pref);
    root.setAttribute('data-theme', resolve(pref));
    themeButtons.forEach(function (b) {
      var on = b.getAttribute('data-theme-set') === pref;
      b.setAttribute('aria-checked', String(on));
      b.tabIndex = on ? 0 : -1;
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
  themeButtons.forEach(function (b, i) {
    b.addEventListener('click', function () {
      var rect = b.getBoundingClientRect();
      setTheme(b.getAttribute('data-theme-set'), { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 });
    });
    b.addEventListener('keydown', function (e) {
      var d = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0;
      if (!d) return;
      e.preventDefault();
      var next = themeButtons[(i + d + themeButtons.length) % themeButtons.length];
      next.focus();
      next.click();
    });
  });
  darkMQ.addEventListener('change', function () {
    if (root.getAttribute('data-theme-pref') === 'system') applyTheme('system');
  });

  /* ---------- Header, nav, reveal, tilt ---------- */
  var header = $('.site-header');
  var win = $('[data-tilt]');
  function onScroll() {
    header.classList.toggle('is-scrolled', window.scrollY > 8);
    if (!win || reduced.matches) return;
    var rect = win.getBoundingClientRect();
    var vh = window.innerHeight;
    var p = Math.min(1, Math.max(0, (vh - rect.top) / (vh * 0.95)));
    win.style.setProperty('--tilt', ((1 - p) * 24).toFixed(2) + 'deg');
    win.style.setProperty('--scale', (0.9 + 0.1 * p).toFixed(4));
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
  $$('.reveal').forEach(function (el, i) {
    el.style.transitionDelay = (i % 3) * 70 + 'ms';
    revealObserver.observe(el);
  });

  $$('.tile').forEach(function (tile) {
    tile.addEventListener('pointermove', function (e) {
      var r = tile.getBoundingClientRect();
      tile.style.setProperty('--mx', (e.clientX - r.left) + 'px');
      tile.style.setProperty('--my', (e.clientY - r.top) + 'px');
    });
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
    var dpr = 1, W = 0, H = 0, drops = [], raf = 0, visible = true, colors = {}, avoid = null;
    function readColors() {
      var cs = getComputedStyle(root);
      colors.base = cs.getPropertyValue('--keys-color').trim().split(/\s+/).join(',');
      colors.accent = cs.getPropertyValue('--accent').trim();
    }
    function make(col, cols, initial, lane) {
      var span = H * 0.8;
      return {
        x: (col + 0.1 + Math.random() * 0.6) * (W / cols),
        y: initial ? (lane * span / 2 + Math.random() * span / 3) : -30 - Math.random() * 120,
        lane: lane,
        v: 0.22 + Math.random() * 0.38,
        key: KEYS[(Math.random() * KEYS.length) | 0],
        col: col,
        hi: Math.random() < 0.14
      };
    }
    function measureAvoid() {
      var copy = $('.hero-copy'), cr = canvas.getBoundingClientRect();
      if (!copy) return;
      var r = copy.getBoundingClientRect();
      avoid = { l: r.left - cr.left - 32, r: r.right - cr.left + 32, t: r.top - cr.top - 24, b: r.bottom - cr.top + 24 };
    }
    function resize() {
      dpr = Math.min(2, window.devicePixelRatio || 1);
      W = canvas.clientWidth; H = canvas.clientHeight;
      canvas.width = W * dpr; canvas.height = H * dpr;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      var cols = Math.max(3, Math.floor(W / 170));
      drops = [];
      for (var c = 0; c < cols; c++) { drops.push(make(c, cols, true, 0)); drops.push(make(c, cols, true, 1)); }
      drops.cols = cols;
      measureAvoid();
      readColors();
      if (reduced.matches) draw();
    }
    function draw() {
      ctx.clearRect(0, 0, W, H);
      ctx.font = '500 11px "Geist Mono", ui-monospace, monospace';
      ctx.textBaseline = 'middle';
      for (var i = 0; i < drops.length; i++) {
        var d = drops[i];
        var fadeIn = Math.min(1, (d.y + 30) / 120);
        var fadeOut = Math.max(0, 1 - d.y / (H * 0.78));
        var a = Math.max(0, Math.min(fadeIn, fadeOut));
        var w = ctx.measureText(d.key).width;
        var x = Math.min(d.x, W - w - 8);
        if (avoid && x + w > avoid.l && x < avoid.r && d.y > avoid.t && d.y < avoid.b) a *= 0.12;
        if (a <= 0.01) continue;
        ctx.fillStyle = d.hi ? colors.accent : 'rgba(' + colors.base + ',' + (a * 0.55).toFixed(3) + ')';
        if (d.hi) ctx.globalAlpha = a * 0.85;
        ctx.fillText(d.key, x, d.y);
        ctx.globalAlpha = 1;
      }
    }
    function step() {
      for (var i = 0; i < drops.length; i++) {
        var d = drops[i];
        d.y += d.v;
        if (d.y > H * 0.8) {
          var sibling = drops[i % 2 ? i - 1 : i + 1];
          if (!sibling || sibling.y > H * 0.3) drops[i] = make(d.col, drops.cols, false, d.lane);
        }
      }
      draw();
      raf = visible ? requestAnimationFrame(step) : 0;
    }
    function start() { if (!raf && visible && !reduced.matches && !document.hidden) raf = requestAnimationFrame(step); }
    new IntersectionObserver(function (entries) {
      visible = entries[0].isIntersecting;
      if (visible) start();
    }).observe(canvas);
    document.addEventListener('visibilitychange', start);
    window.addEventListener('resize', resize);
    if (document.fonts && document.fonts.ready) document.fonts.ready.then(function () { measureAvoid(); if (reduced.matches) draw(); });
    resize();
    start();
    api.recolor = function () { readColors(); if (reduced.matches) draw(); };
    api.relayout = function () { measureAvoid(); };
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
        { g: 'pgActions', icon: 'i-globe', label: t('pcLang'), k: 'language english chinese 语言 中文 英文', run: function () { setLang(lang() === 'zh' ? 'en' : 'zh'); } },
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
      var html = '', lastGroup = null;
      state.items.forEach(function (it, i) {
        if (it.g !== lastGroup) { html += '<li class="palette-group" role="presentation">' + t(it.g) + '</li>'; lastGroup = it.g; }
        html += '<li class="palette-item" role="option" id="pi-' + i + '" data-index="' + i + '" aria-selected="' + (i === state.active) + '">' +
          '<svg class="i"><use href="#' + it.icon + '"></use></svg><span>' + it.label + '</span>' +
          (it.hint ? '<svg class="i pi-hint"><use href="#i-check"></use></svg>' : '') + '</li>';
      });
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
      input.focus();
      document.body.style.overflow = 'hidden';
    }
    function close() {
      if (!state.open) return;
      state.open = false;
      el.hidden = true;
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
    else if (action === 'lang') setLang(lang() === 'zh' ? 'en' : 'zh');
  });

  applyTheme(root.getAttribute('data-theme-pref') || 'system');
  applyLang();
  window.addEventListener('load', function () { keys.relayout && keys.relayout(); });
})();
