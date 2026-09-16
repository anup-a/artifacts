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
    if (ev.key !== "Escape") return;
    var hadMenu = document.querySelector("details.menu-wrap[open], details.switcher[open], details.confirm[open]");
    closeMenus(null);
    if (hadMenu || !peek.open) return;
    // A half-written reply is worth more than the shortcut: leave the box first.
    if (ev.target instanceof HTMLTextAreaElement && ev.target.value) { ev.target.blur(); return; }
    closePeek(true);
  });

  // ---- Side peek -----------------------------------------------------
  // Links marked data-peek (row titles, View) open the artifact detail in a
  // slide-over instead of leaving the list. The server renders the same
  // detail as a shell-less fragment at ?panel=1; the URL is pushed so it is
  // shareable and Back closes the panel. Forms inside post with panel=1 and
  // get the fresh fragment back, except Delete, which keeps its native post:
  // the redirect to the list is exactly the "close and refresh" wanted.
  var peek = { el: null, backdrop: null, open: false, opener: null, listURL: null, listTitle: null, dirty: false };

  function ensurePeek() {
    if (peek.el) return;
    peek.backdrop = document.createElement("div");
    peek.backdrop.className = "peek-backdrop";
    peek.backdrop.setAttribute("data-peek-close", "");
    peek.el = document.createElement("aside");
    peek.el.className = "peek";
    peek.el.setAttribute("role", "dialog");
    peek.el.setAttribute("aria-modal", "true");
    peek.el.setAttribute("aria-label", "Artifact details");
    document.body.appendChild(peek.backdrop);
    document.body.appendChild(peek.el);
  }

  function isFragment(html) {
    return html.indexOf('class="peek-inner"') !== -1;
  }

  // renderPeek swaps the fragment in, keeping the scroll position when it is
  // a re-render after a form post, and scrolls to the hash on a fresh open.
  function renderPeek(html, hash) {
    var top = peek.el.scrollTop;
    peek.el.innerHTML = html;
    peek.el.classList.remove("is-loading");
    var inner = peek.el.querySelector(".peek-inner");
    if (inner && inner.getAttribute("data-peek-title")) document.title = inner.getAttribute("data-peek-title") + " · Artifacts";
    var focus = peek.el.querySelector("[autofocus]");
    if (focus) { focus.focus(); return; }
    var target = hash ? peek.el.querySelector("#" + hash) : null;
    if (target) { target.scrollIntoView({ block: "start" }); return; }
    peek.el.scrollTop = top;
  }

  function withPanel(url) {
    var parts = url.split("#");
    return parts[0] + (parts[0].indexOf("?") === -1 ? "?" : "&") + "panel=1";
  }

  function openPeek(url, push, opener) {
    ensurePeek();
    if (!peek.open) {
      peek.listURL = location.href;
      peek.listTitle = document.title;
      peek.dirty = false;
    }
    peek.open = true;
    peek.opener = opener || peek.opener;
    peek.el.classList.add("is-open", "is-loading");
    peek.backdrop.classList.add("is-open");
    if (push) history.pushState({ peek: url }, "", url);
    fetch(withPanel(url), { credentials: "same-origin", headers: { Accept: "text/html" } })
      .then(function (res) { return res.text().then(function (html) { return { ok: res.ok, html: html }; }); })
      .then(function (r) {
        if (!r.ok || !isFragment(r.html)) { location.href = url; return; }
        renderPeek(r.html, url.split("#")[1] || "");
        var close = peek.el.querySelector(".peek-close");
        if (close) close.focus();
      }, function () { location.href = url; });
  }

  // refreshList re-fetches the list page behind the panel after a change
  // made inside it (comment count, "off" badge, version) so it is not stale.
  function refreshList() {
    var main = document.querySelector("main.content");
    if (!main || !peek.listURL) return;
    fetch(peek.listURL, { credentials: "same-origin", headers: { Accept: "text/html" } })
      .then(function (res) { return res.ok ? res.text() : ""; })
      .then(function (html) {
        var fresh = html && new DOMParser().parseFromString(html, "text/html").querySelector("main.content");
        if (fresh) main.innerHTML = fresh.innerHTML;
      }, function () {});
  }

  function closePeek(pop) {
    if (!peek.open) return;
    peek.open = false;
    peek.el.classList.remove("is-open", "is-loading");
    peek.backdrop.classList.remove("is-open");
    if (peek.listTitle) document.title = peek.listTitle;
    if (pop && history.state && history.state.peek) history.back();
    if (peek.dirty) refreshList();
    var opener = peek.opener;
    peek.opener = null;
    if (opener && document.contains(opener)) opener.focus();
  }

  document.addEventListener("click", function (ev) {
    if (ev.target.closest("[data-peek-close]")) { ev.preventDefault(); closePeek(true); return; }
    var a = ev.target.closest("a[data-peek]");
    if (!a || ev.defaultPrevented || ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey) return;
    if (!window.fetch || !history.pushState) return;
    ev.preventDefault();
    closeMenus(null);
    openPeek(a.getAttribute("href"), true, a);
  });

  window.addEventListener("popstate", function (ev) {
    if (ev.state && ev.state.peek) openPeek(ev.state.peek, false, null);
    else closePeek(false);
  });

  document.addEventListener("submit", function (ev) {
    var form = ev.target;
    if (!peek.open || !peek.el.contains(form) || /\/delete$/.test(form.getAttribute("action") || "")) return;
    ev.preventDefault();
    var body = new URLSearchParams(new FormData(form));
    body.append("panel", "1");
    form.classList.add("is-busy");
    fetch(form.getAttribute("action"), { method: "POST", body: body, credentials: "same-origin", headers: { Accept: "text/html" } })
      .then(function (res) { return res.text().then(function (html) { return { ok: res.ok, html: html }; }); })
      .then(function (r) {
        if (!isFragment(r.html)) { location.reload(); return; }
        if (r.ok) peek.dirty = true;
        renderPeek(r.html, "");
      }, function () { location.reload(); });
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
