# Artifact gallery: projects, tags, covers and a bento dashboard

Date: 2026-09-16. Direction A ("Bento") chosen from the rendered mockups at
https://byagent.dev/a/gYNtcGRyN5sD/.

## Goal

The Artifacts page reads as a plain table. Recent artifacts should be shown as
cards with cover art grouped by day (Today, Yesterday, then dates), older ones as
the existing list. Every artifact carries a project, optional tags, a cover and a
publish time, all supplied by the publisher (the agent or CLI), with generated
fallbacks so nothing is ever blank.

## Data model

New columns on `artifacts` (migration `0004_gallery.sql`, both dialects):

| column | type | notes |
| --- | --- | --- |
| `project` | text, nullable | free text, 1 to 64 chars, trimmed; `NULL` = no project |
| `tags` | text, nullable | JSON array of 0 to 10 strings, each 1 to 32 chars, lowercased, deduplicated |
| `cover_path` | text, nullable | path of a file inside the current version to use as cover; `NULL` = generated |
| `cover_style` | text, not null, default `''` | generator name the server picked or the publisher asked for; empty = derive from id |

`created_at` already exists and is the publish time; versions carry their own
`created_at` for "updated". No new tables. Projects are not entities, they are
labels. A project colour is derived from a stable hash of its name (six-colour
palette shared with avatars) so it needs no storage.

`store.Artifact` gains `Project *string`, `Tags []string`, `CoverPath *string`,
`CoverStyle string`. `ArtifactFilter` gains `Project string` (exact match). A new
`store.Projects(ctx, tenantID) ([]ProjectCount, error)` returns distinct projects
with counts for the filter chips. `store.SetMetadata(ctx, id, project *string,
tags []string, coverPath *string, coverStyle string)` updates the four columns
without touching `updated_at`.

## Cover generation

Covers are SVG, 1200 x 630, served at `GET /a/{id}/_cover.svg` on the share
origin and reused by the dashboard. Two sources:

1. **Publisher file.** If `cover_path` is set and that file exists in the current
   version, the endpoint streams it (SVG, PNG, JPEG or WebP; content type from the
   stored file). Rollback to a version without that file falls back to generated.
2. **Generated.** A Go package `internal/cover` renders an OG-style card, the
   way Vercel's OG image generator does: the text is the subject, the background
   is quiet. Anup's direction after seeing loud Creatica art as covers: "too
   attractive... some text and a subtle background pattern which is not overly
   taking attention".
   - Canvas 1200 x 630, near-white ground (`#fafafa`) with the pattern drawn in
     a neutral grey at 6 to 10 percent opacity. Dark variant is not generated;
     the dashboard shows the same card in both themes.
   - Styles, picked by `hash(id) mod len(styles)` unless `cover_style` names one:
     `dots` (dot grid, 24px pitch), `grid` (hairline grid, 48px), `lines`
     (diagonal hairlines), `wash` (one very soft radial tint in the project
     colour at 8 percent, top-right), `rings` (faint concentric circles anchored
     off-canvas bottom-right), `plain` (no pattern). All plain shapes, no
     `<filter>`, no external fonts (system font stack).
   - Text: title in the foreground colour (`#171717`), 600 weight, 60px, left
     aligned at x=80, vertically centred, escaped and clamped to three lines of at
     most 26 characters each with an ellipsis. Above it a small line: an 10px
     project-coloured dot and the project name in 24px grey (omitted when no
     project). Below, bottom-left, a 22px grey footer `byagent.dev · 16 Sep 2026`
     using the share host and the publish date. Bottom-right, a 6px accent bar
     in the project colour, 160px wide, as the only saturated element. Empty title
     shows the id.
   - Project colour: the same six-colour hash palette the dashboard uses for
     project dots, so card and list agree; no project = neutral grey.

Generated covers are pure functions of `(id, title, style)`, so the endpoint sets
`ETag` from a hash of those inputs and `Cache-Control: public, max-age=3600`.
Publisher-file covers use the file's sha256 as ETag like any asset.

Creatica: the agent skill tells agents they may generate a cover with the
Creatica CLI or MCP (`npx @creatica/cli gen wave --seed <slug> --width 1200
--height 630 -o cover.svg`) and pass it with `--cover cover.svg`, with a note to
prefer filter-free generators (wave, small_sur, layered_waves, mountains,
gradientblob, overlapping_circles) because filtered ones are slow to paint as
thumbnails. The server never shells out to Node.

`og:image`, `og:title` and `twitter:card` meta tags are injected into served
`index.html` pages next to the runtime tag, pointing at the cover URL. This is
the only serve change. Platforms that ignore SVG `og:image` simply show no image,
as today.

## API and CLI

- Manifest (`POST /api/artifacts`, `PUT /api/artifacts/{id}`) accepts optional
  `project` (string or null), `tags` (array), `cover` (path of a file in the same
  manifest), `cover_style` (one of the generated style names). Omitted keys keep
  the current value on PUT; `null` clears project or cover.
- Artifact JSON everywhere gains `project`, `tags`, `cover_url`, `cover_style`,
  `published_at` (alias of `created_at`, kept for readability).
- `GET /api/artifacts?project=<name>` filters. `GET /api/projects` lists
  `{name, count}` for the workspace.
- CLI `publish` gains `--project <name>`, `--tag <t>` (repeatable), `--cover
  <file>` (must be inside the directory, or the CLI copies it in as
  `_cover.<ext>`), `--cover-style <name>`. `artifacts list` prints project and
  tags; `--project` filters. `.artifacts.json` remembers project and tags so a
  republish keeps them without repeating flags.
- `skill/SKILL.md` documents the flags and asks agents to always pass a project
  and one to three tags, and to generate a cover with Creatica when the page has
  no obvious visual.

## Dashboard

`/app/artifacts` becomes:

- **Toolbar:** search (as now) plus project chips from `store.Projects` ("All"
  first), each a link with `?project=`; the active chip is filled.
- **Recent sections:** artifacts published in the last 7 days, grouped by
  calendar day in the viewer's timezone (client-side script rewrites the section
  labels; server renders UTC labels so no-JS still works). Labels: Today,
  Yesterday, then `Mon 14 Sep`. Each group is a bento grid: 4 columns, 150px rows,
  first card of Today spans 2 x 2 (hero), every fourth card spans 2 columns
  (wide), the rest single. Yesterday and older groups have no hero. Cards show the
  cover (title already baked in), then a meta row: project dot + name, up to two
  tag chips, comment count with icon, relative time. Clicking a card opens the
  existing side peek; a small "Open ↗" icon button in the meta row opens the
  published page.
- **Older list:** everything beyond 7 days in the current table, with a 44 x 26
  cover thumbnail before the title and a Project column. Pagination unchanged
  (cursor applies to the whole list; recent sections render only on the first
  page).
- **Search or project filter active:** no day grouping; results render as one
  bento without a hero, then the list for older items, so filtering never hides
  the covers.
- **Empty state:** unchanged copy, plus a line about `--project` and `--tag`.
- **Detail page and peek:** an "About" card with editable project (text input
  with datalist of existing projects), tags (comma-separated input), and cover
  (radio: Generated with style select, or a file from the current version chosen
  from a select). Saves via `POST /app/artifacts/{id}/meta` and works inside the
  peek like the other forms.

All CSS in `app.css`, JS in `app.js` (day-label localisation and nothing else new),
no inline styles. Dark mode via existing tokens. Covers are `<img loading="lazy">`
with a fixed aspect ratio box so the grid never jumps.

## Error handling

- Validation errors on project, tags, cover or style return 400 `INVALID` from
  the API with the field name; the CLI prints them; the dashboard form re-renders
  with the message.
- A `cover` path not present in the manifest is a 400 at publish time.
- A stored `cover_path` missing from the current version (after rollback) falls
  back to the generated cover without error.
- The cover endpoint returns 404 for unknown or deleted artifacts and 410 for
  soft-deleted ones, like pages.

## Testing

- `internal/cover`: golden tests per style (byte-identical for a fixed seed),
  title escaping (`<script>` in a title is text), clamping, and a benchmark that
  keeps a render under 1 ms.
- Store: migration test, metadata round trip on sqlite and Postgres, project
  counts, project filter.
- API: manifest validation cases, cover endpoint (file, generated, rollback
  fallback, 404/410), `og:image` injected, projects endpoint.
- CLI: unit tests for the new flags and `.artifacts.json` persistence, e2e
  publish with `--project`/`--tag`/`--cover`.
- App: list renders sections and hero, project chip filter, older list, meta
  form save (full page and panel mode).
- Runtime e2e `dashboard.spec.mjs`: extend the existing flow with a project
  chip and a cover image visible on a card.
- Visual: ego-browser screenshots of the list in light and dark with at least six
  artifacts across three days.

## Out of scope

Server-side AI classification, PNG rasterisation of covers, screenshot covers,
project entities with their own pages, bulk re-tagging.
