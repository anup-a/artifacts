// Starts GA4, but only after the visitor says yes. It is a file rather than
// an inline <script> because the Content-Security-Policy allows no inline
// script. The Measurement ID comes from this tag's data-ga-id attribute.
//
// GDPR and ePrivacy: GA sets cookies that are not needed to run the site, so
// gtag.js is not even fetched until the visitor accepts. Until then Google
// receives nothing. The choice is kept in a byagent_consent cookie on the
// parent domain, so byagent.dev and app.byagent.dev ask once between them.
// Declining (now or later, from "Cookie settings") removes any _ga cookies.
(function () {
  var t = window.byagentI18n.t;
  var tag = document.currentScript;
  var id = tag && tag.getAttribute("data-ga-id");
  if (!id) return;

  var NAME = "byagent_consent";
  var host = location.hostname;
  // byagent.dev for app.byagent.dev; nothing for localhost or an IP.
  var parent = /\.[a-z]+$/i.test(host) ? host.split(".").slice(-2).join(".") : "";
  var domain = parent ? "; domain=" + parent : "";

  function choice() {
    var m = document.cookie.match(/(?:^|; )byagent_consent=(yes|no)/);
    return m ? m[1] : "";
  }
  function remember(v) {
    var rest = "; path=/; max-age=" + 60 * 60 * 24 * 182 + "; samesite=lax" + (location.protocol === "https:" ? "; secure" : "");
    document.cookie = NAME + "=" + v + rest + domain;
    // A browser refuses a public suffix such as fly.dev; keep it on this host then.
    if (choice() !== v) document.cookie = NAME + "=" + v + rest;
  }
  function clearGA() {
    document.cookie.split("; ").forEach(function (c) {
      var name = c.split("=")[0];
      if (name !== "_ga" && name.indexOf("_ga_") !== 0) return;
      ["", "; domain=" + host, domain].forEach(function (d) {
        document.cookie = name + "=; path=/; max-age=0" + d;
      });
    });
  }

  var started = false;
  function start() {
    if (started) return;
    started = true;
    window.dataLayer = window.dataLayer || [];
    // gtag.js reads the Arguments object itself, not an array copy of it.
    window.gtag = function () { window.dataLayer.push(arguments); };
    window.gtag("js", new Date());
    window.gtag("config", id);
    var s = document.createElement("script");
    s.async = true;
    s.src = "https://www.googletagmanager.com/gtag/js?id=" + encodeURIComponent(id);
    document.head.appendChild(s);
  }

  var bar = null;
  function ask() {
    if (bar) return;
    bar = document.createElement("div");
    bar.className = "consent";
    bar.setAttribute("role", "region");
    bar.setAttribute("aria-label", t("Cookie choice"));
    var p = document.createElement("p");
    p.appendChild(document.createTextNode(t("Can we use Google Analytics cookies to see which pages get used?") + " "));
    var a = document.createElement("a");
    a.href = "/privacy#cookies";
    a.textContent = t("Details");
    p.appendChild(a);
    var row = document.createElement("div");
    row.className = "consent-row";
    [["no", t("Decline"), "btn"], ["yes", t("Accept"), "btn primary"]].forEach(function (b) {
      var btn = document.createElement("button");
      btn.type = "button";
      btn.className = b[2];
      btn.textContent = b[1];
      btn.addEventListener("click", function () { decide(b[0]); });
      row.appendChild(btn);
    });
    bar.appendChild(p);
    bar.appendChild(row);
    document.body.appendChild(bar);
  }
  function decide(v) {
    remember(v);
    if (bar) { bar.remove(); bar = null; }
    if (v === "yes") start();
    else {
      clearGA();
      // gtag.js already running keeps its cookies alive; a reload drops it.
      if (started) location.reload();
    }
  }

  var c = choice();
  if (c === "yes") start();
  else if (c === "no") clearGA();
  else if (document.body) ask();
  else document.addEventListener("DOMContentLoaded", ask);

  document.addEventListener("click", function (ev) {
    if (!ev.target.closest) return;
    // "Cookie settings" in the footer and on the privacy page asks again.
    if (ev.target.closest("[data-consent-open]")) { ev.preventDefault(); ask(); return; }
    // The public pages' one action is copying the prompt, and no page loads
    // after it, so nothing would see it. Every button that copies the prompt
    // carries data-ga-copy; the nearest data-ga-where says which spot it is
    // (bar, hero, try, close, showcase), so spots can be compared.
    var btn = ev.target.closest("[data-ga-copy]");
    if (!btn) return;
    var where = btn.closest("[data-ga-where]");
    var placement = where ? where.getAttribute("data-ga-where") : "other";
    // A count with no cookie and no identifier, so it needs no consent and
    // covers the visitors who declined GA too.
    if (navigator.sendBeacon) navigator.sendBeacon("/e/copy?at=" + encodeURIComponent(placement));
    if (started) window.gtag("event", "copy_prompt", { placement: placement });
  });
})();
