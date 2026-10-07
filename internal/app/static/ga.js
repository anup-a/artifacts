// Starts GA4. It is a file rather than an inline <script> because the
// Content-Security-Policy allows no inline script, and an inline snippet
// was silently blocked: gtag.js loaded but was never told to send a hit.
// The Measurement ID comes from this tag's data-ga-id attribute.
(function () {
  var id = document.currentScript && document.currentScript.getAttribute("data-ga-id");
  if (!id) return;
  window.dataLayer = window.dataLayer || [];
  // gtag.js reads the Arguments object itself, not an array copy of it.
  window.gtag = function () { window.dataLayer.push(arguments); };
  window.gtag("js", new Date());
  window.gtag("config", id);

  // The public pages' one action is copying the prompt, and no page loads
  // after it, so GA would never see it. Every button that copies the prompt
  // carries data-ga-copy; the nearest data-ga-where says which spot it is
  // (bar, hero, try, close, showcase), so spots can be compared.
  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest && ev.target.closest("[data-ga-copy]");
    if (!btn) return;
    var where = btn.closest("[data-ga-where]");
    window.gtag("event", "copy_prompt", { placement: where ? where.getAttribute("data-ga-where") : "other" });
  });
})();
