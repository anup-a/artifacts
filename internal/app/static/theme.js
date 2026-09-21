// The landing page carries its own light/dark switch: a visitor meets it
// before they have an account to hold a preference in. The choice is one of
// "auto", "light" or "dark" and lives in localStorage.
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

  document.addEventListener("DOMContentLoaded", function () {
    var picker = document.querySelector("[data-theme-picker]");
    if (!picker) return;
    mark(picker, stored());
    picker.addEventListener("click", function (event) {
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
      mark(picker, choice);
    });
  });
})();
