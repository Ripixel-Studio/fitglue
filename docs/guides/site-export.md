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

Protobuf messages are serialised with `protojson` with the options the old
`api-public` gateway used (`UseProtoNames: false`, `EmitUnpopulated: true`),
so the JSON keeps the shape the React showcase pages consumed before both were
deleted (F1.5b). On top of that, per the contract:

- `index.json`: `profile` is `GetPublicShowcaseProfile(slug).profile` (so
  `topPrs` is filled the way the public page sees it) minus `entries` and
  `userId`; `streaks` is derived from `profile.streakHistory.weeklyActive`;
  `activities[]` is every `ShowcaseProfileEntry` plus `slug`; `roundups[]` is a
  derived summary per roundup.
- `activity/<id>.json`: `activity` is the hydrated `ShowcasedActivity` as
  `GetPublicShowcase` returns it, minus `activityDataUri`, `fitFileUri`,
  `pipelineExecutionId`, `activityId`, and with every `laps[].records` emptied.
  What the records fed is re-exported as `series` (≤ 120 bucket-averaged
  samples per metric, records in time order) and `routePoints` (≤ 200
  `[lat, lng]` pairs). `category` is derived (`strength` / `cardio-distance` /
  `cardio-time` / `sport` / `untraditional`). Hyrox races with splits also get
  `race` (official splits, division, placings, per-segment avg/max HR —
  contract §3.4) and distance activities with HR get `kmSplits` (avg/max HR per
  whole km — §3.5); both are omitted otherwise.
- `roundup/<key>.json`: the `ShowcaseRoundup` minus `userId`, plus the derived
  `previousKey`.

Every file carries `schemaVersion` (currently 1).

Activities whose showcase document is gone, or whose `expiresAt` has passed, are
skipped and left out of the index.

## Privacy zones

Like Strava, only the start and end of a route are hidden. At export time the leading
points are dropped while the route is still within 500 m of a home, and the trailing
points the same way; everything between (including mid-route passes near home) is
published as one polyline (`routePoints` is `[]` below 10 points). A route that never
leaves the zone gets `routePoints: []` and `"routeHidden": true` so the site can say
"route hidden near home". Zones are the homes in `~/.jarvis/home/places.json`
(`{"homes":[{"lat","lon"}]}`) only; override the path with `SITE_EXPORT_PLACES_FILE`. Whole (untrimmed) means a
race (`DeriveRace` finds one) or a parkrun (parkrun in the title and 4.8-5.3 km); a bare
"parkrun" or "hyrox" in a title or tag does not count. If `places.json` is missing or
unreadable the export **fails closed**: `routePoints` is `[]` and `routeHidden` true for every other activity and
location coordinates are dropped (one log line per activity). `enrichments.location` and
`hintLocation` within 500 m of a home are removed. Skipped or expired activities have their old
file deleted, and `--all` prunes everything not written in that run.

Route thumbnails (`route-thumbnail.svg`) are drawn from the same trimmed track, by the
enricher going forward and, for the ones already published, by
`bin/fitgluectl rethumb-site --slug james` (overwrites the same objects at the same URLs;
a route that is entirely inside the zone, or any non-whole route without zones, gets a neutral
placeholder). After changing the zones run both `export-site --all` and `rethumb-site`.

## Calories

An activity's calories are the source platform's figure (the sessions'
`total_calories`: Strava's, or a FIT file's own) and FitGlue's MET estimate only
when there is none (`pkg/calories`). Older calories boosters always stored the
estimate, so the export puts the platform figure on each activity file's
`enrichments.calories` and index entry as it writes them, and
`fitgluectl fix-calories --slug james [--apply]` corrects the stored profile
entries (dry run by default; it also lists the roundups whose totals were built
from the old figures).

Two more correction commands write through the activity service with the same
site-export hook the service runs (with `--apply`: re-export the changed files,
refresh `index.json`, append to the rebuild signal file `SITE_EXPORT_SIGNAL_FILE`,
default `~/.jarvis/home/site-changed`):

- `fitgluectl fix-activity --id <showcase id> --type RUN [--distance-m M]
  [--calories-kcal K] [--apply]` corrects a mislabelled activity in the data blob,
  the showcase document and the profile entry, then recomputes lifetime totals and
  the stored roundups holding it. A non-strength type drops sets that carry no
  lifting (a Hevy "Running" set), since strength sets force the strength layout
  (`ResolveCategory`). `--scan` lists runs stored as strength sessions.
- `fitgluectl regen-roundup-stories [--key | --kind | --since] [--force] [--apply]`
  rewrites roundup stories (`aiSummary`) through Claude in James's voice. Each
  roundup file carries `aiSummaryModel` (`claude-sonnet`, or absent on old Gemini
  text) and `aiSummaryGeneratedAt`, so the site can tell old stories from new.
- `fitgluectl regen-activity-summaries [--id | --since | --limit] [--force] [--apply]`
  rewrites each activity's `enrichments.aiSummary` through the live enricher's
  path (`ai_companion.Summarize`: observer guide, Sonnet, voice guard), from the
  stored activity data (description cut to the text before the AI block, the
  other FitGlue sections as the other enrichers' descriptions). Only the site
  copy changes: `aiSummary` (with `model: claude-sonnet-observer` and
  `generatedAt`, which the enricher now stamps on new ones too) and
  `ENRICHER_PROVIDER_AI_COMPANION` in `appliedEnrichments`, which the site needs
  to show it. Title, description and activity data are untouched; nothing goes
  to Strava. Current ones are skipped unless `--force`, so a re-run resumes.

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

## Export on change (the activity service)

The bucket stays current without a timer: the **activity service** runs the
same export after the writes that change what the site shows (S1.2). The
wiring is `siteexport.Hook`, installed on `activity.Service` at boot
(`services/activity/main.go`, `Service.SetSiteExporter`), and it is told:

| After | Call | Writes |
|---|---|---|
| `AddShowcaseEntry` persisted the profile entry and recomputed the profile stats | `ActivityChanged(showcaseId)` | `activity/<id>.json`, then `index.json` |
| `RemoveShowcaseEntry` deleted the entry | `ActivityChanged(showcaseId)` | `index.json` (the id is no longer exportable, so the index drops it; the orphan file waits for `--all`) |
| `generateRoundup` saved the roundup (`SetRoundup`) | `RoundupChanged(slug, periodKey)` | `roundup/<key>.json`, then `index.json` |
| `UpdateShowcase` / `DeleteShowcase` (the API's showcase edit and delete) | `ActivityChanged(showcaseId)` | `activity/<id>.json` (or, deleted, only `index.json`) |
| `UpdateShowcaseSettings` / `UpdateShowcasePreferences` / `UpdateRoundupSettings` | `ProfileChanged()` | `index.json` (name, bio, links and totals live there) |

The showcase destination uploader (`services/destination/.../uploaders/showcase`)
needs no hook of its own: both its `Create` and `Update` paths end in the
`AddShowcaseEntry` RPC, and that is the write that makes an activity
exportable — `ExportActivity` finds the activity through the profile's entries,
not through the showcase document alone. The backfill tool (`showcase-reboost`)
reaches the same RPC through the pipeline. Keeping the hook in the one process
that owns the data also means one debouncer sees every change.

How it behaves:

- **Never on the request path.** Each call returns at once; the export runs in
  a goroutine on its own context with a 60 s timeout. A failed export is one
  Warn line (`site export: ... failed`); the upload or roundup that triggered
  it has already succeeded and is not affected.
- **Coalesced.** The file for the changed activity/roundup is written per call
  (at most 4 in flight; a backfill queues the rest), but `index.json` is
  rewritten at most once per 10 s window, and always once more after the last
  change in a burst. Hundreds of re-boosted activities produce a handful of
  index rewrites, not hundreds.
- **Only the exported profile.** A roundup for another slug is ignored; an
  activity that is not in `SITE_EXPORT_SLUG`'s profile (or has no showcase
  document, or has expired) is logged as skipped and only the index is
  refreshed.
- **Tells the box.** Every successful index refresh carries the reasons
  collected since the last one (`activity <id>`, `removed activity <id>`,
  `roundup <key>`, `profile`; more than 20 become `(+N more)`), and:
  - when `SITE_EXPORT_SIGNAL_FILE` is set, appends one line
    `<unix seconds> fitglue <reasons>` to that file (S6). On the box it is
    `~/.jarvis/home/site-changed`: a systemd path unit in the jarvis repo
    (`jarvis-site-rebuild.path`) starts the site rebuild while the file
    exists, the rebuild waits for 2 min of quiet (at most 10 min after the
    first line), consumes the file and deploys james.ripixel.co.uk once. A
    file, not an HTTP call, so a change survives a gateway reload or a reboot
    and systemd guarantees one rebuild at a time. A failed refresh signals
    nothing and carries its reasons to the next good one.
  - when `SITE_EXPORT_HOOK_URL` is set, `POST`s
    `{"slug":"<slug>","what":"index","reasons":"<reasons>"}` to it, 2 s
    timeout, best-effort: the gateway's `/hooks/fitness-updated`, which makes
    the kiosk's fitness panels refetch.

  Both are unset on Cloud Run, so nothing is signalled there.

Configuration (activity service environment; `terraform/cloud_run.tf` sets
the slug):

| Variable | Default | Meaning |
|---|---|---|
| `SITE_EXPORT_SLUG` | `james` | profile exported to `site/<slug>/` |
| `SITE_EXPORT_BUCKET` | `$SHOWCASE_ASSETS_BUCKET` | destination bucket |
| `SITE_EXPORT_DISABLED` | unset | `1`/`true`/`yes` turns the hook off entirely |
| `SITE_EXPORT_HOOK_URL` | unset | POSTed after each index refresh; set on the box only |
| `SITE_EXPORT_SIGNAL_FILE` | unset | a line appended after each index refresh; the box unit sets `%h/.jarvis/home/site-changed` |

No new IAM: the activity service account already holds
`roles/storage.objectAdmin` on the project (`terraform/iam.tf`,
`storage_services`), which covers writes to the showcase-assets bucket.

Caveat on Cloud Run: the backend services run with `cpu_idle = true`, so a
goroutine that outlives its request is CPU-throttled until the next request
arrives. The export is I/O-bound and usually completes anyway, and a backfill
keeps requests flowing, but a single late-night activity can land in the
bucket later than the 10 s window suggests. The nightly site rebuild (04:30,
jarvis `jarvis-site-nightly.timer`) and the repair path below cover whatever slips; on the box (post-F4) the
throttle does not exist.

**`fitgluectl export-site --all` remains the repair path**: it rewrites every
file, prunes orphans and rewrites the index, and is the thing to run after an
outage, a schema bump, or whenever the index and the bucket disagree.

## Calling it from code

`siteexport` is a library first; the CLI and the hook are thin wrappers:

```go
deps := siteexport.Deps{Source: siteexport.ServiceSource{Service: svc, Store: store},
    Writer: &siteexport.GCSWriter{Client: gcsClient, Bucket: bucket, Prefix: "site/" + slug}}

siteexport.ExportActivity(ctx, deps, slug, showcaseID)  // then
siteexport.RefreshIndex(ctx, deps, slug)

siteexport.ExportRoundup(ctx, deps, slug, periodKey)    // then RefreshIndex
siteexport.ExportAll(ctx, deps, slug, siteexport.Options{Prune: true}) // the repair path

// Or, in a long-running process, the background/debounced form:
hook := siteexport.NewHook(siteexport.HookConfig{Slug: slug, Deps: deps, HookURL: os.Getenv("SITE_EXPORT_HOOK_URL")})
hook.ActivityChanged(ctx, showcaseID) // returns at once
hook.RoundupChanged(ctx, slug, periodKey)
hook.ProfileChanged(ctx)              // index only
hook.Wait()                           // tests / shutdown only
```
