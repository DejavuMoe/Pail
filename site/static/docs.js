/* Documentation pages: highlight the "On this page" entry for the h2 currently in view. */
(function () {
  'use strict';

  var toc = document.querySelector('.docs-toc');
  if (!toc || !('IntersectionObserver' in window)) return;

  var items = Array.prototype.slice.call(toc.querySelectorAll('a[href^="#"]')).map(function (a) {
    return { a: a, h: document.getElementById(decodeURIComponent(a.getAttribute('href').slice(1))) };
  }).filter(function (x) { return x.h; });
  if (!items.length) return;

  // Distance from the viewport top at which a heading counts as "reached":
  // sticky header (64px) + the 24px scroll margin + a little slack.
  var LINE = 120;
  var current = null;

  function set(next) {
    if (next === current) return;
    if (current) { current.a.classList.remove('is-active'); current.a.removeAttribute('aria-current'); }
    current = next;
    if (current) { current.a.classList.add('is-active'); current.a.setAttribute('aria-current', 'location'); }
  }

  function update() {
    var active = null;
    items.forEach(function (x) { if (x.h.getBoundingClientRect().top <= LINE) active = x; });
    // A last section too short to reach the line still counts once the page ends.
    if (window.scrollY > 0 && window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2) {
      active = items[items.length - 1];
    }
    set(active);
  }

  // Each heading crossing the line (threshold 1 = fully inside the shrunken root) and the
  // footer becoming fully visible trigger a recompute; nothing runs on every scroll event.
  var io = new IntersectionObserver(update, { rootMargin: '-' + LINE + 'px 0px 0px 0px', threshold: [0, 1] });
  items.forEach(function (x) { io.observe(x.h); });
  var footer = document.querySelector('.site-footer');
  if (footer) io.observe(footer);

  toc.addEventListener('click', function (e) {
    var a = e.target.closest('a[href^="#"]');
    if (!a) return;
    items.forEach(function (x) { if (x.a === a) set(x); });
  });
  window.addEventListener('hashchange', update);
  window.addEventListener('load', update);
  update();
})();
