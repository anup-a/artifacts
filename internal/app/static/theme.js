// The light/dark switch on the landing page and in the dashboard sidebar.
// A visitor meets it before they have an account to hold a preference in, so
// the choice lives in localStorage: one of "auto", "light" or "dark", shared
// by both.
//
// This file is loaded without defer so the resolved theme is on <html> before
// the first paint. Without that, a visitor who chose against their system
// setting would see the other theme flash first.
(function () {
  "use strict";

  var KEY = "byagent-theme";
  var CHOICES = { auto: 1, light: 1, dark: 1 };
  var root = document.documentElement;
  var media = window.matchMedia("(prefers-color-scheme: dark)");

  function stored() {
    try {
      var v = localStorage.getItem(KEY);
      return CHOICES[v] ? v : "auto";
    } catch (e) {
      return "auto"; // private mode, or storage disabled
    }
  }

  function apply(choice) {
    root.dataset.themeChoice = choice;
    root.dataset.theme = choice === "auto" ? (media.matches ? "dark" : "light") : choice;
  }

  function mark(picker, choice) {
    var buttons = picker.querySelectorAll("[data-set-theme]");
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].setAttribute(
        "aria-pressed",
        buttons[i].getAttribute("data-set-theme") === choice ? "true" : "false"
      );
    }
  }

  apply(stored());

  // Only while the choice is "auto" does the system setting still lead.
  media.addEventListener("change", function () {
    if (stored() === "auto") apply("auto");
  });

  // A page can carry more than one picker (the public pages have one in the
  // bar and one in the footer for phones), and they all show the same choice.
  document.addEventListener("DOMContentLoaded", function () {
    var pickers = document.querySelectorAll("[data-theme-picker]");
    for (var i = 0; i < pickers.length; i++) {
      mark(pickers[i], stored());
      pickers[i].addEventListener("click", function (event) {
        var button = event.target.closest("[data-set-theme]");
        if (!button) return;
        var choice = button.getAttribute("data-set-theme");
        if (!CHOICES[choice]) return;
        try {
          localStorage.setItem(KEY, choice);
        } catch (e) {
          // Not storable: the page still switches for this visit.
        }
        apply(choice);
        for (var j = 0; j < pickers.length; j++) mark(pickers[j], choice);
      });
    }
  });
})();
