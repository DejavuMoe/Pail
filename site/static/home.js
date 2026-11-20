// pail.download landing page: the point-cloud pail, the window regions, the
// feature tour, the feature filter, the terminal and the download picker.
// Without this file every pane shows its finished state inside its section.
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

  // A timeline of [delay ms, fn] steps that loops until stopped.
  function sequence(steps, loop) {
    var timer = 0, i = 0, alive = false;
    function next() {
      if (!alive) return;
      if (i >= steps.length) { if (!loop) return; i = 0; }
      var s = steps[i++];
      timer = setTimeout(function () { s[1](); next(); }, s[0]);
    }
    return {
      start: function () { if (alive) return; alive = true; i = 0; next(); },
      stop: function () { alive = false; clearTimeout(timer); }
    };
  }
  function press(el, then) {
    el.classList.add('is-press');
    setTimeout(function () { el.classList.remove('is-press'); if (then) then(); }, 160);
  }

  /* ---------------- hero: objects falling into a pail of points ---------------- */

  (function () {
    var canvas = document.querySelector('[data-pail]');
    if (!canvas || !canvas.getContext) return;
    var ctx = canvas.getContext('2d');
    var log = canvas.parentNode.querySelector('.drop-log');
    var objects = (canvas.getAttribute('data-objects') || '').split('|').filter(Boolean).map(function (s) {
      var a = s.split('~');
      return { key: a[0], size: a[1] || '' };
    });
    var W = 0, H = 0, dpr = 1, colors = {}, mono = '';
    var pts = [], drops = [], rings = [], t0 = 0, last = 0, raf = 0, angle = 0.6, nextDrop = 0, flash = -1e9, pick = 0;
    var tiltX = 0, tiltTarget = 0, yawNudge = 0, yawTarget = 0;

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
        d: rnd() * 0.55 + (y + 0.6) * 0.35 });
    }
    bands.forEach(function (band, bi) {
      var rows = 8;
      for (var k = 0; k < rows; k++) {
        var u = band[0] + (band[1] - band[0]) * (k / (rows - 1));
        var r = radiusAt(u), n = Math.round(r * 112);
        var off = (k % 2) * Math.PI / n;
        for (var j = 0; j < n; j++) {
          var a = off + j / n * Math.PI * 2;
          add(Math.cos(a) * r, RIM + u * HEIGHT, Math.sin(a) * r, band[2], k === 0 || k === rows - 1 ? 1.25 : 1, bi === 0 && k < 2);
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

    function readColors() {
      colors = { s1: css('--s1'), s2: css('--s2'), s3: css('--s3'), text: css('--text-3'), label: css('--text-2'), signal: css('--signal') };
      mono = css('--mono');
    }
    function resize() {
      var r = canvas.getBoundingClientRect();
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      W = Math.max(1, Math.round(r.width)); H = Math.max(1, Math.round(r.height));
      canvas.width = Math.round(W * dpr); canvas.height = Math.round(H * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    }
    function ease(x) { return x <= 0 ? 0 : x >= 1 ? 1 : 1 - Math.pow(1 - x, 4); }

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

    // Objects fall, cross the rim, and show up in the log.
    function step(dt, now) {
      if (now - t0 < 1800 || !objects.length) return;
      if (now >= nextDrop) {
        nextDrop = now + 1500 + Math.random() * 500;
        var a = Math.random() * Math.PI * 2, r = Math.sqrt(Math.random()) * TOP * 0.55;
        drops.push({ o: objects[pick++ % objects.length], x: Math.cos(a) * r, z: Math.sin(a) * r, y: -1.6, v: 0.15, inside: false });
      }
      for (var i = drops.length - 1; i >= 0; i--) {
        var p = drops[i];
        p.v += dt * 1.1;
        p.y += p.v * dt;
        if (!p.inside && p.y > RIM) { p.inside = true; flash = now; rings.push({ x: p.x, z: p.z, t: now }); logRow(p.o); }
        if (p.y > HEIGHT * 0.2) drops.splice(i, 1);
      }
      rings = rings.filter(function (g) { return now - g.t < 900; });
    }

    function draw(el, now) {
      ctx.clearRect(0, 0, W, H);
      var scale = Math.min(W * 0.36, H * 0.33);
      var cx = W / 2, cy = H * 0.58;
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
      var p = {};
      var glow = Math.max(0, 1 - (now - flash) / 600);
      for (var i = 0; i < pts.length; i++) {
        var q = pts[i];
        var k = ease((el - q.d) / 1.5);
        var x = q.sx + (q.x - q.sx) * k, y = q.sy + (q.y - q.sy) * k, z = q.sz + (q.z - q.sz) * k;
        project(x, y, z, p);
        var front = Math.max(0, Math.min(1, 0.5 - p.z * 0.75));
        var lit = q.rim ? glow : 0;
        ctx.globalAlpha = Math.min(1, (0.22 + 0.78 * front) * (0.35 + 0.65 * k) + lit * 0.5);
        ctx.fillStyle = lit > 0.3 ? colors.signal : colors[q.c];
        var s = (1.15 + front * 1.25 + lit * 1.2) * q.s * p.f * unit;
        ctx.fillRect(p.x - s / 2, p.y - s / 2, s, s);
      }
      // a ring where each object goes in
      rings.forEach(function (g) {
        var age = (now - g.t) / 900, rad = 0.06 + age * 0.4;
        ctx.fillStyle = colors.s1;
        for (var n = 0; n < 28; n++) {
          var a = n / 28 * Math.PI * 2;
          project(g.x + Math.cos(a) * rad, RIM + 0.02, g.z + Math.sin(a) * rad, p);
          ctx.globalAlpha = (1 - age) * 0.9;
          ctx.fillRect(p.x - 1.2 * unit, p.y - 1.2 * unit, 2.4 * unit, 2.4 * unit);
        }
      });
      // falling objects, each with its key
      ctx.font = '500 ' + Math.round(11 + unit) + 'px ' + mono;
      ctx.textBaseline = 'middle';
      drops.forEach(function (o) {
        var fadeIn = Math.min(1, (o.y + 1.6) / 0.3);
        var fadeOut = o.inside ? Math.max(0, 1 - (o.y - RIM) / 0.35) : 1;
        for (var tr = 0; tr < 6; tr++) {
          project(o.x, o.y - tr * 0.04, o.z, p);
          ctx.globalAlpha = fadeIn * fadeOut * (1 - tr / 6);
          ctx.fillStyle = tr ? colors.s2 : colors.signal;
          var ds = (4.2 - tr * 0.5) * p.f * unit;
          ctx.fillRect(p.x - ds / 2, p.y - ds / 2, ds, ds);
        }
        project(o.x, o.y, o.z, p);
        var label = o.o.key, tw = ctx.measureText(label).width, lx = p.x + 12;
        if (lx + tw > W - 8) lx = p.x - 12 - tw;
        ctx.globalAlpha = o.inside ? 0 : fadeIn * Math.min(1, (RIM - o.y) / 0.3);
        ctx.fillStyle = colors.label;
        ctx.fillText(label, lx, p.y);
      });
      ctx.globalAlpha = 1;
    }

    function frame(now) {
      var dt = Math.min(0.05, (now - (last || now)) / 1000);
      last = now;
      angle += dt * 0.2;
      tiltX += (tiltTarget - tiltX) * Math.min(1, dt * 3);
      yawNudge += (yawTarget - yawNudge) * Math.min(1, dt * 3);
      step(dt, now);
      draw((now - t0) / 1000, now);
      raf = requestAnimationFrame(frame);
    }
    function start() { if (raf) return; last = 0; raf = requestAnimationFrame(frame); }
    function stop() {
      cancelAnimationFrame(raf);
      raf = 0; drops = []; rings = [];
      draw(99, performance.now());
    }

    readColors();
    resize();
    t0 = performance.now();
    draw(moving() ? 0 : 99, t0);
    requestAnimationFrame(function () { canvas.classList.add('is-on'); });
    whileVisible(canvas, start, stop);
    document.addEventListener('pail:theme', function () { readColors(); if (!raf) draw(99, performance.now()); });
    if ('ResizeObserver' in window) new ResizeObserver(function () { resize(); if (!raf) draw(99, performance.now()); }).observe(canvas);
    var hero = canvas.closest('.hero');
    if (hero && window.matchMedia('(pointer: fine)').matches) {
      hero.addEventListener('pointermove', function (e) {
        var r = hero.getBoundingClientRect();
        tiltTarget = ((e.clientY - r.top) / r.height - 0.5) * -0.22;
        yawTarget = ((e.clientX - r.left) / r.width - 0.5) * 0.5;
      });
      hero.addEventListener('pointerleave', function () { tiltTarget = 0; yawTarget = 0; });
    }
  })();

  /* ---------------- features: one window, four views ---------------- */

  // Each pane plays what the feature does; stop() leaves the finished state.
  var panes = [
    function (el) {
      var row = el.querySelector('[data-parts]');
      var parts = all('.tr-parts i', row), done = row.querySelector('[data-parts-done]'), rate = row.querySelector('[data-parts-rate]');
      var size = rate.textContent, total = parts.length, next = 0, count = 0, timers = [], alive = false;
      var dl = el.querySelector('[data-dl]'), bar = dl.querySelector('[data-dl-bar]');
      var checking = dl.querySelector('[data-dl-run]'), checked = dl.querySelector('[data-dl-done]');
      function launch() {
        if (!alive || next >= total) return;
        var li = parts[next++], dur = 900 + Math.random() * 1000;
        li.classList.add('is-up');
        timers.push(setTimeout(function () {
          li.classList.remove('is-up'); li.classList.add('is-done');
          done.textContent = ++count;
          if (count === total) { rate.textContent = size; timers.push(setTimeout(cycle, 2600)); return; }
          rate.textContent = (150 + Math.round(Math.random() * 60)) + ' MiB/s';
          launch();
        }, dur));
      }
      function download() {
        var p = 0;
        checking.hidden = true; checked.hidden = true;
        dl.classList.add('is-run');
        bar.style.setProperty('--p', '0');
        (function tick() {
          if (!alive) return;
          p = Math.min(1, p + 0.08 + Math.random() * 0.06);
          bar.style.setProperty('--p', p.toFixed(3));
          if (p < 1) timers.push(setTimeout(tick, 280));
          else {
            checking.hidden = false;
            timers.push(setTimeout(function () { checking.hidden = true; checked.hidden = false; dl.classList.remove('is-run'); }, 900));
          }
        })();
      }
      function cycle() {
        if (!alive) return;
        next = 0; count = 0; done.textContent = '0';
        parts.forEach(function (li) { li.classList.remove('is-done', 'is-up'); });
        rate.textContent = '— MiB/s';
        timers.push(setTimeout(function () { for (var k = 0; k < 4; k++) launch(); }, 400));
        download();
      }
      return {
        start: function () { if (alive) return; alive = true; cycle(); },
        stop: function () {
          alive = false;
          timers.forEach(clearTimeout); timers = [];
          parts.forEach(function (li) { li.classList.remove('is-up'); li.classList.add('is-done'); });
          done.textContent = String(total); rate.textContent = size;
          bar.style.removeProperty('--p'); checking.hidden = true; checked.hidden = false; dl.classList.remove('is-run');
        }
      };
    },
    function (el) {
      var fresh = el.querySelector('.vr-new'), pick = el.querySelector('.vr-pick');
      var seq = sequence([
        [0, function () { fresh.classList.remove('is-shown'); pick.classList.remove('is-pick'); }],
        [1200, function () { pick.classList.add('is-pick'); }],
        [1100, function () { press(pick); }],
        [300, function () { fresh.classList.add('is-shown'); }],
        [900, function () { pick.classList.remove('is-pick'); }],
        [3200, function () {}]
      ], true);
      return { start: seq.start, stop: function () { seq.stop(); fresh.classList.add('is-shown'); pick.classList.remove('is-pick'); } };
    },
    function (el) {
      var sig = el.querySelector('[data-sig]'), clock = el.querySelector('[data-clock]'), meter = el.querySelector('[data-clock-bar]');
      var copy = el.querySelector('[data-copy]');
      var hex = '0123456789abcdef', life = 3600, left = life, tick = 0, timer = 0, raf = 0, later = 0, alive = false;
      var original = sig.textContent;
      function show() {
        clock.textContent = String(Math.floor(left / 60)).padStart(2, '0') + ':' + String(left % 60).padStart(2, '0');
        meter.style.setProperty('--left', (left / life).toFixed(4));
      }
      function sign() {
        var to = '', s0 = performance.now();
        for (var n = 0; n < original.length; n++) to += hex[(Math.random() * 16) | 0];
        copy.classList.remove('is-done');
        (function step(t) {
          var k = Math.min(1, (t - s0) / 700), out = '';
          for (var c = 0; c < to.length; c++) out += c / to.length < k ? to[c] : hex[(Math.random() * 16) | 0];
          sig.textContent = out;
          if (k < 1) raf = requestAnimationFrame(step);
        })(s0);
        left = life; show();
        later = setTimeout(function () { press(copy, function () { if (alive) copy.classList.add('is-done'); }); }, 2200);
      }
      return {
        start: function () {
          if (alive) return;
          alive = true;
          sign();
          tick = setInterval(function () { left = Math.max(0, left - 1); show(); }, 1000);
          timer = setInterval(sign, 6000);
        },
        stop: function () {
          alive = false;
          clearInterval(tick); clearInterval(timer); clearTimeout(later); cancelAnimationFrame(raf);
          sig.textContent = original; left = life; show(); copy.classList.remove('is-done');
        }
      };
    },
    function (el) {
      var lines = all('.sy-plan li', el), sum = el.querySelector('.sy-sum'), runBtn = el.querySelector('[data-run]');
      function reset(on) {
        lines.forEach(function (l) { l.classList.toggle('is-shown', on); l.classList.remove('is-done'); });
        sum.style.opacity = on ? '' : '0';
      }
      var steps = [[0, function () { reset(false); }]];
      lines.forEach(function (l, n) { steps.push([n ? 450 : 600, function () { l.classList.add('is-shown'); }]); });
      steps.push([450, function () { sum.style.opacity = ''; }]);
      steps.push([1100, function () { press(runBtn); }]);
      lines.forEach(function (l) { steps.push([380, function () { l.classList.add('is-done'); }]); });
      steps.push([3000, function () {}]);
      var seq = sequence(steps, true);
      return { start: seq.start, stop: function () { seq.stop(); reset(true); } };
    }
  ];

  // The four features become one list beside one window. A feature opens on
  // a click; until then the next one opens every few seconds, and hovering or
  // focusing the tour holds it. On narrow screens the list is an accordion and
  // the window opens inside the feature, so nothing changes off screen.
  (function () {
    var tour = document.querySelector('[data-tour]');
    if (!tour) return;
    var steps = all('.step', tour), stage = tour.querySelector('.tour-stage');
    var win = stage.querySelector('.win'), body = stage.querySelector('.win-body'), title = stage.querySelector('.win-title');
    var narrow = window.matchMedia('(max-width: 1000px)');
    var players = [], views = [], buttons = [], active = -1, visible = false, touched = false;

    steps.forEach(function (s, i) {
      var pane = s.querySelector('.pane');
      body.appendChild(pane);
      views[i] = pane;
      players[i] = panes[i] ? panes[i](pane) : { start: function () {}, stop: function () {} };
      var h = s.querySelector('h3'), b = document.createElement('button');
      b.type = 'button';
      b.className = 'step-btn';
      b.setAttribute('aria-expanded', 'false');
      b.textContent = h.textContent;
      b.insertAdjacentHTML('beforeend', '<svg class="ic" aria-hidden="true"><use href="#i-chev"/></svg>');
      h.textContent = '';
      h.appendChild(b);
      b.addEventListener('click', function () {
        touched = true;
        // Keep the tapped heading where it was while the one above folds.
        var y = b.getBoundingClientRect().top;
        set(i);
        var dy = b.getBoundingClientRect().top - y;
        if (dy) window.scrollBy(0, dy);
      });
      buttons[i] = b;
    });
    tour.classList.add('has-stage');
    stage.hidden = false;

    // Wide screens: the stage keeps the height of the tallest view, so the
    // page never jumps. Narrow screens: the stage sits in the open feature.
    function place() {
      if (narrow.matches) {
        if (steps[active] && stage.parentNode !== steps[active]) steps[active].appendChild(stage);
        stage.style.removeProperty('--stage-h');
        return;
      }
      if (stage.parentNode !== tour) tour.appendChild(stage);
      var keep = active, max = 0;
      views.forEach(function (v, i) {
        views.forEach(function (w, k) { w.classList.toggle('is-on', k === i); });
        max = Math.max(max, win.offsetHeight);
      });
      views.forEach(function (w, k) { w.classList.toggle('is-on', k === keep); });
      stage.style.setProperty('--stage-h', max + 'px');
    }

    function run() {
      var go = visible && moving();
      players.forEach(function (p, i) { if (i !== active || !go) p.stop(); });
      if (go && players[active]) players[active].start();
      var auto = go && !touched && !narrow.matches;
      tour.classList.toggle('is-auto', auto);
      steps.forEach(function (s) { s.classList.remove('is-timing'); });
      if (auto && steps[active]) { void steps[active].offsetWidth; steps[active].classList.add('is-timing'); }
    }
    function set(n) {
      if (n === active || n < 0 || n >= steps.length) return;
      var from = body.offsetHeight;
      active = n;
      steps.forEach(function (s, i) { s.classList.toggle('is-active', i === n); buttons[i].setAttribute('aria-expanded', String(i === n)); });
      views.forEach(function (p, i) { p.classList.toggle('is-on', i === n); });
      title.textContent = steps[n].getAttribute('data-title');
      if (narrow.matches) { place(); run(); return; }
      if (from && moving()) {
        body.style.height = 'auto';
        var to = body.offsetHeight;
        body.style.height = from + 'px';
        void body.offsetHeight;
        body.style.height = to + 'px';
        clearTimeout(body._t);
        body._t = setTimeout(function () { body.style.height = ''; }, 450);
      }
      run();
    }
    tour.addEventListener('animationend', function (e) {
      if (e.animationName === 'step-time' && !touched) set((active + 1) % steps.length);
    });

    set(0);
    place();
    if ('ResizeObserver' in window) {
      var w0 = 0;
      new ResizeObserver(function () { if (tour.offsetWidth !== w0) { w0 = tour.offsetWidth; place(); run(); } }).observe(tour);
    }
    whileVisible(stage, function () { visible = true; run(); }, function () { visible = false; run(); }, 0.3);
    syncs.push(run);
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
