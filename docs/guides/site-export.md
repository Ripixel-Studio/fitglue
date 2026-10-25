# Site export (`fitgluectl export-site`)

The public showcase is published as static JSON that James's personal site
(`Ripixel-Studio/ripixel-website`, `/life`) builds from. `fitgluectl export-site`
writes those files to the showcase-assets bucket; nothing on the site talks to a
FitGlue service at build or request time.

The file format is defined by the **data contract** in
`jarvis-memory/specs/ripixel-website/fitness-zine.md`, section "Data contract
(S0)". That document is authoritative; this page only describes the command.
The Go implementation lives in `src/go/internal/activity/siteexport/` (the
transformation and the orchestration) and `src/go/cmd/fitgluectl/` (the CLI).

## Bucket layout

Bucket: `${PROJECT_ID}-showcase-assets` (`fitglue-server-prod-showcase-assets`),
the same `SHOWCASE_ASSETS_BUCKET` every service already has. Public read,
uniform bucket-level access, no lifecycle rule.

```
site/<slug>/index.json                     profile, streaks, every activity card, every roundup tile
site/<slug>/activity/<showcaseId>.json     one per showcased activity
site/<slug>/roundup/<periodKey>.json       one per roundup (week-WW-YYYY, month-MM-YYYY, year-YYYY)
```

Every object is written with `Content-Type: application/json; charset=utf-8`
and `Cache-Control: public, max-age=300`. Activity and roundup files are written
first and `index.json` last, so a reader never sees an index that points at a
missing file. `index.json` lists only activities and roundups whose file exists
in the bucket, and is the source of truth for what the site should build.

Public fetch URL: `https://storage.googleapis.com/fitglue-server-prod-showcase-assets/site/james/…`

## What is in the files

Protobuf messages are serialised with `protojson` using the same options as
`api-public`'s `WriteJSON` (`UseProtoNames: false`, `EmitUnpopulated: true`),
so the JSON matches what the React showcase pages consume today. On top of
that, per the contract:

- `index.json`: `profile` is `GetPublicShowcaseProfile(slug).profile` (so
  `topPrs` is filled the way the public page sees it) minus `entries` and
  `userId`; `streaks` is derived from `profile.streakHistory.weeklyActive`;
  `activities[]` is every `ShowcaseProfileEntry` plus `slug`; `roundups[]` is a
  derived summary per roundup.
- `activity/<id>.json`: `activity` is the hydrated `ShowcasedActivity` as
  `GetPublicShowcase` returns it, minus `activityDataUri`, `fitFileUri`,
  `pipelineExecutionId`, `activityId`, and with every `laps[].records` emptied.
  What the records fed is re-exported as `series` (≤ 120 bucket-averaged
  samples per metric) and `routePoints` (≤ 200 `[lat, lng]` pairs). `category`
  is derived (`strength` / `cardio-distance` / `cardio-time` / `sport` /
  `untraditional`).
- `roundup/<key>.json`: the `ShowcaseRoundup` minus `userId`, plus the derived
  `previousKey`.

Every file carries `schemaVersion` (currently 1).

Activities whose showcase document is gone, or whose `expiresAt` has passed, are
skipped and left out of the index.

## Running it

```bash
make build-ctl                      # bin/fitgluectl (also built by make build)

# Full export, and delete activity/roundup files no longer in the profile:
bin/fitgluectl export-site --slug james --all

# Only activities showcased / roundups generated after an instant (index.json is always rewritten):
bin/fitgluectl export-site --slug james --since 2026-10-19T00:00:00Z

# One activity or roundup, then refresh the index:
bin/fitgluectl export-site --slug james --activity colwick-parkrun-2026-10-17
bin/fitgluectl export-site --slug james --roundup week-42-2026

# Dry run: same reads, files land in a local directory, the bucket is never written:
bin/fitgluectl export-site --slug james --dry-run --out /tmp/site-export --all
```

Flags: `--project` (default `fitglue-server-prod`), `--bucket` (default
`$SHOWCASE_ASSETS_BUCKET`, else `<project>-showcase-assets`), `--prefix`
(default `site/<slug>`), `--concurrency` (default 8).

The command uses Application Default Credentials for Firestore and GCS. A dry
run still reads from GCS (each showcase's activity data is an offloaded blob in
the showcase-assets bucket) but writes nothing there.

Notes:

- `--since` compares the entry's `createdAt` (when it was showcased) and the
  roundup's `generatedAt`. A showcase that was re-boosted in place keeps its
  `createdAt`, so re-export it with `--activity <id>` or a full run.
- The export is idempotent: running it twice over unchanged data produces
  byte-identical files apart from `exportedAt` / `generatedAt`.
- Pruning happens only with `--all`. Objects under `activity/` or `roundup/`
  without a `.json` extension are never touched.

## Calling it from code

`siteexport` is a library first; the CLI is a thin wrapper. The showcase
destination uploader and the roundup generator can keep the bucket current by
calling, after they write:

```go
deps := siteexport.Deps{Source: siteexport.ServiceSource{Service: svc, Store: store},
    Writer: &siteexport.GCSWriter{Client: gcsClient, Bucket: bucket, Prefix: "site/" + slug}}

siteexport.ExportActivity(ctx, deps, slug, showcaseID)  // then
siteexport.RefreshIndex(ctx, deps, slug)

siteexport.ExportRoundup(ctx, deps, slug, periodKey)    // then RefreshIndex
siteexport.ExportAll(ctx, deps, slug, siteexport.Options{Prune: true}) // the repair path
```
