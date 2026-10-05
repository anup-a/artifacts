// Progressive touches on top of plain forms: copy buttons, confirm dialogs,
// the side peek, and growing reply boxes. On the dashboard, Basecoat
// (basecoat.js) runs the menus, sidebar and tabs; this file adds what is
// byagent's own. Every form posts without it.
(function () {
  "use strict";

  // ---- Copy to clipboard ---------------------------------------------
  // data-copy="#input" copies that field's value; data-copy-text="…" copies
  // the literal. The button shows a check for a moment.
  // The dashboard confirms with a toast; elsewhere the button's own label
  // reads "Copied" for a moment.
  function flash(btn) {
    var toaster = document.getElementById("toaster");
    btn.classList.add("is-copied");
    setTimeout(function () { btn.classList.remove("is-copied"); }, 1400);
    if (toaster && toaster.toast) {
      toaster.toast({ category: "success", title: "Copied to clipboard", duration: 2000 });
      return;
    }
    var label = btn.querySelector(".menu-item-text") || (btn.classList.contains("btn") ? btn : null);
    if (!label || label.children.length) return;
    var old = label.textContent;
    label.textContent = "Copied";
    setTimeout(function () { label.textContent = old; }, 1400);
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

  // ---- Menus, dialogs and the sidebar --------------------------------
  // Basecoat's dropdown menus close themselves. The <details> menus left on
  // the public site get the same manners: one open at a time, closed by a
  // click elsewhere or Escape.
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

  function menuOpen() {
    return document.querySelector("details.menu-wrap[open], details.switcher[open], details.confirm[open], .dropdown-menu [aria-expanded='true'], dialog[open]");
  }

  // data-dialog="id" opens that <dialog> as a modal (the confirm before a
  // delete, the small edit forms); data-dialog-close and a click on the
  // backdrop close it.
  document.addEventListener("click", function (ev) {
    var opener = ev.target.closest("[data-dialog]");
    if (opener) {
      var dialog = document.getElementById(opener.getAttribute("data-dialog"));
      if (dialog && dialog.showModal) { ev.preventDefault(); dialog.showModal(); }
      return;
    }
    if (ev.target.closest("[data-dialog-close]")) {
      var open = ev.target.closest("dialog");
      if (open) { ev.preventDefault(); open.close(); }
      return;
    }
    if (ev.target instanceof HTMLDialogElement && ev.target.open) ev.target.close();
    var toggle = ev.target.closest("[data-sidebar-toggle]");
    var sidebar = toggle && document.querySelector("aside.sidebar");
    if (sidebar && sidebar.toggle) sidebar.toggle();
  });

  document.addEventListener("keydown", function (ev) {
    if (ev.key !== "Escape") return;
    var hadMenu = menuOpen();
    closeMenus(null);
    if (hadMenu || !peek.open) return;
    // A half-written reply is worth more than the shortcut: leave the box first.
    if (ev.target instanceof HTMLTextAreaElement && ev.target.value) { ev.target.blur(); return; }
    closePeek(true);
  });

  // ---- Tabs and the address ------------------------------------------
  // selectTab marks a tab chosen in fresh markup, before Basecoat wires it:
  // Basecoat then starts on whichever tab says aria-selected.
  function selectTab(tab) {
    var list = tab.closest("[role=tablist]");
    if (!list) return;
    list.querySelectorAll("[role=tab]").forEach(function (t) {
      var on = t === tab;
      t.setAttribute("aria-selected", on ? "true" : "false");
      var panel = document.getElementById(t.getAttribute("aria-controls"));
      if (panel) panel.hidden = !on;
    });
  }

  // A link to #comments or #versions lands on the tab holding it, and a
  // picked tab is kept in the address so a reload or a shared link opens
  // the same one. Basecoat switches the panels; this only chooses which.
  function revealHash(root) {
    var id = location.hash.slice(1);
    if (!id) return;
    var target = null;
    try { target = (root || document).querySelector("#" + CSS.escape(id)); } catch (e) { return; }
    var panel = target && target.closest("[role=tabpanel]");
    if (!panel || !panel.hidden) return;
    var tab = document.querySelector("[role=tab][aria-controls='" + panel.id + "']");
    if (tab) tab.click();
    target.scrollIntoView({ block: "nearest" });
  }

  document.addEventListener("click", function (ev) {
    var tab = ev.target.closest("[role=tab][data-hash]");
    if (tab && history.replaceState && !tab.closest(".peek")) history.replaceState(history.state, "", "#" + tab.getAttribute("data-hash"));
  });
  window.addEventListener("hashchange", function () { revealHash(document); });
  // Basecoat wires its tabs on DOMContentLoaded, which fires after every
  // deferred script; this listener is added after its own, so runs later.
  document.addEventListener("DOMContentLoaded", function () { revealHash(document); });

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
    // A save re-renders the fragment; it stays on the tab it was posted from.
    var was = peek.el.querySelector("[role=tab][aria-selected=true]");
    var wasId = was && was.id;
    peek.el.innerHTML = html;
    var tab = wasId && !hash ? document.getElementById(wasId) : null;
    if (tab && peek.el.contains(tab)) selectTab(tab);
    peek.el.classList.remove("is-loading");
    var inner = peek.el.querySelector(".peek-inner");
    if (inner && inner.getAttribute("data-peek-title")) document.title = inner.getAttribute("data-peek-title") + " · Artifacts";
    var focus = peek.el.querySelector("[autofocus]");
    if (focus) { focus.focus(); return; }
    var target = hash ? peek.el.querySelector("#" + hash) : null;
    var panel = target && target.closest("[role=tabpanel]");
    if (panel && panel.hidden) {
      var tab = peek.el.querySelector("[role=tab][aria-controls='" + panel.id + "']");
      if (tab) tab.click();
    }
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
        if (fresh) { main.innerHTML = fresh.innerHTML; relabelDays(main); collapseAll(main); }
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

  // ---- Day labels ----------------------------------------------------
  // The server groups recent artifacts by UTC day and labels them in UTC.
  // Relabel each section from its newest publish time in the viewer's
  // timezone: Today, Yesterday, then "Mon 14 Sep". Grouping stays as served.
  function localDayLabel(at) {
    var now = new Date();
    var day = new Date(at);
    if (isNaN(day)) return null;
    var startOf = function (d) { return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime(); };
    var diff = Math.round((startOf(now) - startOf(day)) / 864e5);
    if (diff === 0) return "Today";
    if (diff === 1) return "Yesterday";
    return day.toLocaleDateString(undefined, { weekday: "short" }) + " " + day.getDate() + " " + day.toLocaleDateString(undefined, { month: "short" });
  }

  function relabelDays(root) {
    (root || document).querySelectorAll(".day[data-at]").forEach(function (sec) {
      var label = sec.querySelector(".day-label");
      var text = localDayLabel(sec.getAttribute("data-at"));
      if (label && text) label.textContent = text;
    });
  }
  relabelDays(document);

  // ---- One-line chip rows --------------------------------------------
  // A [data-collapse] chip row keeps to one line: chips that do not fit are
  // hidden behind a "+N more" chip that expands the row in place. The row
  // starts open when the selected chip would be hidden, so the current
  // filter is always visible. Without this script every chip wraps.
  function collapseChips(nav) {
    var toggle = nav.querySelector("[data-chips-toggle]");
    if (!toggle) return;
    var chips = Array.prototype.filter.call(nav.querySelectorAll(".pchip"), function (c) { return c !== toggle; });
    nav.classList.remove("is-collapsed");
    chips.forEach(function (c) { c.classList.remove("is-overflow"); });
    toggle.hidden = true;
    if (!chips.length) return;
    var top = chips[0].offsetTop;
    var wraps = chips.filter(function (c) { return c.offsetTop > top; });
    if (!wraps.length) return;
    var active = nav.querySelector(".pchip.is-on");
    var open = nav.getAttribute("data-open") === "1" || wraps.indexOf(active) >= 0;
    toggle.hidden = false;
    toggle.setAttribute("aria-expanded", open ? "true" : "false");
    if (open) {
      toggle.textContent = "Show less";
      return;
    }
    nav.classList.add("is-collapsed");
    var hidden = 0;
    for (var i = chips.length - 1; i > 0 && nav.scrollWidth > nav.clientWidth + 1; i--) {
      if (chips[i] === active) continue;
      chips[i].classList.add("is-overflow");
      hidden++;
      toggle.textContent = "+" + hidden + " more";
    }
    if (!hidden) toggle.hidden = true;
  }

  function collapseAll(root) {
    Array.prototype.forEach.call(root.querySelectorAll(".chips[data-collapse]"), collapseChips);
  }

  document.addEventListener("click", function (ev) {
    var toggle = ev.target.closest && ev.target.closest("[data-chips-toggle]");
    if (!toggle) return;
    var nav = toggle.closest(".chips[data-collapse]");
    nav.setAttribute("data-open", toggle.getAttribute("aria-expanded") === "true" ? "0" : "1");
    collapseChips(nav);
  });

  var chipTimer = 0;
  window.addEventListener("resize", function () {
    clearTimeout(chipTimer);
    chipTimer = setTimeout(function () { collapseAll(document); }, 100);
  });
  collapseAll(document);
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(function () { collapseAll(document); });

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
