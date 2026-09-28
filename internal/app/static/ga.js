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
})();
