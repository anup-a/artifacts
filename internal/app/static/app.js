// Small progressive touches: confirm before destructive forms, copy buttons,
// and a workspace switcher that submits on change. Everything works without
// this file, just less smoothly.
(function () {
  "use strict";

  document.addEventListener("submit", function (ev) {
    var form = ev.target;
    var msg = form.getAttribute("data-confirm");
    if (msg && !window.confirm(msg)) ev.preventDefault();
  });

  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest("[data-copy]");
    if (!btn) return;
    var input = document.querySelector(btn.getAttribute("data-copy"));
    if (!input) return;
    var done = function () {
      var old = btn.textContent;
      btn.textContent = "Copied";
      setTimeout(function () { btn.textContent = old; }, 1500);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(input.value).then(done, function () { input.select(); });
    } else {
      input.select();
      try { document.execCommand("copy"); done(); } catch (e) { /* leave selected */ }
    }
  });

  document.addEventListener("change", function (ev) {
    var sel = ev.target;
    if (sel.matches && sel.matches("select[data-autosubmit]") && sel.form) sel.form.submit();
  });
})();
