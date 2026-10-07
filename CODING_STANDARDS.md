# Coding standards

Judgement rules for review. Anything mechanical belongs in a test or a CI step instead.

## Tracking and cookies

Any change that adds or alters analytics, an event, a cookie, local storage that identifies a visitor, or a third-party script must:

- start only after consent, through the gate in `internal/app/static/ga.js`. A cookieless first-party count, like the `/e/copy` beacon, needs no consent and sets nothing.
- update `templates/privacy.html` (what is collected, the legal basis, retention) and `templates/subprocessors.html` if a new vendor receives data, then bump the policy's version and date.

Flag a diff that sends something new to a third party when the policy pages don't change with it.

## Landing styles over shared ones

`landing.css` (scoped under `.lp3`) loads on top of `app.css`. A class name the landing page reuses from `app.css` (`.seg`, `.btn`, `.link` …) inherits that rule's border, padding and background. When a diff restructures such an element, check that the landing rule still resets what `app.css` sets, or that the shared rule still applies as intended.
