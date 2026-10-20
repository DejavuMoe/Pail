(function () {
  'use strict';

  var root = document.documentElement;
  root.classList.add('js');

  var dict = window.PAIL_I18N;
  var reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
  var darkMQ = window.matchMedia('(prefers-color-scheme: dark)');

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
    $$('[data-i18n-aria-label], [data-i18n-title], [data-i18n-alt], [data-i18n-placeholder], [data-i18n-content], [data-i18n-href]').forEach(function (el) {
      ['aria-label', 'title', 'alt', 'placeholder', 'content', 'href'].forEach(function (attr) {
        var key = el.getAttribute('data-i18n-' + attr);
        if (key) el.setAttribute(attr, t(key));
      });
    });
    var cta = $('[data-os-cta-label]');
    if (cta) cta.textContent = OS ? t('heroDownloadFor', { os: OS_NAMES[OS] }) : t('heroDownload');
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
    if (header) header.classList.toggle('is-scrolled', window.scrollY > 8);
    if (!win || reduced.matches) return;
    tilt.target = tiltTarget();
    if (tilt.cur === null) tilt.cur = tilt.target;
    if (!tilt.raf) tilt.raf = requestAnimationFrame(tiltFrame);
  }
  window.addEventListener('scroll', onScroll, { passive: true });
  window.addEventListener('resize', onScroll);
  onScroll();

  var navLinks = $$('.nav a[href^="#"], .mobile-nav a[href^="#"]');
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
  if (navLinks.length) $$('main section[id]').forEach(function (s) { sectionObserver.observe(s); });

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
    // Time-based: progress = elapsed / duration, with a slow sine ripple on the
    // speed (integral kept monotonic). The fill is scaled, never resized.
    var DURATIONS = [9000, 16000]; // upload row, download row: ms to cover 0 -> 1
    var rows = $$('.tq-row[data-total]', tile).filter(function (row) {
      return !row.classList.contains('is-done');
    }).map(function (row, i) {
      return {
        row: row, total: +row.getAttribute('data-total'), p: i ? 0.66 : 0.64,
        dur: DURATIONS[i] || DURATIONS[DURATIONS.length - 1], phase: i * 1.7,
        state: 'run', until: 0, lastText: -1e9,
        fill: $('.tq-bar i', row), bytes: $('.tq-bytes', row)
      };
    });
    function paintFill(r) { r.fill.style.transform = 'scaleX(' + r.p.toFixed(4) + ')'; }
    function paintText(r, now, force) {
      if (!force && now - r.lastText < 160) return;
      r.lastText = now;
      r.bytes.textContent = humanSize(Math.round(r.total * r.p)) + ' / ' + humanSize(r.total);
    }
    rows.forEach(function (r) { paintFill(r); paintText(r, 0); });
    var tRaf = 0, tLast = 0, tVisible = false;
    function tFrame(now) {
      var dt = tLast ? Math.min(100, now - tLast) : 0;
      tLast = now;
      rows.forEach(function (r) {
        if (r.state === 'run') {
          // speed factor 1 +/- 20 %, smooth in time
          var k = 1 + 0.2 * Math.sin(now / 1900 + r.phase);
          r.p = Math.min(1, r.p + (dt / r.dur) * k);
          paintFill(r);
          paintText(r, now);
          if (r.p >= 1) { r.state = 'hold'; r.until = now + 1200; paintText(r, now, true); }
        } else if (r.state === 'hold' && now >= r.until) {
          r.state = 'fadeout'; r.until = now + 320;
          r.fill.classList.add('is-fading');
        } else if (r.state === 'fadeout' && now >= r.until) {
          r.p = 0; paintFill(r); paintText(r, now, true);
          r.state = 'fadein'; r.until = now + 60;
        } else if (r.state === 'fadein' && now >= r.until) {
          r.fill.classList.remove('is-fading');
          r.state = 'run';
        }
      });
      tRaf = tVisible && !document.hidden ? requestAnimationFrame(tFrame) : 0;
      if (!tRaf) tLast = 0;
    }
    function tStart() { if (!tRaf && tVisible && !reduced.matches && !document.hidden) tRaf = requestAnimationFrame(tFrame); }
    new IntersectionObserver(function (entries) {
      tVisible = entries[entries.length - 1].isIntersecting;
      if (tVisible) tStart();
    }).observe(tile);
    document.addEventListener('visibilitychange', tStart);
    var chips = $$('[data-cycle] .chip', tile), k = 0;
    if (!chips.length) return;
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
    if (!chips.length) return;
    loop($('.mini-settings'), function () {
      chips.forEach(function (c) { c.classList.remove('is-lit'); });
      chips[k % chips.length].classList.add('is-lit');
      k += 5;
    }, 2400);
  })();

  // Versions
  (function () {
    var box = $('.mini-versions');
    if (!box) return;
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
    var api = { recolor: function () {}, relayout: function () {} };
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

  /* ---------- Mobile navigation ---------- */
  var menuBtn = $('.menu-btn');
  var mobileNav = $('#mobile-nav');
  function setMenu(open, restoreFocus) {
    if (!menuBtn || !mobileNav) return;
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
    var onWide = function () { if (wideMQ.matches) setMenu(false, false); };
    if (wideMQ.addEventListener) wideMQ.addEventListener('change', onWide); else wideMQ.addListener(onWide);
  }

  /* ---------- Actions ---------- */
  document.addEventListener('click', function (e) {
    var trigger = e.target.closest('[data-action]');
    if (!trigger) return;
    if (trigger.getAttribute('data-action') !== 'lang') return;
    if (trigger.tagName === 'A') {
      // Docs pages: the control links to the same page in the other language.
      // Remember the choice for the landing page, keep the section hash, and let the link navigate.
      store('pail-lang', trigger.getAttribute('data-lang-target') === 'zh' ? 'zh' : 'en');
      if (location.hash) trigger.setAttribute('href', trigger.getAttribute('href').split('#')[0] + location.hash);
      return;
    }
    setLang(lang() === 'zh' ? 'en' : 'zh');
  });

  applyTheme(root.getAttribute('data-theme-pref') || 'system');
  applyLang();
  // Re-measure the landing area once the hero entrance has settled.
  window.addEventListener('load', function () { setTimeout(keys.relayout, reduced.matches ? 0 : 1800); });
})();
