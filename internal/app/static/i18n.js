// Messages in the page's language for the other scripts. The layout writes
// the translations of every message they use as a JSON data block (none in
// English, where each message is its own text). Loaded before them, not
// deferred, so ga.js can use it from the head.
//
//   var t = byagentI18n.t, tn = byagentI18n.tn;
//   t("Copied")                      the message in the page's language
//   t("Saved %s", name)              %s and %d filled in order, or by %[2]s
//   tn(n, "%s page", "%s pages")     the form for the count, which comes first
window.byagentI18n = (function () {
  "use strict";
  var messages = null;
  function load() {
    if (messages) return messages;
    var el = document.getElementById("i18n-messages");
    try { messages = el ? JSON.parse(el.textContent) : {}; } catch (e) { messages = {}; }
    return messages;
  }
  function fill(s, args) {
    var i = 0;
    return s.replace(/%(?:\[(\d+)\])?[sd]/g, function (_, n) { return String(n ? args[n - 1] : args[i++]); });
  }
  function t(msg) {
    var s = load()[msg];
    return fill(typeof s === "string" ? s : msg, [].slice.call(arguments, 1));
  }
  // single matches oneForm in internal/i18n: no plural in Chinese, Japanese
  // and Korean, and zero is singular in French and Portuguese.
  function single(n) {
    var lang = document.documentElement.lang || "en";
    if (/^(zh|ja|ko)/.test(lang)) return false;
    if (/^(fr|pt)/.test(lang)) return n === 0 || n === 1;
    return n === 1;
  }
  function tn(n, one, other) {
    var e = load()[one + "|" + other], s = single(n) ? one : other;
    if (typeof e === "string") s = e;
    else if (e) s = (single(n) && e.one) || e.other;
    var lang = document.documentElement.lang || "en";
    return fill(s, [Number(n).toLocaleString(lang)].concat([].slice.call(arguments, 3)));
  }
  return { t: t, tn: tn };
})();
