/* agent-bus site: theme toggle, copy buttons, reveal on scroll. No network, no dependencies. */
(function () {
  "use strict";
  var root = document.documentElement;
  root.classList.add("js");

  // Theme: the stored choice wins, else the system's (the CSS follows prefers-color-scheme alone).
  var KEY = "agentbus-theme";
  function stored() { try { return localStorage.getItem(KEY); } catch (e) { return null; } }
  function store(v) { try { localStorage.setItem(KEY, v); } catch (e) { /* private window: no memory, still works */ } }
  var s = stored();
  if (s === "light" || s === "dark") root.setAttribute("data-theme", s);

  function effective() {
    var t = root.getAttribute("data-theme");
    if (t) return t;
    return window.matchMedia && matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  var btn = document.getElementById("theme");
  if (btn) {
    var sync = function () { btn.setAttribute("aria-pressed", effective() === "dark" ? "true" : "false"); };
    sync();
    btn.addEventListener("click", function () {
      var next = effective() === "dark" ? "light" : "dark";
      root.setAttribute("data-theme", next);
      store(next);
      sync();
    });
  }

  // Copy buttons: data-copy holds the exact text.
  Array.prototype.forEach.call(document.querySelectorAll("[data-copy]"), function (b) {
    b.addEventListener("click", function () {
      var text = b.getAttribute("data-copy");
      var done = b.getAttribute("data-done") || "copied";
      var label = b.textContent;
      var ok = function () { b.textContent = done; setTimeout(function () { b.textContent = label; }, 1600); };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(ok, function () { fallback(text, ok); });
      } else { fallback(text, ok); }
    });
  });
  function fallback(text, ok) {
    try {
      var t = document.createElement("textarea");
      t.value = text; t.setAttribute("readonly", ""); t.style.position = "fixed"; t.style.opacity = "0";
      document.body.appendChild(t); t.select(); document.execCommand("copy"); document.body.removeChild(t); ok();
    } catch (e) { /* nothing to do: the text is on screen */ }
  }

  // Reveal on scroll. Without IntersectionObserver, or with reduced motion, everything is shown.
  var items = document.querySelectorAll(".reveal");
  var calm = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (!("IntersectionObserver" in window) || calm) {
    Array.prototype.forEach.call(items, function (e) { e.classList.add("in"); });
    return;
  }
  var io = new IntersectionObserver(function (es) {
    es.forEach(function (e) { if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); } });
  }, { rootMargin: "0px 0px -8% 0px", threshold: 0.06 });
  Array.prototype.forEach.call(items, function (e) { io.observe(e); });
})();
