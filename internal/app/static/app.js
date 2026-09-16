// Progressive touches on top of plain forms: copy buttons, menus that close
// when you click elsewhere or press Escape, and growing reply boxes. Every
// action on the page works without this file; it just feels rougher.
(function () {
  "use strict";

  // ---- Copy to clipboard ---------------------------------------------
  // data-copy="#input" copies that field's value; data-copy-text="…" copies
  // the literal. The button shows a check for a moment.
  function flash(btn) {
    var label = btn.querySelector(".menu-item-text") || (btn.classList.contains("btn") ? btn : null);
    var old = label ? label.textContent : null;
    btn.classList.add("is-copied");
    if (label) label.textContent = "Copied";
    setTimeout(function () {
      btn.classList.remove("is-copied");
      if (label) label.textContent = old;
    }, 1400);
  }

  function copyText(text, fallbackInput) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      var el = fallbackInput;
      if (!el) {
        el = document.createElement("textarea");
        el.value = text;
        el.setAttribute("readonly", "");
        el.style && (el.style.position = "fixed");
        document.body.appendChild(el);
      }
      el.select();
      try {
        document.execCommand("copy") ? resolve() : reject(new Error("copy failed"));
      } catch (e) {
        reject(e);
      } finally {
        if (el !== fallbackInput) el.remove();
      }
    });
  }

  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest("[data-copy], [data-copy-text]");
    if (!btn) return;
    ev.preventDefault();
    var input = null;
    var text = btn.getAttribute("data-copy-text");
    if (text === null) {
      input = document.querySelector(btn.getAttribute("data-copy"));
      if (!input) return;
      text = input.value;
    }
    copyText(text, input).then(function () { flash(btn); }, function () { if (input) input.select(); });
    var menu = btn.closest("details.menu-wrap");
    if (menu) setTimeout(function () { menu.removeAttribute("open"); }, 500);
  });

  // ---- Menus and confirms --------------------------------------------
  // Only one details-based menu stays open; clicking outside or pressing
  // Escape closes it, like a real popover.
  function closeMenus(except) {
    document.querySelectorAll("details.menu-wrap[open], details.switcher[open], details.confirm[open]").forEach(function (d) {
      if (except && (d === except || d.contains(except))) return;
      d.removeAttribute("open");
    });
  }

  document.addEventListener("toggle", function (ev) {
    var d = ev.target;
    if (!(d instanceof HTMLDetailsElement) || !d.open) return;
    if (d.matches("details.menu-wrap, details.switcher, details.confirm")) closeMenus(d);
  }, true);

  document.addEventListener("pointerdown", function (ev) {
    if (ev.target.closest("details.menu-wrap, details.switcher, details.confirm")) return;
    closeMenus(null);
  });

  document.addEventListener("keydown", function (ev) {
    if (ev.key === "Escape") closeMenus(null);
  });

  // ---- Reply boxes ---------------------------------------------------
  // Grow with their content; Cmd/Ctrl+Enter submits.
  document.addEventListener("input", function (ev) {
    var ta = ev.target;
    if (!(ta instanceof HTMLTextAreaElement)) return;
    ta.style.height = "auto";
    ta.style.height = Math.min(ta.scrollHeight, 240) + "px";
  });

  document.addEventListener("keydown", function (ev) {
    var ta = ev.target;
    if (!(ta instanceof HTMLTextAreaElement) || ev.key !== "Enter" || !(ev.metaKey || ev.ctrlKey)) return;
    if (ta.form) ta.form.requestSubmit ? ta.form.requestSubmit() : ta.form.submit();
  });
})();
