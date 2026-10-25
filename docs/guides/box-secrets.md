# Secrets on the box

Since rethink F3.5 every secret the FitGlue services need lives in one file on
the box, `~/.jarvis/fitglue.env`, loaded by each `fitglue-<svc>.service` unit
through `EnvironmentFile=`. Secret Manager is no longer read by anything the
box runs; it stays only until F4.2 deletes it with the Cloud Run services.

## What lives where

| Thing | Where | Who reads it |
|---|---|---|
| Provider app credentials, webhook verify tokens, `OAUTH_STATE_SECRET`, `GEMINI_API_KEY`, `FITGLUE_API_TOKEN`, `FITGLUE_OWNER_UID` | `~/.jarvis/fitglue.env` (mode 600) | every unit; `fitgluectl` via a shell that sources it |
| Deployment settings (`PROJECT_ID`, buckets, `ENVIRONMENT`, `LOG_LEVEL`, `ASSETS_BASE_URL`, `SITE_EXPORT_SLUG`, …) | the unit files, **and** repeated in `fitglue.env` so a shell running `fitgluectl` sees them. The unit value and the file value must agree; the file wins when they differ. | units, `fitgluectl` |
| Per-box values (`PUBSUB_PUSH_AUDIENCE`, `FITGLUE_PUBLIC_BASE_URL`, `WEB_URL`, `SITE_EXPORT_HOOK_URL`, `SENTRY_DSN`) | `fitglue.env` only | pipeline/activity/destination, api-client, activity |
| Google Cloud identity | `~/.jarvis/fitglue-box-sa.json` (F3.2; `GOOGLE_APPLICATION_CREDENTIALS` in the units) | every unit. **It has no `secretmanager.secretAccessor`** and must not get one. |
| Per-user integration tokens (Strava/Fitbit/Hevy… access + refresh tokens, Hevy API key) | Firestore `users/{uid}/integrations/*` | api-client, destination, pipeline — never env |
| The schema: every key, with comments, no values | `deploy/box/fitglue.env.example` | you, and `deploy/box/env-check.sh` |

Keys the example lists and the values written on 2026-10-25:

- **From Secret Manager** (`fitglue-server-prod`, copied once under James's
  own gcloud identity): `OAUTH_STATE_SECRET`, `GEMINI_API_KEY`,
  `STRAVA_CLIENT_ID`, `STRAVA_CLIENT_SECRET`, `STRAVA_WEBHOOK_VERIFY_TOKEN`
  (← `strava-verify-token`), `FITBIT_CLIENT_ID`, `FITBIT_CLIENT_SECRET`,
  `FITBIT_OAUTH_CLIENT_SECRET` (same value as `FITBIT_CLIENT_SECRET`; the
  name api-webhook reads for the signature check),
  `FITBIT_SUBSCRIBER_VERIFICATION_TOKEN` (← `fitbit-verification-code`),
  `TRAININGPEAKS_CLIENT_ID`, `TRAININGPEAKS_CLIENT_SECRET`,
  `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, `GITHUB_WEBHOOK_SECRET`,
  `SPOTIFY_CLIENT_ID`, `SPOTIFY_CLIENT_SECRET`. The TrainingPeaks and
  Spotify pairs are the literal `PLACEHOLDER_REPLACE_ME` — they were never
  set in Secret Manager either, so Cloud Run ran without them too. Replace
  them in the file if those providers are ever connected.
- **Not copied**: `google-client-id` / `google-client-secret` (no reader left;
  the Google Sheets destination is gone), `showcase-view-salt` (F1.5a),
  `email-app-password` (F1.7), Stripe (F1.1), Oura/Polar/Wahoo (F1.6).
- **Fixed values**: `PUBSUB_PUSH_AUDIENCE=https://jarvis.tail3065f7.ts.net/fg/pubsub`
  (F3.4 sets the same audience on each push subscription),
  `FITGLUE_PUBLIC_BASE_URL=https://jarvis.tail3065f7.ts.net/fg` (see below),
  `API_URL=` (empty: the legacy fallback, unused), `WEB_URL=https://fitglue.tech`
  (where the browser lands after an OAuth callback, now a redirect site — a
  404 there is fine, the integration is already written),
  `SITE_EXPORT_HOOK_URL=http://127.0.0.1:8790/hooks/fitness-updated`,
  `SENTRY_DSN` (the DSN from `terraform/envs/prod.tfvars`), and the
  deployment settings listed above.

### The OAuth callback base

`internal/oauthflow` builds the redirect_uri as
`<FITGLUE_PUBLIC_BASE_URL>/api/v2/oauth/<provider>/callback` — the path is a
constant, only the origin is configurable. With the base set to
`https://jarvis.tail3065f7.ts.net/fg`, the providers redirect to
`https://jarvis.tail3065f7.ts.net/fg/api/v2/oauth/<provider>/callback`, and
the Caddyfile routes `/fg/api/v2/oauth/*` to api-client for exactly that
(F3.5 added the route; only the OAuth prefix is exposed, the rest of
`/api/v2` is not). `/fg/oauth/<provider>/callback` still works as a short
alias, but it is **not** what the providers see: register the long form in
each provider's developer console (`docs/guides/connect-sources.md`).

The alternative — a second env var for the callback origin — would have been
Go code, tests and two more docs for the same result; a Caddy route was the
smaller change.

## Checking the file

```bash
deploy/box/env-check.sh          # names only; exit 1 if a required key is missing/empty
deploy/box/env-check.sh --quiet  # just "ok" or the problems
```

It diffs the file against `fitglue.env.example`: required keys missing or
empty fail; optional keys (`API_URL`, `WEB_URL`, `SENTRY_*`,
`PARKRUN_FETCHER_URL`, `SITE_EXPORT_BUCKET`, `SITE_EXPORT_DISABLED`,
`PUBSUB_PUSH_SERVICE_ACCOUNT`, `GOOGLE_CLIENT_*`) are reported, not failed;
keys in the file the example does not list are reported as info. Run it after
every edit and before `make deploy-box`. The file is systemd's format: no
`export`, no inline comments, no quotes unless the value has spaces, and
`KEY=` means empty (the services treat empty as unset).

Never `cat` the file into a terminal that is being transcribed, pasted into
Slack, or run by an agent. `cut -d= -f1 ~/.jarvis/fitglue.env` lists the keys.

## Rotating a value

1. Get the new value from the provider's console (or `openssl rand -hex 32`
   for `FITGLUE_API_TOKEN` / `OAUTH_STATE_SECRET`).
2. Edit the line in `~/.jarvis/fitglue.env` — `sed -i 's/^KEY=.*/KEY=newvalue/'
   ~/.jarvis/fitglue.env` keeps it out of a pager and the file's mode intact.
   If you use an editor, check the mode afterwards (`stat -c %a`, expect 600).
3. `deploy/box/env-check.sh`.
4. Restart what reads it: `systemctl --user restart fitglue-<svc>` (the
   services read env at start). `make deploy-box` restarts every active unit.
5. If it is `FITGLUE_API_TOKEN`, update the copy in `~/.jarvis/env` on the
   box that Jarvis's own calls use, and the MCP connector config. If it is
   `OAUTH_STATE_SECRET`, any `fitgluectl connect` URL printed before the
   rotation is now invalid (`error=invalid_state_signature`) — print a new one.
6. Secret Manager is **not** updated: it is retired. Until F4.2 removes it the
   value there is stale, which is fine — nothing reads it.

Rotation of a provider's *client secret* also needs the provider's console;
the per-user access/refresh tokens in Firestore are untouched by this.

## Backup and loss

`~/.jarvis` is in the restic set, so the file is backed up with the rest of
the box. If it is lost before F4.2, the same values can be re-copied from
Secret Manager with James's gcloud (`gcloud secrets versions access latest
--secret <id> --project fitglue-server-prod`, mapping in the header of
`terraform/secrets.tf`); after F4.2, restic or the providers' consoles are
the only sources. `FITGLUE_API_TOKEN` and `OAUTH_STATE_SECRET` can simply be
regenerated (then rotate as above).

## Terraform

`terraform/secrets.tf` and the `secret_key_ref` env blocks in `cloud_run.tf`
are intentionally still there. The Cloud Run services keep running on their
current revision until F4.1 deletes them; dropping the refs now would make the
next `terraform apply` roll a revision with no secrets while Cloud Run is still
the live deployment. `secrets.tf` carries a `RETIRED IN F4.2` header listing
exactly what goes then. The `fitglue-box` service account never gets
`secretmanager.secretAccessor`; the copy was a one-off under James's identity
(decision 10).
