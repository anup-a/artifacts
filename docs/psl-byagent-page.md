# Public Suffix List request for byagent.page (draft, not submitted)

> **Optional, later.** byagent serves page hosts at `<slug>.byagent.dev`
> (`CONTENT_DOMAIN=byagent.dev`); `byagent.page` is not registered. This
> draft only applies if pages ever move to a domain of their own. byagent.dev
> itself must not be listed: the site, API and dashboard live on it.

Status: draft. Nothing here has been sent. Submitting is Anup's call, after
`byagent.page` is registered with at least two years left on it and serving
pages (deploy/RUNBOOK.md, "Page hosts").

## Why

Every published page is served from its own host, `<slug>.byagent.page`, so
that one page's script cannot read another page. Browsers already keep
origins apart, but until `byagent.page` is on the Public Suffix List they
treat every `*.byagent.page` host as the same site:

- a page can set a cookie with `Domain=byagent.page`, which the browser then
  sends to every other page host (cookie tossing), and
- `SameSite` cookies are sent on requests from one page host to another, so
  SameSite does not stop one page from posting to another with its cookies.

The server already defends against both (host-only `__Host-` cookies, and an
Origin check on every state-changing request on a page host). Listing the
domain moves the boundary into the browser, where it belongs, the same way
GitHub Pages (`github.io`), Netlify (`netlify.app`) and Vercel
(`vercel.app`) are listed.

## Before submitting

1. `byagent.page` registered with at least two years remaining, and kept that
   way (the PSL removes entries whose domains lapse).
2. Fork `publicsuffix/list`, add the entry below, run `make test`, open the PR.
3. Add the `_psl` TXT record with the PR's URL (below), then confirm it with
   `dig +short TXT _psl.byagent.page`.
4. Leave the record in place for as long as the entry exists.

## The `_psl` TXT record

In the Cloudflare zone `byagent.page` (DNS only; TXT records are never
proxied):

| Type | Name | Content |
| --- | --- | --- |
| `TXT` | `_psl` | `https://github.com/publicsuffix/list/pull/<PR number>` |

Check:

```
dig +short TXT _psl.byagent.page @1.1.1.1
"https://github.com/publicsuffix/list/pull/<PR number>"
```

`_psl` is a single label, so it does not collide with page hosts (a slug is 16
characters of `a-z2-7`, and the server only answers names it issued).

## The entry

In `public_suffix_list.dat`, in the PRIVATE DOMAINS section, in alphabetical
order by organization name:

```
// byagent : https://byagent.dev
// Submitted by Anup Aglawe <hello@byagent.dev>
byagent.page
```

Only the bare domain is listed, not `*.byagent.page`: every page is exactly
one label under it.

## PR text

Title: `Add byagent.page`

The repository's PR template supplies the checklist (registration term, the
`_psl` record, `make test`, and whether any third-party limit is being worked
around). Open the PR with the template and tick only what is true: this
request works around no third-party limit, rate limits included. The sections
below fill in the template's free-text parts.

```
Description of Organization
====

byagent (https://byagent.dev) is a publishing service for the output of AI
coding agents. An agent publishes an HTML or Markdown page with a CLI, and
people read it and comment on it in the browser. It is run by Anup Aglawe,
an individual developer in India, who is the submitter and maintainer of
this entry.

Organization Website: https://byagent.dev

Reason for PSL Inclusion
====

Every published page is served from its own subdomain,
https://<slug>.byagent.page/, and pages are written by many different users
(and their agents), so they must not be able to affect each other. Each page
has its own origin, but without a PSL entry browsers treat all of
*.byagent.page as one site: a page can set cookies for byagent.page that
every other page host receives, and SameSite cookies flow between page hosts.
Listing byagent.page makes each page host its own site, as github.io,
netlify.app and vercel.app are for theirs.

byagent.page is used only for user content. The service's own site, API and
dashboard are on byagent.dev, a separate registrable domain. The apex
byagent.page only redirects to byagent.dev.

This request is not about rate limits (Let's Encrypt or any other).

Number of users this request is being made to serve: every page published on
byagent, each on its own subdomain.

DNS Verification via dig
=======

dig +short TXT _psl.byagent.page
"https://github.com/publicsuffix/list/pull/<PR number>"

Results of Syntax Checker (`make test`)
=========

<paste the tail of `make test` here>
```

Check the current PR template on publicsuffix/list before submitting; it
changes from time to time, and the checkboxes above should match it, ticked
only where true.
