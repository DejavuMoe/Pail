// pail.download landing page: the point-cloud pail that becomes the outline of
// the window, the feature views that the scroll position plays, the feature
// filter, the terminal and the download picker. Without this file every view
// shows its finished state beside its text.
(function () {
  'use strict';
  var site = window.PailSite || {};
  var S = site.strings || {};
  var d = document.documentElement;
  var reduce = site.reduce || window.matchMedia('(prefers-reduced-motion: reduce)');
  function store(k, v) { try { localStorage.setItem(k, v); } catch (e) {} }
  function read(k) { try { return localStorage.getItem(k); } catch (e) { return null; } }
  function css(name) { return getComputedStyle(d).getPropertyValue(name).trim(); }
  function onReduce(fn) { if (reduce.addEventListener) reduce.addEventListener('change', fn); else if (reduce.addListener) reduce.addListener(fn); }
  function all(sel, root) { return [].slice.call((root || document).querySelectorAll(sel)); }
  function clamp(x) { return x < 0 ? 0 : x > 1 ? 1 : x; }

  // Motion that runs on its own stops for reduced motion.
  var syncs = [];
  function moving() { return !reduce.matches; }
  function resync() { syncs.forEach(function (f) { f(); }); }
  onReduce(resync);
  document.addEventListener('visibilitychange', resync);

  // Runs start() while el is on screen and motion is allowed, stop() otherwise.
  function whileVisible(el, start, stop, threshold) {
    var seen = false, running = false;
    function sync() {
      var go = seen && !document.hidden && moving();
      if (go && !running) { running = true; start(); }
      else if (!go && running) { running = false; stop(); }
    }
    if ('IntersectionObserver' in window) {
      new IntersectionObserver(function (es) { seen = es[es.length - 1].isIntersecting; sync(); }, { threshold: threshold || 0 }).observe(el);
    } else { seen = true; }
    syncs.push(sync);
    sync();
  }

  // Everything that follows the scroll position reads it here, once a frame.
  // The listeners are passive: the page scrolls on its own and the views follow.
  var ticks = [], ticking = false;
  function tick() {
    ticking = false;
    var y = window.scrollY || 0, vh = window.innerHeight || 0;
    for (var i = 0; i < ticks.length; i++) ticks[i](y, vh);
  }
  function requestTick() { if (!ticking) { ticking = true; requestAnimationFrame(tick); } }
  window.addEventListener('scroll', requestTick, { passive: true });
  window.addEventListener('resize', requestTick);
  syncs.push(requestTick);

  /* ---------------- the pail of points, and the window it turns into ---------------- */

  (function () {
    var canvas = document.querySelector('[data-pail]');
    if (!canvas || !canvas.getContext) return;
    var intro = canvas.parentNode;
    var stage = intro.querySelector('.hero-stage'), frame = intro.querySelector('.shot-frame'), log = intro.querySelector('.drop-log');
    if (!stage || !frame) return;
    var ctx = canvas.getContext('2d');
    var objects = (canvas.getAttribute('data-objects') || '').split('|').filter(Boolean).map(function (s) {
      var a = s.split('~');
      return { key: a[0], size: a[1] || '' };
    });
    var W = 0, H = 0, dpr = 1, colors = {}, mono = '';
    var cx = 0, cy = 0, scale = 1, wireSize = 1.4;
    var s0 = 0, s1 = 1, sA = 0, sB = 1, end = 0, can = true, morph = 0, shot = 1, shotSet = 1, past = false, drawn = '';
    var pts = [], drops = [], rings = [], t0 = 0, last = 0, raf = 0, angle = 0.6, nextDrop = 0, flash = -1e9, pick = 0;
    var tiltX = 0, tiltTarget = 0, yawNudge = 0, yawTarget = 0, band = null;

    // Seeded random numbers, so the cloud is the same on every visit.
    var seed = 7;
    function rnd() { seed = (seed * 16807) % 2147483647; return (seed - 1) / 2147483646; }

    // The icon: three bands of a pail, widest at the top, and a handle.
    var TOP = 0.66, BOTTOM = 0.46, HEIGHT = 1.06, RIM = -HEIGHT / 2;
    var bands = [[0.0, 0.29, 's1'], [0.355, 0.645, 's2'], [0.71, 1.0, 's3']];
    function radiusAt(u) { return TOP + (BOTTOM - TOP) * u; }
    function add(x, y, z, c, size, rim) {
      var r = 2.2 + rnd() * 2.2, a = rnd() * Math.PI * 2, b = Math.acos(2 * rnd() - 1);
      pts.push({ x: x, y: y, z: z, c: c, s: size || 1, rim: !!rim,
        sx: r * Math.sin(b) * Math.cos(a), sy: r * Math.cos(b), sz: r * Math.sin(b) * Math.sin(a),
        d: rnd() * 0.55 + (y + 0.6) * 0.35, u: 0, v: 0, tx: 0, ty: 0, md: 0, arc: 0 });
    }
    bands.forEach(function (b, bi) {
      var rows = 8;
      for (var k = 0; k < rows; k++) {
        var u = b[0] + (b[1] - b[0]) * (k / (rows - 1));
        var r = radiusAt(u), n = Math.round(r * 112);
        var off = (k % 2) * Math.PI / n;
        for (var j = 0; j < n; j++) {
          var a = off + j / n * Math.PI * 2;
          add(Math.cos(a) * r, RIM + u * HEIGHT, Math.sin(a) * r, b[2], k === 0 || k === rows - 1 ? 1.25 : 1, bi === 0 && k < 2);
        }
      }
    });
    for (var fr = 1; fr <= 4; fr++) {
      var rr = BOTTOM * fr / 4.4, fn = Math.round(rr * 90);
      for (var fj = 0; fj < fn; fj++) { var fa = fj / fn * Math.PI * 2; add(Math.cos(fa) * rr, HEIGHT / 2, Math.sin(fa) * rr, 's3', 0.8); }
    }
    for (var h = 0; h <= 90; h++) {
      var ha = Math.PI + h / 90 * Math.PI;
      add(Math.cos(ha) * TOP * 1.02, RIM + Math.sin(ha) * 0.62, 0, 'text', 0.9);
    }

    // The window in the screenshot, as fractions of the image: its outline,
    // the edges of the sidebar and the inspector, and the lines under the
    // toolbar and above the transfers strip and the status bar. Every point
    // of the pail gets a place on these lines; the top of the pail goes to
    // the top of the window and leaves first.
    (function () {
      var ASPECT = 1750 / 2684, L = 0.0414, T = 0.0429, R = 0.9579, B = 0.9149, RX = 0.0075, RY = RX / ASPECT;
      var SIDE = 0.2269, INSP = 0.7195, TOOL = 0.092, STRIP = 0.8509, STATUS = 0.8874;
      var segs = [
        [L + RX, T, R - RX, T], [R, T + RY, R, B - RY], [R - RX, B, L + RX, B], [L, B - RY, L, T + RY],
        [SIDE, T, SIDE, STRIP], [INSP, TOOL, INSP, STRIP], [SIDE, TOOL, R, TOOL], [L, STRIP, R, STRIP], [L, STATUS, R, STATUS]
      ];
      var corners = [[L + RX, T + RY, Math.PI, 1.5 * Math.PI], [R - RX, T + RY, 1.5 * Math.PI, 2 * Math.PI], [R - RX, B - RY, 0, 0.5 * Math.PI], [L + RX, B - RY, 0.5 * Math.PI, Math.PI]];
      var total = 0, lens = segs.map(function (s) { var l = Math.abs(s[2] - s[0]) + Math.abs(s[3] - s[1]) * ASPECT; total += l; return l; });
      var spare = corners.length * 3, n = pts.length - spare, wire = [];
      for (var i = 0; i < n; i++) {
        var at = (i + 0.5) / n * total, k = 0;
        while (k < segs.length - 1 && at > lens[k]) { at -= lens[k]; k++; }
        var f = at / lens[k], s = segs[k];
        wire.push([s[0] + (s[2] - s[0]) * f, s[1] + (s[3] - s[1]) * f]);
      }
      corners.forEach(function (c) {
        for (var j = 1; j <= 3; j++) { var a = c[2] + (c[3] - c[2]) * j / 4; wire.push([c[0] + Math.cos(a) * RX, c[1] + Math.sin(a) * RY]); }
      });
      wire.sort(function (a, b) { return a[1] - b[1] || a[0] - b[0]; });
      var order = pts.map(function (q, i) { return i; }).sort(function (a, b) {
        return pts[a].y - pts[b].y || Math.atan2(pts[a].z, pts[a].x) - Math.atan2(pts[b].z, pts[b].x);
      });
      order.forEach(function (pi, rank) {
        var q = pts[pi];
        q.u = wire[rank][0]; q.v = wire[rank][1];
        q.md = 0.48 * rank / pts.length + rnd() * 0.06;
        q.arc = (rnd() - 0.5) * 2;
      });
    })();

    function readColors() {
      colors = { s1: css('--s1'), s2: css('--s2'), s3: css('--s3'), text: css('--text-3'), label: css('--text-2'), signal: css('--signal') };
      mono = css('--mono');
    }

    // The canvas lies in the page from the top of the hero to the foot of the
    // screenshot and scrolls with it, so the points never trail the content.
    function measure() {
      var ir = intro.getBoundingClientRect(), sr = stage.getBoundingClientRect(), fr = frame.getBoundingClientRect();
      var y = window.scrollY || 0, vh = window.innerHeight || 1;
      var w = Math.max(1, Math.round(ir.width)), h = Math.max(1, Math.ceil(fr.bottom - ir.top + 16));
      var ratio = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(7e6 / (w * h)), 4096 / h);
      if (w !== W || h !== H || ratio !== dpr) {
        W = w; H = h; dpr = ratio;
        canvas.style.height = H + 'px';
        canvas.width = Math.round(W * dpr); canvas.height = Math.round(H * dpr);
        ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        band = null;
        drawn = '';
      }
      var sh = Math.max(1, sr.height - 60), fx = fr.left - ir.left, fy = fr.top - ir.top;
      cx = sr.left - ir.left + sr.width / 2; cy = sr.top - ir.top + sh * 0.58;
      scale = Math.min(sr.width * 0.36, sh * 0.33);
      wireSize = Math.max(1.1, Math.min(2, fr.width / 600));
      for (var i = 0; i < pts.length; i++) { pts[i].tx = fx + pts[i].u * fr.width; pts[i].ty = fy + pts[i].v * fr.height; }
      var top = ir.top + y;
      s0 = Math.max(24, top + cy - 0.5 * vh);
      s1 = Math.max(s0 + 240, top + fy - 0.42 * vh);
      sA = s0 + 0.9 * (s1 - s0); sB = s1 + 0.2 * vh;
      // On a screen tall enough to show the screenshot before any scrolling
      // the pail stays a pail: there is no room for it to become the window.
      can = top + fy > 0.85 * vh;
      end = can ? sB + 4 : top + sr.bottom - ir.top;
    }
    function ease(x) { return x <= 0 ? 0 : x >= 1 ? 1 : 1 - Math.pow(1 - x, 4); }
    function inOut(x) { return x <= 0 ? 0 : x >= 1 ? 1 : x * x * (3 - 2 * x); }

    function logRow(o) {
      if (!log) return;
      var li = document.createElement('li');
      [['verb', 'PUT'], ['k', o.key], ['s', o.size], ['ok', '200']].forEach(function (c) {
        var s = document.createElement('span');
        s.className = c[0];
        s.textContent = c[1];
        li.appendChild(s);
      });
      li.className = 'is-new';
      log.insertBefore(li, log.firstChild);
      while (log.children.length > 3) log.removeChild(log.lastChild);
      requestAnimationFrame(function () { requestAnimationFrame(function () { li.className = ''; }); });
    }

    // Objects fall, cross the rim, and show up in the log, while the pail is whole.
    function step(dt, now) {
      if (now - t0 < 1800 || !objects.length) return;
      if (now >= nextDrop && morph < 0.02) {
        nextDrop = now + 1500 + Math.random() * 500;
        var a = Math.random() * Math.PI * 2, r = Math.sqrt(Math.random()) * TOP * 0.55;
        drops.push({ o: objects[pick++ % objects.length], x: Math.cos(a) * r, z: Math.sin(a) * r, y: -1.6, v: 0.15, inside: false });
      }
      for (var i = drops.length - 1; i >= 0; i--) {
        var p = drops[i];
        p.v += dt * 1.1;
        p.y += p.v * dt;
        if (!p.inside && p.y > RIM) { p.inside = true; if (morph < 0.02) { flash = now; rings.push({ x: p.x, z: p.z, t: now }); logRow(p.o); } }
        if (p.y > HEIGHT * 0.2) drops.splice(i, 1);
      }
      rings = rings.filter(function (g) { return now - g.t < 900; });
    }

    function draw(el, now) {
      if (band) ctx.clearRect(0, band[0], W, band[1] - band[0]); else ctx.clearRect(0, 0, W, H);
      var lo = H, hi = 0;
      var ya = angle + yawNudge, ca = Math.cos(ya), sa = Math.sin(ya);
      var tx = -0.42 + tiltX, ct = Math.cos(tx), st = Math.sin(tx);
      var D = 3.4, unit = Math.max(1, scale / 190);
      function project(x, y, z, out) {
        var x1 = x * ca + z * sa, z1 = -x * sa + z * ca;
        var y2 = y * ct - z1 * st, z2 = y * st + z1 * ct;
        var f = D / (D + z2);
        out.x = cx + x1 * f * scale; out.y = cy + y2 * f * scale; out.f = f; out.z = z2;
        return out;
      }
      var p = {}, m = morph, whole = 1 - clamp(m * 8);
      var glow = Math.max(0, 1 - (now - flash) / 600) * whole;
      var sway = Math.min(80, W * 0.06);
      for (var i = 0; i < pts.length; i++) {
        var q = pts[i];
        var k = ease((el - q.d) / 1.5);
        project(q.sx + (q.x - q.sx) * k, q.sy + (q.y - q.sy) * k, q.sz + (q.z - q.sz) * k, p);
        var front = Math.max(0, Math.min(1, 0.5 - p.z * 0.75));
        var lit = q.rim ? glow : 0;
        var alpha = Math.min(1, (0.22 + 0.78 * front) * (0.35 + 0.65 * k) + lit * 0.5);
        var s = (1.15 + front * 1.25 + lit * 1.2) * q.s * p.f * unit;
        var x = p.x, y = p.y;
        if (m > 0) {
          var km = inOut((m - q.md) / 0.46);
          if (km > 0) {
            x += (q.tx - x) * km + Math.sin(km * Math.PI) * q.arc * sway;
            y += (q.ty - y) * km;
            s += (wireSize - s) * km;
            alpha = (alpha + (0.9 - alpha) * km) * (1 - shot * km);
          }
        }
        if (alpha <= 0.004) continue;
        ctx.globalAlpha = alpha;
        ctx.fillStyle = lit > 0.3 ? colors.signal : colors[q.c];
        ctx.fillRect(x - s / 2, y - s / 2, s, s);
        if (y < lo) lo = y;
        if (y > hi) hi = y;
      }
      // a ring where each object goes in
      rings.forEach(function (g) {
        var age = (now - g.t) / 900, rad = 0.06 + age * 0.4;
        ctx.fillStyle = colors.s1;
        for (var n = 0; n < 28; n++) {
          var a = n / 28 * Math.PI * 2;
          project(g.x + Math.cos(a) * rad, RIM + 0.02, g.z + Math.sin(a) * rad, p);
          ctx.globalAlpha = (1 - age) * 0.9 * whole;
          ctx.fillRect(p.x - 1.2 * unit, p.y - 1.2 * unit, 2.4 * unit, 2.4 * unit);
        }
      });
      // falling objects, each with its key
      ctx.font = '500 ' + Math.round(11 + unit) + 'px ' + mono;
      ctx.textBaseline = 'middle';
      drops.forEach(function (o) {
        var fadeIn = Math.min(1, (o.y + 1.6) / 0.3) * whole;
        var fadeOut = o.inside ? Math.max(0, 1 - (o.y - RIM) / 0.35) : 1;
        for (var tr = 0; tr < 6; tr++) {
          project(o.x, o.y - tr * 0.04, o.z, p);
          ctx.globalAlpha = fadeIn * fadeOut * (1 - tr / 6);
          ctx.fillStyle = tr ? colors.s2 : colors.signal;
          var ds = (4.2 - tr * 0.5) * p.f * unit;
          ctx.fillRect(p.x - ds / 2, p.y - ds / 2, ds, ds);
          if (p.y < lo) lo = p.y;
        }
        project(o.x, o.y, o.z, p);
        var label = o.o.key, tw = ctx.measureText(label).width, lx = p.x + 12;
        if (lx + tw > W - 8) lx = p.x - 12 - tw;
        ctx.globalAlpha = o.inside ? 0 : fadeIn * Math.min(1, (RIM - o.y) / 0.3);
        ctx.fillStyle = colors.label;
        ctx.fillText(label, lx, p.y);
      });
      ctx.globalAlpha = 1;
      band = hi >= lo ? [Math.max(0, Math.floor(lo - 40)), Math.min(H, Math.ceil(hi + 40))] : [0, 0];
    }

    function frameStep(now) {
      var dt = Math.min(0.05, (now - (last || now)) / 1000);
      last = now;
      angle += dt * 0.2;
      tiltX += (tiltTarget - tiltX) * Math.min(1, dt * 3);
      yawNudge += (yawTarget - yawNudge) * Math.min(1, dt * 3);
      step(dt, now);
      draw((now - t0) / 1000, now);
      raf = requestAnimationFrame(frameStep);
    }
    function still() { drops = []; rings = []; draw(99, performance.now()); }
    function wipe() { ctx.clearRect(0, 0, W, H); band = [0, 0]; }

    // The loop runs while the points are in view. Past the window they are
    // gone and nothing is drawn; reduced motion and a hidden tab get one still pail.
    function follow(y) {
      var on = moving() && can;
      morph = on ? clamp((y - s0) / (s1 - s0)) : 0;
      var o = on ? inOut((y - sA) / (sB - sA)) : 1;
      shot = on ? o : 0;
      past = moving() && y > end;
      // The screenshot takes over from its outline; with reduced motion it is simply there.
      o = Math.round(o * 200) / 200;
      if (o !== shotSet) { shotSet = o; frame.style.opacity = o >= 1 ? '' : String(o); }
      if (!past && !document.hidden && moving()) {
        drawn = '';
        if (!raf) { if (!t0) t0 = performance.now(); last = 0; raf = requestAnimationFrame(frameStep); }
        return;
      }
      if (raf) { cancelAnimationFrame(raf); raf = 0; }
      var want = past ? 'none' : 'still';
      if (want === drawn) return;
      drawn = want;
      if (past) wipe(); else still();
    }

    readColors();
    measure();
    follow(window.scrollY || 0);
    requestAnimationFrame(function () { canvas.classList.add('is-on'); });
    ticks.push(function (y) { follow(y); });
    syncs.push(function () { follow(window.scrollY || 0); });
    document.addEventListener('pail:theme', function () { readColors(); drawn = ''; follow(window.scrollY || 0); });
    if ('ResizeObserver' in window) new ResizeObserver(function () { measure(); follow(window.scrollY || 0); }).observe(intro);
    window.addEventListener('resize', function () { measure(); });
    var hero = intro.querySelector('.hero');
    if (hero && window.matchMedia('(pointer: fine)').matches) {
      hero.addEventListener('pointermove', function (e) {
        var r = hero.getBoundingClientRect();
        tiltTarget = ((e.clientY - r.top) / r.height - 0.5) * -0.22;
        yawTarget = ((e.clientX - r.left) / r.width - 0.5) * 0.5;
      });
      hero.addEventListener('pointerleave', function () { tiltTarget = 0; yawTarget = 0; });
    }
    // A press on the pail drops the next object at once.
    stage.addEventListener('pointerdown', function () { nextDrop = 0; });
  })();

  /* ---------------- features: one window, played by the scroll position ---------------- */

  // Each view is a function of how far its chapter has scrolled, from 0 to 1.
  // It writes only what changed, so scrolling back plays the view backwards.
  var scenes = {
    // Type in the folder filter, open the folder that is left, select an object.
    objects: function (el) {
      var q = el.querySelector('[data-ob-q]'), word = q.getAttribute('data-q') || '';
      var rows = all('.ob-root .ob-row', el), names = rows.map(function (r) { return (r.getAttribute('data-name') || '').toLowerCase(); });
      var typed = -1, left = rows.length, stage = -1, status = -1;
      return function (a) {
        var s = a < 0.38 ? 0 : a < 0.5 ? 1 : a < 0.72 ? 2 : 3;
        var n = s < 2 ? Math.max(0, Math.min(word.length, Math.floor((a - 0.02) / 0.08))) : 0;
        if (n !== typed) {
          typed = n; left = 0;
          var t = word.slice(0, n).toLowerCase(), gone = 0;
          q.textContent = word.slice(0, n);
          // rows that do not match fade out and the rest move up, as transforms
          rows.forEach(function (r, i) {
            var hit = names[i].indexOf(t) >= 0;
            r.classList.toggle('is-out', !hit);
            r.style.setProperty('--up', gone);
            if (hit) left++; else gone++;
          });
        }
        var st = s === 3 ? 4 : s === 2 ? 3 : left === rows.length ? 0 : left > 1 ? 1 : 2;
        if (s !== stage) { stage = s; el.setAttribute('data-s', s); }
        if (st !== status) { status = st; el.setAttribute('data-st', st); }
      };
    },
    // A multipart upload fills part by part; a download finishes and is checked.
    transfers: function (el) {
      var row = el.querySelector('[data-parts]');
      var parts = all('.tr-parts i', row), done = row.querySelector('[data-parts-done]'), rate = row.querySelector('[data-parts-rate]');
      var size = rate.textContent, unit = row.getAttribute('data-rate') || '', total = parts.length;
      var dl = el.querySelector('[data-dl]'), bar = dl.querySelector('[data-dl-bar]');
      var checking = dl.querySelector('[data-dl-run]'), checked = dl.querySelector('[data-dl-done]');
      var key = -1, got = -1, phase = -1;
      return function (a) {
        var n = Math.floor(clamp((a - 0.06) / 0.66) * total), up = a > 0.03 && n < total;
        var k = n * 2 + (up ? 1 : 0);
        if (k !== key) {
          key = k;
          parts.forEach(function (p, i) { p.classList.toggle('is-done', i < n); p.classList.toggle('is-up', up && i >= n && i < n + 3); });
          done.textContent = n;
          rate.textContent = n >= total ? size : (n ? 150 + (n * 37) % 60 : '—') + ' ' + unit;
        }
        var p = Math.round(clamp((a - 0.1) / 0.36) * 100);
        if (p !== got) { got = p; bar.style.setProperty('--p', (p / 100).toFixed(2)); }
        var ph = a < 0.46 ? 0 : a < 0.58 ? 1 : 2;
        if (ph !== phase) { phase = ph; checking.hidden = ph !== 1; checked.hidden = ph !== 2; dl.classList.toggle('is-run', ph !== 2); }
      };
    },
    // Pick an old version, restore it, and it becomes the latest.
    versions: function (el) {
      var stage = -1;
      return function (a) {
        var s = a < 0.18 ? 0 : a < 0.42 ? 1 : a < 0.5 ? 2 : a < 0.78 ? 3 : 4;
        if (s !== stage) { stage = s; el.setAttribute('data-s', s); }
      };
    },
    // Change how long the link is valid: the URL is signed again, then copied.
    links: function (el) {
      var sig = el.querySelector('[data-sig]'), exp = el.querySelector('[data-exp]'), clock = el.querySelector('[data-clock]');
      var presets = all('[data-presets] b', el), copy = el.querySelector('[data-copy]');
      var to = sig.textContent, from = sig.getAttribute('data-before') || to, hex = '0123456789abcdef';
      var stage = -1, signed = -2, pressed = -1;
      return function (a) {
        var s = a < 0.16 ? 0 : 1;
        if (s !== stage) {
          stage = s;
          presets.forEach(function (b, i) { b.classList.toggle('is-on', i === s); });
          exp.textContent = s ? '3600' : '900';
          clock.textContent = s ? '60:00' : '15:00';
        }
        var k = s ? Math.round(clamp((a - 0.16) / 0.3) * to.length) : -1;
        if (k !== signed) {
          signed = k;
          var out = k < 0 ? from : to.slice(0, k);
          for (var c = out.length; c < to.length; c++) out += hex[(c * 7 + k * 13) % 16];
          sig.textContent = out;
        }
        var c2 = a < 0.6 ? 0 : a < 0.66 ? 1 : 2;
        if (c2 !== pressed) { pressed = c2; copy.classList.toggle('is-press', c2 === 1); copy.classList.toggle('is-done', c2 === 2); }
      };
    },
    // The plan lists every change; then the sync runs and each line is done.
    sync: function (el) {
      var lines = all('.sy-plan li', el), sum = el.querySelector('.sy-sum'), run = el.querySelector('[data-run]'), key = '';
      return function (a) {
        var shown = a < 0.08 ? 0 : Math.min(lines.length, Math.floor((a - 0.08) / 0.09) + 1);
        var done = a < 0.56 ? 0 : Math.min(lines.length, Math.floor((a - 0.56) / 0.09) + 1);
        var k = shown + ':' + done + ':' + (a >= 0.35) + ':' + (a >= 0.5 && a < 0.56);
        if (k === key) return;
        key = k;
        lines.forEach(function (l, i) { l.classList.toggle('is-shown', i < shown); l.classList.toggle('is-done', i < done); });
        sum.classList.toggle('is-off', a < 0.35);
        run.classList.toggle('is-press', a >= 0.5 && a < 0.56);
      };
    }
  };

  // Wide screens: the views sit in one window that stays in place, the chapter
  // at the middle of the screen is the open one, and its scroll progress plays
  // it. Narrow screens: every chapter keeps its view under its text and plays
  // it as it comes up the screen. Reduced motion shows the finished views.
  (function () {
    var story = document.querySelector('[data-story]');
    if (!story) return;
    var stage = story.querySelector('.story-stage'), win = stage.querySelector('.win'), body = stage.querySelector('.win-body');
    var prog = stage.querySelector('.win-prog i');
    var wide = window.matchMedia('(min-width: 1000px) and (min-height: 600px)');
    var items = all('.chapter', story).map(function (el) {
      var pane = el.querySelector('.pane'), name = pane.getAttribute('data-pane');
      return { el: el, home: el.querySelector('.chapter-pane'), pane: pane, tab: stage.querySelector('[data-tab="' + name + '"]'),
        render: (scenes[name] || function () { return function () {}; })(pane), a: -1, top: 0, h: 0, from: 0, to: 1, ph: 0 };
    });
    var staged = false, active = -1, bar = 60, shownProg = -1, queued = false;

    function paint(it, a) {
      a = Math.round(a * 400) / 400;
      if (a !== it.a) { it.a = a; it.render(a); }
    }
    function activate(n) {
      active = n;
      items.forEach(function (it, i) {
        it.pane.classList.toggle('is-on', i === n);
        it.pane.classList.toggle('is-before', i < n);
        it.el.classList.toggle('is-active', i === n);
        if (it.tab) { if (i === n) it.tab.setAttribute('aria-current', 'true'); else it.tab.removeAttribute('aria-current'); }
      });
      body.style.height = items[n].ph + 'px';
    }
    function measure() {
      queued = false;
      var y = window.scrollY || 0, vh = window.innerHeight || 1, r = story.getBoundingClientRect();
      var top = r.top + y, bottom = top + r.height;
      bar = parseFloat(css('--bar')) || 60;
      if (staged) {
        // The tallest view sets the room the window is given, which moves the
        // chapters: set it first, then read where they are.
        var mid = bar + (vh - bar) / 2, max = 0;
        items.forEach(function (it) { it.ph = it.pane.offsetHeight; if (it.ph > max) max = it.ph; });
        story.style.setProperty('--slot', (max + win.offsetHeight - body.offsetHeight) + 'px');
        if (active >= 0) body.style.height = items[active].ph + 'px';
        r = story.getBoundingClientRect(); top = r.top + y; bottom = top + r.height;
        items.forEach(function (it) {
          var c = it.el.getBoundingClientRect();
          it.top = c.top + y; it.h = c.height;
          it.from = Math.max(top - bar, it.top - mid);
          it.to = Math.max(it.from + 1, Math.min(bottom - vh, it.top + it.h - mid));
        });
      } else {
        items.forEach(function (it) { var c = it.home.getBoundingClientRect(); it.top = c.top + y; it.h = c.height; });
      }
      run(y, vh);
    }
    function remeasure() { if (!queued) { queued = true; requestAnimationFrame(measure); } }
    function place() {
      if (wide.matches !== staged) {
        staged = wide.matches;
        items.forEach(function (it) {
          (staged ? body : it.home).appendChild(it.pane);
          it.pane.classList.remove('is-on', 'is-before');
          it.el.classList.remove('is-active');
        });
        story.classList.toggle('is-live', staged);
        active = -1;
        if (!staged) { body.style.height = ''; story.style.removeProperty('--slot'); }
      }
      measure();
    }
    function run(y, vh) {
      var on = moving();
      if (staged) {
        var c = y + bar + (vh - bar) / 2, n = 0;
        for (var i = 1; i < items.length; i++) if (c >= items[i].top) n = i;
        if (n !== active) activate(n);
        var it = items[n], a = clamp((y - it.from) / (it.to - it.from));
        paint(it, on ? Math.min(1, a * 1.2) : 1);
        a = on ? Math.round(a * 200) / 200 : 0;
        if (a !== shownProg) { shownProg = a; prog.style.transform = 'scaleX(' + a + ')'; }
        return;
      }
      items.forEach(function (it) {
        if (!on || it.top + it.h < y) paint(it, 1);
        else if (it.top > y + vh) paint(it, 0);
        else paint(it, clamp((y + 0.8 * vh - it.top) / (0.5 * vh)));
      });
    }

    place();
    ticks.push(run);
    if (wide.addEventListener) wide.addEventListener('change', place); else if (wide.addListener) wide.addListener(place);
    window.addEventListener('resize', remeasure);
    if ('ResizeObserver' in window) {
      var ro = new ResizeObserver(remeasure);
      ro.observe(document.body);
      items.forEach(function (it) { ro.observe(it.pane); });
    }
    // The feature's window is the one its guide page opens with: name it for
    // the page transition as the link is followed.
    story.addEventListener('click', function (e) {
      var link = e.target.closest ? e.target.closest('.chapter-text a') : null;
      if (!link) return;
      var mine = items.filter(function (it) { return it.el.contains(link); })[0];
      items.forEach(function (it) { it.home.style.viewTransitionName = ''; });
      win.style.viewTransitionName = '';
      if (staged) win.style.viewTransitionName = 'feature-win';
      else if (mine) mine.home.style.viewTransitionName = 'feature-win';
    });
  })();

  // The header marks the section the page is in.
  (function () {
    if (!('IntersectionObserver' in window)) return;
    var pairs = all('.bar-nav a').map(function (a) {
      var id = (a.getAttribute('href') || '').split('#')[1];
      var el = id ? document.getElementById(id) : null;
      return el ? { a: a, el: el } : null;
    }).filter(Boolean);
    if (!pairs.length) return;
    var io = new IntersectionObserver(function (es) {
      es.forEach(function (e) {
        var p = pairs.filter(function (x) { return x.el === e.target; })[0];
        if (!p) return;
        if (e.isIntersecting) p.a.setAttribute('aria-current', 'true'); else p.a.removeAttribute('aria-current');
      });
    }, { rootMargin: '-45% 0px -55% 0px' });
    pairs.forEach(function (p) { io.observe(p.el); });
  })();

  /* ---------------- the other features, filtered as you type ---------------- */

  (function () {
    var box = document.querySelector('[data-cmdk]');
    if (!box) return;
    var field = box.querySelector('.cmdk-field'), input = field.querySelector('input');
    var items = all('.cmdk-list li', box), empty = box.querySelector('.cmdk-empty');
    field.hidden = false;
    input.addEventListener('input', function () {
      var q = input.value.trim().toLowerCase(), shown = 0;
      items.forEach(function (li) {
        var hit = !q || li.textContent.toLowerCase().indexOf(q) >= 0;
        li.hidden = !hit;
        if (hit) shown++;
      });
      empty.textContent = shown ? '' : empty.getAttribute('data-none');
    });
  })();

  /* ---------------- terminal ---------------- */

  (function () {
    var term = document.querySelector('[data-term]');
    var data = document.getElementById('cli-demo');
    if (!term || !data) return;
    var demo;
    try { demo = JSON.parse(data.textContent); } catch (e) { return; }
    var tabs = [].slice.call(term.querySelectorAll('[data-example]'));
    var out = document.getElementById('term-out');
    var code = out.querySelector('code');
    var list = term.querySelector('.term-tabs');
    var current = 0, timers = [], touched = false, auto = 0, visible = false;
    if (list) list.hidden = false;

    // termKind matches termClass in tools/site/clidemo.go.
    function termKind(l) {
      var c = 't-out';
      if (l.kind === 'cmd') c = 't-cmd';
      else if (l.kind === 'err') c = 't-dim';
      else if (/^(upload|download)\s/.test(l.text)) c = 't-ok';
      else if (/^delete\s/.test(l.text)) c = 't-del';
      else if (/^dry run:/.test(l.text)) c = 't-dim';
      if (!/\S {2,}\S/.test(l.text)) c += ' t-wrap';
      return c;
    }
    function lineEl(l, text) {
      var row = document.createElement('span');
      row.className = 't-line';
      if (l.kind === 'cmd') {
        var pr = document.createElement('span');
        pr.className = 't-prompt'; pr.textContent = '$';
        row.appendChild(pr); row.appendChild(document.createTextNode(' '));
      }
      var body = document.createElement('span');
      body.className = termKind(l);
      body.textContent = text === undefined ? l.text : text;
      row.appendChild(body);
      return row;
    }
    function clear() { timers.forEach(clearTimeout); timers = []; clearTimeout(auto); }
    function later(ms, fn) { timers.push(setTimeout(fn, ms)); }
    function play(n, animate) {
      clear();
      var ex = demo.examples[n];
      current = n;
      tabs.forEach(function (b, i) { b.setAttribute('aria-selected', String(i === n)); b.tabIndex = i === n ? 0 : -1; });
      out.setAttribute('aria-labelledby', tabs[n] ? tabs[n].id : '');
      code.textContent = '';
      out.scrollTop = 0;
      if (!animate) {
        ex.lines.forEach(function (l) { code.appendChild(lineEl(l)); code.appendChild(document.createTextNode('\n')); });
        return;
      }
      var cmd = ex.lines[0];
      var row = lineEl(cmd, '');
      var body = row.lastChild;
      var cursor = document.createElement('span');
      cursor.className = 't-cursor';
      row.appendChild(cursor);
      code.appendChild(row);
      code.appendChild(document.createTextNode('\n'));
      var per = Math.max(8, Math.min(32, 1300 / cmd.text.length)), t = 250;
      for (var c = 1; c <= cmd.text.length; c++) {
        (function (c) { later(t + c * per, function () { body.textContent = cmd.text.slice(0, c); }); })(c);
      }
      t += cmd.text.length * per + 320;
      ex.lines.slice(1).forEach(function (l, i) {
        later(t + i * 70, function () {
          if (cursor.parentNode) cursor.parentNode.removeChild(cursor);
          code.appendChild(lineEl(l));
          code.appendChild(document.createTextNode('\n'));
          out.scrollTop = out.scrollHeight;
        });
      });
      t += (ex.lines.length - 1) * 70;
      later(t + 40, function () {
        var end = lineEl({ kind: 'cmd', text: '' });
        end.appendChild(cursor);
        code.appendChild(end);
        if (!touched && visible && moving()) auto = setTimeout(function () { play((current + 1) % demo.examples.length, true); }, 3600);
      });
    }
    tabs.forEach(function (b, i) {
      b.addEventListener('click', function () { touched = true; play(i, !reduce.matches); });
      b.addEventListener('keydown', function (e) {
        var n = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 }[e.key];
        if (n === undefined) return;
        e.preventDefault();
        n = (n + tabs.length) % tabs.length;
        tabs[n].focus();
        touched = true;
        play(n, !reduce.matches);
      });
    });
    // The terminal keeps the height of its longest example at this width, so
    // switching examples never moves the page.
    function fit() {
      var probe = out.cloneNode(false), c = document.createElement('code');
      probe.removeAttribute('id');
      probe.setAttribute('aria-hidden', 'true');
      probe.style.cssText = 'position:absolute;visibility:hidden;height:auto;width:' + out.offsetWidth + 'px';
      probe.appendChild(c);
      term.appendChild(probe);
      var max = 0;
      demo.examples.forEach(function (ex) {
        c.textContent = '';
        ex.lines.forEach(function (l) { c.appendChild(lineEl(l)); c.appendChild(document.createTextNode('\n')); });
        c.appendChild(lineEl({ kind: 'cmd', text: '' }));
        max = Math.max(max, probe.offsetHeight);
      });
      term.removeChild(probe);
      out.style.height = max + 'px';
    }
    if ('ResizeObserver' in window) {
      var tw = 0;
      new ResizeObserver(function () { if (out.clientWidth !== tw) { tw = out.clientWidth; fit(); } }).observe(out);
    }
    var first = true;
    whileVisible(term, function () {
      visible = true;
      if (first) { first = false; play(0, true); }
      else if (!touched) play(current, true);
    }, function () {
      visible = false;
      clear();
      play(current, false);
    }, 0.4);
  })();

  /* ---------------- downloads ---------------- */

  (function () {
    var grid = document.querySelector('[data-downloads]');
    if (!grid) return;
    var bases = { github: grid.getAttribute('data-release-base'), mirror: grid.getAttribute('data-mirror-base') };
    var heroBtn = document.querySelector('[data-hero-download]');
    var heroLabel = document.querySelector('[data-hero-download-label]');

    var ua = navigator.userAgent || '';
    var plat = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || '';
    var os = '';
    if (/Android|iPhone|iPad|iPod/i.test(ua)) os = '';
    else if (/Win/i.test(plat) || /Windows/.test(ua)) os = 'windows';
    else if (/Mac/i.test(plat) || /Mac OS X/.test(ua)) os = 'macos';
    else if (/Linux|X11|CrOS/i.test(plat + ua)) os = 'linux';
    var mine = os && grid.querySelector('[data-os="' + os + '"]');
    var usual = mine && mine.querySelector('[data-usual]');
    if (mine) {
      mine.classList.add('is-mine');
      if (heroLabel && S.heroDownloadFor) heroLabel.textContent = S.heroDownloadFor.replace('{os}', mine.querySelector('h3').textContent);
    }

    function apply(source) {
      var base = bases[source] || bases.github;
      document.querySelectorAll('a[data-file]').forEach(function (a) { a.href = base + a.getAttribute('data-file'); });
      if (heroBtn && usual) heroBtn.href = usual.href;
    }
    var box = document.querySelector('[data-source]');
    var source = read('pail-source') === 'mirror' && bases.mirror ? 'mirror' : 'github';
    if (box && bases.mirror) {
      box.hidden = false;
      var note = document.querySelector('[data-source-note]');
      var radios = box.querySelectorAll('input[name="source"]');
      var set = function (s) {
        source = s;
        radios.forEach(function (r) { r.checked = r.value === s; });
        if (note) note.textContent = s === 'mirror' ? S.sourceMirrorNote : S.sourceGithubNote;
        apply(s);
      };
      radios.forEach(function (r) { r.addEventListener('change', function () { if (r.checked) { store('pail-source', r.value); set(r.value); } }); });
      set(source);
    } else {
      apply('github');
    }
  })();
})();
